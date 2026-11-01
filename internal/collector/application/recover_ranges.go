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

// RecoverCommitRanges is explicit local recovery. Only supplied Git-resolved
// object IDs and recorded successful commit output establish endpoints. This
// remains labelled recovered evidence; dates and assistant prose are ignored.
func (s *Service) RecoverCommitRanges(ctx context.Context, worktree, last string, known []string) (int, error) {
	count := 0
	err := s.ledger.Within(ctx, func(tx Transaction) error {
		entries := []RecordingEntry{}
		seen := map[string]bool{}
		id := last
		for len(entries) < 256 {
			if seen[id] {
				return domain.ErrConflict
			}
			seen[id] = true
			e, found, err := tx.FindSession(id)
			if err != nil {
				return err
			}
			if !found || e.Recording.State().Worktree != worktree {
				return domain.ErrConflict
			}
			entries = append(entries, e)
			c := e.Recording.State().Snapshot.Continuation
			if c == nil {
				break
			}
			id = c.PreviousSessionID
		}
		if entries[len(entries)-1].Recording.State().Snapshot.Continuation != nil {
			return domain.ErrConflict
		}
		sort.Slice(entries, func(i, j int) bool { return segment(entries[i]) < segment(entries[j]) })
		type endpoint struct {
			part, index int
			shas        []string
		}
		points := []endpoint{}
		seenSHA := map[string]bool{}
		for part, e := range entries {
			for index, message := range e.Recording.State().Snapshot.Messages {
				if message.Role != "tool" {
					continue
				}
				var tool struct {
					Command string `json:"command"`
					Cmd     string `json:"cmd"`
					Output  string `json:"output"`
					Exit    *int   `json:"exit_code"`
					Status  string `json:"status"`
				}
				if json.Unmarshal([]byte(message.Text), &tool) != nil || tool.Exit == nil || *tool.Exit != 0 || !commitCommand.MatchString(tool.Command+" "+tool.Cmd) {
					continue
				}
				point := endpoint{part: part, index: index + 1}
				for _, match := range commitOutput.FindAllStringSubmatch(tool.Output, -1) {
					found := ""
					for _, sha := range known {
						if strings.HasPrefix(sha, match[1]) {
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
				if len(point.shas) > 0 {
					points = append(points, point)
				}
			}
		}
		ranges := make([][]capture.CommitRange, len(entries))
		previousPart, previousIndex := 0, 0
		// The first recovered endpoint lacks a trustworthy previous checkpoint.
		// Establish a floor there; do not attach the entire older conversation.
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
						count++
					}
				}
			}
			previousPart, previousIndex = point.part, point.index
		}
		for part, e := range entries {
			if len(ranges[part]) == 0 {
				continue
			}
			if err := e.Recording.MergeRecoveredRanges(ranges[part]); err != nil {
				return err
			}
			if err := seal(tx, e); err != nil {
				return err
			}
		}
		return flushPublished(tx)
	})
	return count, err
}
func segment(e RecordingEntry) int64 {
	c := e.Recording.State().Snapshot.Continuation
	if c == nil {
		return 1
	}
	return c.Segment
}
