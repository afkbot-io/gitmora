package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/contextpack/domain"
)

// Use provider-confirmed selected commit membership, never a chronological guess
// based on commit timestamps or a shared root-session identifier.
const selectedRangeCommit = `c.confirmed_at IS NOT NULL AND (($4::text<>'' AND z.sha=$4) OR ($7::text<>'' AND EXISTS(SELECT 1 FROM forge_mr_commits mc WHERE mc.sha=z.sha AND mc.repository_id::text=$5 AND mc.iid=$6 AND mc.code_key=$7 AND mc.source_project_id=$8)))`
const rangeProjection = `(SELECT COALESCE(json_agg(json_build_object('commit_sha',z.sha,'start',z.start_message,'end',z.end_message,'basis',z.basis) ORDER BY z.start_message,z.end_message,z.sha),'[]') FROM capture_commit_ranges z JOIN commits c ON c.repository_id=z.repository_id AND c.sha=z.sha WHERE z.repository_id=s.repository_id AND z.session_id=s.session_id AND z.revision=s.latest_revision AND ` + selectedRangeCommit + `)`
const scopedWindowFilter = `($3::text<>'' OR EXISTS(SELECT 1 FROM capture_commit_ranges z JOIN commits c ON c.repository_id=z.repository_id AND c.sha=z.sha WHERE z.repository_id=s.repository_id AND z.session_id=s.session_id AND z.revision=s.latest_revision AND ` + selectedRangeCommit + `)
 OR (EXISTS(SELECT 1 FROM seeds legacy WHERE legacy.repository_id=s.repository_id AND legacy.session_id=s.session_id) AND NOT EXISTS(SELECT 1 FROM tree family JOIN capture_commit_ranges z ON z.repository_id=family.repository_id AND z.session_id=family.session_id AND z.revision=family.latest_revision JOIN commits c ON c.repository_id=z.repository_id AND c.sha=z.sha WHERE family.repository_id=s.repository_id AND family.root_id=s.root_id AND ` + selectedRangeCommit + `)))`

// readContextMessages emits bounded original message positions. Scope authorization
// is already held by ContextStore.Read; every subsequent cursor repeats it.
func readContextMessages(ctx context.Context, tx pgx.Tx, q domain.Query, page *domain.MessagePage, scope *domain.Scope) error {
	page.Items = []domain.ScopedMessage{}
	if q.SessionID == "" && q.DialogueID == "" {
		return nil
	}
	if q.MessageAfter != nil && q.MR > 0 {
		if q.ExpectedHead != "" && q.MessageAfter.Head != "" && q.ExpectedHead != q.MessageAfter.Head {
			return domain.ErrInvalid
		}
	}
	args := append(contextArgs(q), q.DialogueID)
	rows, err := tx.Query(ctx, contextTree+`SELECT s.repository_id::text,s.session_id::text,s.latest_revision,r.digest,s.segment,r.canonical,`+rangeProjection+`
 FROM wanted w JOIN scoped s ON s.repository_id=w.repository_id AND s.session_id=w.session_id JOIN capture_revisions r ON r.repository_id=s.repository_id AND r.session_id=s.session_id AND r.revision=s.latest_revision
 WHERE `+scopedWindowFilter+` AND ($9::text='' OR s.session_id::text=$9 OR (s.root_id::text=$9 AND EXISTS(SELECT 1 FROM tree verified WHERE verified.repository_id=s.repository_id AND verified.session_id=s.session_id))) ORDER BY s.repository_id::text,s.segment,s.session_id::text LIMIT 257`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	bytes := 0
	limit := q.MessageLimit
	if limit == 0 {
		limit = 20
	}
	after := q.MessageAfter
	seenCursor := after == nil
	for rows.Next() {
		var repo, id, digest string
		var rev, segment int64
		var raw, rangesRaw []byte
		if err := rows.Scan(&repo, &id, &rev, &digest, &segment, &raw, &rangesRaw); err != nil {
			return err
		}
		if after != nil && !seenCursor {
			if repo != after.RepositoryID || id != after.SessionID {
				continue
			}
			if rev != after.Revision || digest != after.Digest || segment != after.Segment {
				return domain.ErrInvalid
			}
			seenCursor = true
		}
		var snapshot capture.Snapshot
		var ranges []capture.CommitRange
		if json.Unmarshal(raw, &snapshot) != nil || json.Unmarshal(rangesRaw, &ranges) != nil {
			return errors.New("invalid stored transcript")
		}
		if q.SessionID == "" && len(ranges) == 0 {
			scope.MissingCommitRanges = true
			continue
		}
		for index, message := range snapshot.Messages {
			changeCount := len(message.Changes)
			// Path reports are metadata, not unbounded transcript payload. Keep complete paths;
			// the canonical session revision retains every change for an explicit source read.
			changeBytes, shown := 0, 0
			for _, change := range message.Changes {
				raw, _ := json.Marshal(change)
				if shown >= 4 || changeBytes+len(raw) > 2048 {
					break
				}
				changeBytes += len(raw)
				shown++
			}
			message.Changes = message.Changes[:shown]
			if after != nil && repo == after.RepositoryID && id == after.SessionID && index < after.Offset {
				continue
			}
			shas := []string{}
			included := q.SessionID != ""
			for _, interval := range ranges {
				if index >= interval.Start && index < interval.End {
					included = true
					shas = append(shas, interval.CommitSHA)
				}
			}
			if !included {
				continue
			}
			preview := q.MessageView == "compact" && len([]rune(message.Text)) > 1200
			if preview {
				if after != nil && repo == after.RepositoryID && id == after.SessionID && index == after.Offset && after.RuneOffset != 0 {
					return domain.ErrInvalid
				}
				if message.Role == "tool" {
					message.Text = compactToolText(message.Text)
				} else {
					message.Text = domain.Clip(message.Text, 1200).Text
				}
			}
			// Page before the next item; callers can use the same validated cursor to retry.
			if len(page.Items) >= limit || bytes+len(message.Text) > 24000 && len(page.Items) > 0 {
				page.Next = &domain.MessageCursor{RepositoryID: repo, SessionID: id, Revision: rev, Digest: digest, Segment: segment, Offset: index, Head: q.ExpectedHead, ScopeKey: q.MRReadKey}
				page.HasMore = true
				return nil
			}
			slices.Sort(shas)
			source := domain.ScopedMessage{Segment: segment, Index: index, Message: message, CommitSHAs: slices.Compact(shas), ChangeCount: changeCount, ChangesMore: shown < changeCount}
			if preview {
				source.TextPreview = true
				source.FullTextAfter = &domain.MessageCursor{RepositoryID: repo, SessionID: id, Revision: rev, Digest: digest, Segment: segment, Offset: index, Head: q.ExpectedHead, ScopeKey: q.MRReadKey}
			}
			source.Source.Kind = "capture"
			source.Source.ProjectID = q.ProjectID
			source.Source.RepositoryID = repo
			source.Source.SessionID = id
			source.Source.Revision = rev
			source.Source.Digest = digest
			text := []rune(message.Text)
			start := 0
			if after != nil && repo == after.RepositoryID && id == after.SessionID && index == after.Offset {
				start = after.RuneOffset
			}
			if start > len(text) {
				return domain.ErrInvalid
			}
			end := min(start+6000, len(text))
			source.MessageOffset = start
			source.Message.Text = string(text[start:end])
			source.TextTruncated = end < len(text)

			bytes += len(source.Message.Text)
			page.Items = append(page.Items, source)
			if source.TextTruncated {
				page.Next = &domain.MessageCursor{RepositoryID: repo, SessionID: id, Revision: rev, Digest: digest, Segment: segment, Offset: index, RuneOffset: end, Head: q.ExpectedHead, ScopeKey: q.MRReadKey}
				page.HasMore = true
				return nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !seenCursor {
		return domain.ErrInvalid
	}
	return nil
}

// Compact previews avoid paging through long hidden records. Exact originals
// remain available through the same authorized, revision-pinned scope.
func compactToolText(text string) string {
	var value map[string]any
	if json.Unmarshal([]byte(text), &value) != nil {
		input, output, ok := strings.Cut(strings.TrimPrefix(text, "Input:\n"), "\nResult:\n")
		var decoded map[string]any
		if !strings.HasPrefix(text, "Input:\n") || !ok || json.Unmarshal([]byte(input), &decoded) != nil || decoded == nil {
			return domain.Clip(text, 800).Text
		}
		value = map[string]any{"input": decoded, "output": output}
	}
	input := value
	for _, key := range []string{"arguments", "input"} {
		if nested, ok := value[key].(map[string]any); ok {
			input = nested
			break
		}
		if encoded, ok := value[key].(string); ok {
			var nested map[string]any
			if json.Unmarshal([]byte(encoded), &nested) == nil && nested != nil {
				input = nested
				break
			}
		}
	}
	first := func(v map[string]any, keys ...string) string {
		for _, key := range keys {
			if s, ok := v[key].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	command := first(input, "cmd", "command", "code", "path", "file_path", "url", "q")
	output := first(value, "output", "stdout", "result")
	if command == "" && output == "" {
		return domain.Clip(text, 800).Text
	}
	preview := map[string]any{"command": domain.Clip(command, 240).Text, "output": domain.Clip(output, 600).Text}
	for _, key := range []string{"exit_code", "isError"} {
		if v, ok := value[key]; ok {
			switch v.(type) {
			case bool, float64:
				preview[key] = v
			}
		}
	}
	if status, ok := value["status"].(string); ok {
		preview["status"] = domain.Clip(status, 32).Text
	}
	raw, _ := json.Marshal(preview)
	return string(raw)
}
