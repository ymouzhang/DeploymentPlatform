package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"DP/internal/access"
	"github.com/google/uuid"
)

func TestRBACRepositoryIntegration(t *testing.T) {
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

	actorID := uuid.NewString()
	operatorID := uuid.NewString()
	for _, user := range []struct {
		id, name string
	}{{actorID, "rbac-super"}, {operatorID, "rbac-operator"}} {
		_, err := db.pool.Exec(ctx, `INSERT INTO users
			(id, username, password_hash, enabled, must_change_password, is_initial_admin, created_at, updated_at)
			VALUES ($1, $2, 'hash', TRUE, FALSE, FALSE, now(), now())`, user.id, user.name)
		if err != nil {
			t.Fatalf("insert user %s: %v", user.name, err)
		}
	}
	_, err = db.pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_id, assigned_at) VALUES
		($1, '00000000-0000-4000-8000-000000000101', now()),
		($2, '00000000-0000-4000-8000-000000000103', now())`, actorID, operatorID)
	if err != nil {
		t.Fatalf("assign built-in roles: %v", err)
	}

	created, err := db.CreateRole(ctx, actorID, access.Role{
		Key: "integration_reader", Name: "集成测试只读",
		Grants: []access.Grant{{Permission: access.EnvironmentRead, Scope: access.ScopeOwn}},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	created.Name = "集成测试查看者"
	created.Grants = []access.Grant{{Permission: access.EnvironmentRead, Scope: access.ScopeAll}}
	updated, err := db.UpdateRole(ctx, created)
	if err != nil {
		t.Fatalf("update role: %v", err)
	}
	if updated.Name != created.Name || len(updated.Grants) != 1 || updated.Grants[0].Scope != access.ScopeAll {
		t.Fatalf("unexpected updated role: %+v", updated)
	}

	if err := db.ReplaceUserRoles(ctx, actorID, operatorID, []string{created.ID}); err != nil {
		t.Fatalf("replace user roles: %v", err)
	}
	if err := db.DeleteRole(ctx, created.ID); !errors.Is(err, access.ErrInUse) {
		t.Fatalf("expected in-use error, got %v", err)
	}
	if err := db.ReplaceUserRoles(ctx, actorID, actorID, []string{created.ID}); !errors.Is(err, access.ErrProtected) {
		t.Fatalf("expected self-demotion protection, got %v", err)
	}

	if err := db.ReplaceUserRoles(ctx, actorID, operatorID, []string{
		"00000000-0000-4000-8000-000000000103",
	}); err != nil {
		t.Fatalf("restore operator role: %v", err)
	}
	if err := db.DeleteRole(ctx, created.ID); err != nil {
		t.Fatalf("delete unused role: %v", err)
	}
}
