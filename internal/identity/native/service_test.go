package native_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/identity/native"
)

type memoryVault struct {
	raw           []byte
	failSave      bool
	saves, failAt int
	busy          bool
	deletes       int
}

func (v *memoryVault) Load(_ context.Context, server string) (native.Record, error) {
	if v.raw == nil {
		return native.Record{}, native.ErrNotFound
	}
	return native.DecodeSecretStoreRecord(server, v.raw)
}
func (v *memoryVault) Save(_ context.Context, r native.Record) error {
	v.saves++
	if v.failSave || v.failAt == v.saves {
		return native.ErrVault
	}
	raw, e := native.EncodeSecretStoreRecord(r)
	if e == nil {
		v.raw = raw
	}
	return e
}
func (v *memoryVault) Delete(context.Context, string) error { v.raw = nil; v.deletes++; return nil }
func (v *memoryVault) Lock(context.Context, string) (func(), error) {
	if v.busy {
		return nil, native.ErrBusy
	}
	v.busy = true
	return func() { v.busy = false }, nil
}

type nativeRemote struct {
	t                                                 *testing.T
	vault                                             *memoryVault
	now                                               time.Time
	active                                            bool
	prepareErr, cancelErr, meErr                      error
	revokeErr                                         error
	preparedURI                                       string
	meOverride                                        func(native.Account) native.Account
	prepareCalls, pollCalls, cancelCalls, revokeCalls int
	last                                              domain.CLIRequest
	approveOnPoll, approveOnCancel                    bool
}

func (r *nativeRemote) Server() string { return "https://workgraph.internal" }
func (r *nativeRemote) Prepare(_ context.Context, i domain.CLIRequest) (native.Prepared, error) {
	r.prepareCalls++
	r.last = i
	saved, e := r.vault.Load(context.Background(), r.Server())
	if e != nil || saved.Pending == nil || saved.Pending.Request.ID != i.ID {
		r.t.Fatal("request reached server before protected persistence")
	}
	uri := r.preparedURI
	if uri == "" {
		uri = r.Server() + "/v1/auth/cli/login"
	}
	return native.Prepared{ID: i.ID, ExpiresAt: r.now.Add(domain.CLIFlowTTL), VerificationURI: uri, IntervalSeconds: 2}, r.prepareErr
}
func (r *nativeRemote) Poll(_ context.Context, id, device, proof string) (domain.CLIView, error) {
	r.pollCalls++
	saved, e := r.vault.Load(context.Background(), r.Server())
	if e != nil || saved.Pending == nil || saved.Pending.Request.ID != id || saved.Pending.Device != device || saved.Pending.Proof != proof {
		r.t.Fatal("poll lost device ownership")
	}
	p := saved.Pending
	v := domain.CLIEnrollment{Request: p.Request, CreatedAt: r.now, ExpiresAt: p.ExpiresAt, Status: "pending"}.View()
	if r.approveOnPoll && r.pollCalls > 1 {
		r.active = true
		a := r.account(saved)
		v.Status = "approved"
		v.UserID = a.UserID
		v.TokenID = a.TokenID
		v.CredentialExpiresAt = &a.ExpiresAt
	}
	return v, nil
}
func (r *nativeRemote) Cancel(context.Context, string, string, string) (domain.CLIView, error) {
	r.cancelCalls++
	if r.approveOnCancel {
		r.active = true
		return domain.CLIView{}, native.ErrConflict
	}
	return domain.CLIView{Status: "cancelled"}, r.cancelErr
}
func (r *nativeRemote) account(saved native.Record) native.Account {
	if saved.Account != nil {
		return *saved.Account
	}
	return native.Account{UserID: "11111111-1111-4111-8111-111111111111", TokenID: "22222222-2222-4222-8222-222222222222", DisplayName: "Local engineer", Scopes: slices.Clone(saved.Pending.Request.Scopes), ExpiresAt: r.now.Add(time.Duration(saved.Pending.Request.TTLSeconds) * time.Second)}
}
func (r *nativeRemote) Me(_ context.Context, key string) (native.Account, error) {
	if r.meErr != nil {
		return native.Account{}, r.meErr
	}
	if !r.active {
		return native.Account{}, native.ErrUnauthorized
	}
	saved, e := r.vault.Load(context.Background(), r.Server())
	if e != nil || saved.Secret != key {
		r.t.Fatal("wrong candidate authenticated")
	}
	a := r.account(saved)
	if r.meOverride != nil {
		a = r.meOverride(a)
	}
	return a, nil
}
func (r *nativeRemote) Revoke(_ context.Context, key, token, request string) error {
	r.revokeCalls++
	if len(request) != 36 || token == "" || !strings.HasPrefix(key, "wg_") {
		r.t.Fatal("invalid authenticated revocation")
	}
	r.active = false
	return r.revokeErr
}
func nativeFixture(t *testing.T) (*native.Service, *memoryVault, *nativeRemote) {
	t.Helper()
	v := &memoryVault{}
	r := &nativeRemote{t: t, vault: v, now: time.Now().UTC()}
	s, e := native.NewService(r.Server(), v, r, func() time.Time { return r.now }, func(ctx context.Context, d time.Duration) error {
		if d < 2*time.Second {
			t.Fatal("poll too fast")
		}
		return ctx.Err()
	})
	if e != nil {
		t.Fatal(e)
	}
	return s, v, r
}
func TestNativeLoginPersistsBeforeRegistrationAndVerifiesApprovedCandidateBeforeActivation(t *testing.T) {
	s, v, r := nativeFixture(t)
	r.approveOnPoll = true
	prompted := 0
	got, e := s.Login(context.Background(), native.LoginOptions{}, func(p native.Prompt) error {
		prompted++
		saved, err := v.Load(context.Background(), r.Server())
		if err != nil || saved.Pending.ExpiresAt.IsZero() {
			t.Fatal("unrecoverable prompt")
		}
		if p.Code != saved.Pending.Code || p.VerificationURI != r.Server()+"/v1/auth/cli/login?code="+p.Code {
			t.Fatal("unsafe routing")
		}
		public, _ := json.Marshal(p)
		if strings.Contains(string(public), saved.Secret) || strings.Contains(string(public), saved.Pending.Proof) {
			t.Fatal("prompt leaked key")
		}
		return nil
	})
	if e != nil || prompted != 1 || got.Account == nil || got.Pending != nil || r.pollCalls != 2 {
		t.Fatal("native activation", e)
	}
	stored, e := v.Load(context.Background(), r.Server())
	if e != nil || stored.Pending != nil || stored.Account.TokenID != got.Account.TokenID {
		t.Fatal("activation not persisted", e)
	}
}
func TestNativeLoginRetainsExactRecoveryIntentAcrossUncertainPreparationAndRejectsRetargeting(t *testing.T) {
	s, v, r := nativeFixture(t)
	r.prepareErr = native.ErrUnavailable
	if _, e := s.Login(context.Background(), native.LoginOptions{}, nil); !errors.Is(e, native.ErrUnavailable) {
		t.Fatal(e)
	}
	first, _ := v.Load(context.Background(), r.Server())
	if _, e := s.Login(context.Background(), native.LoginOptions{Label: "Another machine"}, nil); !errors.Is(e, native.ErrConflict) {
		t.Fatal("pending intent changed", e)
	}
	r.prepareErr = nil
	r.approveOnPoll = true
	got, e := s.Login(context.Background(), native.LoginOptions{}, nil)
	if e != nil || got.Secret != first.Secret || r.last.ID != first.Pending.Request.ID || r.prepareCalls != 2 {
		t.Fatal("uncertain recovery minted another candidate", e)
	}
}
func TestNativeVaultFailurePreventsAnyEnrollmentAndUnavailableAuthenticationKeepsSavedState(t *testing.T) {
	s, v, r := nativeFixture(t)
	v.failSave = true
	if _, e := s.Login(context.Background(), native.LoginOptions{}, nil); !errors.Is(e, native.ErrVault) || r.prepareCalls != 0 {
		t.Fatal("unprotected key registered", e)
	}
	v.failSave = false
	r.prepareErr = native.ErrUnavailable
	_, _ = s.Login(context.Background(), native.LoginOptions{}, nil)
	first := slices.Clone(v.raw)
	r.meErr = native.ErrUnavailable
	if _, e := s.Login(context.Background(), native.LoginOptions{}, nil); !errors.Is(e, native.ErrUnavailable) || !slices.Equal(first, v.raw) {
		t.Fatal("network failure replaced recoverable state", e)
	}
}
func TestNativeRecoveryAuthenticatesKeyAfterFlowDeadlineWithoutReenrollment(t *testing.T) {
	s, v, r := nativeFixture(t)
	p, e := native.NewPendingRecord(r.Server(), "Saved", []access.Permission{access.ManageCredentials, access.WriteCapture}, 86400)
	if e != nil {
		t.Fatal(e)
	}
	p.Pending.ExpiresAt = r.now.Add(-time.Minute)
	if e = v.Save(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	r.active = true
	got, e := s.Login(context.Background(), native.LoginOptions{}, nil)
	if e != nil || got.Account == nil || r.prepareCalls != 0 {
		t.Fatal("lost approved ACK could not recover", e)
	}
}
func TestNativeLogoutClosesPendingApprovalRaceBeforeForgettingAndPreservesUncertainRecovery(t *testing.T) {
	for _, race := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel-first", true: "approval-first"}[race], func(t *testing.T) {
			s, v, r := nativeFixture(t)
			p, _ := native.NewPendingRecord(r.Server(), "Saved", []access.Permission{access.ManageCredentials}, 86400)
			_ = v.Save(context.Background(), p)
			r.approveOnCancel = race
			if e := s.Logout(context.Background(), false); e != nil || v.raw != nil || r.active || r.cancelCalls != 1 || r.revokeCalls != map[bool]int{false: 0, true: 1}[race] {
				t.Fatal("logout orphaned an approved key", e)
			}
		})
	}
	s, v, r := nativeFixture(t)
	p, _ := native.NewPendingRecord(r.Server(), "Saved", []access.Permission{access.ManageCredentials}, 86400)
	_ = v.Save(context.Background(), p)
	r.cancelErr = native.ErrUnavailable
	if e := s.Logout(context.Background(), false); !errors.Is(e, native.ErrUnavailable) || v.raw == nil {
		t.Fatal("uncertain logout erased recovery", e)
	}
	if e := s.Logout(context.Background(), true); e != nil || v.raw != nil || r.cancelCalls != 1 {
		t.Fatal("explicit local forget used remote authority", e)
	}
}

func TestNativeActivationSaveFailureAndLostRevokeACKRemainRecoverable(t *testing.T) {
	s, v, r := nativeFixture(t)
	v.failAt = 3
	r.approveOnPoll = true
	if _, e := s.Login(context.Background(), native.LoginOptions{}, nil); !errors.Is(e, native.ErrVault) {
		t.Fatal("activation save failure hidden", e)
	}
	pending, e := v.Load(context.Background(), r.Server())
	if e != nil || pending.Pending == nil || !r.active {
		t.Fatal("approved candidate recovery erased", e)
	}
	v.failAt = 0
	got, e := s.Login(context.Background(), native.LoginOptions{}, nil)
	if e != nil || got.Secret != pending.Secret || got.Account == nil || r.prepareCalls != 1 {
		t.Fatal("approved recovery reenrolled", e)
	}
	r.revokeErr = native.ErrUnavailable
	if e = s.Logout(context.Background(), false); !errors.Is(e, native.ErrUnavailable) || v.raw == nil || r.active {
		t.Fatal("lost revoke ACK erased recovery", e)
	}
	if e = s.Logout(context.Background(), false); e != nil || v.raw != nil || r.revokeCalls != 1 {
		t.Fatal("revoked root retry could not forget safely", e)
	}
}

func TestNativeVerificationDestinationAndCurrentMetadataCannotBeRetargeted(t *testing.T) {
	s, v, r := nativeFixture(t)
	r.preparedURI = "https://other.internal/v1/auth/cli/login"
	if _, e := s.Login(context.Background(), native.LoginOptions{}, func(native.Prompt) error { t.Fatal("untrusted verification URL prompted"); return nil }); !errors.Is(e, native.ErrProtocol) || v.raw == nil {
		t.Fatal("verification destination accepted", e)
	}
	r.preparedURI = ""
	r.active = true
	if _, e := s.Login(context.Background(), native.LoginOptions{}, nil); e != nil {
		t.Fatal(e)
	}
	r.meOverride = func(a native.Account) native.Account { a.TokenID = "33333333-3333-4333-8333-333333333333"; return a }
	if _, e := s.Inspect(context.Background()); !errors.Is(e, native.ErrProtocol) {
		t.Fatal("status accepted a different credential identity", e)
	}
}

func TestNativeExplicitLocalForgetCanRemoveACorruptOwnedSlotWithoutRemoteAuthority(t *testing.T) {
	s, v, r := nativeFixture(t)
	v.raw = []byte(`{"version":99}`)
	if e := s.Logout(context.Background(), false); !errors.Is(e, native.ErrInvalid) || v.raw == nil {
		t.Fatal("corrupt slot automatically erased", e)
	}
	if e := s.Logout(context.Background(), true); e != nil || v.raw != nil || r.cancelCalls != 0 || r.revokeCalls != 0 {
		t.Fatal("explicit local forget cannot repair owned corrupt slot", e)
	}
}
