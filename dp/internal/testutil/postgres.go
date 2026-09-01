// Package testutil contains integration-test helpers shared across DP packages.
package testutil

import (
	"context"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
	"DP/internal/repository/postgres"
	"DP/internal/testdb"
)

var systemRoleIDs = map[string]string{
	access.RoleSuperAdmin:    "00000000-0000-4000-8000-000000000101",
	access.RolePlatformAdmin: "00000000-0000-4000-8000-000000000102",
	access.RoleOperator:      "00000000-0000-4000-8000-000000000103",
	access.RoleViewer:        "00000000-0000-4000-8000-000000000104",
}

// RoleRef returns the immutable reference for a built-in role.
func RoleRef(t testing.TB, key string) access.RoleRef {
	t.Helper()
	id, ok := systemRoleIDs[key]
	if !ok {
		t.Fatalf("unknown built-in role %q", key)
	}
	return access.RoleRef{ID: id, Key: key}
}

// User returns a repository-ready user with one built-in role.
func User(t testing.TB, username, roleKey string, enabled bool) domain.User {
	t.Helper()
	return domain.User{
		Username: username, PasswordHash: "test-password-hash", Enabled: enabled,
		Roles: []access.RoleRef{RoleRef(t, roleKey)},
	}
}

// OpenPostgres creates an isolated schema in the configured PostgreSQL test database.
// Tests using it are integration tests and are skipped unless DP_TEST_DATABASE_URL is set.
func OpenPostgres(t testing.TB) *postgres.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, testdb.PostgresURL(t))
	if err != nil {
		t.Fatalf("open PostgreSQL test repository: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}
