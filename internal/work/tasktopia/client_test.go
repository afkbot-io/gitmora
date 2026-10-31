package tasktopia_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workgraph.local/platform/internal/work/application"
	"workgraph.local/platform/internal/work/domain"
	"workgraph.local/platform/internal/work/tasktopia"
)

const country = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const taskID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func TestActualMCPTransportAndTasktopiaEnvelope(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "Tasktopia-contract", Version: "1.24.11"}, nil)
	var endpoint string
	calls := 0
	server.AddTool(&mcp.Tool{Name: "country.list", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"result": []any{map[string]any{"id": country}}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "task.get", InputSchema: json.RawMessage(`{"type":"object","properties":{"countryId":{"type":"string"},"taskId":{"type":"string"}},"required":["countryId","taskId"]}`)}, func(_ context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]string
		_ = json.Unmarshal(r.Params.Arguments, &args)
		if args["countryId"] != country || args["taskId"] != taskID {
			t.Error("scope not sent")
		}
		calls++
		task := map[string]any{"id": taskID, "taskNumber": 7, "title": "Task", "description": "token=never-store-this", "status": "IN_PROGRESS", "progress": 20, "updatedAt": "2026-10-30T12:00:00Z", "url": strings.TrimSuffix(endpoint, "/mcp") + "/task/7?countryId=" + country + "&taskId=" + taskID, "geometry": "ignored-provider-field"}
		raw, _ := json.Marshal(task)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: map[string]any{"result": task}}, nil
	})
	writes := []map[string]string{}
	linkWrites := []map[string]string{}
	server.AddTool(&mcp.Tool{Name: "task.link_add", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`)}, func(_ context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]string
		_ = json.Unmarshal(r.Params.Arguments, &args)
		linkWrites = append(linkWrites, args)
		return &mcp.CallToolResult{StructuredContent: map[string]any{"result": map[string]any{"mergeRequests": []any{map[string]string{"url": args["url"], "title": args["title"]}}, "url": strings.TrimSuffix(endpoint, "/mcp") + "/task/7?countryId=" + country + "&taskId=" + taskID}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "task.document_upsert", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`)}, func(_ context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]string
		_ = json.Unmarshal(r.Params.Arguments, &args)
		writes = append(writes, args)
		return &mcp.CallToolResult{StructuredContent: map[string]any{"result": map[string]any{"taskId": args["taskId"], "fileName": args["fileName"], "content": args["content"]}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-token" {
			t.Error("bearer missing")
			w.WriteHeader(401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	endpoint = httpServer.URL + "/mcp"
	client, err := tasktopia.New([]string{endpoint})
	if err != nil {
		t.Fatal(err)
	}
	config := domain.Config{Endpoint: endpoint, CountryID: country, Token: "synthetic-token"}
	if err := client.Verify(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	task, err := client.Task(context.Background(), config, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != taskID || strings.Contains(task.Description, "never-store-this") || calls != 1 {
		t.Fatal(task)
	}
	doc := domain.EvidenceDocument{FileName: "workgraph-evidence-" + taskID + ".md", Title: "Workgraph evidence", Content: "immutable report"}
	for i := 0; i < 2; i++ {
		if err := client.Publish(context.Background(), config, taskID, taskID, doc); err != nil {
			t.Fatal(err)
		}
	}
	if len(writes) != 2 || writes[0]["idempotencyKey"] != writes[1]["idempotencyKey"] || writes[0]["countryId"] != country {
		t.Fatal("retry changed identity")
	}
	link, err := domain.NewMRLink(country, "https://gitlab.internal", 42, 7, "https://gitlab.internal/team/project/-/merge_requests/7")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := client.AddLink(context.Background(), config, taskID, taskID, link); err != nil {
			t.Fatal(err)
		}
	}
	if len(linkWrites) != 2 || linkWrites[0]["idempotencyKey"] != linkWrites[1]["idempotencyKey"] || linkWrites[0]["countryId"] != country || linkWrites[0]["taskId"] != taskID || linkWrites[0]["title"] != link.Title {
		t.Fatal("link retry changed scope/payload", linkWrites)
	}
}

func TestEndpointAndCredentialFailuresAreBounded(t *testing.T) {
	if _, err := tasktopia.New([]string{"http://tasks.internal/mcp"}); err == nil {
		t.Fatal("non-TLS remote accepted")
	}
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	c, _ := tasktopia.New([]string{source.URL + "/mcp"})
	config := domain.Config{Endpoint: source.URL + "/mcp", CountryID: country, Token: "must-never-leak"}
	err := c.Verify(context.Background(), config)
	var provider *application.ProviderError
	if !errors.As(err, &provider) || redirected || strings.Contains(err.Error(), config.Token) {
		t.Fatal("unsafe redirect/error", err)
	}
	config.Endpoint = target.URL + "/mcp"
	if err = c.Verify(context.Background(), config); !errors.As(err, &provider) || redirected {
		t.Fatal("endpoint allowlist bypass", err)
	}
}

func TestTasktopiaRejectsWrongIdentityAndClassifiesActualScopeErrors(t *testing.T) {
	for _, scenario := range []string{"task", "country", "scope", "oversize"} {
		t.Run(scenario, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "task-error-contract", Version: "1"}, nil)
			var endpoint string
			server.AddTool(&mcp.Tool{Name: "task.get", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if scenario == "scope" {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: `{"code":"FORBIDDEN_SCOPE","message":"private-provider-diagnostic"}`}}}, nil
				}
				id, scope := taskID, country
				if scenario == "task" {
					id = country
				}
				if scenario == "country" {
					scope = taskID
				}
				title := "Task"
				if scenario == "oversize" {
					title = strings.Repeat("x", 5*1024*1024)
				}
				return &mcp.CallToolResult{StructuredContent: map[string]any{"result": map[string]any{"id": id, "taskNumber": 1, "title": title, "status": "PLANNING", "progress": 0, "updatedAt": "2026-10-30T10:00:00Z", "url": endpoint + "/task/1?countryId=" + scope + "&taskId=" + taskID}}}, nil
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			endpoint = httpServer.URL + "/mcp"
			client, _ := tasktopia.New([]string{endpoint})
			_, err := client.Task(context.Background(), domain.Config{Endpoint: endpoint, CountryID: country, Token: "synthetic-token"}, taskID)
			var provider *application.ProviderError
			if !errors.As(err, &provider) || strings.Contains(err.Error(), "private-provider-diagnostic") {
				t.Fatal("unsafe provider response", err)
			}
			if scenario == "scope" && (provider.Code != "provider_unauthorized" || provider.Retryable) {
				t.Fatal("scope failure misclassified", provider)
			}
		})
	}
}
