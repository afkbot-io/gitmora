package httpapi_test

import (
	"context"
	"testing"

	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/transport/httpapi"
)

type directoryFixture struct {
	httpapi.DirectoryService
	calls int
}

func (s *directoryFixture) Apply(_ context.Context, _ access.Principal, c directory.Command) (directory.Receipt, bool, error) {
	s.calls++
	return directory.Receipt{EntityID: c.EntityID, Action: c.Action}, true, nil
}
func (s *directoryFixture) Projects(context.Context, access.Principal, directory.Cursor) (directory.Page[directory.Project], error) {
	s.calls++
	return directory.Page[directory.Project]{Items: []directory.Project{}}, nil
}

func TestDirectoryHTTPDoesNotAcceptProviderRetargetingOrMixedBodies(t *testing.T) {
	s := &directoryFixture{}
	h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadDirectory, access.ManageDirectory}}, Ready: func(context.Context) error { return nil }, Directory: s})
	body := `{"request_id":"11111111-1111-4111-8111-111111111111","expected_version":2,"name":"Rename","gitlab_project_id":999}`
	path := "/v1/projects/22222222-2222-4222-8222-222222222222/repositories/33333333-3333-4333-8333-333333333333"
	if w := request(h, "PATCH", path, body, "test-secret"); w.Code != 400 || s.calls != 0 {
		t.Fatal(w.Code, w.Body.String(), s.calls)
	}
	for _, query := range []string{"?limit=1&limit=2", "?limit=101", "?after_id=invalid", "?unknown=1", "?after_id=%zz"} {
		if w := request(h, "GET", "/v1/projects"+query, "", "test-secret"); w.Code != 422 || s.calls != 0 {
			t.Fatal(query, w.Code, w.Body.String(), s.calls)
		}
	}
	if w := request(h, "POST", "/v1/me/tokens", `{"request_id":"x","label":"Key","scopes":["capture:read"],"ttl_seconds":600,"user_id":"another-user"}`, "test-secret"); w.Code != 400 || s.calls != 0 {
		t.Fatal("cross-user token body accepted", w.Code, s.calls)
	}
}
