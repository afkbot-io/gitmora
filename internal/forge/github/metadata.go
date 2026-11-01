package github

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/jsonwire"
)

// Changes supports pull requests and Actions runs. GitLab-specific lanes are not advertised for GitHub.
func (c *Client) Changes(ctx context.Context, repo domain.Repository, token string, cursor domain.Cursor) (domain.ChangePage, error) {
	root, err := repoPath(repo)
	if err != nil || cursor.Page < 1 || cursor.Page > 1000000 || cursor.After.IsZero() || cursor.Before.Before(cursor.After) {
		return domain.ChangePage{}, domain.ErrInvalid
	}
	query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "page": {strconv.Itoa(cursor.Page)}}
	path := ""
	switch cursor.Kind {
	case domain.MergeRequests:
		query.Set("state", "all")
		query.Set("sort", "updated")
		query.Set("direction", "desc")
		path = root + "/pulls?" + query.Encode()
	case domain.Pipelines:
		path = root + "/actions/runs?" + query.Encode()
	default:
		return domain.ChangePage{}, domain.ErrInvalid
	}
	raw, header, found, err := c.request(ctx, token, path, 8*1024*1024)
	var provider *application.ProviderError
	if !found && err == nil || errors.As(err, &provider) && provider.Code == "provider_forbidden" {
		return domain.ChangePage{Unavailable: true}, nil
	}
	if err != nil {
		return domain.ChangePage{}, err
	}
	next, err := c.nextPage(header, path, cursor.Page)
	if err != nil {
		return domain.ChangePage{}, err
	}
	out := domain.ChangePage{NextPage: next}
	if cursor.Kind == domain.MergeRequests {
		var items []struct {
			Number    int64     `json:"number"`
			UpdatedAt time.Time `json:"updated_at"`
		}
		if jsonwire.DecodeExternalArray(raw, &items) != nil || len(items) > pageSize {
			return out, invalid()
		}
		var previous time.Time
		for _, item := range items {
			if item.Number < 1 || item.UpdatedAt.IsZero() || !previous.IsZero() && item.UpdatedAt.After(previous) {
				return out, invalid()
			}
			previous = item.UpdatedAt
			if item.UpdatedAt.Before(cursor.After) {
				out.NextPage = 0
				continue
			}
			if !item.UpdatedAt.After(cursor.Before) {
				out.MergeRequests = append(out.MergeRequests, domain.MRHint{IID: item.Number, UpdatedAt: item.UpdatedAt})
			}
		}
	} else {
		var value struct {
			Runs []struct {
				ID         int64     `json:"id"`
				SHA        string    `json:"head_sha"`
				Branch     string    `json:"head_branch"`
				Status     string    `json:"status"`
				Conclusion *string   `json:"conclusion"`
				Event      string    `json:"event"`
				URL        string    `json:"html_url"`
				UpdatedAt  time.Time `json:"updated_at"`
				Repository struct {
					ID int64 `json:"id"`
				} `json:"repository"`
			} `json:"workflow_runs"`
		}
		if decodeObject(raw, &value) != nil || len(value.Runs) > pageSize {
			return out, invalid()
		}
		for _, run := range value.Runs {
			canonicalURL, urlOK := repositoryItemURL(run.URL, repo.BaseURL, "/actions/runs/"+strconv.FormatInt(run.ID, 10))
			if run.Repository.ID != repo.ProjectID || !urlOK {
				return out, invalid()
			}
			status := run.Status
			if status == "completed" && run.Conclusion != nil {
				status = *run.Conclusion
			}
			model, err := domain.NormalizePipeline(domain.Pipeline{ID: run.ID, ProjectID: repo.ProjectID, SHA: run.SHA, Status: status, Ref: run.Branch, Source: run.Event, URL: canonicalURL, UpdatedAt: run.UpdatedAt})
			if err != nil {
				return out, invalid()
			}
			if !model.UpdatedAt.Before(cursor.After) && !model.UpdatedAt.After(cursor.Before) {
				out.Pipelines = append(out.Pipelines, model)
			}
		}
	}
	return out, nil
}

type pullRequest struct {
	ID        int64     `json:"id"`
	Number    int64     `json:"number"`
	State     string    `json:"state"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	URL       string    `json:"html_url"`
	UpdatedAt time.Time `json:"updated_at"`
	Merged    bool      `json:"merged"`
	Merge     *string   `json:"merge_commit_sha"`
	Commits   int       `json:"commits"`
	Head      struct {
		SHA        string `json:"sha"`
		Repository *struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		SHA        string `json:"sha"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"base"`
}

func (c *Client) readMR(ctx context.Context, repo domain.Repository, token string, iid int64) (domain.MergeRequest, int, bool, error) {
	root, err := repoPath(repo)
	if err != nil || iid < 1 {
		return domain.MergeRequest{}, 0, false, domain.ErrInvalid
	}
	raw, _, found, err := c.request(ctx, token, root+"/pulls/"+strconv.FormatInt(iid, 10), 2*1024*1024)
	if err != nil || !found {
		return domain.MergeRequest{}, 0, false, err
	}
	var pr pullRequest
	decodeErr := decodeObject(raw, &pr)
	canonicalURL, urlOK := repositoryItemURL(pr.URL, repo.BaseURL, "/pull/"+strconv.FormatInt(iid, 10))
	if decodeErr != nil || pr.Number != iid || pr.Base.Repository.ID != repo.ProjectID || !urlOK || pr.Commits < 0 || !identifier.ValidCommit(pr.Base.SHA) || !identifier.ValidCommit(pr.Head.SHA) {
		return domain.MergeRequest{}, 0, false, invalid()
	}
	model := domain.MergeRequest{ID: pr.ID, IID: iid, ProjectID: repo.ProjectID, State: pr.State, Title: pr.Title, Description: pr.Body, URL: canonicalURL, Head: pr.Head.SHA, Start: pr.Base.SHA, UpdatedAt: pr.UpdatedAt}
	if pr.Head.Repository != nil {
		model.SourceProjectID = pr.Head.Repository.ID
	}
	if pr.Merged {
		model.State = "merged"
		if pr.Merge == nil || !identifier.ValidCommit(*pr.Merge) {
			return model, 0, false, invalid()
		}
		model.Merge = *pr.Merge
	}
	// The base tip is not the merge base. Read the exact comparison rather than inventing diff references.
	path := root + "/compare/" + pr.Base.SHA + "..." + pr.Head.SHA + "?per_page=1&page=1"
	raw, _, comparison, err := c.request(ctx, token, path, 8*1024*1024)
	if err != nil {
		return model, 0, false, err
	}
	if comparison {
		var value struct {
			Base struct {
				SHA string `json:"sha"`
			} `json:"base_commit"`
			MergeBase struct {
				SHA string `json:"sha"`
			} `json:"merge_base_commit"`
		}
		if decodeObject(raw, &value) != nil || value.Base.SHA != pr.Base.SHA || !identifier.ValidCommit(value.MergeBase.SHA) {
			return model, 0, false, invalid()
		}
		model.Base = value.MergeBase.SHA
	}
	model, err = domain.NormalizeMR(model)
	if err != nil {
		return model, 0, false, invalid()
	}
	return model, pr.Commits, true, nil
}

// MR reads authoritative detail; test merge SHAs on open PRs are never labelled merged results.
func (c *Client) MR(ctx context.Context, repo domain.Repository, token string, iid int64) (domain.MergeRequest, bool, error) {
	model, _, found, err := c.readMR(ctx, repo, token, iid)
	return model, found, err
}

// MRCommits fences both sides of each page by exact diff identity. GitHub caps this endpoint at 250 commits.
func (c *Client) MRCommits(ctx context.Context, repo domain.Repository, token string, expected domain.MergeRequest, page int) (domain.MRCommitPage, error) {
	root, err := repoPath(repo)
	if err != nil || page < 1 || page > 5 || !expected.CodeReady() {
		return domain.MRCommitPage{}, domain.ErrInvalid
	}
	before, count, found, err := c.readMR(ctx, repo, token, expected.IID)
	if err != nil {
		return domain.MRCommitPage{}, err
	}
	if !found {
		return domain.MRCommitPage{Missing: true}, nil
	}
	if before.CodeKey() != expected.CodeKey() {
		return domain.MRCommitPage{Replacement: &before}, nil
	}
	path := root + "/pulls/" + strconv.FormatInt(expected.IID, 10) + "/commits?per_page=" + strconv.Itoa(pageSize) + "&page=" + strconv.Itoa(page)
	raw, header, found, err := c.request(ctx, token, path, 8*1024*1024)
	if err != nil {
		return domain.MRCommitPage{}, err
	}
	if !found {
		return domain.MRCommitPage{Missing: true}, nil
	}
	var values []struct {
		SHA string `json:"sha"`
	}
	if jsonwire.DecodeExternalArray(raw, &values) != nil || len(values) > pageSize {
		return domain.MRCommitPage{}, invalid()
	}
	next, err := c.nextPage(header, path, page)
	if err != nil {
		return domain.MRCommitPage{}, err
	}
	after, afterCount, found, err := c.readMR(ctx, repo, token, expected.IID)
	if err != nil {
		return domain.MRCommitPage{}, err
	}
	if !found {
		return domain.MRCommitPage{Missing: true}, nil
	}
	if after.CodeKey() != expected.CodeKey() {
		return domain.MRCommitPage{Replacement: &after}, nil
	}
	if afterCount != count {
		return domain.MRCommitPage{}, invalid()
	}
	out := domain.MRCommitPage{Commits: []string{}, NextPage: next}
	for _, value := range values {
		if !identifier.ValidCommit(value.SHA) {
			return out, invalid()
		}
		out.Commits = append(out.Commits, value.SHA)
	}
	if page == 5 || next == 0 {
		if count > 250 {
			out.Truncated = true
			out.NextPage = 0
		} else if (page-1)*pageSize+len(values) != count {
			return out, invalid()
		}
	}
	return out, nil
}
