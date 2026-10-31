package domain

import (
	"bufio"
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/jsonwire"
)

type noteMetadata struct {
	Version  string                     `json:"schema_version"`
	Producer string                     `json:"git_ai_version"`
	Base     string                     `json:"base_commit_sha"`
	Prompts  map[string]json.RawMessage `json:"prompts"`
	Sessions map[string]json.RawMessage `json:"sessions"`
	Humans   map[string]json.RawMessage `json:"humans"`
}
type agentRecord struct {
	Agent struct {
		Tool  string `json:"tool"`
		ID    string `json:"id"`
		Model string `json:"model"`
	} `json:"agent_id"`
	MessagesURL string `json:"messages_url"`
}

// ParseGitAI reads an authorship/3.0.0 note at its annotated commit; Base is retained separately.
// External transcript pointers and directing-human metadata never become authorship evidence.
func ParseGitAI(scope Scope, raw []byte) (*Projection, error) {
	if len(raw) > MaxSourceBytes {
		return nil, ErrLimit
	}
	p, err := newProjection(scope, "git-ai", "authorship/3.0.0")
	if err != nil {
		return nil, err
	}
	if scope.Ref != "refs/notes/ai" || scope.Time.IsZero() {
		return nil, ErrInvalid
	}
	dividerStart, dividerEnd := -1, -1
	for pos, lines := 0, 0; pos < len(raw); lines++ {
		if lines > MaxItems {
			return nil, ErrLimit
		}
		end := bytes.IndexByte(raw[pos:], '\n')
		next := len(raw)
		if end >= 0 {
			end += pos
			next = end + 1
		} else {
			end = len(raw)
		}
		if string(bytes.TrimSuffix(raw[pos:end], []byte{'\r'})) == "---" {
			dividerStart, dividerEnd = pos, next
			break
		}
		pos = next
	}
	if dividerStart < 0 {
		return nil, ErrInvalid
	}
	var meta noteMetadata
	if jsonwire.DecodeExternalObject(raw[dividerEnd:], &meta) != nil {
		return nil, ErrInvalid
	}
	if meta.Version != "authorship/3.0.0" {
		return nil, ErrUnsupported
	}
	if !capture.ValidCommit(meta.Base) || meta.Prompts == nil || len(meta.Prompts)+len(meta.Sessions)+len(meta.Humans) > 5000 {
		return nil, ErrInvalid
	}
	p.origin.SourceID = scope.TargetCommit
	p.origin.BaseCommit = meta.Base
	p.origin.ProducerVersion = meta.Producer
	p.origin.SourceDigest = sourceDigest(raw)
	p.origin.TimeKind = "commit_recorded"
	p.started = scope.Time
	// Validate all registered identities, including sessions with no accepted ranges.
	for key, entry := range meta.Sessions {
		if !sessionClaimKey(key + "::t_00000000000000") {
			return nil, ErrInvalid
		}
		if _, err := readAgent(entry, true); err != nil {
			return nil, err
		}
	}
	for key, entry := range meta.Prompts {
		if !legacyKey(key) {
			return nil, ErrInvalid
		}
		record, err := readAgent(entry, false)
		if err != nil {
			return nil, err
		}
		if record.MessagesURL != "" {
			p.origin.ExternalTranscripts = true
		}
	}
	for key, entry := range meta.Humans {
		if !humanKey(key) {
			return nil, ErrInvalid
		}
		if _, err := readHuman(entry); err != nil {
			return nil, err
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw[:dividerStart]))
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	path := ""
	claims := []capture.LineClaim{}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				return nil, ErrInvalid
			}
			path = line
			if strings.HasPrefix(path, "\"") {
				if json.Unmarshal([]byte(path), &path) != nil {
					return nil, ErrInvalid
				}
			} else if strings.ContainsAny(path, " \t") {
				return nil, ErrInvalid
			}
			if !capture.ValidFilePath(path) {
				return nil, ErrInvalid
			}
			p.files = append(p.files, path)
			continue
		}
		if path == "" {
			return nil, ErrInvalid
		}
		key, ranges, found := strings.Cut(line[2:], " ")
		if !found || strings.ContainsAny(ranges, " \t") || strings.Count(ranges, ",") >= MaxItems {
			return nil, ErrInvalid
		}
		base, err := resolveClaim(meta, key)
		if err != nil {
			return nil, err
		}
		base.Path = path
		previous := 0
		for _, part := range strings.Split(ranges, ",") {
			first, last, rangeFound := strings.Cut(part, "-")
			start, err := lineNumber(first)
			if err != nil {
				return nil, err
			}
			end := start
			if rangeFound {
				end, err = lineNumber(last)
				if err != nil {
					return nil, err
				}
			}
			if start <= previous || end < start {
				return nil, ErrInvalid
			}
			previous = end
			claim := base
			claim.Start, claim.End = start, end
			claims = append(claims, claim)
			if len(claims) > MaxItems {
				return nil, ErrLimit
			}
		}
	}
	if scanner.Err() != nil {
		return nil, ErrLimit
	}
	slices.SortFunc(claims, func(a, b capture.LineClaim) int {
		if v := strings.Compare(a.Path, b.Path); v != 0 {
			return v
		}
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		return 0
	})
	for i, c := range claims {
		if i > 0 && claims[i-1].Path == c.Path && claims[i-1].End >= c.Start {
			return nil, ErrInvalid
		}
	}
	header := "Imported Git AI source-reported provenance at " + scope.TargetCommit + ". Uncovered lines are unknown; reported authors are not verified platform accounts. Transcript URLs were not fetched."
	if err := p.add(capture.Message{Role: "system", Kind: "provenance_report", Text: header}, 0, nil); err != nil {
		return nil, err
	}
	for _, c := range claims {
		raw, _ := json.Marshal(c)
		if err := p.add(capture.Message{Role: "system", Kind: "provenance_report", Text: "Source-reported ranges at commit " + scope.TargetCommit + ":\n" + string(raw)}, 0, &c); err != nil {
			return nil, err
		}
	}
	p.files = slices.Compact(sorted(p.files))
	if _, err := p.Windows(); err != nil {
		return nil, err
	}
	return p, nil
}
func readAgent(raw []byte, session bool) (agentRecord, error) {
	var record agentRecord
	var fields map[string]json.RawMessage
	if jsonwire.DecodeExternalObject(raw, &record) != nil || json.Unmarshal(raw, &fields) != nil {
		return record, ErrInvalid
	}
	if _, found := fields["messages"]; found {
		return record, ErrInvalid
	}
	if session {
		for _, name := range []string{"messages_url", "total_additions", "total_deletions", "accepted_lines", "overriden_lines"} {
			if _, found := fields[name]; found {
				return record, ErrInvalid
			}
		}
	}
	if record.Agent.Tool == "" || record.Agent.ID == "" || len(record.Agent.ID) > 200 || len(record.Agent.Tool) > 120 || len(record.Agent.Model) > 240 {
		return record, ErrInvalid
	}
	if record.Agent.Model == "" {
		record.Agent.Model = "unreported"
	}
	return record, nil
}
func readHuman(raw []byte) (string, error) {
	var record struct {
		Author string `json:"author"`
	}
	if jsonwire.DecodeExternalObject(raw, &record) != nil || strings.TrimSpace(record.Author) == "" || len(record.Author) > 240 {
		return "", ErrInvalid
	}
	return record.Author, nil
}
func resolveClaim(meta noteMetadata, key string) (capture.LineClaim, error) {
	claim := capture.LineClaim{Key: key}
	if humanKey(key) {
		author, err := readHuman(meta.Humans[key])
		claim.Kind, claim.Author = "known_human", author
		return claim, err
	}
	var record agentRecord
	var err error
	if sessionClaimKey(key) {
		name, _, _ := strings.Cut(key, "::")
		record, err = readAgent(meta.Sessions[name], true)
	} else if legacyKey(key) {
		record, err = readAgent(meta.Prompts[key], false)
	} else {
		return claim, ErrInvalid
	}
	if err != nil {
		return claim, err
	}
	claim.Kind = "ai"
	claim.Agent = &capture.SourceAgent{Tool: record.Agent.Tool, SessionID: record.Agent.ID, Model: record.Agent.Model}
	return claim, nil
}
func lineNumber(raw string) (int, error) {
	if raw == "" || raw[0] == '0' || len(raw) > 10 {
		return 0, ErrInvalid
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n < 1 || n > 1000000000 {
		return 0, ErrInvalid
	}
	return int(n), nil
}
func hexKey(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func legacyKey(s string) bool { return hexKey(s, 7) || hexKey(s, 16) }
func humanKey(s string) bool  { return strings.HasPrefix(s, "h_") && hexKey(s[2:], 14) }
func sessionClaimKey(s string) bool {
	a, b, ok := strings.Cut(s, "::")
	return ok && strings.HasPrefix(a, "s_") && hexKey(a[2:], 14) && strings.HasPrefix(b, "t_") && hexKey(b[2:], 14)
}
func sorted(values []string) []string { slices.Sort(values); return values }
