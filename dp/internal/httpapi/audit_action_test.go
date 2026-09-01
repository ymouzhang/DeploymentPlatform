package httpapi

import (
	"net/http"
	"testing"
)

func TestRBACWriteRoutesAreAudited(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method string
		path   string
		action string
	}{
		{http.MethodPost, "/api/v1/roles", "role.create"},
		{http.MethodPut, "/api/v1/roles/00000000-0000-4000-8000-000000000201", "role.update"},
		{http.MethodDelete, "/api/v1/roles/00000000-0000-4000-8000-000000000201", "role.delete"},
		{http.MethodPut, "/api/v1/users/00000000-0000-4000-8000-000000000001/roles", "account.role.update"},
	}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			category, action, target, audited := auditAction(test.method, test.path)
			if !audited || category != "authorization" || action != test.action || target == "" {
				t.Fatalf("auditAction(%s, %s) = %q, %q, %q, %v", test.method, test.path, category, action, target, audited)
			}
		})
	}
}
