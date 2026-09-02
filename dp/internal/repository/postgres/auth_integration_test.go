package postgres

import (
	"context"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
	"DP/internal/testdb"
)

func TestAuthRepositoryIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, testdb.PostgresURL(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	pending, exists, err := db.PendingInitialAdmin(ctx)
	if err != nil || !exists {
		t.Fatalf("pending initial administrator: exists=%v err=%v", exists, err)
	}
	if !pending.IsInitialAdmin || !hasRoleRef(pending.Roles, access.RoleSuperAdmin) {
		t.Fatalf("pending administrator lacks protected role: %+v", pending)
	}
	admin, err := db.InitializeAdmin(ctx, initialAdminID, "integration-admin", "hash")
	if err != nil {
		t.Fatalf("initialize administrator: %v", err)
	}
	if admin.Username != "integration-admin" || !hasRoleRef(admin.Roles, access.RoleSuperAdmin) {
		t.Fatalf("unexpected administrator: %+v", admin)
	}

	user, err := db.CreateUser(ctx, domain.User{
		Username: "integration-user", PasswordHash: "hash",
		Roles:   []access.RoleRef{{ID: "00000000-0000-4000-8000-000000000103", Key: access.RoleOperator}},
		Enabled: true, CreatedBy: admin.ID,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if !hasRoleRef(user.Roles, access.RoleOperator) || user.Permissions[access.HostRead] != access.ScopeOwn {
		t.Fatalf("new user has unexpected access: %+v", user)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	session, err := db.CreateSession(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		user.ID, "192.0.2.1", "integration test", expiresAt)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	authenticated, loadedSession, err := db.UserForSession(ctx,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now().UTC())
	if err != nil {
		t.Fatalf("load session user: %v", err)
	}
	if authenticated.ID != user.ID || loadedSession.ID != session.ID || len(authenticated.Permissions) == 0 {
		t.Fatalf("unexpected session principal: user=%+v session=%+v", authenticated, loadedSession)
	}

	keys := []string{"username:integration-user", "ip:192.0.2.1"}
	var blockedUntil time.Time
	for range 5 {
		blockedUntil, err = db.RecordLoginFailure(ctx, keys, time.Now().UTC())
		if err != nil {
			t.Fatalf("record login failure: %v", err)
		}
	}
	if !blockedUntil.After(time.Now().UTC()) {
		t.Fatalf("expected active throttle, got %v", blockedUntil)
	}
	if err := db.ClearLoginThrottle(ctx, keys); err != nil {
		t.Fatalf("clear login throttle: %v", err)
	}
	if err := db.UpdateUserPasswordAndRevokeSessions(ctx, user.ID, "next-hash", false); err != nil {
		t.Fatalf("update password: %v", err)
	}
	items, err := db.ListUserSessions(ctx, user.ID, time.Now().UTC())
	if err != nil || len(items) != 0 {
		t.Fatalf("sessions were not revoked: items=%+v err=%v", items, err)
	}
}

func hasRoleRef(roles []access.RoleRef, key string) bool {
	for _, role := range roles {
		if role.Key == key {
			return true
		}
	}
	return false
}
