// Package application coordinates trusted recovery authority through an atomic persistence port.
package application

import (
	"context"
	"encoding/json"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/recovery/domain"
)

// Store atomically quarantines restored authority and records the exact recoverable intent.
// Its caller must already own a stopped, isolated restore target; this is not an online administration API.
type Store interface {
	Quarantine(context.Context, domain.Plan) (domain.Receipt, error)
}

// Prepared retains a private candidate in the host operator process, independently of database/logging adapters.
type Prepared struct {
	Plan   domain.Plan `json:"plan"`
	secret string
}

// Secret explicitly reveals the candidate only for its protected host recovery record.
func (p Prepared) Secret() string { return p.secret }

func (Prepared) String() string   { return "[private recovery preparation]" }
func (Prepared) GoString() string { return "[private recovery preparation]" }
func (p Prepared) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Plan domain.Plan `json:"plan"`
	}{p.Plan})
}

// Service supplies fresh bounded authority; it does not restore database bytes or start application workers.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService supplies the offline authority transaction and preparation clock.
func NewService(store Store, now func() time.Time) *Service { return &Service{store: store, now: now} }

// Prepare generates an independent operator/key before external work; callers must durably protect it before apply.
func (s *Service) Prepare(source domain.Provenance, name string, ttl time.Duration) (Prepared, error) {
	if ttl < time.Minute || ttl > 24*time.Hour || ttl%time.Second != 0 {
		return Prepared{}, domain.ErrInvalid
	}
	id, e := identifier.New()
	if e != nil {
		return Prepared{}, domain.ErrUnavailable
	}
	user, e := identifier.New()
	if e != nil {
		return Prepared{}, domain.ErrUnavailable
	}
	now := s.now()
	key, e := access.IssueCredential(user, "Recovery metadata operator", domain.MetadataScopes(), now, ttl)
	if e != nil {
		return Prepared{}, domain.ErrInvalid
	}
	plan, e := (domain.Plan{Version: 1, ID: id, BackupID: source.BackupID, Schema: source.Schema, ParentID: source.ParentID, UserID: user, Name: name, Key: domain.CredentialProof{ID: key.Record.ID, Hash: key.Record.Hash, CreatedAt: key.Record.CreatedAt, ExpiresAt: key.Record.ExpiresAt}}).Normalize(now)
	if e != nil {
		return Prepared{}, e
	}
	return Prepared{Plan: plan, secret: key.Secret}, nil
}

// Quarantine sends only normalized hash commitments to the stopped-database adapter.
func (s *Service) Quarantine(ctx context.Context, p domain.Plan) (domain.Receipt, error) {
	p, e := p.Normalize(s.now())
	if e != nil {
		return domain.Receipt{}, e
	}
	if s.store == nil {
		return domain.Receipt{}, domain.ErrUnavailable
	}
	return s.store.Quarantine(ctx, p)
}
