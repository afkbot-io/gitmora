package github

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"

	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/redact"
)

// CodeDiff enumerates changed paths; GitHub's own diff cap remains explicit in code status.
func (c *Client) CodeDiff(ctx context.Context, repo domain.Repository, token, sha string, page int) (domain.DiffPage, error) {
	root, err := repoPath(repo)
	if err != nil || !identifier.ValidCommit(sha) || page < 1 || page > 1000000 {
		return domain.DiffPage{}, domain.ErrInvalid
	}
	if err = c.VerifyProject(ctx, repo, token); err != nil {
		return domain.DiffPage{}, err
	}
	path := root + "/commits/" + sha + "?per_page=" + strconv.Itoa(pageSize) + "&page=" + strconv.Itoa(page)
	raw, header, found, err := c.request(ctx, token, path, 8*1024*1024)
	if err != nil {
		return domain.DiffPage{}, err
	}
	if !found {
		return domain.DiffPage{Missing: true}, nil
	}
	var value struct {
		SHA   string `json:"sha"`
		Files []struct {
			Path   string `json:"filename"`
			Status string `json:"status"`
		} `json:"files"`
	}
	if decodeObject(raw, &value) != nil || value.SHA != sha || len(value.Files) > pageSize {
		return domain.DiffPage{}, invalid()
	}
	next, err := c.nextPage(header, path, page)
	if err != nil {
		return domain.DiffPage{}, err
	}
	out := domain.DiffPage{Files: []domain.ChangedFile{}, NextPage: next}
	for _, file := range value.Files {
		if !identifier.ValidGitPath(file.Path) || redact.Text(file.Path) != file.Path {
			reason := "unsupported_path"
			if redact.Text(file.Path) != file.Path {
				reason = "sensitive_path"
			}
			hash := sha256.Sum256([]byte(file.Path))
			out.Files = append(out.Files, domain.ChangedFile{Path: "__workgraph_unsupported__/" + hex.EncodeToString(hash[:]), Skip: reason})
			continue
		}
		skip := ""
		if file.Status == "removed" {
			skip = "deleted"
		}
		out.Files = append(out.Files, domain.ChangedFile{Path: file.Path, Skip: skip})
	}
	return out, nil
}

type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Kind string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// CodeFile walks immutable trees, refusing symlinks/submodules before fetching a hash-checked blob.
// No download_url, mutable branch, archive or external LFS pointer is followed.
func (c *Client) CodeFile(ctx context.Context, repo domain.Repository, token, sha, filePath string) (domain.FileObservation, error) {
	root, err := repoPath(repo)
	if err != nil || !identifier.ValidCommit(sha) || !identifier.ValidGitPath(filePath) {
		return domain.FileObservation{}, domain.ErrInvalid
	}
	parts := strings.Split(filePath, "/")
	if len(parts) > 32 {
		return domain.FileObservation{Skip: "path_depth_limit"}, nil
	}
	if err = c.VerifyProject(ctx, repo, token); err != nil {
		return domain.FileObservation{}, err
	}
	raw, _, found, err := c.request(ctx, token, root+"/git/commits/"+sha, 2*1024*1024)
	if err != nil {
		return domain.FileObservation{}, err
	}
	if !found {
		return domain.FileObservation{Missing: true}, nil
	}
	var commit struct {
		SHA  string `json:"sha"`
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if decodeObject(raw, &commit) != nil || commit.SHA != sha || !identifier.ValidCommit(commit.Tree.SHA) {
		return domain.FileObservation{}, invalid()
	}
	treeSHA := commit.Tree.SHA
	var selected treeEntry
	for i, part := range parts {
		raw, _, found, err = c.request(ctx, token, root+"/git/trees/"+url.PathEscape(treeSHA), 8*1024*1024)
		if err != nil {
			return domain.FileObservation{}, err
		}
		if !found {
			return domain.FileObservation{Missing: true}, nil
		}
		var tree struct {
			SHA       string      `json:"sha"`
			Truncated bool        `json:"truncated"`
			Entries   []treeEntry `json:"tree"`
		}
		if decodeObject(raw, &tree) != nil || tree.SHA != treeSHA || tree.Truncated {
			return domain.FileObservation{}, invalid()
		}
		matches := 0
		for _, entry := range tree.Entries {
			if entry.Path == part {
				selected = entry
				matches++
			}
		}
		if matches == 0 {
			return domain.FileObservation{Missing: true}, nil
		}
		if matches != 1 || !identifier.ValidCommit(selected.SHA) {
			return domain.FileObservation{}, invalid()
		}
		if i < len(parts)-1 {
			if selected.Kind != "tree" || selected.Mode != "040000" {
				return domain.FileObservation{Skip: "unsupported_mode"}, nil
			}
			treeSHA = selected.SHA
			continue
		}
	}
	switch {
	case selected.Mode == "120000":
		return domain.FileObservation{Skip: "symlink"}, nil
	case selected.Mode == "160000" || selected.Kind == "commit":
		return domain.FileObservation{Skip: "submodule"}, nil
	case selected.Kind != "blob" || selected.Mode != "100644" && selected.Mode != "100755":
		return domain.FileObservation{Skip: "unsupported_mode"}, nil
	case selected.Size > domain.MaxRawFileBytes:
		return domain.FileObservation{Skip: "too_large"}, nil
	case selected.Size < 0:
		return domain.FileObservation{}, invalid()
	}
	raw, _, found, err = c.request(ctx, token, root+"/git/blobs/"+selected.SHA, 2*1024*1024)
	if err != nil {
		return domain.FileObservation{}, err
	}
	if !found {
		return domain.FileObservation{Missing: true}, nil
	}
	var blob struct {
		SHA      string `json:"sha"`
		Size     int64  `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if jsonwire.DecodeExternalObject(raw, &blob) != nil || blob.SHA != selected.SHA || blob.Size != selected.Size || blob.Encoding != "base64" {
		return domain.FileObservation{}, invalid()
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(blob.Content)
	if err != nil || int64(len(decoded)) != selected.Size {
		return domain.FileObservation{}, invalid()
	}
	identity := domain.FileIdentity{RepositoryID: repo.ID, BaseURL: repo.BaseURL, ProjectID: repo.ProjectID, CommitSHA: sha, Path: filePath, BlobID: selected.SHA, Size: selected.Size}
	observed, err := domain.ObserveFile(identity, decoded)
	if err != nil {
		return domain.FileObservation{}, invalid()
	}
	return observed, nil
}
