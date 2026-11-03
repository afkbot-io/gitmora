package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/retrieval/application"
	"workgraph.local/platform/internal/retrieval/domain"
)

// Synthetic vectors test plumbing, scopes and pagination; real-model quality is
// measured separately by scripts/evaluate-search.py against an explicit corpus.
type testEmbeddings struct {
	calls   int
	down    bool
	changed bool
}

func (*testEmbeddings) Profile() string { return strings.Repeat("a", 64) }
func (m *testEmbeddings) Embed(_ context.Context, texts []string, query bool) ([][]float32, error) {
	m.calls++
	if m.down {
		return nil, domain.ErrUnavailable
	}
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vectors[i] = []float32{0, 1}
		if query || strings.Contains(text, "renewal") {
			vectors[i] = []float32{1, 0}
		}
		if m.changed {
			vectors[i] = []float32{1, 1}
		}
	}
	return vectors, nil
}

func TestHybridSearchKeepsCommitWindowsPagesAndReauthorization(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	snap := snapshot()
	snap.Commits = []string{a, b}
	snap.Messages = nil
	for i := 0; i < 45; i++ {
		model := "alpha"
		if i >= 30 {
			model = "beta"
		}
		snap.Messages = append(snap.Messages, capture.Message{Role: "tool", ToolName: "edit", Text: fmt.Sprintf("renewal cache policy %d", i), Model: model, Changes: []capture.FileChange{{Path: "src/auth.go"}}})
	}
	snap.CommitRanges = []capture.CommitRange{{CommitSHA: a, Start: 0, End: 30, Basis: "observed_checkpoint"}, {CommitSHA: b, Start: 30, End: 45, Basis: "observed_checkpoint"}}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commits SET confirmed_at=now()`); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	model := &testEmbeddings{}
	if _, err := application.NewIndexer(store).ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	worker := application.NewEmbeddingIndexer(store, model)
	for i := 0; i < 30; i++ {
		worked, err := worker.ReconcileOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
		if i == 29 {
			t.Fatal("embedding queue did not finish")
		}
	}
	service := application.NewService(store).WithEmbeddings(model)
	p := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	q := domain.Query{ProjectID: project, RepositoryID: repository, Text: "продление входа", CommitSHA: a, Model: "alpha", FilePath: "src/auth.go", Limit: 20}
	var lateID string
	if err := pool.QueryRow(ctx, `SELECT min(d.id) FROM retrieval_chunks d WHERE d.metadata->>'model'='alpha' AND d.kind='session'`).Scan(&lateID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM retrieval_embeddings WHERE profile=$1 AND chunk_id=$2`, model.Profile(), lateID); err != nil {
		t.Fatal(err)
	}
	late, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Rollback(ctx)
	if _, err := late.Exec(ctx, `SELECT transaction_timestamp()`); err != nil {
		t.Fatal(err)
	}
	first, err := service.Search(ctx, p, q)
	if err != nil || len(first.Items) != 20 || first.Next == nil || first.SearchMode != "hybrid" || first.SemanticPending != 1 {
		t.Fatal(first, err)
	}
	seen := map[string]bool{}
	for _, h := range first.Items {
		seen[h.ID] = true
		if h.Metadata.Model != "alpha" {
			t.Fatal("wrong commit model")
		}
	}
	// The embedding transaction began before the first read but commits afterward.
	// A timestamp cutoff alone would admit it and change RRF ranks on later pages.
	if _, err := late.Exec(ctx, `INSERT INTO retrieval_embeddings(profile,chunk_id,embedding,completed_at) VALUES($1,$2,'[1,0]'::public.vector,transaction_timestamp())`, model.Profile(), lateID); err != nil {
		t.Fatal(err)
	}
	if err := late.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	q.After = first.Next
	resume := q
	last, err := service.Search(ctx, p, q)
	if err != nil || len(last.Items) != 9 || last.Next != nil {
		t.Fatal(last, err)
	}
	for _, h := range last.Items {
		if seen[h.ID] {
			t.Fatal("duplicate semantic page")
		}
	}
	model.changed = true
	if page, err := service.Search(ctx, p, q); err != nil || len(page.Items) != 9 {
		t.Fatal("snapshot unnecessarily repeated inference", err)
	}
	model.changed = false
	model.down = true
	if page, err := service.Search(ctx, p, q); err != nil || page.SearchMode != "hybrid" || len(page.Items) != 9 {
		t.Fatal("cached page requires a running model", err)
	}
	q.After = nil
	q.Text = "renewal"
	q.CommitSHA = ""
	fallback, err := service.Search(ctx, p, q)
	if err != nil || fallback.SearchMode != "lexical" || !fallback.SemanticUnavailable || len(fallback.Items) == 0 {
		t.Fatal("lexical fallback lost", fallback, err)
	}
	q.Mode = "semantic"
	if _, err := service.Search(ctx, p, q); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("explicit semantic silently changed mode", err)
	}
	model.down = false
	q.Mode = ""
	q.Text = "продление входа"
	q.CommitSHA = b
	q.Model = "alpha"
	wrong, err := service.Search(ctx, p, q)
	if err != nil || len(wrong.Items) != 0 {
		t.Fatal("cross-commit dialogue leaked", wrong, err)
	}
	if _, err := service.Search(ctx, access.Principal{UserID: subject, Scopes: []access.Permission{access.ReadCapture}}, resume); !errors.Is(err, domain.ErrSearchExpired) {
		t.Fatal("another user read a cached candidate list", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE retrieval_search_snapshots SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(ctx, p, resume); !errors.Is(err, domain.ErrSearchExpired) {
		t.Fatal("expired candidates reused", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, reader); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(ctx, p, q); !errors.Is(err, access.ErrDenied) {
		t.Fatal("revoked semantic query allowed", err)
	}
	if _, err := service.Expand(ctx, p, domain.Expansion{EvidenceID: first.Items[0].ID}); err == nil {
		t.Fatal("revoked vector citation expanded")
	}
}

func TestEmbeddingRetryLeaseAndProjectionReplacement(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	snap := snapshot()
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	if _, err := application.NewIndexer(store).ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	model := &testEmbeddings{down: true}
	worker := application.NewEmbeddingIndexer(store, model)
	if worked, err := worker.ReconcileOnce(ctx); !errors.Is(err, domain.ErrUnavailable) || !worked {
		t.Fatal(worked, err)
	}
	var failed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM retrieval_embeddings WHERE last_error_code='embedding_unavailable' AND completed_at IS NULL`).Scan(&failed); err != nil || failed == 0 {
		t.Fatal(failed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE retrieval_embeddings SET available_at=now()`); err != nil {
		t.Fatal(err)
	}
	batch, err := store.ClaimEmbeddings(ctx, model.Profile())
	if err != nil || len(batch) == 0 {
		t.Fatal(batch, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE retrieval_embeddings SET lease_until=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	vectors := make([][]float32, len(batch))
	for i := range vectors {
		vectors[i] = []float32{1, 0}
	}
	if err := store.FinishEmbeddings(ctx, model.Profile(), batch, vectors, ""); !errors.Is(err, domain.ErrLease) {
		t.Fatal("stale embedding writer accepted", err)
	}
	model.down = false
	if _, err := worker.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM retrieval_chunks`); err != nil {
		t.Fatal(err)
	}
	if count(t, pool, "retrieval_embeddings") != 0 {
		t.Fatal("derived vectors survived source projection deletion")
	}
}

func TestSemanticRankingAppliesAccessBeforeCandidateWindow(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	foreignProject := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	if _, err := pool.Exec(ctx, `INSERT INTO projects(id,name) VALUES($1,'Private');`, foreignProject); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(project_id,user_id,role) VALUES($1,$2,'writer')`, foreignProject, subject); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$2,'https://gitlab.internal',99,'Private')`, otherRepository, foreignProject); err != nil {
		t.Fatal(err)
	}
	visible := snapshot()
	visible.Messages = []capture.Message{{Role: "assistant", Text: "An authorized decision to preserve the browser login."}}
	captures := captureapp.NewService(postgres.NewCaptureStore(pool))
	if _, err := captures.Ingest(ctx, subject, visible); err != nil {
		t.Fatal(err)
	}
	private := visible
	private.RepositoryID = otherRepository
	private.Messages = nil
	for i := 0; i < 210; i++ {
		private.Messages = append(private.Messages, capture.Message{Role: "assistant", Text: fmt.Sprintf("Private unrelated record %d", i)})
	}
	if _, err := captures.Ingest(ctx, subject, private); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	for i := 0; i < 2; i++ {
		if _, err := application.NewIndexer(store).ReconcileOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	model := &testEmbeddings{}
	if _, err := pool.Exec(ctx, `INSERT INTO retrieval_embeddings(profile,chunk_id,embedding,completed_at) SELECT $1,id,'[1,0]'::public.vector,now() FROM retrieval_chunks`, model.Profile()); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store).WithEmbeddings(model)
	p := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	result, err := service.Search(ctx, p, domain.Query{ProjectID: project, Text: "meaning without lexical overlap", Mode: "semantic", Limit: 1})
	if err != nil || len(result.Items) != 1 || result.Items[0].Source.RepositoryID != repository {
		t.Fatal("foreign candidates displaced authorized evidence", result, err)
	}
}

func TestEmbeddingReuseKeepsEachOriginalProducer(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewRetrievalStore(pool)
	model := &testEmbeddings{}
	worker := application.NewEmbeddingIndexer(store, model)
	captures := captureapp.NewService(postgres.NewCaptureStore(pool))
	snap := snapshot()
	snap.Messages = []capture.Message{{Role: "assistant", Text: "renewal identical retained text", Model: "alpha"}}
	if _, err := captures.Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewIndexer(store).ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	calls := model.calls
	snap.SessionID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	snap.Messages[0].Model = "beta"
	if _, err := captures.Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewIndexer(store).ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if model.calls != calls {
		t.Fatal("identical project content inferred twice")
	}
	result, err := application.NewService(store).WithEmbeddings(model).Search(ctx, access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}, domain.Query{ProjectID: project, Text: "meaning", Model: "beta", Mode: "semantic"})
	if err != nil || len(result.Items) != 1 || result.Items[0].Source.SessionID != snap.SessionID || result.Items[0].Metadata.Model != "beta" {
		t.Fatal("vector reuse borrowed another source's producer", result, err)
	}
}
