package gitrepo

import (
	"context"
	"slices"
	"strings"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/domain"
)

// ReferenceHeads snapshots bounded pre-tool commit tips without branch names or network I/O.
// Annotated tags are peeled only when their target is a commit; blob/tree tags are ignored.
func (r *Repository) ReferenceHeads(ctx context.Context) ([]string, error) {
	raw, err := r.run(ctx, nil, "for-each-ref", "--format=%(objectname) %(objecttype) %(*objectname) %(*objecttype)")
	if err != nil {
		return nil, err
	}
	heads := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sha := ""
		if fields[1] == "commit" {
			sha = fields[0]
		} else if len(fields) == 4 && fields[3] == "commit" {
			sha = fields[2]
		}
		if sha != "" {
			if !capture.ValidCommit(sha) {
				return nil, domain.ErrInvalid
			}
			if !seen[sha] {
				heads = append(heads, sha)
				seen[sha] = true
			}
		}
		if len(heads) > 4096 {
			return nil, domain.ErrInvalid
		}
	}
	slices.Sort(heads)
	return slices.Compact(heads), nil
}

// CheckpointWindow excludes history already reachable before the tool. Checking
// out an existing branch adds no checkpoint; commits made after that checkout do.
// This is observed Git context, never proof of which actor authored a commit.
func (r *Repository) CheckpointWindow(ctx context.Context, before, after string, heads []string) ([]string, error) {
	if len(heads) > 4096 || before != "" && !capture.ValidCommit(before) || after != "" && !capture.ValidCommit(after) {
		return nil, domain.ErrInvalid
	}
	if before == after || after == "" {
		return []string{}, nil
	}
	refs := []string{after}
	if before != "" {
		refs = append(refs, "^"+before)
	}
	for _, head := range heads {
		if !capture.ValidCommit(head) {
			return nil, domain.ErrInvalid
		}
		refs = append(refs, "^"+head)
	}
	raw, err := r.run(ctx, strings.NewReader(strings.Join(refs, "\n")+"\n"), "rev-list", "--reverse", "--max-count=2001", "--stdin")
	if err != nil {
		return nil, err
	}
	commits := strings.Fields(string(raw))
	if len(commits) > 2000 {
		return nil, domain.ErrInvalid
	}
	for _, sha := range commits {
		if !capture.ValidCommit(sha) {
			return nil, domain.ErrInvalid
		}
	}
	return commits, nil
}

// ResolveCommitPrefixes verifies recorded short/full object IDs in one local Git batch.
// Missing or ambiguous objects are skipped; repository length and current branch do not select evidence.
func (r *Repository) ResolveCommitPrefixes(ctx context.Context, prefixes []string) ([]string, error) {
	if len(prefixes) > 4096 {
		return nil, domain.ErrInvalid
	}
	if len(prefixes) == 0 {
		return []string{}, nil
	}
	lines := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if len(prefix) < 7 || len(prefix) > 64 || strings.Trim(prefix, "0123456789abcdef") != "" {
			return nil, domain.ErrInvalid
		}
		lines = append(lines, prefix+"^{commit}")
	}
	raw, err := r.run(ctx, strings.NewReader(strings.Join(lines, "\n")+"\n"), "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	commits := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "commit" && capture.ValidCommit(fields[0]) {
			commits = append(commits, fields[0])
		}
	}
	slices.Sort(commits)
	return slices.Compact(commits), nil
}
