// Package domain defines country-scoped task projections and immutable evidence publications.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/redact"
)

var (
	ErrInvalid    = errors.New("invalid task integration input")
	ErrNotFound   = errors.New("task integration resource not found")
	ErrConflict   = errors.New("task integration identity conflict")
	ErrLease      = errors.New("task integration lease lost")
	filePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,78}\.md$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Config supplies immutable external scope and a replaceable service-account credential.
type Config struct {
	Endpoint    string `json:"endpoint"`
	CountryID   string `json:"country_id"`
	Token       string `json:"api_token"`
	AutoLinkMRs bool   `json:"auto_link_merge_requests"`
}

// Validate rejects ambiguous scope and malformed HTTP credentials before provider I/O.
func (c Config) Validate() error {
	if !identifier.Valid(c.CountryID) || len(c.Endpoint) > 2000 || !safeURL(c.Endpoint) || len(c.Token) < 8 || len(c.Token) > 4096 || strings.ContainsAny(c.Token, " \t\r\n\x00") {
		return ErrInvalid
	}
	return nil
}

// Document is a bounded Markdown source, independent of Tasktopia's world/geometry DTOs.
type Document struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

// Link is descriptive source evidence; it never establishes a GitLab commit association.
type Link struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Task retains only retrieval-relevant fields returned by task.get in an explicit country.
type Task struct {
	CountryID          string          `json:"country_id"`
	ID                 string          `json:"id"`
	Number             int64           `json:"taskNumber"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	AcceptanceCriteria string          `json:"acceptanceCriteria"`
	Status             string          `json:"status"`
	Progress           int             `json:"progress"`
	UpdatedAt          time.Time       `json:"updatedAt"`
	URL                string          `json:"url"`
	Documents          []Document      `json:"documents"`
	Links              []Link          `json:"mergeRequests"`
	Checklist          []ChecklistItem `json:"checklist,omitempty"`
	Comments           []Comment       `json:"comments,omitempty"`
	Defects            []Defect        `json:"defects,omitempty"`
	Dependencies       []Dependency    `json:"dependencies,omitempty"`
}

// NormalizeTask validates provider identities and sanitizes an owned bounded projection.
func NormalizeTask(t Task) (Task, error) {
	if !identifier.Valid(t.CountryID) || !identifier.Valid(t.ID) || t.Number < 1 || t.Progress < 0 || t.Progress > 100 || t.UpdatedAt.IsZero() || !safeURL(t.URL) || !text(t.Title, 512, true) || !text(t.Description, 64000, false) || !text(t.AcceptanceCriteria, 64000, false) || len(t.Documents) > 128 || len(t.Links) > 256 {
		return Task{}, ErrInvalid
	}
	switch t.Status {
	case "PLANNING", "STARTED", "IN_PROGRESS", "TESTING", "COMPLETED":
	default:
		return Task{}, ErrInvalid
	}
	t.Documents = slices.Clone(t.Documents)
	t.Links = slices.Clone(t.Links)
	t.Title = redact.Text(t.Title)
	t.Description = redact.Text(t.Description)
	t.AcceptanceCriteria = redact.Text(t.AcceptanceCriteria)
	t.URL = sanitizedURL(t.URL)
	t.UpdatedAt = t.UpdatedAt.UTC()
	if !safeURL(t.URL) || !text(t.Title, 512, true) || !text(t.Description, 64000, false) || !text(t.AcceptanceCriteria, 64000, false) {
		return Task{}, ErrInvalid
	}
	seen := map[string]bool{}
	for i, d := range t.Documents {
		if !identifier.Valid(d.ID) || !filePattern.MatchString(d.FileName) || seen[d.FileName] || !text(d.Title, 400, true) || !text(d.Content, 256000, false) {
			return Task{}, ErrInvalid
		}
		seen[d.FileName] = true
		d.Title = redact.Text(d.Title)
		d.Content = redact.Text(d.Content)
		if !text(d.Title, 400, true) || !text(d.Content, 256000, false) {
			return Task{}, ErrInvalid
		}
		t.Documents[i] = d
	}
	for i, l := range t.Links {
		if !safeURL(l.URL) || !text(l.Title, 800, false) {
			return Task{}, ErrInvalid
		}
		l.Title = redact.Text(l.Title)
		l.URL = sanitizedURL(l.URL)
		if !safeURL(l.URL) || !text(l.Title, 800, false) {
			return Task{}, ErrInvalid
		}
		t.Links[i] = l
	}
	if err := normalizeContext(&t); err != nil {
		return Task{}, err
	}
	raw, err := json.Marshal(t)
	if err != nil || len(raw) > 2*1024*1024 {
		return Task{}, ErrInvalid
	}
	return t, nil
}

// Digest identifies the exact sanitized projection returned to local readers.
func (t Task) Digest() string {
	raw, _ := json.Marshal(t)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func text(s string, max int, required bool) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0) && (!required || strings.TrimSpace(s) != "")
}
func safeURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && len(s) <= 2000 && u.Host != "" && u.User == nil && u.Opaque == "" && (u.Scheme == "https" || u.Scheme == "http") && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

func sanitizedURL(s string) string {
	u, _ := url.Parse(s)
	u.Fragment = ""
	u.RawFragment = ""
	query := u.Query()
	for key, values := range query {
		lower := strings.ToLower(key)
		sensitive := false
		for _, part := range []string{"token", "password", "passwd", "secret", "authorization", "credential", "signature", "api_key", "apikey", "session"} {
			sensitive = sensitive || strings.Contains(lower, part)
		}
		if sensitive {
			query.Del(key)
			continue
		}
		for i, v := range values {
			values[i] = redact.Text(v)
		}
		query[key] = values
	}
	u.RawQuery = query.Encode()
	u.Path = redact.Text(u.Path)
	u.RawPath = ""
	return u.String()
}

// CaptureEvidence is the compact read-model boundary imported from stored capture evidence.
// It contains no transcript, agent instructions, or mutable capture aggregate.
type CaptureEvidence struct {
	RepositoryID, SessionID, CountryID, TaskID, Digest string
	Revision                                           int64
	Commits                                            []string
}

// Evidence binds a publication to an exact stored revision and independently verified commits.
type Evidence struct {
	ID        string
	Capture   CaptureEvidence
	Confirmed []string
}

// EvidenceDocument is a dedicated append-only export; it never updates a task's standard documents.
type EvidenceDocument struct {
	FileName string `json:"fileName"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

// NewEvidenceDocument renders deterministic metadata, avoiding transcript or model-generated claims.
func NewEvidenceDocument(e Evidence) (EvidenceDocument, error) {
	s := e.Capture
	if !identifier.Valid(e.ID) || !identifier.Valid(s.RepositoryID) || !identifier.Valid(s.SessionID) || s.Revision < 1 || !identifier.Valid(s.CountryID) || !identifier.Valid(s.TaskID) || !digestPattern.MatchString(s.Digest) || len(e.Confirmed) == 0 || len(e.Confirmed) > 2000 {
		return EvidenceDocument{}, ErrInvalid
	}
	commits := slices.Clone(e.Confirmed)
	slices.Sort(commits)
	commits = slices.Compact(commits)
	for _, sha := range commits {
		if !identifier.ValidCommit(sha) || !slices.Contains(s.Commits, sha) {
			return EvidenceDocument{}, ErrInvalid
		}
	}
	content := fmt.Sprintf("# Workgraph evidence\n\nRepository: `%s`\n\nSession: `%s`\n\nRevision: %d\n\nCapture digest: `%s`\n\nCountry: `%s`\n\nTask: `%s`\n\nProvider-confirmed commits (%d):\n\n", s.RepositoryID, s.SessionID, s.Revision, s.Digest, s.CountryID, s.TaskID, len(commits))
	for i, sha := range commits {
		if i == 256 {
			content += "\nAdditional confirmed commits omitted from this compact report. Expand the exact capture revision in Workgraph.\n"
			break
		}
		content += "- `" + sha + "`\n"
	}
	content += "\nThis records capture provenance and commit existence. It does not establish test success, acceptance, deployment, or task completion.\n"
	return EvidenceDocument{"workgraph-evidence-" + e.ID + ".md", "Workgraph evidence", content}, nil
}
