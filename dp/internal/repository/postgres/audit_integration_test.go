package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
)

func TestAuditRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("DP_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DP_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	event, err := db.CreateAuditEvent(ctx, domain.AuditEvent{
		Category: "authorization", Action: "role.create", Outcome: "success", RiskLevel: "normal",
		ActorUserID: domain.InitialAdminID, ActorUsername: "integration-admin",
		ActorRoles: []string{access.RoleSuperAdmin}, OwnerID: domain.InitialAdminID,
		OwnerUsername: "integration-admin", TargetType: "role", TargetID: domain.NewID(),
		TargetLabel: "integration-role", RequestID: domain.NewID(), SourceIP: "192.0.2.10",
		UserAgent: "integration test", Changes: map[string]any{"permission": "role.create"},
	})
	if err != nil {
		t.Fatalf("create audit event: %v", err)
	}
	loaded, err := db.GetAuditEvent(ctx, event.ID)
	if err != nil || len(loaded.ActorRoles) != 1 || loaded.ActorRoles[0] != access.RoleSuperAdmin ||
		loaded.SourceIP != "192.0.2.10" || loaded.Changes["permission"] != "role.create" {
		t.Fatalf("unexpected audit event: %+v err=%v", loaded, err)
	}
	items, err := db.ListAuditEvents(ctx, domain.AuditFilter{Keyword: "integration-role", Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("unexpected audit list: %+v err=%v", items, err)
	}
	summary, err := db.AuditSummary(ctx, domain.AuditFilter{Category: "authorization"})
	if err != nil || summary.Total != 1 {
		t.Fatalf("unexpected audit summary: %+v err=%v", summary, err)
	}

	notification := domain.Notification{
		DedupeKey: "integration-audit", RiskLevel: "normal", Category: "system",
		Title: "Integration", Message: "Integration notification", TargetType: "role",
		TargetID: event.TargetID, OwnerID: domain.InitialAdminID, OwnerUsername: "integration-admin",
		Link: "/roles",
	}
	created, err := db.CreateNotification(ctx, notification)
	if err != nil {
		t.Fatalf("create notification: %v", err)
	}
	duplicate, err := db.CreateNotification(ctx, notification)
	if err != nil || duplicate.ID != created.ID {
		t.Fatalf("notification dedupe failed: first=%+v duplicate=%+v err=%v", created, duplicate, err)
	}
	notificationSummary, err := db.NotificationSummary(ctx)
	if err != nil || notificationSummary.Unread != 1 || notificationSummary.Unresolved != 1 {
		t.Fatalf("unexpected notification summary: %+v err=%v", notificationSummary, err)
	}
	resolved, err := db.ResolveNotification(ctx, created.ID, domain.InitialAdminID, time.Now().UTC())
	if err != nil || !resolved.Read || !resolved.Resolved {
		t.Fatalf("resolve notification failed: %+v err=%v", resolved, err)
	}
	deleted, err := db.DeleteResolvedNotificationsBefore(ctx, time.Now().UTC().Add(time.Minute), 10)
	if err != nil || deleted != 1 {
		t.Fatalf("delete resolved notifications: deleted=%d err=%v", deleted, err)
	}
	deleted, err = db.DeleteAuditEventsBefore(ctx, time.Now().UTC().Add(time.Minute), 10)
	if err != nil || deleted != 1 {
		t.Fatalf("delete audit events: deleted=%d err=%v", deleted, err)
	}
}
