package application

import (
	"context"
	"errors"
	"testing"

	"DP/internal/access"
	"DP/internal/domain"
)

func TestRoleServiceRejectsGrantEscalation(t *testing.T) {
	repository := &roleRepositoryStub{
		permissions: []access.Definition{
			{Key: access.EnvironmentRead, Scoped: true},
		},
	}
	service := NewRoleService(repository)
	actor := access.Subject{
		UserID: "actor",
		Grants: access.Grants{
			access.RoleCreate:      access.ScopeAll,
			access.EnvironmentRead: access.ScopeOwn,
		},
	}

	_, err := service.CreateRole(context.Background(), actor, RoleCreateInput{
		Key: "cross_owner_reader", Name: "跨账号只读",
		Grants: []access.Grant{{Permission: access.EnvironmentRead, Scope: access.ScopeAll}},
	})
	assertAppErrorCode(t, err, "GRANT_FORBIDDEN")
	if repository.created {
		t.Fatal("repository must not be called after grant escalation is rejected")
	}
}

func TestRoleServiceRejectsOwnScopeForGlobalPermission(t *testing.T) {
	repository := &roleRepositoryStub{
		permissions: []access.Definition{{Key: access.AuditRead, Scoped: false}},
	}
	service := NewRoleService(repository)
	actor := access.Subject{UserID: "actor", Grants: access.Grants{
		access.RoleCreate: access.ScopeAll,
		access.AuditRead:  access.ScopeAll,
	}}

	_, err := service.CreateRole(context.Background(), actor, RoleCreateInput{
		Key: "audit_reader", Name: "审计查看者",
		Grants: []access.Grant{{Permission: access.AuditRead, Scope: access.ScopeOwn}},
	})
	var fieldErr *domain.FieldValidationError
	if !errors.As(err, &fieldErr) || fieldErr.Field != "grants" {
		t.Fatalf("expected grants validation error, got %v", err)
	}
}

func TestRoleServiceRejectsSystemRoleUpdate(t *testing.T) {
	repository := &roleRepositoryStub{roles: map[string]access.Role{
		"system": {ID: "system", Key: access.RoleOperator, System: true},
	}}
	service := NewRoleService(repository)
	actor := access.Subject{UserID: "actor", Grants: access.Grants{access.RoleUpdate: access.ScopeAll}}

	_, err := service.UpdateRole(context.Background(), actor, "system", RoleUpdateInput{Name: "changed"})
	assertAppErrorCode(t, err, "ROLE_PROTECTED")
}

func TestRoleServiceRejectsSuperAdminAssignmentByNonSuperAdmin(t *testing.T) {
	repository := &roleRepositoryStub{roles: map[string]access.Role{
		"super": {ID: "super", Key: access.RoleSuperAdmin},
	}}
	service := NewRoleService(repository)
	actor := access.Subject{UserID: "actor", Grants: access.Grants{access.AccountAssignRoles: access.ScopeAll}}

	err := service.ReplaceUserRoles(context.Background(), actor, "target", []string{"super"})
	assertAppErrorCode(t, err, "GRANT_FORBIDDEN")
}

func assertAppErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *domain.AppError
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("expected app error %s, got %v", code, err)
	}
}

type roleRepositoryStub struct {
	permissions []access.Definition
	roles       map[string]access.Role
	created     bool
}

func (r *roleRepositoryStub) ListPermissions(context.Context) ([]access.Definition, error) {
	return r.permissions, nil
}

func (r *roleRepositoryStub) ListRoles(context.Context) ([]access.Role, error) {
	return nil, nil
}

func (r *roleRepositoryStub) GetRole(_ context.Context, id string) (access.Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return access.Role{}, access.ErrNotFound
	}
	return role, nil
}

func (r *roleRepositoryStub) AccessForUser(context.Context, string) (access.Subject, error) {
	return access.Subject{}, nil
}

func (r *roleRepositoryStub) CreateRole(_ context.Context, _ string, role access.Role) (access.Role, error) {
	r.created = true
	return role, nil
}

func (r *roleRepositoryStub) UpdateRole(_ context.Context, role access.Role) (access.Role, error) {
	return role, nil
}

func (r *roleRepositoryStub) DeleteRole(context.Context, string) error {
	return nil
}

func (r *roleRepositoryStub) ReplaceUserRoles(context.Context, string, string, []string) error {
	return nil
}
