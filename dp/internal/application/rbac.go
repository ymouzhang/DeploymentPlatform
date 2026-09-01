package application

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"DP/internal/access"
	"DP/internal/domain"
)

var roleKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,62}$`)

type RoleRepository interface {
	ListPermissions(context.Context) ([]access.Definition, error)
	ListRoles(context.Context) ([]access.Role, error)
	GetRole(context.Context, string) (access.Role, error)
	AccessForUser(context.Context, string) (access.Subject, error)
	CreateRole(context.Context, string, access.Role) (access.Role, error)
	UpdateRole(context.Context, access.Role) (access.Role, error)
	DeleteRole(context.Context, string) error
	ReplaceUserRoles(context.Context, string, string, []string) error
}

type RoleCreateInput struct {
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Grants      []access.Grant `json:"grants"`
}

type RoleUpdateInput struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Grants      []access.Grant `json:"grants"`
}

type RoleService struct {
	repository RoleRepository
}

func NewRoleService(repository RoleRepository) *RoleService {
	return &RoleService{repository: repository}
}

func (s *RoleService) ListPermissions(ctx context.Context, actor access.Subject) ([]access.Definition, error) {
	if err := requirePermission(actor, access.RoleRead); err != nil {
		return nil, err
	}
	return s.repository.ListPermissions(ctx)
}

func (s *RoleService) ListRoles(ctx context.Context, actor access.Subject) ([]access.Role, error) {
	if err := requirePermission(actor, access.RoleRead); err != nil {
		return nil, err
	}
	return s.repository.ListRoles(ctx)
}

func (s *RoleService) GetRole(ctx context.Context, actor access.Subject, id string) (access.Role, error) {
	if err := requirePermission(actor, access.RoleRead); err != nil {
		return access.Role{}, err
	}
	role, err := s.repository.GetRole(ctx, id)
	return role, mapRoleRepositoryError(err)
}

func (s *RoleService) CreateRole(
	ctx context.Context,
	actor access.Subject,
	input RoleCreateInput,
) (access.Role, error) {
	if err := requirePermission(actor, access.RoleCreate); err != nil {
		return access.Role{}, err
	}
	input.Key = strings.ToLower(strings.TrimSpace(input.Key))
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if err := validateRoleIdentity(input.Key, input.Name, input.Description, true); err != nil {
		return access.Role{}, err
	}
	if err := s.validateGrants(ctx, actor, input.Grants); err != nil {
		return access.Role{}, err
	}
	role, err := s.repository.CreateRole(ctx, actor.UserID, access.Role{
		Key: input.Key, Name: input.Name, Description: input.Description, Grants: input.Grants,
	})
	return role, mapRoleRepositoryError(err)
}

func (s *RoleService) UpdateRole(
	ctx context.Context,
	actor access.Subject,
	id string,
	input RoleUpdateInput,
) (access.Role, error) {
	if err := requirePermission(actor, access.RoleUpdate); err != nil {
		return access.Role{}, err
	}
	current, err := s.repository.GetRole(ctx, id)
	if err != nil {
		return access.Role{}, mapRoleRepositoryError(err)
	}
	if current.System {
		return access.Role{}, roleProtectedError()
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if err := validateRoleIdentity("", input.Name, input.Description, false); err != nil {
		return access.Role{}, err
	}
	if err := s.validateGrants(ctx, actor, input.Grants); err != nil {
		return access.Role{}, err
	}
	current.Name = input.Name
	current.Description = input.Description
	current.Grants = input.Grants
	role, err := s.repository.UpdateRole(ctx, current)
	return role, mapRoleRepositoryError(err)
}

func (s *RoleService) DeleteRole(ctx context.Context, actor access.Subject, id string) error {
	if err := requirePermission(actor, access.RoleDelete); err != nil {
		return err
	}
	return mapRoleRepositoryError(s.repository.DeleteRole(ctx, id))
}

func (s *RoleService) ReplaceUserRoles(
	ctx context.Context,
	actor access.Subject,
	userID string,
	roleIDs []string,
) error {
	if err := requirePermission(actor, access.AccountAssignRoles); err != nil {
		return err
	}
	if hasDuplicateStrings(roleIDs) {
		return domain.FieldError("role_ids", "角色不能重复")
	}
	for _, roleID := range roleIDs {
		if strings.TrimSpace(roleID) == "" {
			return domain.FieldError("role_ids", "角色 ID 不能为空")
		}
		role, err := s.repository.GetRole(ctx, roleID)
		if err != nil {
			return mapRoleRepositoryError(err)
		}
		if role.Key == access.RoleSuperAdmin && !actor.HasRole(access.RoleSuperAdmin) {
			return grantForbiddenError()
		}
		for _, grant := range role.Grants {
			if !actor.Grants.CanGrant(grant.Permission, grant.Scope) {
				return grantForbiddenError()
			}
		}
	}
	return mapRoleRepositoryError(s.repository.ReplaceUserRoles(ctx, actor.UserID, userID, roleIDs))
}

func (s *RoleService) validateGrants(
	ctx context.Context,
	actor access.Subject,
	grants []access.Grant,
) error {
	definitions, err := s.repository.ListPermissions(ctx)
	if err != nil {
		return err
	}
	catalog := make(map[access.Permission]access.Definition, len(definitions))
	for _, definition := range definitions {
		catalog[definition.Key] = definition
	}
	seen := make(map[access.Permission]struct{}, len(grants))
	for _, grant := range grants {
		definition, ok := catalog[grant.Permission]
		if !ok {
			return domain.FieldError("grants", "包含未知权限")
		}
		if _, ok := seen[grant.Permission]; ok {
			return domain.FieldError("grants", "权限不能重复")
		}
		seen[grant.Permission] = struct{}{}
		if !grant.Scope.Valid() || !definition.Scoped && grant.Scope != access.ScopeAll {
			return domain.FieldError("grants", "权限数据范围无效")
		}
		if !actor.Grants.CanGrant(grant.Permission, grant.Scope) {
			return grantForbiddenError()
		}
	}
	return nil
}

func validateRoleIdentity(key, name, description string, validateKey bool) error {
	if validateKey && !roleKeyPattern.MatchString(key) {
		return domain.FieldError("key", "角色标识必须以小写字母开头，只能包含小写字母、数字、下划线或连字符，长度为 2 到 63")
	}
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 64 || strings.ContainsAny(name, "\x00\r\n") {
		return domain.FieldError("name", "角色名称长度必须为 1 到 64 个字符且不能换行")
	}
	if utf8.RuneCountInString(description) > 500 || strings.ContainsAny(description, "\x00\r\n") {
		return domain.FieldError("description", "角色说明不能超过 500 个字符且不能换行")
	}
	return nil
}

func requirePermission(actor access.Subject, permission access.Permission) error {
	if actor.Grants.Allows(permission, actor.UserID, actor.UserID) {
		return nil
	}
	return domain.ErrForbidden
}

func mapRoleRepositoryError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, access.ErrNotFound):
		return domain.ErrNotFound
	case errors.Is(err, access.ErrConflict):
		return &domain.AppError{Code: "ROLE_KEY_CONFLICT", Message: "角色标识已存在", Err: err}
	case errors.Is(err, access.ErrInUse):
		return &domain.AppError{Code: "ROLE_IN_USE", Message: "角色仍有成员，不能删除", Err: err}
	case errors.Is(err, access.ErrProtected):
		return roleProtectedError()
	case errors.Is(err, access.ErrInvalidInput):
		return domain.FieldError("roles", "角色或权限参数无效")
	default:
		return err
	}
}

func grantForbiddenError() error {
	return &domain.AppError{Code: "GRANT_FORBIDDEN", Message: "不能授予超出当前账号权限范围的角色或权限"}
}

func roleProtectedError() error {
	return &domain.AppError{Code: "ROLE_PROTECTED", Message: "该角色或超级管理员绑定受系统保护"}
}

func hasDuplicateStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}
