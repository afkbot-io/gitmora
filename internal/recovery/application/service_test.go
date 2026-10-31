package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/recovery/application"
	"workgraph.local/platform/internal/recovery/domain"
)

type store struct {
	calls int
	plan  domain.Plan
}

func (s *store) Quarantine(_ context.Context, p domain.Plan) (domain.Receipt, error) {
	s.calls++
	s.plan = p
	return domain.Receipt{ID: p.ID, BackupID: p.BackupID, UserID: p.UserID, TokenID: p.Key.ID}, nil
}

func TestRecoveryPreparationIsPrivateAndApplyUsesOnlyTheHash(t *testing.T) {
	now := time.Now().UTC()
	port := &store{}
	s := application.NewService(port, func() time.Time { return now })
	p, e := s.Prepare(domain.Provenance{BackupID: "22222222-2222-4222-8222-222222222222", Schema: strings.Repeat("a", 64)}, "Recovery", time.Hour)
	if e != nil || port.calls != 0 || access.CredentialHash(p.Secret()) != p.Plan.Key.Hash {
		t.Fatal("preparation wrote authority or lost credential commitment", e)
	}
	encoded, _ := json.Marshal(p)
	if strings.Contains(string(encoded), p.Secret()) || strings.Contains(fmt.Sprintf("%v %#v", p, p), p.Secret()) {
		t.Fatal("generic preparation output revealed a credential")
	}
	r, e := s.Quarantine(context.Background(), p.Plan)
	if e != nil || port.calls != 1 || port.plan.Key.Hash != access.CredentialHash(p.Secret()) || r.UserID != p.Plan.UserID {
		t.Fatal("trusted recovery store contract", e)
	}
	invalid := p.Plan
	invalid.Key.Hash = "wg_private"
	if _, e = s.Quarantine(context.Background(), invalid); e == nil || port.calls != 1 {
		t.Fatal("invalid plan reached authority adapter")
	}
	for _, ttl := range []time.Duration{0, 30 * time.Second, 25 * time.Hour} {
		if _, e := s.Prepare(domain.Provenance{BackupID: p.Plan.BackupID, Schema: p.Plan.Schema}, "Recovery", ttl); e == nil {
			t.Fatal("unbounded operator preparation")
		}
	}
}
