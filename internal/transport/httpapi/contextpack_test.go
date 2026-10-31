package httpapi_test

import (
	"context"
	"testing"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/domain"
	"workgraph.local/platform/internal/transport/httpapi"
)

type contextPacks struct {
	calls int
	p     access.Principal
}

func (s *contextPacks) Pack(_ context.Context, p access.Principal, q domain.Query) (domain.Pack, error) {
	s.calls++
	s.p = p
	return domain.Pack{SchemaVersion: 1, Query: q}, nil
}
func TestContextPackHTTPDoesNotBroadenIndependentCapabilities(t *testing.T) {
	packs := &contextPacks{}
	config := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadCapture}}, Ready: func(context.Context) error { return nil }, Retrieval: &retrieval{}, ContextPacks: packs}
	h := httpapi.NewHandler(config)
	if w := request(h, "POST", "/v1/context/pack", `{"project_id":"a","project_id":"b"}`, "test-secret"); w.Code != 400 || packs.calls != 0 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/v1/context/pack", `{"project_id":"p","session_id":"s"}`, "test-secret"); w.Code != 200 || packs.calls != 1 || packs.p.Has(access.ReadIntegration) {
		t.Fatal(w.Code)
	}
	config.Authenticator = auth{scopes: []access.Permission{access.WriteCapture}}
	if w := request(httpapi.NewHandler(config), "POST", "/v1/context/pack", `{}`, "test-secret"); w.Code != 403 || packs.calls != 1 {
		t.Fatal(w.Code)
	}
}
