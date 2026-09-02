package health

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"DP/internal/domain"
	"DP/internal/testutil"
)

func TestCheckRequiresHTTP200AndOK(t *testing.T) {
	tests := []struct {
		name       string
		status     any
		httpStatus int
		want       string
	}{
		{name: "healthy", status: "ok", want: "ok"},
		{name: "error", status: "error", want: "error"},
		{name: "strict value", status: "OK", want: "error"},
		{name: "non string", status: true, want: "error"},
		{name: "non 200", status: "ok", httpStatus: http.StatusServiceUnavailable, want: "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					t.Errorf("path = %q", r.URL.Path)
				}
				if test.httpStatus != 0 {
					w.WriteHeader(test.httpStatus)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": test.status})
			}))
			defer server.Close()
			result := NewMonitor(nil, "", time.Minute).check(context.Background(), testServiceInstance(t, server))
			if result.Status != test.want {
				t.Fatalf("status = %q, want %q", result.Status, test.want)
			}
		})
	}
}

func TestCheckRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: "{"},
		{name: "oversized", body: `{"status":"ok","padding":"` + strings.Repeat("x", maxResponseBytes) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			result := NewMonitor(nil, "", time.Minute).check(context.Background(), testServiceInstance(t, server))
			if result.Status != "error" {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestCheckTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	monitor := NewMonitor(nil, "", time.Minute)
	monitor.client.Timeout = 20 * time.Millisecond
	if result := monitor.check(context.Background(), testServiceInstance(t, server)); result.Status != "error" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSnapshotMarksStaleResultUnknown(t *testing.T) {
	monitor := NewMonitor(nil, "", time.Second)
	checkedAt := time.Now().Add(-time.Minute)
	monitor.results["serviceInstance"] = domain.HealthResult{Status: "ok", CheckedAt: &checkedAt}
	if result := monitor.Snapshot("serviceInstance"); result.Status != "unknown" {
		t.Fatalf("result = %#v", result)
	}
}

func TestHealthChecksDatabaseAndStorage(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db := testutil.OpenPostgres(t)
	monitor := NewMonitor(db, dataDir, time.Minute)
	if result := monitor.Health(ctx); result.Status != "ok" {
		t.Fatalf("health result = %#v", result)
	}
	db.Close()
	if result := monitor.Health(ctx); result.Status != "error" {
		t.Fatalf("closed database result = %#v", result)
	}

	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := NewMonitor(nil, filePath, time.Minute).Health(ctx); result.Status != "error" {
		t.Fatalf("storage result = %#v", result)
	}
}

func TestHealthNotificationResolvesAfterRecovery(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db := testutil.OpenPostgres(t)
	monitor := NewMonitor(db, dataDir, time.Minute)
	env := domain.ServiceInstance{ID: "serviceInstance", Name: "service", Installed: true}
	for range 3 {
		monitor.recordResult(ctx, env, domain.HealthResult{Status: "error"})
	}
	summary, err := db.NotificationSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Unresolved != 1 {
		t.Fatalf("unresolved notifications = %d", summary.Unresolved)
	}
	monitor.recordResult(ctx, env, domain.HealthResult{Status: "ok"})
	summary, err = db.NotificationSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Unresolved != 0 {
		t.Fatalf("unresolved notifications after recovery = %d", summary.Unresolved)
	}
}

func TestCheckAllClearsAndRemovesResults(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db := testutil.OpenPostgres(t)
	env, err := testutil.CreateServiceInstance(t, ctx, db, domain.ServiceInstance{
		OwnerID: domain.InitialAdminID, Name: "reset", Host: domain.Host{IP: "127.0.0.1", SSHUser: "user", SSHPort: 22, SSHPasswordEnc: "encrypted"}, InstallDir: "/opt/reset", ServiceType: "dp-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(db, dataDir, time.Minute)
	monitor.results[env.ID] = domain.HealthResult{Status: "ok"}
	monitor.results["deleted"] = domain.HealthResult{Status: "ok"}
	monitor.checkAll(ctx)
	if result := monitor.Snapshot(env.ID); result.Status != "unknown" {
		t.Fatalf("uninstalled result = %#v", result)
	}
	if _, exists := monitor.results["deleted"]; exists {
		t.Fatal("deleted serviceInstance result was retained")
	}
}

func testServiceInstance(t *testing.T, server *httptest.Server) domain.ServiceInstance {
	t.Helper()
	host, rawPort, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ServiceInstance{Host: domain.Host{IP: host}, HealthPort: &port, Installed: true}
}
