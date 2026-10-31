package domain

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/redact"
)

// MaxRawFileBytes bounds one verified Git blob before text normalization or persistence.
const MaxRawFileBytes = 512 * 1024

// FileIdentity is provider metadata for one immutable repository/commit/path binding.
type FileIdentity struct {
	RepositoryID string `json:"repository_id"`
	BaseURL      string `json:"provider_base_url"`
	ProjectID    int64  `json:"provider_project_id"`
	CommitSHA    string `json:"commit_sha"`
	Path         string `json:"path"`
	BlobID       string `json:"blob_id"`
	Size         int64  `json:"raw_size"`
}

// Validate checks canonical identity without treating it as provider authorization.
func (f FileIdentity) Validate() error {
	u, err := url.Parse(f.BaseURL)
	if !identifier.Valid(f.RepositoryID) || !identifier.ValidCommit(f.CommitSHA) || !identifier.ValidCommit(f.BlobID) || !identifier.ValidGitPath(f.Path) || f.ProjectID < 1 || f.Size < 0 || err != nil || len(f.BaseURL) > 2000 || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ErrInvalid
	}
	if len(f.BlobID) != len(f.CommitSHA) || redact.Text(f.Path) != f.Path {
		return ErrInvalid
	}
	return nil
}

// CodeFile retains sanitized text and verifiable original-blob metadata, never original secret-bearing bytes.
type CodeFile struct {
	Identity  FileIdentity `json:"identity"`
	RawSHA256 string       `json:"raw_sha256"`
	Content   string       `json:"content"`
	Policy    string       `json:"redaction_policy"`
	Redacted  bool         `json:"redacted"`
}

// ValidateRetained rechecks bounded sanitized data when crossing persistence/read boundaries.
func (f CodeFile) ValidateRetained() error {
	hash, err := hex.DecodeString(f.RawSHA256)
	if f.Identity.Validate() != nil || f.Identity.Size > MaxRawFileBytes || f.Policy != "code-lines-v1" || err != nil || len(hash) != 32 || f.RawSHA256 != strings.ToLower(f.RawSHA256) || !utf8.ValidString(f.Content) || strings.ContainsRune(f.Content, 0) || len(f.Content) > 1024*1024 || redact.Lines(f.Content) != f.Content {
		return ErrInvalid
	}
	return nil
}

// ProjectionDigest identifies the exact sanitized immutable file projection and its original provenance.
func (f CodeFile) ProjectionDigest() string {
	raw, _ := json.Marshal(f)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// FileObservation distinguishes retained code, an unavailable path and a deliberately unsupported blob.
type FileObservation struct {
	File    *CodeFile
	Missing bool
	Skip    string
}

// ObserveFile verifies raw Git object identity before retaining a line-preserving sanitized projection.
func ObserveFile(id FileIdentity, raw []byte) (FileObservation, error) {
	if id.Validate() != nil || id.Size != int64(len(raw)) || len(raw) > MaxRawFileBytes {
		return FileObservation{}, ErrInvalid
	}
	header := []byte(fmt.Sprintf("blob %d\x00", len(raw)))
	object := append(header, raw...)
	blob := ""
	if len(id.BlobID) == 40 {
		hash := sha1.Sum(object)
		blob = hex.EncodeToString(hash[:])
	} else {
		hash := sha256.Sum256(object)
		blob = hex.EncodeToString(hash[:])
	}
	if blob != id.BlobID {
		return FileObservation{}, ErrInvalid
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return FileObservation{Skip: "binary_or_encoding"}, nil
	}
	for _, r := range string(raw) {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			return FileObservation{Skip: "binary_or_encoding"}, nil
		}
	}
	if strings.HasPrefix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "version https://git-lfs.github.com/spec/v1\n") {
		return FileObservation{Skip: "lfs_pointer"}, nil
	}
	content := redact.Lines(string(raw))
	if len(content) > 1024*1024 {
		return FileObservation{Skip: "redaction_expansion_limit"}, nil
	}
	original := sha256.Sum256(raw)
	file := &CodeFile{Identity: id, RawSHA256: hex.EncodeToString(original[:]), Content: content, Policy: "code-lines-v1", Redacted: content != string(raw)}
	return FileObservation{File: file}, nil
}

// ChangedFile is provider-reported diff membership; it is not a claim of complete repository coverage.
type ChangedFile struct {
	Path string
	Skip string
}

// DiffPage is one bounded enumeration page. GitLab diff limits can stop discovery before all changed files.
type DiffPage struct {
	Files    []ChangedFile
	NextPage int
	Missing  bool
}

// CodeRange is a bounded quote in the retained sanitized file, with original line numbers preserved.
type CodeRange struct {
	Content    string `json:"content"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	LineStart  int    `json:"line_start"`
	LineEnd    int    `json:"line_end"`
	NextOffset int    `json:"next_offset,omitempty"`
}

// SliceCode uses Unicode rune offsets and never returns an unbounded long source line.
func SliceCode(content string, offset, limit int) (CodeRange, error) {
	if offset < 0 || offset > 1024*1024 || limit < 0 || limit > 2048 {
		return CodeRange{}, ErrInvalid
	}
	runes := []rune(content)
	if offset > len(runes) {
		return CodeRange{}, ErrInvalid
	}
	if limit == 0 {
		limit = 1200
	}
	end := min(offset+limit, len(runes))
	result := CodeRange{Content: string(runes[offset:end]), Start: offset, End: end}
	if end < len(runes) {
		result.NextOffset = end
	}
	if end > offset {
		result.LineStart = 1 + strings.Count(string(runes[:offset]), "\n")
		result.LineEnd = 1 + strings.Count(string(runes[:end-1]), "\n")
	}
	return result, nil
}
