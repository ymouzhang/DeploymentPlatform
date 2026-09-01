package httpapi

import (
	"strings"
	"testing"

	"DP/internal/access"
)

func TestProtectedRoutesMatchPermissionCatalog(t *testing.T) {
	t.Parallel()
	catalog := make(map[access.Permission]struct{})
	for _, definition := range access.Definitions() {
		catalog[definition.Key] = struct{}{}
	}

	used := make(map[access.Permission]struct{})
	patterns := make(map[string]struct{})
	for _, route := range (&API{}).protectedRoutes() {
		if !strings.Contains(route.pattern, " /api/v1/") {
			t.Errorf("invalid protected route pattern %q", route.pattern)
		}
		if route.handler == nil {
			t.Errorf("protected route %q has no handler", route.pattern)
		}
		if _, exists := patterns[route.pattern]; exists {
			t.Errorf("duplicate protected route %q", route.pattern)
		}
		patterns[route.pattern] = struct{}{}
		if _, exists := catalog[route.permission]; !exists {
			t.Errorf("route %q uses unknown permission %q", route.pattern, route.permission)
		}
		used[route.permission] = struct{}{}
	}

	for permission := range catalog {
		if _, exists := used[permission]; !exists {
			t.Errorf("permission %q has no protected route", permission)
		}
	}
}
