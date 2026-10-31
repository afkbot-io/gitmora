package domain

import (
	"bufio"
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/jsonwire"
)

var entireID = regexp.MustCompile(`^(?:[0-9a-f]{12}|[0-7][0-9ABCDEFGHJKMNPQRSTVWXYZ]{25})$`)

// ValidEntireID accepts canonical legacy IDs and ULIDs, including the timestamp overflow bound.
func ValidEntireID(value string) bool { return entireID.MatchString(value) }

type entireMetadata struct {
	Producer   string    `json:"cli_version"`
	Checkpoint string    `json:"checkpoint_id"`
	Session    string    `json:"session_id"`
	Created    time.Time `json:"created_at"`
	Agent      string    `json:"agent"`
	Model      string    `json:"model"`
	Files      []string  `json:"files_touched"`
	Offset     *int      `json:"compact_transcript_start"`
}
type entireLine struct {
	Version  int             `json:"v"`
	Type     string          `json:"type"`
	Agent    string          `json:"agent"`
	Producer string          `json:"cli_version"`
	Model    string          `json:"model"`
	Content  json.RawMessage `json:"content"`
}
type toolBlock struct {
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input"`
	Result *struct {
		Output string `json:"output"`
		Status string `json:"status"`
	} `json:"result"`
}

// ParseEntire decodes the checkpoint's compact-v1 slice, retaining source identity and omissions.
// It neither executes tools nor reads raw agent logs or attachment references.
func ParseEntire(scope Scope, checkpoint string, metadata, transcript []byte) (*Projection, error) {
	if len(metadata)+len(transcript) > MaxSourceBytes {
		return nil, ErrLimit
	}
	if !ValidEntireID(checkpoint) {
		return nil, ErrInvalid
	}
	p, err := newProjection(scope, "entire", "transcript/1")
	if err != nil {
		return nil, err
	}
	var meta entireMetadata
	if jsonwire.DecodeExternalObject(metadata, &meta) != nil || meta.Checkpoint != checkpoint || meta.Session == "" || meta.Created.IsZero() || len(meta.Files) > 5000 {
		return nil, ErrInvalid
	}
	offset := 0
	if meta.Offset != nil {
		offset = *meta.Offset
	}
	if offset < 0 || offset > 50000 {
		return nil, ErrInvalid
	}
	for _, path := range meta.Files {
		if !capture.ValidFilePath(path) {
			return nil, ErrInvalid
		}
	}
	model, agent := meta.Model, meta.Agent
	if model == "" {
		model = "unreported"
	}
	if agent == "" {
		agent = "unreported"
	}
	p.origin.SourceID = checkpoint
	p.origin.SourceSessionID = meta.Session
	p.origin.ProducerVersion = meta.Producer
	p.origin.SourceDigest = sourceDigest(metadata, transcript)
	p.origin.TimeKind = "checkpoint_created"
	p.origin.SourceOffset = offset
	p.origin.Agent = &capture.SourceAgent{Tool: agent, SessionID: meta.Session, Model: model}
	p.agent.Model = model
	p.started = meta.Created
	p.files = meta.Files
	scanner := bufio.NewScanner(bytes.NewReader(transcript))
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	physical, blocks := 0, 0
	for scanner.Scan() {
		physical++
		if physical > 50000 {
			return nil, ErrLimit
		}
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var line entireLine
		if jsonwire.DecodeExternalObject(raw, &line) != nil {
			return nil, ErrInvalid
		}
		if line.Version != 1 {
			return nil, ErrUnsupported
		}
		if line.Type == "" || line.Content == nil {
			return nil, ErrInvalid
		}
		if physical <= offset {
			continue
		}
		if line.Type != "user" && line.Type != "assistant" {
			p.origin.OmittedBlocks++
			continue
		}
		content := bytes.TrimSpace(line.Content)
		if bytes.Equal(content, []byte("null")) {
			continue
		}
		if len(content) > 0 && content[0] == '"' {
			var text string
			if json.Unmarshal(content, &text) != nil {
				return nil, ErrInvalid
			}
			if err := p.add(capture.Message{Role: line.Type, Text: text}, physical, nil); err != nil {
				return nil, err
			}
			continue
		}
		var values []json.RawMessage
		if jsonwire.DecodeExternalArray(content, &values) != nil {
			return nil, ErrInvalid
		}
		blocks += len(values)
		if blocks > 200000 {
			return nil, ErrLimit
		}
		for _, rawBlock := range values {
			var kind struct {
				Type string `json:"type"`
			}
			if jsonwire.DecodeExternalObject(rawBlock, &kind) != nil {
				return nil, ErrInvalid
			}
			switch kind.Type {
			case "", "text":
				var text struct {
					Text *string `json:"text"`
				}
				if json.Unmarshal(rawBlock, &text) != nil {
					return nil, ErrInvalid
				}
				if text.Text == nil {
					p.origin.OmittedBlocks++
					continue
				}
				if err := p.add(capture.Message{Role: line.Type, Text: *text.Text}, physical, nil); err != nil {
					return nil, err
				}
			case "tool_use":
				if line.Type != "assistant" {
					return nil, ErrInvalid
				}
				var tool toolBlock
				if json.Unmarshal(rawBlock, &tool) != nil || strings.TrimSpace(tool.Name) == "" {
					return nil, ErrInvalid
				}
				input := []byte("null")
				if len(tool.Input) > 0 {
					var value any
					if json.Unmarshal(tool.Input, &value) != nil {
						return nil, ErrInvalid
					}
					input, _ = json.Marshal(value)
				}
				result := "unavailable in source (invocation only)"
				if tool.Result != nil {
					if tool.Result.Status != "success" && tool.Result.Status != "error" {
						return nil, ErrInvalid
					}
					result = "source status: " + tool.Result.Status + "\n" + tool.Result.Output
				}
				if err := p.add(capture.Message{Role: "tool", ToolName: tool.Name, Text: "Input:\n" + string(input) + "\nResult:\n" + result}, physical, nil); err != nil {
					return nil, err
				}
			default:
				p.origin.OmittedBlocks++
			}
		}
	}
	if scanner.Err() != nil {
		return nil, ErrLimit
	}
	if offset > physical {
		return nil, ErrInvalid
	}
	if len(p.items) == 0 {
		if err := p.add(capture.Message{Role: "system", Kind: "import_notice", Text: "Imported checkpoint metadata; the selected source slice contains no exposed text/tool content."}, 0, nil); err != nil {
			return nil, err
		}
	}
	if _, err := p.Windows(); err != nil {
		return nil, err
	}
	return p, nil
}
