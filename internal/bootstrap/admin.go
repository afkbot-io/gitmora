package bootstrap

import (
	"context"
	"strings"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
)

// AdminSetup initializes only a local operator and a hashed expiring credential; no provider identity is invented.
type AdminSetup struct {
	UserID, DisplayName string
	Credential          access.CredentialRecord
}

// AdminStore atomically refuses any already initialized catalog/account installation.
type AdminStore interface {
	InitializeAdmin(context.Context, AdminSetup) error
}

// AdminResult publishes its one-time credential only after the initial operator transaction commits.
type AdminResult struct {
	UserID     string
	Credential access.IssuedCredential
}

// AdminService is trusted offline installation setup, independent from the legacy convenience catalog bootstrap.
type AdminService struct {
	store AdminStore
	now   func() time.Time
}

// NewAdminService supplies its atomic operator store and clock.
func NewAdminService(store AdminStore, now func() time.Time) *AdminService {
	return &AdminService{store: store, now: now}
}

// Initialize creates a bounded first-operator credential without granting content membership by operator status.
func (s *AdminService) Initialize(ctx context.Context, name string, ttl time.Duration) (AdminResult, error) {
	if !validName(name) || ttl < time.Minute || ttl > 90*24*time.Hour || ttl%time.Second != 0 {
		return AdminResult{}, ErrInvalid
	}
	id, e := identifier.New()
	if e != nil {
		return AdminResult{}, e
	}
	scopes := []access.Permission{access.ReadDirectory, access.ManageDirectory, access.ManagePlatform, access.ManageCredentials, access.ReadCapture, access.WriteCapture, access.ReadIntegration, access.ManageIntegration}
	credential, e := access.IssueCredential(id, "Initial Gitmora operator", scopes, s.now(), ttl)
	if e != nil {
		return AdminResult{}, e
	}
	if e = s.store.InitializeAdmin(ctx, AdminSetup{UserID: id, DisplayName: strings.TrimSpace(name), Credential: credential.Record}); e != nil {
		return AdminResult{}, e
	}
	return AdminResult{UserID: id, Credential: credential}, nil
}
