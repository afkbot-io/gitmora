package github_test

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/github"
)

func TestImmutableTreeWalkRejectsLinksAndVerifiesBlobBeforeRetention(t *testing.T) {
	text := []byte("package example\n")
	hash := sha1.Sum(append([]byte(fmt.Sprintf("blob %d%c", len(text), 0)), text...))
	blob := hex.EncodeToString(hash[:])
	tree := strings.Repeat("b", 40)
	mode := "100644"
	size := len(text)
	corrupt := false
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("write requested")
		}
		w.Header().Set("Content-Type", "application/json")
		var value any
		switch r.URL.Path {
		case "/repositories/42":
			value = map[string]any{"id": 42, "private": false, "html_url": repositoryURL, "name": "project"}
		case "/repos/example/project/git/commits/" + commitSHA:
			value = map[string]any{"sha": commitSHA, "tree": map[string]string{"sha": tree}}
		case "/repos/example/project/git/trees/" + tree:
			value = map[string]any{"sha": tree, "truncated": false, "tree": []any{map[string]any{"path": "code.go", "mode": mode, "type": "blob", "sha": blob, "size": size}}}
		case "/repos/example/project/git/blobs/" + blob:
			reads++
			content := text
			if corrupt {
				content = []byte("different bytes\n")
			}
			value = map[string]any{"sha": blob, "size": len(text), "encoding": "base64", "content": base64.StdEncoding.EncodeToString(content)}
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(value)
	}))
	defer server.Close()
	client, err := github.New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	repo := domain.Repository{ID: "11111111-1111-4111-8111-111111111111", Provider: "github", BaseURL: repositoryURL, ProjectID: 42}
	value, err := client.CodeFile(context.Background(), repo, "", commitSHA, "code.go")
	if err != nil || value.File == nil || value.File.Content != string(text) {
		t.Fatal(value, err)
	}
	mode = "120000"
	before := reads
	value, err = client.CodeFile(context.Background(), repo, "", commitSHA, "code.go")
	if err != nil || value.Skip != "symlink" || reads != before {
		t.Fatal("symlink followed", value, err)
	}
	mode = "100644"
	size = domain.MaxRawFileBytes + 1
	value, err = client.CodeFile(context.Background(), repo, "", commitSHA, "code.go")
	if err != nil || value.Skip != "too_large" || reads != before {
		t.Fatal("oversized blob downloaded", value, err)
	}
	size = len(text)
	corrupt = true
	if _, err = client.CodeFile(context.Background(), repo, "", commitSHA, "code.go"); err == nil {
		t.Fatal("corrupt raw content retained")
	}
}
