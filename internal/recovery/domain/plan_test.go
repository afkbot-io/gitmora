package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/recovery/domain"
)

func plan() domain.Plan {
	now := time.Now().UTC().Truncate(time.Second)
	return domain.Plan{Version: 1, ID: "11111111-1111-4111-8111-111111111111", BackupID: "22222222-2222-4222-8222-222222222222", Schema: strings.Repeat("a", 64), UserID: "33333333-3333-4333-8333-333333333333", Name: "Recovery operator", Key: domain.CredentialProof{ID: "44444444-4444-4444-8444-444444444444", Hash: strings.Repeat("b", 64), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}
}

func TestRecoveryIntentIsOwnedBoundedAndExact(t *testing.T) {
	p := plan()
	normal, e := p.Normalize(time.Now())
	if e != nil || len(normal.Fingerprint()) != 64 {
		t.Fatal("valid recovery intent rejected", e)
	}
	changed := normal
	changed.BackupID = "55555555-5555-4555-8555-555555555555"
	if changed.Fingerprint() == normal.Fingerprint() {
		t.Fatal("backup identity is absent from exact intent")
	}
	changed = normal
	changed.ParentID = "55555555-5555-4555-8555-555555555555"
	if changed.Fingerprint() == normal.Fingerprint() {
		t.Fatal("previous authority epoch is absent from exact intent")
	}
	raw, e := json.Marshal(p)
	if e != nil || strings.Contains(string(raw), "wg_") || !strings.Contains(string(raw), p.Key.Hash) {
		t.Fatal("wire plan must carry a hash, never its bearer")
	}
}

func TestRecoveryPlanRejectsMalformedOrUnboundedAuthority(t *testing.T) {
	for name, mutate := range map[string]func(*domain.Plan){
		"version":  func(p *domain.Plan) { p.Version = 2 },
		"identity": func(p *domain.Plan) { p.ID = "wrong" },
		"backup":   func(p *domain.Plan) { p.BackupID = "" },
		"parent":   func(p *domain.Plan) { p.ParentID = p.ID },
		"schema":   func(p *domain.Plan) { p.Schema = strings.Repeat("A", 64) },
		"name":     func(p *domain.Plan) { p.Name = "operator\nsecret" },
		"hash":     func(p *domain.Plan) { p.Key.Hash = "wg_private" },
		"key id":   func(p *domain.Plan) { p.Key.ID = "" },
		"future":   func(p *domain.Plan) { p.Key.CreatedAt = time.Now().Add(time.Hour) },
		"expired":  func(p *domain.Plan) { p.Key.ExpiresAt = time.Now().Add(-time.Second) },
		"duration": func(p *domain.Plan) { p.Key.ExpiresAt = p.Key.CreatedAt.Add(25 * time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			p := plan()
			mutate(&p)
			if _, e := p.Normalize(time.Now()); e == nil || strings.Contains(e.Error(), "private") {
				t.Fatal("unsafe recovery plan accepted or leaked")
			}
		})
	}
}
