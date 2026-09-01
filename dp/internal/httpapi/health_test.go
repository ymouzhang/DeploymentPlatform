package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"DP/internal/health"
	"DP/internal/testutil"
)

func TestHealthzReturnsMinimalJSON(t *testing.T) {
	dataDir := t.TempDir()
	db := testutil.OpenPostgres(t)
	api := &API{health: health.NewMonitor(db, dataDir, time.Minute)}
	response := httptest.NewRecorder()
	api.healthz(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q", contentType)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || len(body) != 1 {
		t.Fatalf("body = %#v", body)
	}
	db.Close()

	response = httptest.NewRecorder()
	api.healthz(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"status\":\"error\"}\n" {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
