// Package domain decodes bounded versioned external evidence without Git, storage or network I/O.
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"
	"unicode/utf8"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/redact"
)

var (
	// ErrInvalid identifies malformed or contradictory source evidence without exposing its contents.
	ErrInvalid = errors.New("invalid compatibility source")
	// ErrUnsupported identifies a breaking source format version outside this decoder's contract.
	ErrUnsupported = errors.New("unsupported compatibility format")
	// ErrLimit identifies bounded acquisition/projection capacity, never a successful partial import.
	ErrLimit = errors.New("compatibility source limit exceeded")
)

// MaxSourceBytes bounds the complete selected encoded metadata/transcript or note input.
const MaxSourceBytes = 64 * 1024 * 1024

// MaxItems bounds projected message fragments and source attestation intervals.
const MaxItems = 50000

// Scope identifies Git objects acquired outside the domain. Time is the annotated commit's time.
type Scope struct {
	ObjectID     string
	Ref          string
	RefObject    string
	TargetCommit string
	Time         time.Time
}

// Window is a complete sanitized projection ready for the collector's native window protocol.
type Window struct {
	Origin    *capture.ImportOrigin
	Agent     capture.Agent
	StartedAt time.Time
	Messages  []capture.Message
	Files     []string
}
type item struct {
	message capture.Message
	claim   *capture.LineClaim
	line    int
}

// Projection owns all source-derived values; Windows validates the entire pass before persistence.
type Projection struct {
	origin  capture.ImportOrigin
	started time.Time
	agent   capture.Agent
	files   []string
	items   []item
}

func newProjection(s Scope, system, format string) (*Projection, error) {
	if !capture.ValidCommit(s.ObjectID) || !capture.ValidCommit(s.RefObject) || !capture.ValidCommit(s.TargetCommit) {
		return nil, ErrInvalid
	}
	return &Projection{origin: capture.ImportOrigin{System: system, Format: format, ObjectID: s.ObjectID, Ref: s.Ref, RefObject: s.RefObject, TargetCommit: s.TargetCommit}, agent: capture.Agent{Name: system + "-import", Version: "gitmora-import/1", Model: "unreported"}}, nil
}
func sourceDigest(parts ...[]byte) string {
	sum := sha256.New()
	var size [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(p)))
		sum.Write(size[:])
		sum.Write(p)
	}
	return hex.EncodeToString(sum.Sum(nil))
}
func (p *Projection) add(message capture.Message, line int, claim *capture.LineClaim) error {
	if !utf8.ValidString(message.Text) {
		return ErrInvalid
	}
	// Sanitize complete text before splitting, so a known token cannot evade a per-fragment scan.
	text := redact.Text(message.Text)
	for {
		size := min(len(text), 100*1024)
		for size < len(text) && !utf8.RuneStart(text[size]) {
			size--
		}
		current := message
		current.Text = text[:size]
		p.items = append(p.items, item{message: current, line: line, claim: claim})
		if len(p.items) > MaxItems {
			return ErrLimit
		}
		if size == len(text) {
			break
		}
		text = text[size:]
	}
	return nil
}

// Windows preserves explicit origin/window positions and returns independently owned evidence.
func (p *Projection) Windows() ([]Window, error) {
	if p == nil || p.started.IsZero() || len(p.items) == 0 {
		return nil, ErrInvalid
	}
	groups := [][]item{}
	current := []item{}
	weight := 0
	for _, i := range p.items {
		rawMessage, _ := json.Marshal(i.message)
		rawClaim, _ := json.Marshal(i.claim)
		size := len(rawMessage) + len(rawClaim)*2 + 128
		if len(current) > 0 && (weight+size > 384*1024 || len(current) >= 1000) {
			groups = append(groups, current)
			current = nil
			weight = 0
		}
		current = append(current, i)
		weight += size
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	if len(groups) > 256 {
		return nil, ErrLimit
	}
	result := make([]Window, 0, len(groups))
	for index, group := range groups {
		origin := p.origin
		origin.Part = index + 1
		origin.Parts = len(groups)
		origin.Claims = nil
		origin.LineStart = 0
		origin.LineEnd = 0
		messages := make([]capture.Message, 0, len(group))
		for _, i := range group {
			messages = append(messages, i.message)
			if i.claim != nil {
				origin.Claims = append(origin.Claims, *i.claim)
			}
			if i.line > 0 {
				if origin.LineStart == 0 {
					origin.LineStart = i.line
				}
				origin.LineEnd = i.line
			}
		}
		snapshot := capture.Snapshot{SchemaVersion: 1, RepositoryID: "11111111-1111-4111-8111-111111111111", SessionID: "22222222-2222-4222-8222-222222222222", Revision: 1, Agent: p.agent, StartedAt: p.started, Messages: messages, Files: p.files, Commits: []string{origin.TargetCommit}, Origin: &origin}
		session, err := capture.Sanitize(snapshot)
		if err != nil {
			return nil, ErrInvalid
		}
		value := session.Snapshot()
		result = append(result, Window{Origin: value.Origin, Agent: value.Agent, StartedAt: value.StartedAt, Messages: value.Messages, Files: slices.Clone(value.Files)})
	}
	return result, nil
}
