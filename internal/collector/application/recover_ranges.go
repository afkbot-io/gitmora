package application

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/domain"
)

var commitOutput = regexp.MustCompile(`(?m)^\[[^\]\r\n]* ([0-9a-f]{7,64})\](?: |$)`)
var commitCommand = regexp.MustCompile(`\bgit\b[^\r\n]{0,100}\bcommit\b`)

// RecoveryReport separates newly projected intervals from unchanged replay.
// Previous immutable revisions are retained, including superseded recovery assertions.
type RecoveryReport struct {
	Ranges         int
	UpdatedWindows int
}

func recoveryChain(tx Transaction, worktree, last string) ([]RecordingEntry, error) {
	entries := []RecordingEntry{}
	config, err := tx.Config()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	id := last
	for len(entries) < 256 {
		if seen[id] {
			return nil, domain.ErrConflict
		}
		seen[id] = true
		e, found, err := tx.FindSession(id)
		if err != nil {
			return nil, err
		}
		if !found || e.Recording.State().Worktree != worktree {
			return nil, domain.ErrConflict
		}
		if !config.AllowsAgent(e.Recording.State().Snapshot.Agent.Name) {
			return nil, domain.ErrDisabled
		}
		entries = append(entries, e)
		c := e.Recording.State().Snapshot.Continuation
		if c == nil {
			break
		}
		id = c.PreviousSessionID
	}
	if entries[len(entries)-1].Recording.State().Snapshot.Continuation != nil {
		return nil, domain.ErrConflict
	}
	sort.Slice(entries, func(i, j int) bool { return segment(entries[i]) < segment(entries[j]) })
	return entries, nil
}

// recordedCommitOutput recognizes only exposed successful tool records. Even an
// unresolved or quiet commit is a barrier: recovery must not bridge it into a later commit.
func recordedCommitOutput(message capture.Message) ([]string, bool) {
	if message.Role != "tool" {
		return nil, false
	}
	var tool struct {
		Command string `json:"command"`
		Cmd     string `json:"cmd"`
		Output  string `json:"output"`
		Exit    *int   `json:"exit_code"`
	}
	if json.Unmarshal([]byte(message.Text), &tool) != nil || tool.Exit == nil || *tool.Exit != 0 || !commitCommand.MatchString(tool.Command+" "+tool.Cmd) {
		return nil, false
	}
	prefixes := []string{}
	for _, match := range commitOutput.FindAllStringSubmatch(tool.Output, -1) {
		prefixes = append(prefixes, match[1])
	}
	return prefixes, true
}

// RecoveryPrefixes returns bounded Git identifiers, never conversation text, for
// independent local object verification before any recovery state is changed.
func (s *Service) RecoveryPrefixes(ctx context.Context, worktree, last string) ([]string, error) {
	prefixes := []string{}
	err := s.ledger.Within(ctx, func(tx Transaction) error {
		entries, err := recoveryChain(tx, worktree, last)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, e := range entries {
			for _, m := range e.Recording.State().Snapshot.Messages {
				found, _ := recordedCommitOutput(m)
				for _, p := range found {
					if !seen[p] {
						seen[p] = true
						prefixes = append(prefixes, p)
					}
					if len(prefixes) > 4096 {
						return domain.ErrInvalid
					}
				}
			}
		}
		return nil
	})
	return prefixes, err
}

// RecoverCommitRanges explicitly rebuilds recovered-only projections from local
// Git objects and successful recorded tool output. Observed checkpoints take priority;
// dates, assistant prose, missing objects and redirected output never establish ownership.
func (s *Service) RecoverCommitRanges(ctx context.Context, worktree, last string, known []string) (RecoveryReport, error) {
	report := RecoveryReport{}
	if len(known) > 4096 {
		return report, domain.ErrInvalid
	}
	for _, sha := range known {
		if !capture.ValidCommit(sha) {
			return report, domain.ErrInvalid
		}
	}
	err := s.ledger.Within(ctx, func(tx Transaction) error {
		entries, err := recoveryChain(tx, worktree, last)
		if err != nil {
			return err
		}
		type endpoint struct {
			part, index int
			shas        []string
		}
		points := []endpoint{}
		seenSHA := map[string]bool{}
		for part, e := range entries {
			for index, m := range e.Recording.State().Snapshot.Messages {
				prefixes, barrier := recordedCommitOutput(m)
				if !barrier {
					continue
				}
				point := endpoint{part: part, index: index + 1}
				for _, prefix := range prefixes {
					found := ""
					for _, sha := range known {
						if strings.HasPrefix(sha, prefix) {
							if found != "" {
								found = ""
								break
							}
							found = sha
						}
					}
					if found != "" && !seenSHA[found] {
						point.shas = append(point.shas, found)
						seenSHA[found] = true
					}
				}
				points = append(points, point)
			}
		}
		ranges := make([][]capture.CommitRange, len(entries))
		previousPart, previousIndex := 0, 0
		for n, point := range points {
			if n > 0 {
				for part := previousPart; part <= point.part; part++ {
					start, end := 0, len(entries[part].Recording.State().Snapshot.Messages)
					if part == previousPart {
						start = previousIndex
					}
					if part == point.part {
						end = point.index
					}
					if end <= start {
						continue
					}
					for _, sha := range point.shas {
						ranges[part] = append(ranges[part], capture.CommitRange{CommitSHA: sha, Start: start, End: end, Basis: "recovered_git_output"})
					}
				}
			}
			previousPart, previousIndex = point.part, point.index
		}
		for part, e := range entries {
			changed, count, err := e.Recording.ReplaceRecoveredRanges(ranges[part])
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			report.Ranges += count
			report.UpdatedWindows++
			if err := seal(tx, e); err != nil {
				return err
			}
		}
		return flushPublished(tx)
	})
	return report, err
}

func segment(e RecordingEntry) int64 {
	if c := e.Recording.State().Snapshot.Continuation; c != nil {
		return c.Segment
	}
	return 1
}
