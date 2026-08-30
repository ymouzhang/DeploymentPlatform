package audit

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/domain"
	"DP/internal/store"
)

func TestLoginFailureThresholdAndOperationCompletion(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewService(db, 180, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for index := 0; index < 5; index++ {
		service.Record(ctx, domain.AuditEvent{Category: "authentication", Action: "auth.login", Outcome: "failure", ActorUsername: "user", SourceIP: "192.0.2.1", RequestID: store.NewID()})
	}
	from, to := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	summary, err := db.AuditSummary(ctx, domain.AuditFilter{From: &from, To: &to})
	if err != nil || summary.LoginFailures != 5 || summary.HighRisk != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	notifications, err := db.NotificationSummary(ctx)
	if err != nil || notifications.Unread != 1 {
		t.Fatalf("login notification summary=%+v err=%v", notifications, err)
	}
	service.Record(ctx, domain.AuditEvent{Category: "account", Action: "account.create", Outcome: "success", ActorUserID: "admin", ActorUsername: "admin", OwnerID: "new-user", OwnerUsername: "new-user", TargetType: "user", TargetID: "new-user", TargetLabel: "new-user"})
	notifications, _ = db.NotificationSummary(ctx)
	if notifications.Unread != 1 {
		t.Fatalf("routine account creation should not alert: %+v", notifications)
	}
	opID := store.NewID()
	service.Record(ctx, domain.AuditEvent{Category: "service", Action: "service.install.requested", Outcome: "success", OperationID: opID, ActorUsername: "admin", RequestID: store.NewID()})
	service.CompleteOperation(ctx, domain.Operation{ID: opID, Status: domain.OperationFailed, Stage: "script", ErrorCode: "FAILED"})
	items, err := db.ListAuditEvents(ctx, domain.AuditFilter{Action: "service.install.completed", Limit: 10})
	if err != nil || len(items) != 1 || items[0].Outcome != "failure" || items[0].ErrorCode != "FAILED" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	notifications, _ = db.NotificationSummary(ctx)
	if notifications.Unread != 2 {
		t.Fatalf("failed operation should alert: %+v", notifications)
	}
}

func TestLoginThrottleAuditsAreAggregated(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewService(db, 180, slog.New(slog.NewTextHandler(io.Discard, nil)))
	event := domain.AuditEvent{Category: "authentication", Action: "auth.login", Outcome: "failure",
		ErrorCode: "LOGIN_THROTTLED", ActorUsername: "user", SourceIP: "192.0.2.1"}
	service.Record(ctx, event)
	service.Record(ctx, event)
	items, err := db.ListAuditEvents(ctx, domain.AuditFilter{Action: "auth.login", Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}
