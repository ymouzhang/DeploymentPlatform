package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
	"DP/internal/testdb"
)

func TestPostgreSQLConstraintsAndTransactionRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, testdb.PostgresURL(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	t.Run("unique role key", func(t *testing.T) {
		role := access.Role{Key: "constraint_reader", Name: "约束测试"}
		if _, err := db.CreateRole(ctx, domain.InitialAdminID, role); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateRole(ctx, domain.InitialAdminID, role); !errors.Is(err, access.ErrConflict) {
			t.Fatalf("duplicate role error = %v", err)
		}
	})

	t.Run("foreign key failure rolls back user", func(t *testing.T) {
		_, err := db.CreateUser(ctx, domain.User{
			Username: "rollback-user", PasswordHash: "hash", Enabled: true,
			Roles: []access.RoleRef{
				{ID: "00000000-0000-4000-8000-000000000103", Key: access.RoleOperator},
				{ID: "00000000-0000-4000-8000-999999999999", Key: "missing"},
			},
		})
		var fieldErr *domain.FieldValidationError
		if !errors.As(err, &fieldErr) || fieldErr.Field != "role_ids" {
			t.Fatalf("unknown role error = %v", err)
		}
		if _, err := db.GetUserByUsername(ctx, "rollback-user"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("partially inserted user remains: %v", err)
		}
	})

	t.Run("soft delete releases tag uniqueness", func(t *testing.T) {
		owner := createRepositoryUser(t, ctx, db, "tag-owner", access.RoleOperator, true)
		input := domain.ResourceTagInput{GroupName: "Project", Value: "Alpha"}
		first, err := db.CreateResourceTag(ctx, owner.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateResourceTag(ctx, owner.ID, domain.ResourceTagInput{GroupName: "project", Value: "alpha"}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("duplicate active tag error = %v", err)
		}
		if err := db.DeleteResourceTag(ctx, first.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateResourceTag(ctx, owner.ID, input); err != nil {
			t.Fatalf("recreate soft-deleted tag: %v", err)
		}
	})

	t.Run("resource foreign key prevents owner deletion", func(t *testing.T) {
		owner := createRepositoryUser(t, ctx, db, "environment-owner", access.RoleOperator, true)
		_, err := db.CreateEnvironment(ctx, domain.Environment{
			OwnerID: owner.ID, Name: "constraint-env", IP: "192.0.2.20", SSHUser: "dp",
			SSHPort: 22, SSHPasswordEnc: "encrypted", InstallDir: "/opt/dp", ServiceType: "demo",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.DeleteUser(ctx, owner.ID); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("delete referenced owner error = %v", err)
		}
	})
}
