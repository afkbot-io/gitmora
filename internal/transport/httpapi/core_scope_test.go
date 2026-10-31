package httpapi_test

import (
	"reflect"
	"testing"

	"workgraph.local/platform/internal/transport/httpapi"
)

func TestCoreHTTPDoesNotExposeRetiredFeatures(t *testing.T) {
	h := handler(&capture{}, auth{})
	for _, route := range []struct{ method, path string }{
		{"POST", "/v1/inference/runs"},
		{"GET", "/v1/projects/11111111-1111-4111-8111-111111111111/inference/configuration"},
		{"POST", "/v1/reviews"},
		{"POST", "/v1/review/runs"},
		{"GET", "/v1/projects/11111111-1111-4111-8111-111111111111/review/configuration"},
		{"GET", "/v1/projects/11111111-1111-4111-8111-111111111111/knowledge"},
	} {
		if result := request(h, route.method, route.path, "{}", ""); result.Code != 404 {
			t.Fatal("retired route remains registered", route.path, result.Code)
		}
	}
	for _, name := range []string{"Intelligence", "IntelligenceQueue", "Review", "ReviewQueue", "Knowledge"} {
		if _, found := reflect.TypeOf(httpapi.Config{}).FieldByName(name); found {
			t.Fatal("retired composition port remains", name)
		}
	}
}
