package domain

import (
	"cmp"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"workgraph.local/platform/internal/redact"
)

var importDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var importRef = regexp.MustCompile(`^refs/[A-Za-z0-9._/-]{1,240}$`)
var sessionClaimKey = regexp.MustCompile(`^s_[0-9a-f]{14}::t_[0-9a-f]{14}$`)
var legacyClaimKey = regexp.MustCompile(`^(?:[0-9a-f]{7}|[0-9a-f]{16})$`)
var humanClaimKey = regexp.MustCompile(`^h_[0-9a-f]{14}$`)
var entireCheckpointID = regexp.MustCompile(`^(?:[0-9a-f]{12}|[0-7][0-9ABCDEFGHJKMNPQRSTVWXYZ]{25})$`)

// SourceAgent retains an external agent identity without mapping it to a platform account.
type SourceAgent struct {
	Tool      string `json:"tool"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
}

// LineClaim is source-reported provenance at TargetCommit, never verified authorship.
// Missing ranges carry no human/AI classification.
type LineClaim struct {
	Path   string       `json:"path"`
	Start  int          `json:"start"`
	End    int          `json:"end"`
	Key    string       `json:"key"`
	Kind   string       `json:"kind"`
	Agent  *SourceAgent `json:"agent,omitempty"`
	Author string       `json:"author,omitempty"`
}

// ImportOrigin identifies one bounded projection of immutable external Git evidence.
// SourceDigest hashes acquired bytes before redaction; it conveys identity, not authority.
type ImportOrigin struct {
	System              string       `json:"system"`
	Format              string       `json:"format"`
	ProducerVersion     string       `json:"producer_version,omitempty"`
	SourceID            string       `json:"source_id"`
	SourceSessionID     string       `json:"source_session_id,omitempty"`
	Agent               *SourceAgent `json:"agent,omitempty"`
	ObjectID            string       `json:"object_id"`
	Ref                 string       `json:"ref"`
	RefObject           string       `json:"ref_object"`
	SourceDigest        string       `json:"source_digest"`
	TargetCommit        string       `json:"target_commit"`
	BaseCommit          string       `json:"base_commit,omitempty"`
	Part                int          `json:"part"`
	Parts               int          `json:"parts"`
	LineStart           int          `json:"line_start,omitempty"`
	LineEnd             int          `json:"line_end,omitempty"`
	SourceOffset        int          `json:"source_offset,omitempty"`
	TimeKind            string       `json:"time_kind"`
	OmittedBlocks       int          `json:"omitted_blocks,omitempty"`
	ExternalTranscripts bool         `json:"external_transcripts,omitempty"`
	Claims              []LineClaim  `json:"claims,omitempty"`
}

func validateOrigin(o *ImportOrigin, s Snapshot) error {
	if o == nil {
		return nil
	}
	if s.Agent.Name != o.System+"-import" {
		return ErrInvalid
	}
	if !boundedText(o.SourceID, 200, true) || !boundedText(o.SourceSessionID, 200, false) || !boundedText(o.ProducerVersion, 120, false) ||
		redact.Text(o.SourceID) != o.SourceID || redact.Text(o.SourceSessionID) != o.SourceSessionID ||
		!ValidCommit(o.ObjectID) || !ValidCommit(o.RefObject) || !ValidCommit(o.TargetCommit) || !slices.Contains(s.Commits, o.TargetCommit) ||
		!importDigest.MatchString(o.SourceDigest) || !importRef.MatchString(o.Ref) || strings.Contains(o.Ref, "..") || strings.Contains(o.Ref, "//") ||
		o.Part < 1 || o.Parts < o.Part || o.Parts > 256 || o.OmittedBlocks < 0 || o.OmittedBlocks > 200000 || o.SourceOffset < 0 || o.SourceOffset > 50000 || len(o.Claims) > 1000 {
		return ErrInvalid
	}
	switch o.System {
	case "entire":
		if !entireCheckpointID.MatchString(o.SourceID) {
			return ErrInvalid
		}
		if strings.HasPrefix(o.Ref, "refs/entire/checkpoints/") {
			parts := strings.Split(strings.TrimPrefix(o.Ref, "refs/entire/checkpoints/"), "/")
			if len(parts) != 2 || parts[1] != o.SourceID || !strings.EqualFold(parts[0], o.SourceID[len(o.SourceID)-2:]) {
				return ErrInvalid
			}
		}
		if o.LineStart == 0 {
			for _, m := range s.Messages {
				if m.Kind != "import_notice" {
					return ErrInvalid
				}
			}
		}
		if o.LineStart > 0 && o.LineStart <= o.SourceOffset {
			return ErrInvalid
		}
		if o.Agent == nil || o.Agent.SessionID != o.SourceSessionID || !boundedText(o.Agent.Tool, 120, true) || !boundedText(o.Agent.Model, 240, true) {
			return ErrInvalid
		}
		if o.Format != "transcript/1" || o.SourceSessionID == "" || o.TimeKind != "checkpoint_created" || o.LineStart < 0 || o.LineEnd < o.LineStart || o.LineEnd > 50000 || (o.LineStart == 0 && o.LineEnd != 0) || len(o.Claims) > 0 || o.BaseCommit != "" || o.ExternalTranscripts || !(strings.HasPrefix(o.Ref, "refs/entire/checkpoints/") || o.Ref == "refs/heads/entire/checkpoints/v1") {
			return ErrInvalid
		}
	case "git-ai":
		if o.Agent != nil {
			return ErrInvalid
		}
		if o.Format != "authorship/3.0.0" || o.SourceID != o.TargetCommit || o.SourceSessionID != "" || o.Ref != "refs/notes/ai" || o.TimeKind != "commit_recorded" || !ValidCommit(o.BaseCommit) || o.LineStart != 0 || o.LineEnd != 0 || o.SourceOffset != 0 {
			return ErrInvalid
		}
		for _, m := range s.Messages {
			if (m.Kind != "provenance_report" && m.Kind != "import_notice") || m.Role != "system" {
				return ErrInvalid
			}
		}
	default:
		return ErrInvalid
	}
	claims := slices.Clone(o.Claims)
	slices.SortFunc(claims, compareClaims)
	for i, c := range claims {
		if !ValidFilePath(c.Path) || !slices.Contains(s.Files, c.Path) || c.Start < 1 || c.End < c.Start || c.End > 1000000000 {
			return ErrInvalid
		}
		if i > 0 && claims[i-1].Path == c.Path && claims[i-1].End >= c.Start {
			return ErrInvalid
		}
		switch c.Kind {
		case "ai":
			if !(sessionClaimKey.MatchString(c.Key) || legacyClaimKey.MatchString(c.Key)) || c.Agent == nil || c.Author != "" {
				return ErrInvalid
			}
			if !boundedText(c.Agent.Tool, 120, true) || !boundedText(c.Agent.Model, 240, true) || !boundedText(c.Agent.SessionID, 200, true) || redact.Text(c.Agent.SessionID) != c.Agent.SessionID {
				return ErrInvalid
			}
		case "known_human":
			if !humanClaimKey.MatchString(c.Key) || c.Agent != nil || !boundedText(c.Author, 240, true) {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}

func compareClaims(a, b LineClaim) int {
	if v := strings.Compare(a.Path, b.Path); v != 0 {
		return v
	}
	if v := cmp.Compare(a.Start, b.Start); v != 0 {
		return v
	}
	if v := cmp.Compare(a.End, b.End); v != 0 {
		return v
	}
	return strings.Compare(a.Key, b.Key)
}
func cloneOrigin(o *ImportOrigin) *ImportOrigin {
	if o == nil {
		return nil
	}
	copy := *o
	if o.Agent != nil {
		agent := *o.Agent
		copy.Agent = &agent
	}
	copy.Claims = slices.Clone(o.Claims)
	for i, c := range copy.Claims {
		if c.Agent != nil {
			agent := *c.Agent
			copy.Claims[i].Agent = &agent
		}
	}
	slices.SortFunc(copy.Claims, compareClaims)
	return &copy
}
func sanitizeOrigin(o *ImportOrigin) {
	if o == nil {
		return
	}
	o.ProducerVersion = redact.Text(o.ProducerVersion)
	if o.Agent != nil {
		o.Agent.Tool = redact.Text(o.Agent.Tool)
		o.Agent.Model = redact.Text(o.Agent.Model)
	}
	for i := range o.Claims {
		c := &o.Claims[i]
		c.Author = redact.Text(c.Author)
		if c.Agent != nil {
			c.Agent.Tool = redact.Text(c.Agent.Tool)
			c.Agent.Model = redact.Text(c.Agent.Model)
		}
	}
}

// SameImportSource compares immutable source scope while permitting adjacent bounded projections.
func SameImportSource(left, right *ImportOrigin) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	a, b := cloneOrigin(left), cloneOrigin(right)
	for _, o := range []*ImportOrigin{a, b} {
		o.Part = 0
		o.LineStart = 0
		o.LineEnd = 0
		o.Claims = nil
	}
	return reflect.DeepEqual(a, b)
}
