package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"DP/internal/application"
	"DP/internal/domain"
	"DP/internal/realtime"
	"DP/internal/store"
)

func TestRealtimeEventsStreamsSyncAndAccountEvent(t *testing.T) {
	hub := realtime.NewHub(4)
	api := &API{realtime: hub}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{
		User: domain.User{ID: "current-user", Role: domain.RoleUser, Enabled: true},
	}))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	recorder := newSSERecorder()
	done := make(chan struct{})
	go func() {
		api.realtimeEvents(recorder, request)
		close(done)
	}()
	waitForSSEContent(t, recorder, "event: sync")
	hub.Publish([]string{"current-user"}, realtime.NewEvent(realtime.CommunicationChanged, "thread-1", realtime.ChangeMessage))
	waitForSSEContent(t, recorder, "event: communication.changed")
	waitForSSEContent(t, recorder, `"resource_id":"thread-1"`)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not stop after disconnect")
	}
	if recorder.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers=%v", recorder.Header())
	}
}

func TestRealtimeEventsRevalidatesRevokedSessionOnHeartbeat(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := application.NewAuthService(db, time.Hour)
	if err := auth.InitializeAdmin(ctx, "stream-admin", "initial-password"); err != nil {
		t.Fatal(err)
	}
	user, token, _, err := auth.Login(ctx, "stream-admin", "initial-password")
	if err != nil {
		t.Fatal(err)
	}
	api := &API{auth: auth, realtime: realtime.NewHub(4), realtimeBeat: 10 * time.Millisecond}
	requestContext, cancel := context.WithCancel(ctx)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(requestContext)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: user}))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	recorder := newSSERecorder()
	done := make(chan struct{})
	go func() {
		api.realtimeEvents(recorder, request)
		close(done)
	}()
	waitForSSEContent(t, recorder, "event: sync")
	if err := auth.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("revoked session kept receiving realtime events")
	}
}

type sseRecorder struct {
	mu      sync.Mutex
	header  http.Header
	content bytes.Buffer
	flushed chan struct{}
}

func newSSERecorder() *sseRecorder {
	return &sseRecorder{header: make(http.Header), flushed: make(chan struct{}, 1)}
}

func (r *sseRecorder) Header() http.Header { return r.header }
func (r *sseRecorder) WriteHeader(int)     {}
func (r *sseRecorder) Write(content []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.content.Write(content)
}
func (r *sseRecorder) Flush() {
	select {
	case r.flushed <- struct{}{}:
	default:
	}
}
func (r *sseRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.content.String()
}

func waitForSSEContent(t *testing.T, recorder *sseRecorder, expected string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if strings.Contains(recorder.String(), expected) {
			return
		}
		select {
		case <-recorder.flushed:
		case <-deadline:
			t.Fatalf("SSE output did not contain %q: %s", expected, recorder.String())
		}
	}
}
