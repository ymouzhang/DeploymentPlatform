package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/domain"
)

func TestAuditEventsPreserveSnapshotsAndFilter(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.CreateUser(ctx, domain.User{Username: "audited", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	event, err := db.CreateAuditEvent(ctx, domain.AuditEvent{
		OccurredAt: now, Category: "account", Action: "account.delete", Outcome: "success",
		RiskLevel: "high", ActorUsername: "admin", OwnerID: user.ID,
		OwnerUsername: user.Username, TargetType: "user", TargetID: user.ID,
		TargetLabel: user.Username, RequestID: NewID(), Changes: map[string]any{"enabled": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetAuditEvent(ctx, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.OwnerUsername != "audited" || stored.TargetLabel != "audited" {
		t.Fatalf("snapshot lost: %+v", stored)
	}
	from, to := now.Add(-time.Minute), now.Add(time.Minute)
	items, err := db.ListAuditEvents(ctx, domain.AuditFilter{From: &from, To: &to, Outcome: "success", Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	summary, err := db.AuditSummary(ctx, domain.AuditFilter{From: &from, To: &to})
	if err != nil || summary.Total != 1 || summary.HighRisk != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}
