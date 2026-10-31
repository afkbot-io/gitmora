package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/jsonwire"
)

const providerPageSize = 50

var missingCollection = errors.New("provider collection unavailable")

func pageNumber(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 1_000_000 {
		return 0, invalidResponse()
	}
	return n, nil
}
func nextPage(headers http.Header, requestURL string, current, count int) (int, error) {
	if len(headers.Values("X-Next-Page")) > 1 {
		return 0, invalidResponse()
	}
	original, err := url.Parse(requestURL)
	if err != nil {
		return 0, invalidResponse()
	}
	next := 0
	explicit := false
	if _, exists := headers[http.CanonicalHeaderKey("X-Next-Page")]; exists {
		explicit = true
		if value := headers.Get("X-Next-Page"); value != "" {
			next, err = pageNumber(value)
			if err != nil || next != current+1 {
				return 0, invalidResponse()
			}
		}
	}
	linkNext := 0
	for _, header := range headers.Values("Link") {
		for _, entry := range strings.Split(header, ",") {
			parts := strings.Split(entry, ";")
			if len(parts) < 2 {
				continue
			}
			isNext := false
			for _, part := range parts[1:] {
				key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
				if ok && key == "rel" {
					for _, rel := range strings.Fields(strings.Trim(value, `"`)) {
						if rel == "next" {
							isNext = true
						}
					}
				}
			}
			if !isNext {
				continue
			}
			value := strings.TrimSpace(parts[0])
			if !strings.HasPrefix(value, "<") || !strings.HasSuffix(value, ">") || linkNext != 0 {
				return 0, invalidResponse()
			}
			target, err := url.Parse(value[1 : len(value)-1])
			if err != nil {
				return 0, invalidResponse()
			}
			target = original.ResolveReference(target)
			if target.Scheme != original.Scheme || target.Host != original.Host || target.Path != original.Path || target.User != nil || target.Fragment != "" {
				return 0, invalidResponse()
			}
			query, err := url.ParseQuery(target.RawQuery)
			if err != nil || len(query["page"]) != 1 {
				return 0, invalidResponse()
			}
			linkNext, err = pageNumber(query.Get("page"))
			if err != nil || linkNext != current+1 {
				return 0, invalidResponse()
			}
			prior := original.Query()
			delete(query, "page")
			delete(prior, "page")
			if !reflect.DeepEqual(query, prior) {
				return 0, invalidResponse()
			}
		}
	}
	if linkNext > 0 {
		if explicit && linkNext != next {
			return 0, invalidResponse()
		}
		return linkNext, nil
	}
	if explicit {
		return next, nil
	}
	if count == providerPageSize {
		return current + 1, nil
	}
	return 0, nil
}
func safeProviderURL(base, value string) bool {
	if value == "" {
		return true
	}
	root, err := url.Parse(base)
	if err != nil {
		return false
	}
	target, err := url.Parse(value)
	return err == nil && target.Scheme == root.Scheme && target.Host == root.Host && target.User == nil && target.RawQuery == "" && target.Fragment == "" && strings.HasPrefix(target.Path, strings.TrimSuffix(root.Path, "/")+"/")
}
func (c *Client) array(ctx context.Context, repo domain.Repository, token, path string, query url.Values, page int) ([]json.RawMessage, int, error) {
	if page < 1 || page > 1_000_000 {
		return nil, 0, domain.ErrInvalid
	}
	query.Set("per_page", strconv.Itoa(providerPageSize))
	query.Set("page", strconv.Itoa(page))
	suffix := path + "?" + query.Encode()
	raw, headers, found, err := c.request(ctx, repo, token, suffix)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return nil, 0, missingCollection
	}
	var rows []json.RawMessage
	if jsonwire.DecodeExternalArray(raw, &rows) != nil || len(rows) > providerPageSize {
		return nil, 0, invalidResponse()
	}
	next, err := nextPage(headers, strings.TrimSuffix(repo.BaseURL, "/")+"/api/v4/projects/"+strconv.FormatInt(repo.ProjectID, 10)+suffix, page, len(rows))
	return rows, next, err
}

// Changes reads one fixed-window collection page and validates returned provider scope.
func (c *Client) Changes(ctx context.Context, repo domain.Repository, token string, cursor domain.Cursor) (domain.ChangePage, error) {
	result := domain.ChangePage{}
	if cursor.After.IsZero() || cursor.Before.Before(cursor.After) {
		return result, domain.ErrInvalid
	}
	query := url.Values{"order_by": {"updated_at"}, "sort": {"asc"}, "updated_after": {cursor.After.UTC().Format(time.RFC3339Nano)}, "updated_before": {cursor.Before.UTC().Format(time.RFC3339Nano)}}
	endpoint := ""
	switch cursor.Kind {
	case domain.MergeRequests:
		endpoint = "/merge_requests"
		query.Set("scope", "all")
		query.Set("state", "all")
	case domain.Pipelines:
		endpoint = "/pipelines"
	case domain.ChildPipelines:
		endpoint = "/pipelines"
		query.Set("source", "parent_pipeline")
	case domain.Deployments:
		endpoint = "/deployments"
	default:
		return result, domain.ErrInvalid
	}
	rows, next, err := c.array(ctx, repo, token, endpoint, query, cursor.Page)
	if errors.Is(err, missingCollection) {
		result.Unavailable = true
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.NextPage = next
	for _, raw := range rows {
		switch cursor.Kind {
		case domain.MergeRequests:
			var wire struct {
				IID       int64     `json:"iid"`
				ProjectID int64     `json:"project_id"`
				UpdatedAt time.Time `json:"updated_at"`
			}
			if json.Unmarshal(raw, &wire) != nil || wire.ProjectID != repo.ProjectID || wire.IID < 1 || wire.UpdatedAt.IsZero() {
				return result, invalidResponse()
			}
			result.MergeRequests = append(result.MergeRequests, domain.MRHint{IID: wire.IID, UpdatedAt: wire.UpdatedAt})
		case domain.Pipelines, domain.ChildPipelines:
			var value domain.Pipeline
			if json.Unmarshal(raw, &value) != nil || value.ProjectID != repo.ProjectID || !safeProviderURL(repo.BaseURL, value.URL) {
				return result, invalidResponse()
			}
			var wire struct {
				URL string `json:"web_url"`
			}
			if json.Unmarshal(raw, &wire) != nil || !safeProviderURL(repo.BaseURL, wire.URL) {
				return result, invalidResponse()
			}
			value.URL = wire.URL
			normalized, err := domain.NormalizePipeline(value)
			if err != nil {
				return result, invalidResponse()
			}
			result.Pipelines = append(result.Pipelines, normalized)
		case domain.Deployments:
			var wire struct {
				ID               int64 `json:"id"`
				SHA, Status, Ref string
				UpdatedAt        time.Time `json:"updated_at"`
				Environment      struct {
					ID   int64
					Name string
				}
				Deployable *struct {
					ID       int64
					Pipeline *struct{ ID int64 }
				}
			}
			if json.Unmarshal(raw, &wire) != nil {
				return result, invalidResponse()
			}
			value := domain.Deployment{ID: wire.ID, SHA: wire.SHA, Status: wire.Status, Ref: wire.Ref, UpdatedAt: wire.UpdatedAt, EnvironmentID: wire.Environment.ID, Environment: wire.Environment.Name}
			if wire.Deployable != nil {
				value.JobID = wire.Deployable.ID
				if wire.Deployable.Pipeline != nil {
					value.PipelineID = wire.Deployable.Pipeline.ID
				}
			}
			normalized, err := domain.NormalizeDeployment(value)
			if err != nil {
				return result, invalidResponse()
			}
			result.Deployments = append(result.Deployments, normalized)
		}
	}
	return result, nil
}

// MR retrieves current authoritative detail; a missing/hidden MR is not treated as deletion.
func (c *Client) MR(ctx context.Context, repo domain.Repository, token string, iid int64) (domain.MergeRequest, bool, error) {
	if iid < 1 {
		return domain.MergeRequest{}, false, domain.ErrInvalid
	}
	body, found, err := c.get(ctx, repo, token, "/merge_requests/"+strconv.FormatInt(iid, 10))
	if err != nil || !found {
		return domain.MergeRequest{}, found, err
	}
	raw, _ := json.Marshal(body)
	var wire struct {
		ID, IID                   int64
		ProjectID                 int64 `json:"project_id"`
		SourceProjectID           int64 `json:"source_project_id"`
		State, Title, Description string
		URL                       string    `json:"web_url"`
		SHA                       string    `json:"sha"`
		Merge                     string    `json:"merge_commit_sha"`
		Squash                    string    `json:"squash_commit_sha"`
		UpdatedAt                 time.Time `json:"updated_at"`
		Refs                      struct {
			Head  string `json:"head_sha"`
			Base  string `json:"base_sha"`
			Start string `json:"start_sha"`
		} `json:"diff_refs"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.IID != iid || wire.ProjectID != repo.ProjectID || !safeProviderURL(repo.BaseURL, wire.URL) || (wire.Refs.Head != "" && wire.SHA != "" && wire.Refs.Head != wire.SHA) {
		return domain.MergeRequest{}, false, invalidResponse()
	}
	head := wire.SHA
	if head == "" {
		head = wire.Refs.Head
	}
	normalized, err := domain.NormalizeMR(domain.MergeRequest{ID: wire.ID, IID: wire.IID, ProjectID: wire.ProjectID, SourceProjectID: wire.SourceProjectID, State: wire.State, Title: wire.Title, Description: wire.Description, URL: wire.URL, Head: head, Base: wire.Refs.Base, Start: wire.Refs.Start, Merge: wire.Merge, Squash: wire.Squash, UpdatedAt: wire.UpdatedAt})
	if err != nil {
		return domain.MergeRequest{}, false, invalidResponse()
	}
	return normalized, true, nil
}

// MRCommits brackets each commit page with diff-identity reads to reject mixed-head membership.
func (c *Client) MRCommits(ctx context.Context, repo domain.Repository, token string, expected domain.MergeRequest, page int) (domain.MRCommitPage, error) {
	result := domain.MRCommitPage{}
	before, found, err := c.MR(ctx, repo, token, expected.IID)
	if err != nil {
		return result, err
	}
	if !found {
		result.Missing = true
		return result, nil
	}
	if before.CodeKey() != expected.CodeKey() {
		if before.UpdatedAt.Before(expected.UpdatedAt) {
			return result, invalidResponse()
		}
		result.Replacement = &before
		return result, nil
	}
	rows, next, err := c.array(ctx, repo, token, "/merge_requests/"+strconv.FormatInt(expected.IID, 10)+"/commits", url.Values{}, page)
	if errors.Is(err, missingCollection) {
		result.Missing = true
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, raw := range rows {
		var item struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &item) != nil || !capture.ValidCommit(item.ID) {
			return result, invalidResponse()
		}
		result.Commits = append(result.Commits, item.ID)
	}
	after, found, err := c.MR(ctx, repo, token, expected.IID)
	if err != nil {
		return result, err
	}
	if !found {
		return domain.MRCommitPage{Missing: true}, nil
	}
	if after.CodeKey() != expected.CodeKey() {
		if after.UpdatedAt.Before(expected.UpdatedAt) {
			return result, invalidResponse()
		}
		return domain.MRCommitPage{Replacement: &after}, nil
	}
	result.NextPage = next
	return result, nil
}
