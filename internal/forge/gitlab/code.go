package gitlab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"

	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/redact"
)

// CodeDiff enumerates provider-reported changed paths; diff-end does not remove GitLab's own diff limits.
func (c *Client) CodeDiff(ctx context.Context, repo domain.Repository, token, sha string, page int) (domain.DiffPage, error) {
	if !identifier.ValidCommit(sha) || page < 1 || page > 1_000_000 {
		return domain.DiffPage{}, domain.ErrInvalid
	}
	suffix := "/repository/commits/" + sha + "/diff?per_page=" + strconv.Itoa(providerPageSize) + "&page=" + strconv.Itoa(page)
	raw, headers, found, err := c.request(ctx, repo, token, suffix)
	if err != nil {
		return domain.DiffPage{}, err
	}
	if !found {
		return domain.DiffPage{Missing: true}, nil
	}
	var items []struct {
		Path    string `json:"new_path"`
		Mode    string `json:"b_mode"`
		Deleted bool   `json:"deleted_file"`
	}
	if jsonwire.DecodeExternalArray(raw, &items) != nil || len(items) > providerPageSize {
		return domain.DiffPage{}, invalidResponse()
	}
	base, _ := normalizeBase(repo.BaseURL)
	next, err := nextPage(headers, base+"/api/v4/projects/"+strconv.FormatInt(repo.ProjectID, 10)+suffix, page, len(items))
	if err != nil {
		return domain.DiffPage{}, err
	}
	result := domain.DiffPage{Files: []domain.ChangedFile{}, NextPage: next}
	for _, item := range items {
		skip := ""
		if !identifier.ValidGitPath(item.Path) || redact.Text(item.Path) != item.Path {
			reason := "unsupported_path"
			if redact.Text(item.Path) != item.Path {
				reason = "sensitive_path"
			}
			hash := sha256.Sum256([]byte(item.Path))
			result.Files = append(result.Files, domain.ChangedFile{Path: "__workgraph_unsupported__/" + hex.EncodeToString(hash[:]), Skip: reason})
			continue
		}
		switch {
		case item.Deleted:
			skip = "deleted"
		case item.Mode == "120000":
			skip = "symlink"
		case item.Mode == "160000":
			skip = "submodule"
		case item.Mode != "100644" && item.Mode != "100755":
			skip = "unsupported_mode"
		}
		result.Files = append(result.Files, domain.ChangedFile{Path: item.Path, Skip: skip})
	}
	return result, nil
}

// CodeFile binds bounded raw bytes to exact HEAD metadata and verifies their Git blob hash.
// Raw transport avoids the JSON file endpoint's conversion of non-UTF-8 content.
func (c *Client) CodeFile(ctx context.Context, repo domain.Repository, token, sha, filePath string) (domain.FileObservation, error) {
	if !identifier.ValidCommit(sha) || !identifier.ValidGitPath(filePath) {
		return domain.FileObservation{}, domain.ErrInvalid
	}
	suffix := "/repository/files/" + url.PathEscape(filePath)
	_, headers, found, err := c.requestBounded(ctx, repo, token, http.MethodHead, suffix+"?ref="+url.QueryEscape(sha), 0)
	if err != nil {
		return domain.FileObservation{}, err
	}
	if !found {
		return domain.FileObservation{Missing: true}, nil
	}
	for _, name := range []string{"X-Gitlab-Blob-Id", "X-Gitlab-Commit-Id", "X-Gitlab-Ref", "X-Gitlab-File-Path", "X-Gitlab-Size"} {
		if len(headers.Values(name)) != 1 {
			return domain.FileObservation{}, invalidResponse()
		}
	}
	size, err := strconv.ParseInt(headers.Get("X-Gitlab-Size"), 10, 64)
	identity := domain.FileIdentity{RepositoryID: repo.ID, BaseURL: repo.BaseURL, ProjectID: repo.ProjectID, CommitSHA: sha, Path: filePath, BlobID: headers.Get("X-Gitlab-Blob-Id"), Size: size}
	if err != nil || identity.Validate() != nil || headers.Get("X-Gitlab-Commit-Id") != sha || headers.Get("X-Gitlab-Ref") != sha || headers.Get("X-Gitlab-File-Path") != filePath {
		return domain.FileObservation{}, invalidResponse()
	}
	if size > domain.MaxRawFileBytes {
		return domain.FileObservation{Skip: "too_large"}, nil
	}
	raw, _, found, err := c.requestBounded(ctx, repo, token, http.MethodGet, suffix+"/raw?ref="+url.QueryEscape(sha)+"&lfs=false", domain.MaxRawFileBytes)
	if err != nil {
		return domain.FileObservation{}, err
	}
	if !found {
		return domain.FileObservation{Missing: true}, nil
	}
	observation, err := domain.ObserveFile(identity, raw)
	if err != nil {
		return domain.FileObservation{}, invalidResponse()
	}
	return observation, nil
}
