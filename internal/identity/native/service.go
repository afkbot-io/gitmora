package native

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/identity/domain"
)

// Vault persists secrets only through an OS-protected service and fences explicit mutations per server.
type Vault interface {
	Load(context.Context, string) (Record, error)
	Save(context.Context, Record) error
	Delete(context.Context, string) error
	Lock(context.Context, string) (func(), error)
}

// Remote is an exact-destination enrollment/self/revocation port; implementations must reject redirects.
type Remote interface {
	Server() string
	Prepare(context.Context, domain.CLIRequest) (Prepared, error)
	Poll(context.Context, string, string, string) (domain.CLIView, error)
	Cancel(context.Context, string, string, string) (domain.CLIView, error)
	Me(context.Context, string) (Account, error)
	Revoke(context.Context, string, string, string) error
}

// Prepared contains public registration metadata and an immutable server-owned deadline.
type Prepared struct {
	ID              string    `json:"id"`
	ExpiresAt       time.Time `json:"expires_at"`
	VerificationURI string    `json:"verification_uri"`
	IntervalSeconds int       `json:"interval_seconds"`
}

// Prompt is safe to print or pass to a browser; it deliberately cannot carry device secrets.
type Prompt struct {
	Server          string    `json:"server"`
	VerificationURI string    `json:"verification_uri"`
	Code            string    `json:"code"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// LoginOptions apply to new enrollment. Zero fields resume the exact protected pending intent.
type LoginOptions struct {
	Label   string
	Scopes  []access.Permission
	TTL     time.Duration
	Replace bool
}

// Status is observed metadata, not a cached permission grant. Recovery requires explicit login.
type Status struct {
	Server  string   `json:"server"`
	State   string   `json:"state"`
	Account *Account `json:"account,omitempty"`
}

// Service owns native enrollment/recovery; no HTTP, OS process, filesystem or browser policy lives here.
type Service struct {
	server string
	vault  Vault
	remote Remote
	now    func() time.Time
	wait   func(context.Context, time.Duration) error
}

// NewService rejects a mismatched destination before any secret can be loaded or transmitted.
func NewService(server string, vault Vault, remote Remote, now func() time.Time, wait func(context.Context, time.Duration) error) (*Service, error) {
	canonical, e := CanonicalServer(server)
	if e != nil || vault == nil || remote == nil || remote.Server() != canonical {
		return nil, ErrInvalid
	}
	if now == nil {
		now = time.Now
	}
	if wait == nil {
		wait = waitForPoll
	}
	return &Service{server: canonical, vault: vault, remote: remote, now: now, wait: wait}, nil
}

func waitForPoll(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) load(ctx context.Context) (Record, error) {
	r, e := s.vault.Load(ctx, s.server)
	if e != nil {
		return Record{}, e
	}
	if r.Server != s.server || r.Validate() != nil {
		return Record{}, ErrInvalid
	}
	return r, nil
}

// Login persists candidate/proofs before registration and retains them across every ambiguous outcome.
// A verified live candidate recovers even after its ephemeral flow/deadline has disappeared.
func (s *Service) Login(ctx context.Context, options LoginOptions, prompt func(Prompt) error) (Record, error) {
	if e := ValidateLoginOptions(options); e != nil {
		return Record{}, e
	}
	release, e := s.vault.Lock(ctx, s.server)
	if e != nil {
		return Record{}, e
	}
	defer release()
	r, e := s.load(ctx)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return Record{}, e
	}
	if e == nil && options.Replace {
		if e = s.close(ctx, r); e != nil {
			return Record{}, e
		}
		r = Record{}
	}
	if r.Secret != "" {
		if r.Pending != nil && !sameIntent(r.Pending.Request, options) {
			return Record{}, ErrConflict
		}
		a, err := s.remote.Me(ctx, r.Secret)
		if err == nil {
			if r.Account != nil && (options.Label != "" || options.Scopes != nil || options.TTL != 0) {
				return Record{}, ErrConflict
			}
			return s.activate(ctx, r, a)
		}
		if !errors.Is(err, ErrUnauthorized) {
			return Record{}, err
		}
		if r.Account != nil {
			r = Record{}
		}
	}
	if r.Secret == "" {
		label := options.Label
		if label == "" {
			label = "Workgraph CLI"
		}
		scopes := options.Scopes
		if scopes == nil {
			scopes = []access.Permission{access.ReadCapture, access.WriteCapture, access.ReadIntegration, access.ReadDirectory, access.ManageCredentials}
		}
		ttl := options.TTL
		if ttl == 0 {
			ttl = 30 * 24 * time.Hour
		}
		r, e = NewPendingRecord(s.server, label, scopes, int64(ttl/time.Second))
		if e != nil {
			return Record{}, e
		}
		if e = s.vault.Save(ctx, r); e != nil {
			return Record{}, e
		}
	}
	p := r.Pending
	if !p.ExpiresAt.IsZero() && !p.ExpiresAt.After(s.now()) {
		return Record{}, ErrEnrollmentGone
	}
	prepared, e := s.remote.Prepare(ctx, p.Request)
	if e != nil {
		return Record{}, e
	}
	if prepared.ID != p.Request.ID || prepared.VerificationURI != s.server+"/v1/auth/cli/login" || prepared.IntervalSeconds < 2 || prepared.IntervalSeconds > 60 || !prepared.ExpiresAt.After(s.now()) || prepared.ExpiresAt.After(s.now().Add(domain.CLIFlowTTL+5*time.Second)) || !p.ExpiresAt.IsZero() && !p.ExpiresAt.Equal(prepared.ExpiresAt) {
		return Record{}, ErrProtocol
	}
	p.ExpiresAt = prepared.ExpiresAt
	if e = s.vault.Save(ctx, r); e != nil {
		return Record{}, e
	}
	if prompt != nil {
		if e = prompt(Prompt{Server: s.server, VerificationURI: prepared.VerificationURI + "?code=" + p.Code, Code: p.Code, ExpiresAt: p.ExpiresAt}); e != nil {
			return Record{}, e
		}
	}
	ctx, cancel := context.WithDeadline(ctx, p.ExpiresAt)
	defer cancel()
	fingerprint := ""
	for {
		if e = s.wait(ctx, time.Duration(prepared.IntervalSeconds)*time.Second); e != nil {
			return Record{}, e
		}
		view, err := s.remote.Poll(ctx, p.Request.ID, p.Device, p.Proof)
		if err != nil {
			return Record{}, err
		}
		if e = validateView(view, *p); e != nil {
			return Record{}, e
		}
		if fingerprint != "" && fingerprint != view.Fingerprint {
			return Record{}, ErrProtocol
		}
		fingerprint = view.Fingerprint
		switch view.Status {
		case "denied", "cancelled":
			// The request is terminal under the approval/cancellation lock. Preserve recovery if deletion fails.
			if e = s.vault.Delete(ctx, s.server); e != nil {
				return Record{}, e
			}
			return Record{}, ErrRejected
		case "approved":
			a, err := s.remote.Me(ctx, r.Secret)
			if err != nil {
				return Record{}, err
			}
			if a.UserID != view.UserID || a.TokenID != view.TokenID || view.CredentialExpiresAt == nil || !a.ExpiresAt.Equal(*view.CredentialExpiresAt) {
				return Record{}, ErrProtocol
			}
			return s.activate(ctx, r, a)
		}
	}
}

// ValidateLoginOptions lets a CLI reject malformed authority/lifetime flags before opening the native store.
func ValidateLoginOptions(o LoginOptions) error {
	if o.TTL != 0 && (o.TTL < time.Minute || o.TTL > 90*24*time.Hour || o.TTL%time.Second != 0) {
		return ErrInvalid
	}
	if o.Scopes != nil && len(o.Scopes) == 0 {
		return ErrInvalid
	}
	// Validate all requested authority/label before opening the system store, using no device secret.
	label := o.Label
	if label == "" {
		label = "Workgraph CLI"
	}
	scopes := o.Scopes
	if scopes == nil {
		scopes = []access.Permission{access.ManageCredentials}
	}
	_, e := (domain.CLIRequest{ID: "11111111-1111-4111-8111-111111111111", DeviceHash: strings.Repeat("a", 64), ProofHash: strings.Repeat("b", 64), CredentialHash: strings.Repeat("c", 64), CodeHash: strings.Repeat("d", 64), Label: label, Scopes: scopes, TTLSeconds: 86400}).Normalize()
	if e != nil {
		return ErrInvalid
	}
	return nil
}

func sameIntent(r domain.CLIRequest, o LoginOptions) bool {
	if o.Label != "" && strings.TrimSpace(o.Label) != r.Label || o.TTL != 0 && int64(o.TTL/time.Second) != r.TTLSeconds {
		return false
	}
	if o.Scopes != nil {
		scopes := slices.Clone(o.Scopes)
		slices.Sort(scopes)
		scopes = slices.Compact(scopes)
		return slices.Equal(scopes, r.Scopes)
	}
	return true
}

func validateView(v domain.CLIView, p Enrollment) error {
	digest, e := hex.DecodeString(v.Fingerprint)
	if e != nil || len(digest) != 32 || v.ID != p.Request.ID || v.Label != p.Request.Label || !slices.Equal(v.Scopes, p.Request.Scopes) || v.TTLSeconds != p.Request.TTLSeconds || !v.ExpiresAt.Equal(p.ExpiresAt) || v.CreatedAt.IsZero() || !v.ExpiresAt.After(v.CreatedAt) || v.ExpiresAt.Sub(v.CreatedAt) > domain.CLIFlowTTL+time.Millisecond {
		return ErrProtocol
	}
	switch v.Status {
	case "pending", "cancelled":
		if v.UserID != "" || v.TokenID != "" || v.CredentialExpiresAt != nil {
			return ErrProtocol
		}
	case "denied":
		if !identifier.Valid(v.UserID) || v.TokenID != "" || v.CredentialExpiresAt != nil {
			return ErrProtocol
		}
	case "approved":
		if !identifier.Valid(v.UserID) || !identifier.Valid(v.TokenID) || v.CredentialExpiresAt == nil {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	return nil
}

func (s *Service) verifiedAccount(r Record, a Account) (Account, error) {
	if a.Validate() != nil || !a.ExpiresAt.After(s.now()) {
		return Account{}, ErrProtocol
	}
	a.Scopes = slices.Clone(a.Scopes)
	slices.Sort(a.Scopes)
	if r.Pending != nil {
		if !slices.Equal(a.Scopes, r.Pending.Request.Scopes) || a.ExpiresAt.After(s.now().Add(time.Duration(r.Pending.Request.TTLSeconds)*time.Second+5*time.Second)) {
			return Account{}, ErrProtocol
		}
	} else if r.Account.UserID != a.UserID || r.Account.TokenID != a.TokenID || !r.Account.ExpiresAt.Equal(a.ExpiresAt) || !slices.Equal(r.Account.Scopes, a.Scopes) {
		return Account{}, ErrProtocol
	}
	return a, nil
}

func (s *Service) activate(ctx context.Context, r Record, a Account) (Record, error) {
	a, e := s.verifiedAccount(r, a)
	if e != nil {
		return Record{}, e
	}
	r.Pending = nil
	r.Account = &a
	if e := s.vault.Save(ctx, r); e != nil {
		return Record{}, e
	}
	return r, nil
}

// Inspect checks live authority without mutating or minting a credential; no browser or enrollment is started.
func (s *Service) Inspect(ctx context.Context) (Status, error) {
	r, e := s.load(ctx)
	if e != nil {
		return Status{}, e
	}
	a, e := s.remote.Me(ctx, r.Secret)
	if r.Pending != nil && errors.Is(e, ErrUnauthorized) {
		return Status{Server: s.server, State: "pending"}, nil
	}
	if e != nil {
		return Status{}, e
	}
	a, e = s.verifiedAccount(r, a)
	if e != nil {
		return Status{}, e
	}
	state := "active"
	if r.Pending != nil {
		state = "recovery_required"
	}
	return Status{Server: s.server, State: state, Account: &a}, nil
}

// Logout verifies remote closure before forgetting, including approval racing with pending cancellation.
// localOnly is an explicitly weaker local-forget action and makes no remote revocation claim.
func (s *Service) Logout(ctx context.Context, localOnly bool) error {
	release, e := s.vault.Lock(ctx, s.server)
	if e != nil {
		return e
	}
	defer release()
	if localOnly {
		return s.vault.Delete(ctx, s.server)
	}
	r, e := s.load(ctx)
	if errors.Is(e, ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	return s.close(ctx, r)
}

func (s *Service) close(ctx context.Context, r Record) error {
	a, e := s.remote.Me(ctx, r.Secret)
	if errors.Is(e, ErrUnauthorized) && r.Pending != nil {
		p := r.Pending
		closed, err := s.remote.Cancel(ctx, p.Request.ID, p.Device, p.Proof)
		if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrEnrollmentGone) {
			return err
		}
		if err == nil && closed.Status != "cancelled" && closed.Status != "denied" {
			return ErrProtocol
		}
		// Recheck even a missing flow: approval may have committed after the first Me and before cleanup.
		a, e = s.remote.Me(ctx, r.Secret)
	}
	if errors.Is(e, ErrUnauthorized) {
		return s.vault.Delete(ctx, s.server)
	}
	if e != nil {
		return e
	}
	a, e = s.verifiedAccount(r, a)
	if e != nil {
		return e
	}
	request, e := identifier.New()
	if e != nil {
		return e
	}
	if e = s.remote.Revoke(ctx, r.Secret, a.TokenID, request); e != nil {
		return e
	}
	if _, e = s.remote.Me(ctx, r.Secret); !errors.Is(e, ErrUnauthorized) {
		if e == nil {
			return ErrProtocol
		}
		return e
	}
	return s.vault.Delete(ctx, s.server)
}
