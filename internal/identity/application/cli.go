package application

import (
	"context"
	"time"

	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/identity/domain"
)

// CLIStore owns atomic proof registration, current browser authority, terminal consent and live grant polling.
type CLIStore interface {
	CreateCLIEnrollment(context.Context, string, domain.CLIRequest) (domain.CLIEnrollment, error)
	InspectCLIEnrollment(context.Context, domain.Session, string, string) (domain.CLIEnrollment, error)
	DecideCLIEnrollment(context.Context, domain.Session, string, string, string, bool) (domain.CLIEnrollment, error)
	PollCLIEnrollment(context.Context, string, string, string, string) (domain.CLIEnrollment, error)
	CancelCLIEnrollment(context.Context, string, string, string, string) (domain.CLIEnrollment, error)
}

// CLIService enrolls a device-owned key after explicit fresh browser consent; it never receives its raw bearer.
type CLIService struct {
	store  CLIStore
	config domain.Config
}

// NewCLIService binds enrollment to the exact configured OIDC deployment and an explicit persistence port.
func NewCLIService(store CLIStore, config domain.Config) *CLIService {
	return &CLIService{store: store, config: config}
}

// CLIStarted contains only public routing/deadline metadata, never a recovery proof or API key.
type CLIStarted struct {
	ID              string    `json:"id"`
	ExpiresAt       time.Time `json:"expires_at"`
	VerificationURI string    `json:"verification_uri"`
	IntervalSeconds int       `json:"interval_seconds"`
}

// Start registers normalized hash commitments. Exact live-flow retries preserve the first deadline.
func (s *CLIService) Start(ctx context.Context, input domain.CLIRequest) (CLIStarted, error) {
	r, e := input.Normalize()
	if e != nil {
		return CLIStarted{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, e := s.store.CreateCLIEnrollment(ctx, s.config.Fingerprint(), r)
	if e != nil {
		return CLIStarted{}, e
	}
	return CLIStarted{ID: v.Request.ID, ExpiresAt: v.ExpiresAt, VerificationURI: s.config.PublicURL + "/v1/auth/cli/login", IntervalSeconds: 2}, nil
}

// Inspect exposes only an exact human-code request to its currently authenticated browser account.
func (s *CLIService) Inspect(ctx context.Context, session domain.Session, code string) (domain.CLIView, error) {
	hash := domain.CLICodeHash(code)
	if hash == "" || !cliSession(session) {
		return domain.CLIView{}, domain.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, e := s.store.InspectCLIEnrollment(ctx, session, s.config.Fingerprint(), hash)
	if e != nil {
		return domain.CLIView{}, e
	}
	return v.View(), nil
}

// Decide pins the inspected request. Only the persistence transaction may create an independent root grant.
func (s *CLIService) Decide(ctx context.Context, session domain.Session, code, fingerprint string, approve bool) (domain.CLIView, error) {
	hash := domain.CLICodeHash(code)
	if hash == "" || len(fingerprint) != 64 || !cliSession(session) {
		return domain.CLIView{}, domain.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, e := s.store.DecideCLIEnrollment(ctx, session, s.config.Fingerprint(), hash, fingerprint, approve)
	if e != nil {
		return domain.CLIView{}, e
	}
	return v.View(), nil
}

// Poll translates raw purpose-specific proofs into hashes before invoking persistence.
func (s *CLIService) Poll(ctx context.Context, id, device, proof string) (domain.CLIView, error) {
	return s.proofs(ctx, id, device, proof, false)
}

// Cancel closes an unapproved request using both device proofs, never a browser account or bearer.
func (s *CLIService) Cancel(ctx context.Context, id, device, proof string) (domain.CLIView, error) {
	return s.proofs(ctx, id, device, proof, true)
}

func (s *CLIService) proofs(ctx context.Context, id, device, proof string, closePending bool) (domain.CLIView, error) {
	d, p := domain.SecretHash(domain.CLIDevicePrefix, device), domain.SecretHash(domain.CLIProofPrefix, proof)
	if !identifier.Valid(id) || d == "" || p == "" {
		return domain.CLIView{}, domain.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	run := s.store.PollCLIEnrollment
	if closePending {
		run = s.store.CancelCLIEnrollment
	}
	v, e := run(ctx, s.config.Fingerprint(), id, d, p)
	if e != nil {
		return domain.CLIView{}, e
	}
	return v.View(), nil
}

func cliSession(s domain.Session) bool {
	return identifier.Valid(s.ID) && identifier.Valid(s.BindingID) && identifier.Valid(s.Principal.UserID) && identifier.Valid(s.Principal.TokenID)
}
