package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
	"DP/internal/testdb"
	"github.com/google/uuid"
)

func TestConcurrentAccountMutationsPreserveEnabledSuperAdmin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, testdb.PostgresURL(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	first := createRepositoryUser(t, ctx, db, "super-first", access.RoleSuperAdmin, true)
	second := createRepositoryUser(t, ctx, db, "super-second", access.RoleSuperAdmin, true)
	operatorRoleID := "00000000-0000-4000-8000-000000000103"
	if err := db.ReplaceUserRoles(ctx, first.ID, domain.InitialAdminID, []string{operatorRoleID}); err != nil {
		t.Fatalf("remove pending administrator role: %v", err)
	}

	start := make(chan struct{})
	errorsByAction := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		_, err := db.UpdateUserEnabled(ctx, first.ID, false)
		errorsByAction <- err
	}()
	go func() {
		defer group.Done()
		<-start
		errorsByAction <- db.DeleteUser(ctx, second.ID)
	}()
	close(start)
	group.Wait()
	close(errorsByAction)

	var succeeded, protected int
	for err := range errorsByAction {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, access.ErrProtected):
			protected++
		default:
			t.Fatalf("unexpected concurrent mutation error: %v", err)
		}
	}
	if succeeded != 1 || protected != 1 {
		t.Fatalf("successful mutations = %d, protected mutations = %d", succeeded, protected)
	}

	var enabledSuperAdmins int
	if err := db.pool.QueryRow(ctx, `SELECT count(DISTINCT u.id)
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE u.enabled AND r.key = $1`, access.RoleSuperAdmin).Scan(&enabledSuperAdmins); err != nil {
		t.Fatal(err)
	}
	if enabledSuperAdmins != 1 {
		t.Fatalf("enabled super administrators = %d, want 1", enabledSuperAdmins)
	}
}

func TestCannotEnableUserWithoutRole(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, testdb.PostgresURL(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	actor := createRepositoryUser(t, ctx, db, "role-actor", access.RoleSuperAdmin, true)
	target := createRepositoryUser(t, ctx, db, "roleless-target", access.RoleOperator, false)
	if err := db.ReplaceUserRoles(ctx, actor.ID, target.ID, nil); err != nil {
		t.Fatalf("clear disabled user roles: %v", err)
	}
	if _, err := db.UpdateUserEnabled(ctx, target.ID, true); !errors.Is(err, access.ErrInvalidInput) {
		t.Fatalf("enable roleless user error = %v", err)
	}
}

func createRepositoryUser(
	t *testing.T,
	ctx context.Context,
	db *DB,
	username string,
	roleKey string,
	enabled bool,
) domain.User {
	t.Helper()
	roleIDs := map[string]string{
		access.RoleSuperAdmin: "00000000-0000-4000-8000-000000000101",
		access.RoleOperator:   "00000000-0000-4000-8000-000000000103",
	}
	user, err := db.CreateUser(ctx, domain.User{
		ID: uuid.NewString(), Username: username, PasswordHash: "hash", Enabled: enabled,
		Roles: []access.RoleRef{{ID: roleIDs[roleKey], Key: roleKey}},
	})
	if err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return user
}
