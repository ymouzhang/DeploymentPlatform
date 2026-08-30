package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"DP/internal/health"
	"DP/internal/store"
)

func TestHealthzReturnsMinimalJSON(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dataDir, "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
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
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	response = httptest.NewRecorder()
	api.healthz(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"status\":\"error\"}\n" {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
