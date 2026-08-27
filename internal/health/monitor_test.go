package health

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"DP/internal/domain"
	"DP/internal/store"
)

func TestCheckAcceptsSupportedHealthyStatusValues(t *testing.T) {
	tests := []struct {
		name      string
		status    any
		wantState string
	}{
		{name: "health", status: "health", wantState: "running"},
		{name: "healthy", status: "healthy", wantState: "running"},
		{name: "unhealthy", status: "unhealthy", wantState: "stopped"},
		{name: "case sensitive", status: "HEALTHY", wantState: "stopped"},
		{name: "non string", status: true, wantState: "stopped"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": test.status})
			}))
			defer server.Close()
			host, rawPort, err := net.SplitHostPort(server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(rawPort)
			if err != nil {
				t.Fatal(err)
			}
			monitor := NewMonitor(nil, time.Minute)
			result := monitor.check(context.Background(), domain.Environment{IP: host, HealthPort: &port, Installed: true})
			if result.State != test.wantState {
				t.Fatalf("status=%v state=%q reason=%q", test.status, result.State, result.Reason)
			}
			if test.wantState == "running" && result.Reason != "" {
				t.Fatalf("running status has reason %q", result.Reason)
			}
		})
	}
}

func TestCheckAllClearsHealthForUninstalledEnvironment(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	env, err := db.CreateEnvironment(ctx, domain.Environment{
		Name:           "reset",
		IP:             "127.0.0.1",
		SSHUser:        "user",
		SSHPort:        22,
		SSHPasswordEnc: "encrypted",
		InstallDir:     "/opt/reset",
		ServiceType:    "image-forward",
	})
	if err != nil {
		t.Fatal(err)
	}

	monitor := NewMonitor(db, time.Minute)
	monitor.results[env.ID] = domain.HealthResult{State: "running"}

	monitor.checkAll(ctx)

	result := monitor.Snapshot(env.ID)
	if result.State != "not_configured" {
		t.Fatalf("health state = %q, want not_configured", result.State)
	}
}

func TestCheckAllRemovesHealthForDeletedEnvironment(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	monitor := NewMonitor(db, time.Minute)
	monitor.results["deleted-environment"] = domain.HealthResult{State: "running"}

	monitor.checkAll(ctx)

	if _, exists := monitor.results["deleted-environment"]; exists {
		t.Fatal("health result for deleted environment was not removed")
	}
}
