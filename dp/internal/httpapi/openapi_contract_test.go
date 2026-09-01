package httpapi

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestProtectedRoutesAreDocumentedInOpenAPI(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Security []map[string]any          `yaml:"security"`
		Paths    map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("parse OpenAPI contract: %v", err)
	}
	for _, route := range (&API{}).protectedRoutes() {
		parts := strings.SplitN(route.pattern, " ", 2)
		method := strings.ToLower(parts[0])
		path := strings.TrimPrefix(parts[1], "/api/v1")
		rawOperation, exists := document.Paths[path][method]
		if !exists {
			t.Errorf("protected route %s is missing from OpenAPI", route.pattern)
			continue
		}
		operation, ok := rawOperation.(map[string]any)
		if !ok {
			t.Errorf("protected route %s has an invalid OpenAPI operation", route.pattern)
		}
		_ = operation
	}
	if len(document.Security) != 1 || document.Security[0]["cookieAuth"] == nil {
		t.Error("OpenAPI must apply cookieAuth to protected operations by default")
	}
}
