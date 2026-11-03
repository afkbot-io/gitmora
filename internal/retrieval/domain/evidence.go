// Package domain defines bounded retrieval queries and exact immutable evidence citations.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"workgraph.local/platform/internal/identifier"
)

var (
	ErrInvalid       = errors.New("invalid retrieval input")
	ErrNotFound      = errors.New("evidence not found or unavailable")
	ErrLease         = errors.New("retrieval lease lost")
	ErrAmbiguous     = errors.New("commit prefix matches multiple confirmed commits; provide a longer SHA")
	ErrUnavailable   = errors.New("semantic search unavailable; retry or use lexical mode")
	ErrSearchExpired = errors.New("search results expired; start a new search")
)

// Source identifies original immutable evidence independently of indexing time or ranking.
type Source struct {
	Kind         string `json:"kind"`
	ProjectID    string `json:"project_id"`
	RepositoryID string `json:"repository_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	Revision     int64  `json:"revision,omitempty"`
	Digest       string `json:"digest"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
	BlobID       string `json:"blob_id,omitempty"`
}

// Validate prevents a citation from ambiguously mixing two source authorities.
func (s Source) Validate() error {
	if !identifier.Valid(s.ProjectID) || !Digest(s.Digest) {
		return ErrInvalid
	}
	switch s.Kind {
	case "capture":
		if s.CommitSHA != "" || s.FilePath != "" || s.BlobID != "" || !identifier.Valid(s.RepositoryID) || !identifier.Valid(s.SessionID) || s.Revision < 1 || s.Revision > 1_000_000_000 {
			return ErrInvalid
		}
	case "code":
		if !identifier.Valid(s.RepositoryID) || !identifier.ValidCommit(s.CommitSHA) || !identifier.ValidCommit(s.BlobID) || !identifier.ValidGitPath(s.FilePath) || s.SessionID != "" || s.Revision != 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// Chunk is an exact Unicode-rune range in one source part, not a model interpretation.
type Chunk struct {
	ID            string   `json:"id"`
	Source        Source   `json:"source"`
	Kind          string   `json:"kind"`
	Part          string   `json:"part"`
	Start         int      `json:"start"`
	End           int      `json:"end"`
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	ContentDigest string   `json:"content_digest"`
	LineStart     int      `json:"line_start,omitempty"`
	LineEnd       int      `json:"line_end,omitempty"`
	Metadata      Metadata `json:"metadata"`
	FilePaths     []string `json:"file_paths,omitempty"`
	FilePathCount int      `json:"file_path_count,omitempty"`
}

const chunkRunes = 2048
const overlapRunes = 128

// Chunks deterministically partitions a sanitized source while preserving exact citation offsets.
func Chunks(source Source, kind, part, title, content string) ([]Chunk, error) {
	if source.Validate() != nil || !slices.Contains([]string{"session", "code_reference", "code"}, kind) || !validText(part, 200, true) || !validText(title, 2048, true) || !validText(content, 2*1024*1024, false) {
		return nil, ErrInvalid
	}
	if source.Kind == "capture" && (kind != "session" && kind != "code_reference") || source.Kind == "code" && kind != "code" {
		return nil, ErrInvalid
	}
	result := []Chunk{}
	runes := []rune(content)
	lineStart := 1
	for start := 0; start < len(runes); {
		end := min(start+chunkRunes, len(runes))
		body := string(runes[start:end])
		if strings.TrimSpace(body) != "" {
			identity := struct {
				Version    int
				Source     Source
				Kind, Part string
				Start, End int
			}{1, source, kind, part, start, end}
			raw, _ := json.Marshal(identity)
			result = append(result, Chunk{ID: Hash(raw), Source: source, Kind: kind, Part: part, Start: start, End: end, Title: title, Content: body, ContentDigest: Hash([]byte(body))})
			if kind == "code" {
				c := &result[len(result)-1]
				c.LineStart = lineStart
				c.LineEnd = lineStart + strings.Count(string(runes[start:end-1]), "\n")
			}
		}
		if end == len(runes) {
			break
		}
		nextStart := end - overlapRunes
		if kind == "code" {
			lineStart += strings.Count(string(runes[start:nextStart]), "\n")
		}
		start = nextStart
	}
	return result, nil
}

// Hash identifies immutable bytes; neither it nor an evidence ID grants access.
func Hash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// Digest accepts canonical SHA-256 text identifiers.
func Digest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}
func validText(s string, max int, required bool) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0) && (!required || strings.TrimSpace(s) != "")
}

// Query combines plain lexical terms with explicit scope and source-reported metadata filters.
type Query struct {
	Snapshot        []SearchPosition `json:"-"`
	Mode            string           `json:"mode,omitempty"`
	Engine          string           `json:"-"`
	Vector          []float32        `json:"-"`
	SemanticProfile string           `json:"-"`
	VectorDigest    string           `json:"-"`
	ProjectID       string           `json:"project_id"`
	RepositoryID    string           `json:"repository_id,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	Text            string           `json:"text,omitempty"`
	CommitSHA       string           `json:"commit_sha,omitempty"`
	CommitPrefix    string           `json:"commit_prefix,omitempty"`
	FilePath        string           `json:"file_path,omitempty"`
	Kinds           []string         `json:"kinds,omitempty"`
	Limit           int              `json:"limit,omitempty"`
	History         bool             `json:"history,omitempty"`
	Terms           string           `json:"-"`
	Agent           string           `json:"agent,omitempty"`
	Model           string           `json:"model,omitempty"`
	Role            string           `json:"role,omitempty"`
	After           *SearchCursor    `json:"after,omitempty"`
	ReadAt          time.Time        `json:"-"`
	PrefixText      string           `json:"-"`
}

// SearchPosition freezes candidate scores without caching source text or access rights.
type SearchPosition struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// NormalizeQuery bounds work and treats a full SHA as an exact confirmed-relation filter.
func NormalizeQuery(q Query) (Query, error) {
	if !slices.Contains([]string{"", "auto", "lexical", "hybrid", "semantic"}, q.Mode) {
		return Query{}, ErrInvalid
	}
	if q.Mode == "auto" {
		q.Mode = ""
	}
	if q.SessionID != "" && !identifier.Valid(q.SessionID) {
		return Query{}, ErrInvalid
	}
	if !identifier.Valid(q.ProjectID) || q.RepositoryID != "" && !identifier.Valid(q.RepositoryID) || !validText(q.Text, 512, false) || q.Limit < 0 || q.Limit > 50 || q.CommitSHA != "" && !identifier.ValidCommit(q.CommitSHA) || len(q.Kinds) > 6 {
		return Query{}, ErrInvalid
	}
	q.Text = strings.TrimSpace(q.Text)
	q.CommitPrefix = strings.ToLower(strings.TrimSpace(q.CommitPrefix))
	if q.CommitPrefix != "" && (q.CommitSHA != "" || !CommitPrefix(q.CommitPrefix)) {
		return Query{}, ErrInvalid
	}
	var err error
	if q.Agent, err = cleanFilter(q.Agent, 120); err != nil {
		return Query{}, err
	}
	if q.Model, err = cleanFilter(q.Model, 240); err != nil {
		return Query{}, err
	}
	if !slices.Contains([]string{"", "user", "assistant", "tool", "system"}, q.Role) {
		return Query{}, ErrInvalid
	}
	if identifier.Valid(strings.ToLower(q.Text)) {
		q.Text = strings.ToLower(q.Text)
		if q.SessionID != "" && q.SessionID != q.Text {
			return Query{}, ErrInvalid
		}
		q.SessionID = q.Text
		q.Text = ""
	}
	if identifier.ValidCommit(strings.ToLower(q.Text)) {
		q.Text = strings.ToLower(q.Text)
		if q.CommitPrefix != "" {
			return Query{}, ErrInvalid
		}
		if q.CommitSHA != "" && q.CommitSHA != q.Text {
			return Query{}, ErrInvalid
		}
		q.CommitSHA = q.Text
		q.Text = ""
	}
	if CommitPrefix(strings.ToLower(q.Text)) {
		if q.CommitSHA != "" || (q.CommitPrefix != "" && q.CommitPrefix != strings.ToLower(q.Text)) {
			return Query{}, ErrInvalid
		}
		q.CommitPrefix = strings.ToLower(q.Text)
		q.PrefixText = q.CommitPrefix
		q.Text = ""
	}
	if q.Text == "" && q.CommitSHA == "" && q.CommitPrefix == "" && q.SessionID == "" && q.Agent == "" && q.Model == "" && q.Role == "" && q.FilePath == "" {
		return Query{}, ErrInvalid
	}
	if q.FilePath != "" && (!identifier.ValidGitPath(q.FilePath) || q.RepositoryID == "") {
		return Query{}, ErrInvalid
	}
	q.Kinds = slices.Clone(q.Kinds)
	slices.Sort(q.Kinds)
	q.Kinds = slices.Compact(q.Kinds)
	for _, kind := range q.Kinds {
		if !slices.Contains([]string{"session", "code_reference", "code"}, kind) {
			return Query{}, ErrInvalid
		}
	}
	if q.Limit == 0 {
		q.Limit = 12
	}
	q.Terms = Terms(q.Text)
	if q.Terms == "" && q.CommitSHA == "" && q.CommitPrefix == "" && q.SessionID == "" && q.Agent == "" && q.Model == "" && q.Role == "" && q.FilePath == "" {
		return Query{}, ErrInvalid
	}
	if q.After != nil && q.After.Validate(q) != nil {
		return Query{}, ErrInvalid
	}
	return q, nil
}

// Terms preserves Unicode words and splits code identifiers/paths before PostgreSQL stemming.
func Terms(s string) string {
	runes := []rune(s)
	var out strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) || i+1 < len(runes) && unicode.IsUpper(runes[i-1]) && unicode.IsLower(runes[i+1])) {
			out.WriteRune(' ')
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		} else {
			out.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// Hit is a compact untrusted source excerpt with explicit freshness and verification provenance.
type Hit struct {
	Chunk
	Current          bool      `json:"current"`
	ObservedAt       time.Time `json:"observed_at"`
	IndexedAt        time.Time `json:"indexed_at"`
	Score            float64   `json:"score"`
	ConfirmedCommits []string  `json:"confirmed_commits"`
	ConfirmedCount   int       `json:"confirmed_count"`
}

// Result bounds retrieval without implying stable pagination while indexing changes.
type Result struct {
	SearchMode          string        `json:"search_mode"`
	SemanticProfile     string        `json:"semantic_profile,omitempty"`
	SemanticPending     int           `json:"semantic_pending,omitempty"`
	SemanticFailed      int           `json:"semantic_failed,omitempty"`
	CandidateWindow     int           `json:"candidate_window,omitempty"`
	SemanticUnavailable bool          `json:"semantic_unavailable,omitempty"`
	Items               []Hit         `json:"items"`
	HasMore             bool          `json:"has_more"`
	PendingSources      int           `json:"pending_sources_in_project"`
	FailedSources       int           `json:"failed_sources_in_project"`
	Next                *SearchCursor `json:"next,omitempty"`
	ResolvedCommitSHA   string        `json:"resolved_commit_sha,omitempty"`
}

// Expansion requests a bounded range inside an exactly identified evidence chunk.
type Expansion struct {
	EvidenceID string `json:"evidence_id"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// Expanded carries exact source offsets and a relative continuation within the same evidence ID.
type Expanded struct {
	Hit
	NextOffset int `json:"next_offset,omitempty"`
}

// Validate rejects excessive expansion requests before accessing a source.
func (e Expansion) Validate() error {
	if !Digest(e.EvidenceID) || e.Offset < 0 || e.Offset > chunkRunes || e.Limit < 0 || e.Limit > chunkRunes {
		return ErrInvalid
	}
	return nil
}

// Expand slices Unicode runes, retains the full chunk digest and reports exact returned offsets.
func Expand(c Chunk, e Expansion) (Expanded, error) {
	if e.Validate() != nil || c.ID != e.EvidenceID {
		return Expanded{}, ErrInvalid
	}
	runes := []rune(c.Content)
	if e.Offset > len(runes) {
		return Expanded{}, ErrInvalid
	}
	if e.Limit == 0 {
		e.Limit = 1200
	}
	end := min(e.Offset+e.Limit, len(runes))
	c.Content = string(runes[e.Offset:end])
	if c.Kind == "code" && end == e.Offset {
		c.LineStart = 0
		c.LineEnd = 0
	}
	if c.Kind == "code" && end > e.Offset {
		base := c.LineStart
		c.LineStart = base + strings.Count(string(runes[:e.Offset]), "\n")
		c.LineEnd = base + strings.Count(string(runes[:end-1]), "\n")
	}
	c.Start += e.Offset
	c.End = c.Start + end - e.Offset
	next := 0
	if end < len(runes) {
		next = end
	}
	return Expanded{Hit: Hit{Chunk: c}, NextOffset: next}, nil
}

// Snippet returns plain source text around a matching lexical term without HTML or model output.
func Snippet(content, terms string, maxRunes int) (string, int) {
	runes := []rune(content)
	if len(runes) <= maxRunes {
		return content, 0
	}
	start := 0
	lower := strings.ToLower(content)
	for _, term := range strings.Fields(strings.ToLower(terms)) {
		if p := strings.Index(lower, term); p >= 0 {
			start = max(0, utf8.RuneCountInString(lower[:p])-80)
			break
		}
	}
	if start+maxRunes > len(runes) {
		start = len(runes) - maxRunes
	}
	return string(runes[start : start+maxRunes]), start
}

// CommitPrefix accepts bounded Git abbreviations; the store resolves only unique confirmed objects.
func CommitPrefix(s string) bool {
	if len(s) < 7 || len(s) > 63 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
