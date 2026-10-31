package postgres_test

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	forgeapp "workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/platform/postgres"
	retrieval "workgraph.local/platform/internal/retrieval/application"
	search "workgraph.local/platform/internal/retrieval/domain"
)

func TestCodeIngestionSurvivesRestartAndBecomesAuthorizedSearchableEvidence(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject)
	snap := snapshot()
	snap.Files = []string{"src/auth.go"}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	work, found, err := store.ClaimCode(ctx)
	if err != nil || !found || work.Path != "" {
		t.Fatal(work, found, err)
	}
	page := domain.DiffPage{Files: []domain.ChangedFile{{Path: "src/auth.go"}, {Path: "link", Skip: "symlink"}}, NextPage: 2}
	if err := store.FinishCode(ctx, work, forgeapp.CodeOutcome{Diff: &page}); err != nil {
		t.Fatal(err)
	}
	// A reconstructed store continues with a pending file, then the next diff page.
	restarted := postgres.NewForgeStore(pool)
	fileWork, found, err := restarted.ClaimCode(ctx)
	if err != nil || !found || fileWork.Path != "src/auth.go" {
		t.Fatal(fileWork, found, err)
	}
	text := []byte("package auth\nvar token=\"must-never-persist\"\nfunc VerifiedRefreshToken() {}\n")
	blob := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(text))), text...))
	observation, err := domain.ObserveFile(domain.FileIdentity{RepositoryID: repository, BaseURL: fileWork.Repository.BaseURL, ProjectID: 42, CommitSHA: strings.Repeat("a", 40), Path: "src/auth.go", BlobID: fmt.Sprintf("%x", blob), Size: int64(len(text))}, text)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.FinishCode(ctx, fileWork, forgeapp.CodeOutcome{File: &observation}); err != nil {
		t.Fatal(err)
	}
	last, found, err := restarted.ClaimCode(ctx)
	if err != nil || !found || last.Page != 2 || last.Path != "" {
		t.Fatal(last, found, err)
	}
	if err := restarted.FinishCode(ctx, last, forgeapp.CodeOutcome{Diff: &domain.DiffPage{}}); err != nil {
		t.Fatal(err)
	}
	status, err := store.CodeStatus(ctx, reader, repository, strings.Repeat("a", 40))
	if err != nil || !status.DiscoveryEnded || status.Stored != 1 || status.Skipped != 1 || !status.DiffLimitsApply {
		t.Fatal(status, err)
	}
	indexer := retrieval.NewIndexer(postgres.NewRetrievalStore(pool))
	for i := 0; i < 3; i++ {
		if _, err := indexer.ReconcileOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	readerService := retrieval.NewService(postgres.NewRetrievalStore(pool))
	p := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadIntegration}}
	result, err := readerService.Search(ctx, p, search.Query{ProjectID: project, Text: "VerifiedRefreshToken", Kinds: []string{"code"}})
	if err != nil || len(result.Items) != 1 || result.Items[0].Source.CommitSHA != strings.Repeat("a", 40) || result.Items[0].Source.FilePath != "src/auth.go" || result.Items[0].LineStart < 1 || strings.Contains(result.Items[0].Content, "must-never-persist") {
		t.Fatal(result, err)
	}
	if _, err := store.ReadCode(ctx, reader, repository, strings.Repeat("a", 40), "src/auth.go"); err != nil {
		t.Fatal(err)
	}
	p.Scopes = []access.Permission{access.ReadCapture}
	if hidden, err := readerService.Search(ctx, p, search.Query{ProjectID: project, Text: "VerifiedRefreshToken", Kinds: []string{"code"}}); err != nil || len(hidden.Items) != 0 {
		t.Fatal("code leaked to capture-only token", hidden, err)
	}
	if err := store.Disconnect(ctx, subject, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCode(ctx, reader, repository, strings.Repeat("a", 40), "src/auth.go"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("disabled code source exposed", err)
	}
}

func storedCodeFixture(t *testing.T) (*forgeapp.Service, *postgres.ForgeStore, *pgxpool.Pool, domain.FileObservation) {
	t.Helper()
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject)
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	work, found, err := store.ClaimCode(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if err := store.FinishCode(ctx, work, forgeapp.CodeOutcome{Diff: &domain.DiffPage{Files: []domain.ChangedFile{{Path: "src/auth.go"}}}}); err != nil {
		t.Fatal(err)
	}
	fileWork, found, err := store.ClaimCode(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	raw := []byte("package auth\nfunc VerifiedCode() {}\n")
	blob := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(raw))), raw...))
	observed, err := domain.ObserveFile(domain.FileIdentity{RepositoryID: repository, BaseURL: fileWork.Repository.BaseURL, ProjectID: 42, CommitSHA: strings.Repeat("a", 40), Path: "src/auth.go", BlobID: fmt.Sprintf("%x", blob), Size: int64(len(raw))}, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishCode(ctx, fileWork, forgeapp.CodeOutcome{File: &observed}); err != nil {
		t.Fatal(err)
	}
	return service, store, pool, observed
}

func TestCodePermissionLeaseProbeRotationAndProviderDenial(t *testing.T) {
	service, store, pool, observed := storedCodeFixture(t)
	ctx := context.Background()
	sha := strings.Repeat("a", 40)
	_, err := pool.Exec(ctx, `UPDATE forge_connections SET code_checked_at=now()-interval '11 minutes',code_next_at=now(),code_probe_at=now()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCode(ctx, reader, repository, sha, "src/auth.go"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("expired code permission returned cached body", err)
	}
	probe, found, err := store.ClaimCode(ctx)
	if err != nil || !found || !probe.Probe {
		t.Fatal(probe, found, err)
	}
	if err := store.FinishCode(ctx, probe, forgeapp.CodeOutcome{File: &observed}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCode(ctx, reader, repository, sha, "src/auth.go"); err != nil {
		t.Fatal(err)
	}
	_, _ = pool.Exec(ctx, `UPDATE forge_connections SET code_next_at=now(),code_probe_at=now()`)
	stale, found, err := store.ClaimCode(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishCode(ctx, stale, forgeapp.CodeOutcome{File: &observed}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("rotated code worker committed", err)
	}
	if _, err := store.ReadCode(ctx, reader, repository, sha, "src/auth.go"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("old credential authority survived rotation", err)
	}
	current, found, err := store.ClaimCode(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if err := store.FinishCode(ctx, current, forgeapp.CodeOutcome{Code: "provider_unauthorized", Blocked: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCode(ctx, reader, repository, sha, "src/auth.go"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("denied provider returned cached body", err)
	}
	status, err := service.Status(ctx, reader, repository)
	if err != nil || status.Blocked {
		t.Fatal("file-scope failure blocked independent MR synchronization", status, err)
	}
}

func TestCodeLocalPathLimitIsVisibleAndDoesNotClaimCompleteDiscovery(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject)
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO forge_code_paths(repository_id,sha,path,status,reason) SELECT $1,$2,'generated/'||n::text,'skipped','unsupported_mode' FROM generate_series(1,10000) n`, repository, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	work, found, err := store.ClaimCode(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if err := store.FinishCode(ctx, work, forgeapp.CodeOutcome{Diff: &domain.DiffPage{Files: []domain.ChangedFile{{Path: "extra.go"}}, NextPage: 2}}); err != nil {
		t.Fatal(err)
	}
	status, err := store.CodeStatus(ctx, reader, repository, strings.Repeat("a", 40))
	if err != nil || !status.LocalLimit || status.DiscoveryEnded || status.Skipped != 10000 {
		t.Fatal(status, err)
	}
	if _, found, err := store.ClaimCode(ctx); err != nil || found {
		t.Fatal("limited source busy-looped", found, err)
	}
}

func TestCodeMissingFileRetriesWithoutBlockingOtherPaths(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject)
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	discovery, found, err := store.ClaimCode(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if err := store.FinishCode(ctx, discovery, forgeapp.CodeOutcome{Diff: &domain.DiffPage{Files: []domain.ChangedFile{{Path: "a.go"}, {Path: "b.go"}}}}); err != nil {
		t.Fatal(err)
	}
	first, found, err := store.ClaimCode(ctx)
	if err != nil || !found || first.Path != "a.go" {
		t.Fatal(first, found, err)
	}
	if err := store.FinishCode(ctx, first, forgeapp.CodeOutcome{File: &domain.FileObservation{Missing: true}}); err != nil {
		t.Fatal(err)
	}
	other, found, err := store.ClaimCode(ctx)
	if err != nil || !found || other.Path != "b.go" {
		t.Fatal("one missing file blocked another path", other, found, err)
	}
	if err := store.FinishCode(ctx, other, forgeapp.CodeOutcome{File: &domain.FileObservation{Skip: "too_large"}}); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE forge_code_paths SET observed_at=now()-interval '16 minutes' WHERE path='a.go'`)
	if err != nil {
		t.Fatal(err)
	}
	// The store's missing-file retry deadline is made due without waiting on a wall-clock sleep.
	_, err = pool.Exec(ctx, `UPDATE forge_code_paths SET retry_at=now() WHERE path='a.go'; UPDATE forge_connections SET code_next_at=now()`)
	if err != nil {
		t.Fatal(err)
	}
	retry, found, err := store.ClaimCode(ctx)
	if err != nil || !found || retry.Path != "a.go" {
		t.Fatal("missing file was stranded", retry, found, err)
	}
}

func TestCodeTransientFileFailureLetsOtherPathsProgress(t *testing.T) {
	_, store, pool, _ := storedCodeFixture(t)
	ctx := context.Background()
	sha := strings.Repeat("a", 40)
	_, err := pool.Exec(ctx, `UPDATE forge_code_paths SET status='pending',retry_at=now() WHERE repository_id=$1 AND sha=$2`, repository, sha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO forge_code_paths(repository_id,sha,path,status) VALUES($1,$2,'z.go','pending')`, repository, sha); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forge_connections SET code_next_at=now()`); err != nil {
		t.Fatal(err)
	}
	first, found, err := store.ClaimCode(ctx)
	if err != nil || !found || first.Path != "src/auth.go" {
		t.Fatal(first, found, err)
	}
	if err := store.FinishCode(ctx, first, forgeapp.CodeOutcome{Code: "provider_unavailable", RetryAfter: time.Minute}); err != nil {
		t.Fatal(err)
	}
	next, found, err := store.ClaimCode(ctx)
	if err != nil || !found || next.Path != "z.go" {
		t.Fatal("transient path error blocked other work", next, found, err)
	}
}

func TestCodeMissingProbeResumesDiscoveryAndRestoresExactFile(t *testing.T) {
	_, store, pool, observed := storedCodeFixture(t)
	ctx := context.Background()
	sha := strings.Repeat("a", 40)
	if _, err := pool.Exec(ctx, `UPDATE forge_connections SET code_next_at=now(),code_probe_at=now()`); err != nil {
		t.Fatal(err)
	}
	probe, found, err := store.ClaimCode(ctx)
	if err != nil || !found || !probe.Probe {
		t.Fatal(probe, found, err)
	}
	if err := store.FinishCode(ctx, probe, forgeapp.CodeOutcome{File: &domain.FileObservation{Missing: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCode(ctx, reader, repository, sha, "src/auth.go"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing probe exposed cached file", err)
	}
	if _, found, err := store.ClaimCode(ctx); err != nil || found {
		t.Fatal("missing probe ignored recovery backoff", found, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forge_code_runs SET available_at=now(); UPDATE forge_connections SET code_next_at=now()`); err != nil {
		t.Fatal(err)
	}
	diff, found, err := store.ClaimCode(ctx)
	if err != nil || !found || diff.Path != "" || diff.Probe || diff.Page != 1 {
		t.Fatal("missing probe stranded discovery", diff, found, err)
	}
	if err := store.FinishCode(ctx, diff, forgeapp.CodeOutcome{Diff: &domain.DiffPage{Files: []domain.ChangedFile{{Path: "src/auth.go"}}}}); err != nil {
		t.Fatal(err)
	}
	file, found, err := store.ClaimCode(ctx)
	if err != nil || !found || file.Path != "src/auth.go" {
		t.Fatal("rediscovery did not revive missing file", file, found, err)
	}
	if err := store.FinishCode(ctx, file, forgeapp.CodeOutcome{File: &observed}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCode(ctx, reader, repository, sha, file.Path); err != nil {
		t.Fatal("reverified immutable file remained unavailable", err)
	}
}

func TestCodeCatalogProviderChangeHidesSearchQuotesAndOldCitations(t *testing.T) {
	_, store, pool, _ := storedCodeFixture(t)
	ctx := context.Background()
	indexer := retrieval.NewIndexer(postgres.NewRetrievalStore(pool))
	for {
		found, err := indexer.ReconcileOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
	}
	readerService := retrieval.NewService(postgres.NewRetrievalStore(pool))
	p := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadIntegration}}
	query := search.Query{ProjectID: project, Text: "VerifiedCode", Kinds: []string{"code"}}
	before, err := readerService.Search(ctx, p, query)
	if err != nil || len(before.Items) != 1 {
		t.Fatal(before, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE repositories SET gitlab_project_id=43 WHERE id=$1`, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadCode(ctx, reader, repository, strings.Repeat("a", 40), "src/auth.go"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("old provider scope returned retained file", err)
	}
	if after, err := readerService.Search(ctx, p, query); err != nil || len(after.Items) != 0 {
		t.Fatal("old provider scope returned code search text", after, err)
	}
	if _, err := readerService.Expand(ctx, p, search.Expansion{EvidenceID: before.Items[0].ID}); !errors.Is(err, search.ErrNotFound) {
		t.Fatal("known citation bypassed changed provider scope", err)
	}
}
