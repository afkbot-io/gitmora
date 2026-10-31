package deployment_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/platform/deployment"
	"workgraph.local/platform/internal/recovery/domain"
)

func TestRecoveryCandidateIsPrivateDurableCreateOnlyAndBoundToItsHash(t *testing.T) {
	ctx := context.Background()
	private := filepath.Join(t.TempDir(), "private")
	if e := os.Mkdir(private, 0o700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(private, "recovery.json")
	provenance := domain.Provenance{BackupID: "22222222-2222-4222-8222-222222222222", Schema: strings.Repeat("a", 64)}
	plan, e := deployment.PrepareRecovery(ctx, path, provenance, "Recovery", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	stat, e := os.Stat(path)
	if e != nil || stat.Mode().Perm() != 0o600 {
		t.Fatal("private candidate mode", e)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var stored struct {
		Version int         `json:"version"`
		Plan    domain.Plan `json:"plan"`
		Token   string      `json:"token"`
	}
	if e = json.Unmarshal(before, &stored); e != nil || stored.Version != 1 || stored.Plan.Fingerprint() != plan.Fingerprint() || access.CredentialHash(stored.Token) != plan.Key.Hash {
		t.Fatal("candidate does not survive process boundary", e)
	}
	if _, e = deployment.PrepareRecovery(ctx, path, provenance, "Replacement", time.Hour); e == nil {
		t.Fatal("pending candidate overwritten")
	}
	after, e := os.ReadFile(path)
	if e != nil || string(before) != string(after) {
		t.Fatal("rejected operation changed candidate", e)
	}
	if e = os.Chmod(private, 0o755); e != nil {
		t.Fatal(e)
	}
	if _, e = deployment.PrepareRecovery(ctx, filepath.Join(private, "exposed.json"), provenance, "Recovery", time.Hour); e == nil {
		t.Fatal("recovery preparation accepted an exposed parent")
	}
}
