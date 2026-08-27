package api

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestOpenAPIContractIsValidYAML(t *testing.T) {
	content, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("parse OpenAPI contract: %v", err)
	}
	var header struct {
		OpenAPI string         `yaml:"openapi"`
		Paths   map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(content, &header); err != nil {
		t.Fatal(err)
	}
	if header.OpenAPI != "3.1.0" {
		t.Fatalf("openapi=%q", header.OpenAPI)
	}
	for _, path := range []string{"/admin/dashboard", "/operations", "/notifications", "/users/{id}/transfer"} {
		if _, ok := header.Paths[path]; !ok {
			t.Errorf("missing P0 path %s", path)
		}
	}
	for _, path := range []string{"/service-types/{type}/package/versions", "/service-types/{type}/package/versions/{versionId}/current", "/services/{id}/config/preview", "/services/{id}/config/revisions", "/services/{id}/config/revisions/{revisionId}/rollback"} {
		if _, ok := header.Paths[path]; !ok {
			t.Errorf("missing change-governance path %s", path)
		}
	}
	for _, path := range []string{"/tags", "/tags/{id}", "/environments/{id}/tags"} {
		if _, ok := header.Paths[path]; !ok {
			t.Errorf("missing resource-tag path %s", path)
		}
	}
	for _, path := range []string{"/auth/sessions", "/auth/sessions/{sessionId}", "/users/{id}/sessions", "/users/{id}/sessions/{sessionId}"} {
		if _, ok := header.Paths[path]; !ok {
			t.Errorf("missing account-security path %s", path)
		}
	}
	for _, path := range []string{"/communications/summary", "/communications", "/communications/{id}", "/communications/{id}/read", "/communications/{id}/messages", "/communications/{id}/close", "/communications/{id}/reopen"} {
		if _, ok := header.Paths[path]; !ok {
			t.Errorf("missing communication path %s", path)
		}
	}
	if _, ok := header.Paths["/events"]; !ok {
		t.Error("missing account-scoped realtime event path")
	}
	for _, field := range []string{"unread_communications:", "communications:"} {
		if !strings.Contains(string(content), field) {
			t.Errorf("admin dashboard contract is missing %s", field)
		}
	}
	var contract map[string]any
	if err := yaml.Unmarshal(content, &contract); err != nil {
		t.Fatal(err)
	}
	components, _ := contract["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	operationIDs := map[string]bool{}
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if ref, ok := typed["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				if _, exists := schemas[name]; !exists {
					t.Errorf("unresolved schema reference %s", ref)
				}
			}
			if id, ok := typed["operationId"].(string); ok {
				if operationIDs[id] {
					t.Errorf("duplicate operationId %s", id)
				}
				operationIDs[id] = true
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(contract)
}
