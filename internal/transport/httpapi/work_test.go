package httpapi_test

import (
	"context"
	"strings"
	"testing"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/transport/httpapi"
	"workgraph.local/platform/internal/work/application"
	"workgraph.local/platform/internal/work/domain"
)

type taskIntegration struct {
	httpapi.WorkService
	calls int
}

func (s *taskIntegration) Connect(context.Context, string, string, domain.Config) (application.Status, error) {
	s.calls++
	return application.Status{}, nil
}
func (s *taskIntegration) QueuePublication(context.Context, string, string, application.PublishInput) (application.Publication, error) {
	s.calls++
	return application.Publication{}, nil
}
func (s *taskIntegration) Links(context.Context, string, string, string) (application.LinkPage, error) {
	s.calls++
	return application.LinkPage{Items: []application.LinkPublication{{ID: "link", Evidence: &domain.LinkEvidence{SessionID: "private-session-identity"}}}}, nil
}

func TestLinkDeliveryJournalKeepsCaptureEvidenceBehindCaptureReadScope(t *testing.T) {
	service := &taskIntegration{}
	configuration := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadIntegration}}, Ready: func(context.Context) error { return nil }, Work: service}
	route := "/v1/projects/55555555-5555-4555-8555-555555555555/tasktopia/tasks/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/links"
	response := request(httpapi.NewHandler(configuration), "GET", route, "", "test-secret")
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-session-identity") {
		t.Fatal("link journal broadened capture authority", response.Code, response.Body.String())
	}
	configuration.Authenticator = auth{scopes: []access.Permission{access.ReadIntegration, access.ReadCapture}}
	response = request(httpapi.NewHandler(configuration), "GET", route, "", "test-secret")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "private-session-identity") {
		t.Fatal("authorized source provenance was absent", response.Code, response.Body.String())
	}
	configuration.Authenticator = auth{scopes: []access.Permission{access.ReadCapture}}
	if response := request(httpapi.NewHandler(configuration), "GET", route, "", "test-secret"); response.Code != 403 || service.calls != 2 {
		t.Fatal("capture credential read integration journal")
	}
}

func TestTaskIntegrationBoundariesAndPublicationCapability(t *testing.T) {
	service := &taskIntegration{}
	configuration := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ManageIntegration}}, Ready: func(context.Context) error { return nil }, Work: service}
	route := "/v1/projects/55555555-5555-4555-8555-555555555555/tasktopia"
	handler := httpapi.NewHandler(configuration)
	if w := request(handler, "POST", route+"/publications", `{}`, "test-secret"); w.Code != 403 || service.calls != 0 {
		t.Fatal("publication bypassed capture scope")
	}
	for _, body := range []string{`{"endpoint":"a","ENDPOINT":"b"}`, `{"country_id":"a","unknown":true}`, `{} {}`} {
		if w := request(handler, "PUT", route, body, "test-secret"); w.Code != 400 || service.calls != 0 {
			t.Fatal("ambiguous input reached service")
		}
	}
	if w := request(handler, "PUT", route, strings.Repeat(" ", 16385), "test-secret"); w.Code != 413 || service.calls != 0 {
		t.Fatal("oversized input reached service")
	}
	if w := request(handler, "PUT", route+"?countryId=other", `{}`, "test-secret"); w.Code != 400 || service.calls != 0 {
		t.Fatal("query changed task scope")
	}
	configuration.Authenticator = auth{scopes: []access.Permission{access.ManageIntegration, access.ReadCapture}}
	if w := request(httpapi.NewHandler(configuration), "POST", route+"/publications", `{"repository_id":"r","session_id":"s","revision":1}`, "test-secret"); w.Code != 202 || service.calls != 1 {
		t.Fatal("authorized publication not routed", w.Code)
	}
}
