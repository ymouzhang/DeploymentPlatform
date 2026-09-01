package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/audit"
	"DP/internal/domain"
	"DP/internal/testutil"
)

func TestAuditMiddlewareRecordsSanitizedTarget(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenPostgres(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditService := audit.NewService(db, 180, logger)
	api := &API{store: db, audit: auditService, log: logger}
	user, err := db.CreateUser(ctx, testutil.User(t, "audit-operator", access.RoleOperator, true))
	if err != nil {
		t.Fatal(err)
	}
	handler := api.auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setAuditTarget(r, user, "environment", "env-1", "production", map[string]any{"ssh_password_changed": true})
		writeData(w, http.StatusCreated, map[string]bool{"ok": true})
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/environments", strings.NewReader(`{"ssh_password":"secret"}`))
	request.RemoteAddr = "192.0.2.10:1234"
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: user}))
	request = request.WithContext(context.WithValue(request.Context(), requestIDKey, domain.NewID()))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	from, to := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	events, err := db.ListAuditEvents(ctx, domain.AuditFilter{From: &from, To: &to, Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	event := events[0]
	if event.ActorUsername != user.Username || event.TargetLabel != "production" || event.SourceIP != "192.0.2.10" {
		t.Fatalf("event=%+v", event)
	}
	encoded := event.Changes["ssh_password_changed"]
	if encoded != true || strings.Contains(string(mustJSON(t, event.Changes)), "secret") {
		t.Fatalf("changes=%+v", event.Changes)
	}
}

func TestAuditSourceIPOnlyTrustsConfiguredProxy(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.8, 10.0.0.3")
	api := &API{}
	if got := api.sourceIP(request); got != "10.0.0.2" {
		t.Fatalf("untrusted source=%q", got)
	}
	api.trustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	if got := api.sourceIP(request); got != "198.51.100.8" {
		t.Fatalf("trusted source=%q", got)
	}
}

func TestAuditEndpointRejectsOrdinaryUser(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/audit-events", nil)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: domain.User{Permissions: access.Grants{}}}))
	request = request.WithContext(context.WithValue(request.Context(), requestIDKey, "request-2"))
	recorder := httptest.NewRecorder()
	api := &API{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := api.requirePermission(access.AuditRead, http.HandlerFunc(api.listAuditEvents))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCSVCellPreventsFormulaInjection(t *testing.T) {
	for _, value := range []string{"=cmd", "+1", "-1", "@SUM(A1)"} {
		if got := csvCell(value); !strings.HasPrefix(got, "'") {
			t.Fatalf("csvCell(%q)=%q", value, got)
		}
	}
	if got := csvCell("normal"); got != "normal" {
		t.Fatalf("normal=%q", got)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
