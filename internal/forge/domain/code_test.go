package domain_test

import (
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"workgraph.local/platform/internal/forge/domain"
)

func codeIdentity(raw []byte) domain.FileIdentity {
	hash := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(raw))), raw...))
	return domain.FileIdentity{RepositoryID: "11111111-1111-4111-8111-111111111111", BaseURL: "https://gitlab.internal", ProjectID: 42, CommitSHA: strings.Repeat("a", 40), Path: "src/auth/refresh.go", BlobID: fmt.Sprintf("%x", hash), Size: int64(len(raw))}
}
func TestCodeSnapshotVerifiesRawGitObjectAndPreservesRedactedLines(t *testing.T) {
	raw := []byte("package auth\nvar token = \"must-never-persist\"\n-----BEGIN PRIVATE KEY-----\nprivate-value\n-----END PRIVATE KEY-----\nfunc RefreshToken() {}\n")
	result, err := domain.ObserveFile(codeIdentity(raw), raw)
	if err != nil || result.File == nil || result.Skip != "" {
		t.Fatal(result, err)
	}
	file := result.File
	if strings.Contains(file.Content, "must-never-persist") || strings.Contains(file.Content, "private-value") {
		t.Fatal("credential persisted")
	}
	if strings.Count(file.Content, "\n") != strings.Count(string(raw), "\n") || !file.Redacted || file.RawSHA256 == file.ProjectionDigest() {
		t.Fatal("redaction provenance lost")
	}
	if file.Identity.CommitSHA != strings.Repeat("a", 40) || file.Identity.Path != "src/auth/refresh.go" || file.Policy != "code-lines-v1" {
		t.Fatal(file)
	}
	retry, _ := domain.ObserveFile(codeIdentity(raw), raw)
	if retry.File.ProjectionDigest() != file.ProjectionDigest() {
		t.Fatal("unstable projection")
	}
	wrong := codeIdentity(raw)
	wrong.BlobID = strings.Repeat("b", 40)
	if _, err := domain.ObserveFile(wrong, raw); err == nil {
		t.Fatal("wrong raw blob accepted")
	}
	wrong = codeIdentity(raw)
	wrong.Size++
	if _, err := domain.ObserveFile(wrong, raw); err == nil {
		t.Fatal("wrong raw size accepted")
	}
}
func TestCodeUnsupportedFilesAreExplicitWithoutPretendingTheyAreText(t *testing.T) {
	for _, raw := range [][]byte{{0, 1, 2}, {0xff, 0xfe, 0x61, 0}, []byte("version https://git-lfs.github.com/spec/v1\noid sha256:abcdef\nsize 2000\n")} {
		result, err := domain.ObserveFile(codeIdentity(raw), raw)
		if err != nil || result.File != nil || result.Skip == "" {
			t.Fatal("unsupported source presented as code", result, err)
		}
	}
	for _, path := range []string{"../secret", "/etc/passwd", "a/../b", ".git/config", "a\\b"} {
		id := codeIdentity([]byte("x"))
		id.Path = path
		if _, err := domain.ObserveFile(id, []byte("x")); err == nil {
			t.Fatal("unsafe Git path accepted", path)
		}
	}
}

func TestQuotedSecretInCallProducesValidRetainedCode(t *testing.T) {
	raw := []byte("connect(password=\"must-never-persist\").call()\nnext()\n")
	observed, err := domain.ObserveFile(codeIdentity(raw), raw)
	if err != nil || observed.File == nil {
		t.Fatal("valid Git blob was rejected", err)
	}
	if err := observed.File.ValidateRetained(); err != nil {
		t.Fatal("observed code cannot cross the persistence boundary", err)
	}
	if strings.Contains(observed.File.Content, "must-never-persist") || strings.Count(observed.File.Content, "\n") != 2 {
		t.Fatal("secret removal or original citation lines were lost")
	}
}

func TestCodeSHA256AndRuneQuotesKeepOriginalLineNumbers(t *testing.T) {
	raw := []byte("// Авторизация🧪\nfunc Refresh(){}\n")
	id := codeIdentity(raw)
	id.CommitSHA = strings.Repeat("a", 64)
	hash := sha256.Sum256(append([]byte(fmt.Sprintf("blob %d\x00", len(raw))), raw...))
	id.BlobID = fmt.Sprintf("%x", hash)
	observed, err := domain.ObserveFile(id, raw)
	if err != nil || observed.File == nil {
		t.Fatal(observed, err)
	}
	offset := len([]rune("// Авторизация🧪\n"))
	quote, err := domain.SliceCode(observed.File.Content, offset, 6)
	if err != nil || quote.Content != "func R" || quote.LineStart != 2 || quote.LineEnd != 2 || quote.NextOffset != offset+6 {
		t.Fatal(quote, err)
	}
	if _, err = domain.SliceCode(observed.File.Content, offset, 2049); err == nil {
		t.Fatal("unbounded code quote accepted")
	}
}
