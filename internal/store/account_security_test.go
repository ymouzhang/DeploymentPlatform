package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/domain"
)

func TestListStaleUsersExcludesInitialAdminAndRecentLogin(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	stale, err := db.CreateUser(ctx, domain.User{Username: "stale-user", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	recent, err := db.CreateUser(ctx, domain.User{Username: "recent-user", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	old := formatTime(now.Add(-100 * 24 * time.Hour))
	if _, err := db.db.ExecContext(ctx, `UPDATE users SET created_at = ? WHERE id IN (?, ?, ?)`, old, stale.ID, recent.ID, InitialAdminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAuditEvent(ctx, domain.AuditEvent{Action: "auth.login", Category: "authentication", Outcome: "success", ActorUserID: recent.ID, ActorUsername: recent.Username, OccurredAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	users, err := db.ListStaleUsers(ctx, now.Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ID != stale.ID {
		t.Fatalf("stale users=%+v", users)
	}
}
