package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	forgeapp "workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/transport/httpapi"
)

type forge struct{ calls int }

func (f *forge) Credentials(context.Context, string, string, string) (forgeapp.CredentialPage, error) {
	f.calls++
	return forgeapp.CredentialPage{}, nil
}
func (f *forge) LookupWithCredential(context.Context, string, string, string, string, forgeapp.CredentialRef) (directory.ResolvedRepository, error) {
	f.calls++
	return directory.ResolvedRepository{}, nil
}
func (f *forge) ConnectUsingCredential(context.Context, string, string, forgeapp.CredentialRef, domain.Secrets) (forgeapp.Status, error) {
	f.calls++
	return forgeapp.Status{}, nil
}

func (f *forge) MergeRequest(context.Context, string, string, forgeapp.MetadataQuery) (forgeapp.MRView, error) {
	f.calls++
	return forgeapp.MRView{}, nil
}
func (f *forge) Pipelines(context.Context, string, string, forgeapp.MetadataQuery) (forgeapp.PipelinePage, error) {
	f.calls++
	return forgeapp.PipelinePage{}, nil
}
func (f *forge) Deployments(context.Context, string, string, forgeapp.MetadataQuery) (forgeapp.DeploymentPage, error) {
	f.calls++
	return forgeapp.DeploymentPage{}, nil
}

func (f *forge) Connect(context.Context, string, string, domain.Secrets) (forgeapp.Status, error) {
	f.calls++
	return forgeapp.Status{}, nil
}
func (f *forge) Status(context.Context, string, string) (forgeapp.Status, error) {
	f.calls++
	return forgeapp.Status{}, nil
}
func (f *forge) Disconnect(context.Context, string, string) error  { f.calls++; return nil }
func (f *forge) RequestSync(context.Context, string, string) error { f.calls++; return nil }
func (f *forge) Receive(context.Context, string, domain.Delivery) (bool, error) {
	f.calls++
	return true, nil
}

func TestForgeManagementRequiresItsOwnScopeAndStrictBoundedInput(t *testing.T) {
	service := &forge{}
	configuration := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.WriteCapture}}, Ready: func(context.Context) error { return nil }, Forge: service}
	route := "/v1/repositories/11111111-1111-4111-8111-111111111111/gitlab"
	if w := request(httpapi.NewHandler(configuration), "PUT", route, `{}`, "test-secret"); w.Code != 403 || service.calls != 0 {
		t.Fatal("capture scope managed provider credentials")
	}
	configuration.Authenticator = auth{scopes: []access.Permission{access.ManageIntegration}}
	handler := httpapi.NewHandler(configuration)
	for _, body := range []string{`{"api_token":"a","API_TOKEN":"b"}`, `{"unknown":true}`, `{} {}`} {
		if w := request(handler, "PUT", route, body, "test-secret"); w.Code != 400 || service.calls != 0 {
			t.Fatal("ambiguous configuration reached use case")
		}
	}
	if w := request(handler, "PUT", route, strings.Repeat(" ", 16385), "test-secret"); w.Code != 413 || service.calls != 0 {
		t.Fatal("oversized credential request reached use case")
	}
}

func TestForgeWebhookRejectsDuplicateAuthenticationHeadersAndOversizedBody(t *testing.T) {
	service := &forge{}
	handler := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, Forge: service})
	for _, oversized := range []bool{false, true} {
		body := "{}"
		if oversized {
			body = strings.Repeat(" ", 2*1024*1024+1)
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/integrations/gitlab/11111111-1111-4111-8111-111111111111/webhook", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Gitlab-Event", "Push Hook")
		expected := 413
		if !oversized {
			r.Header.Add("Webhook-Id", "one")
			r.Header.Add("Webhook-Id", "two")
			expected = 401
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != expected || service.calls != 0 {
			t.Fatal("invalid webhook reached use case")
		}
	}
}

func TestSavedForgeTokenLabelsRequireManageScopeAndStrictQuery(t *testing.T) {
	service := &forge{}
	config := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadIntegration}}, Ready: func(context.Context) error { return nil }, Forge: service}
	path := "/v1/projects/11111111-1111-4111-8111-111111111111/forge/credentials"
	if w := request(httpapi.NewHandler(config), "GET", path, "", "test-secret"); w.Code != 403 || service.calls != 0 {
		t.Fatal("read-only caller listed credential labels")
	}
	config.Authenticator = auth{scopes: []access.Permission{access.ManageIntegration}}
	for _, query := range []string{"?unexpected=1", "?after_id=one&after_id=two", "?after_id=%zz"} {
		if w := request(httpapi.NewHandler(config), "GET", path+query, "", "test-secret"); w.Code != 422 || service.calls != 0 {
			t.Fatal("ambiguous credential list request reached use case", w.Code)
		}
	}
}

func TestMetadataReadsRequireTheirScopeAndBoundedUnambiguousCursors(t *testing.T) {
	service := &forge{}
	configuration := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadCapture}}, Ready: func(context.Context) error { return nil }, Forge: service}
	root := "/v1/repositories/11111111-1111-4111-8111-111111111111/gitlab"
	if w := request(httpapi.NewHandler(configuration), "GET", root+"/pipelines", "", "test-secret"); w.Code != 403 || service.calls != 0 {
		t.Fatal("capture scope retrieved provider metadata")
	}
	configuration.Authenticator = auth{scopes: []access.Permission{access.ReadIntegration}}
	handler := httpapi.NewHandler(configuration)
	for _, path := range []string{root + "/pipelines?limit=0", root + "/pipelines?limit=1&limit=2", root + "/pipelines?after_id=-1", root + "/deployments?unknown=true", root + "/merge-requests/7?observation=invalid"} {
		if w := request(handler, "GET", path, "", "test-secret"); w.Code != 400 || service.calls != 0 {
			t.Fatal("invalid metadata cursor reached use case")
		}
	}
	if w := request(handler, "GET", root+"/pipelines?limit=1", "", "test-secret"); w.Code != 200 || service.calls != 1 {
		t.Fatal("authorized bounded metadata read failed")
	}
}
