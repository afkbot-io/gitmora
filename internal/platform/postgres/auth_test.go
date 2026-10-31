package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/capture/application"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/transport/httpapi"
)

func TestHashedCredentialsExpireRevokeAndRespectDisabledIdentity(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewCredentialStore(pool)
	issued, err := access.IssueCredential(subject, "Test", []access.Permission{access.ReadCapture}, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, issued.Record); err != nil {
		t.Fatal(err)
	}
	principal, err := store.Authenticate(ctx, issued.Secret)
	if err != nil || principal.UserID != subject || !principal.Has(access.ReadCapture) || principal.Has(access.WriteCapture) {
		t.Fatalf("invalid principal: %+v %v", principal, err)
	}
	var storedHash string
	if err := pool.QueryRow(ctx, "SELECT secret_hash FROM api_tokens WHERE id=$1", issued.Record.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == issued.Secret || strings.Contains(storedHash, "wg_") {
		t.Fatal("raw credential persisted")
	}
	if err := store.Revoke(ctx, reader, issued.Record.ID); !errors.Is(err, access.ErrDenied) {
		t.Fatal("other user revoked credential")
	}
	if err := store.Revoke(ctx, subject, issued.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, issued.Secret); !errors.Is(err, access.ErrUnauthenticated) {
		t.Fatal("revoked credential accepted")
	}
	expired, err := access.IssueCredential(subject, "Expired", []access.Permission{access.ReadCapture}, time.Now().Add(-2*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, expired.Record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, expired.Secret); !errors.Is(err, access.ErrUnauthenticated) {
		t.Fatal("expired credential accepted")
	}
	active, err := access.IssueCredential(subject, "Active", []access.Permission{access.ReadCapture}, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, active.Record); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET enabled=false WHERE id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, active.Secret); !errors.Is(err, access.ErrUnauthenticated) {
		t.Fatal("disabled identity authenticated")
	}
}

func TestHTTPUploadReadConflictAndRevocationWithRealPostgres(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	tokens := postgres.NewCredentialStore(pool)
	issued, err := access.IssueCredential(subject, "Client", []access.Permission{access.ReadCapture, access.WriteCapture}, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := tokens.Save(ctx, issued.Record); err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewHandler(httpapi.Config{Captures: application.NewService(postgres.NewCaptureStore(pool)), Authenticator: tokens, Ready: pool.Ping})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	perform := func(method, path string, body any) int {
		t.Helper()
		var raw []byte
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		request, err := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+issued.Secret)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if got := perform("POST", "/v1/captures", snapshot()); got != 201 {
		t.Fatalf("upload status=%d", got)
	}
	if got := perform("POST", "/v1/captures", snapshot()); got != 200 {
		t.Fatalf("retry status=%d", got)
	}
	changed := snapshot()
	changed.Messages[0].Text = "Different evidence"
	if got := perform("POST", "/v1/captures", changed); got != 409 {
		t.Fatalf("conflict status=%d", got)
	}
	path := "/v1/repositories/" + repository + "/sessions/" + snapshot().SessionID
	if got := perform("GET", path, nil); got != 200 {
		t.Fatalf("read status=%d", got)
	}
	if err := tokens.Revoke(ctx, subject, issued.Record.ID); err != nil {
		t.Fatal(err)
	}
	if got := perform("GET", path, nil); got != 401 {
		t.Fatalf("revoked status=%d", got)
	}
}
