package postgres

import (
	"context"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/testdb"
)

func TestPermissionCatalogAndBuiltInRoleMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, testdb.PostgresURL(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	definitions, err := db.ListPermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantDefinitions := make(map[access.Permission]access.Definition)
	for _, definition := range access.Definitions() {
		wantDefinitions[definition.Key] = definition
	}
	if len(definitions) != len(wantDefinitions) {
		t.Fatalf("permission count = %d, want %d", len(definitions), len(wantDefinitions))
	}
	for _, definition := range definitions {
		want, exists := wantDefinitions[definition.Key]
		if !exists || definition != want {
			t.Errorf("database permission %+v, want %+v", definition, want)
		}
	}

	roles, err := db.ListRoles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]access.Role, len(roles))
	for _, role := range roles {
		byKey[role.Key] = role
	}
	if len(byKey) != 4 {
		t.Fatalf("built-in role count = %d, want 4", len(byKey))
	}
	allPermissions := make(map[access.Permission]access.Scope, len(definitions))
	for _, definition := range definitions {
		allPermissions[definition.Key] = access.ScopeAll
	}
	assertRoleGrants(t, byKey[access.RoleSuperAdmin], allPermissions)
	assertRoleGrants(t, byKey[access.RolePlatformAdmin], allPermissions)

	operator := make(map[access.Permission]access.Scope)
	for _, definition := range definitions {
		switch definition.Resource {
		case "package", "environment", "tag", "model", "service", "operation":
			operator[definition.Key] = access.ScopeOwn
		}
	}
	operator[access.CommunicationRead] = access.ScopeOwn
	operator[access.CommunicationReply] = access.ScopeOwn
	assertRoleGrants(t, byKey[access.RoleOperator], operator)

	viewer := map[access.Permission]access.Scope{
		access.PackageRead:       access.ScopeOwn,
		access.EnvironmentRead:   access.ScopeOwn,
		access.TagRead:           access.ScopeOwn,
		access.ModelRead:         access.ScopeOwn,
		access.ServiceRead:       access.ScopeOwn,
		access.ServiceConfigRead: access.ScopeOwn,
		access.ServiceLogRead:    access.ScopeOwn,
		access.OperationRead:     access.ScopeOwn,
		access.CommunicationRead: access.ScopeOwn,
	}
	assertRoleGrants(t, byKey[access.RoleViewer], viewer)
}

func assertRoleGrants(t *testing.T, role access.Role, want map[access.Permission]access.Scope) {
	t.Helper()
	if !role.System {
		t.Errorf("role %q is not system-owned", role.Key)
	}
	got := make(map[access.Permission]access.Scope, len(role.Grants))
	for _, grant := range role.Grants {
		got[grant.Permission] = grant.Scope
	}
	if len(got) != len(want) {
		t.Errorf("role %q grant count = %d, want %d", role.Key, len(got), len(want))
	}
	for permission, scope := range want {
		if got[permission] != scope {
			t.Errorf("role %q permission %q = %q, want %q", role.Key, permission, got[permission], scope)
		}
	}
}
