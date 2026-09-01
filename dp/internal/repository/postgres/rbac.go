package postgres

import (
	"context"
	"errors"
	"fmt"

	"DP/internal/access"
	"github.com/jackc/pgx/v5"
)

func (db *DB) ListPermissions(ctx context.Context) ([]access.Definition, error) {
	rows, err := db.pool.Query(ctx, `SELECT key, resource, action, description, scoped
		FROM permissions ORDER BY resource, action`)
	if err != nil {
		return nil, fmt.Errorf("query permissions: %w", err)
	}
	defer rows.Close()

	items := make([]access.Definition, 0, 48)
	for rows.Next() {
		var item access.Definition
		if err := rows.Scan(&item.Key, &item.Resource, &item.Action, &item.Description, &item.Scoped); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permissions: %w", err)
	}
	return items, nil
}

func (db *DB) ListRoles(ctx context.Context) ([]access.Role, error) {
	rows, err := db.pool.Query(ctx, `SELECT r.id::text, r.key, r.name, r.description, r.system,
		(SELECT count(*) FROM user_roles ur WHERE ur.role_id = r.id)
		FROM roles r ORDER BY r.system DESC, r.key`)
	if err != nil {
		return nil, fmt.Errorf("query roles: %w", err)
	}
	defer rows.Close()

	items := make([]access.Role, 0, 8)
	byID := make(map[string]int)
	for rows.Next() {
		var item access.Role
		if err := rows.Scan(&item.ID, &item.Key, &item.Name, &item.Description, &item.System, &item.MemberCount); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		byID[item.ID] = len(items)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate roles: %w", err)
	}

	grantRows, err := db.pool.Query(ctx, `SELECT rp.role_id::text, p.key, rp.scope
		FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
		ORDER BY rp.role_id, p.key`)
	if err != nil {
		return nil, fmt.Errorf("query role permissions: %w", err)
	}
	defer grantRows.Close()
	for grantRows.Next() {
		var roleID string
		var grant access.Grant
		if err := grantRows.Scan(&roleID, &grant.Permission, &grant.Scope); err != nil {
			return nil, fmt.Errorf("scan role permission: %w", err)
		}
		if index, ok := byID[roleID]; ok {
			items[index].Grants = append(items[index].Grants, grant)
		}
	}
	if err := grantRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role permissions: %w", err)
	}
	return items, nil
}

func (db *DB) GetRole(ctx context.Context, id string) (access.Role, error) {
	var role access.Role
	err := db.pool.QueryRow(ctx, `SELECT r.id::text, r.key, r.name, r.description, r.system,
		(SELECT count(*) FROM user_roles ur WHERE ur.role_id = r.id)
		FROM roles r WHERE r.id = $1`, id).
		Scan(&role.ID, &role.Key, &role.Name, &role.Description, &role.System, &role.MemberCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Role{}, fmt.Errorf("role %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return access.Role{}, fmt.Errorf("query role %q: %w", id, err)
	}
	rows, err := db.pool.Query(ctx, `SELECT p.key, rp.scope
		FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = $1 ORDER BY p.key`, id)
	if err != nil {
		return access.Role{}, fmt.Errorf("query role %q permissions: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var grant access.Grant
		if err := rows.Scan(&grant.Permission, &grant.Scope); err != nil {
			return access.Role{}, fmt.Errorf("scan role permission: %w", err)
		}
		role.Grants = append(role.Grants, grant)
	}
	if err := rows.Err(); err != nil {
		return access.Role{}, fmt.Errorf("iterate role permissions: %w", err)
	}
	return role, nil
}

func (db *DB) AccessForUser(ctx context.Context, userID string) (access.Subject, error) {
	roleRows, err := db.pool.Query(ctx, `SELECT r.id::text, r.key, r.name
		FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1 ORDER BY r.key`, userID)
	if err != nil {
		return access.Subject{}, fmt.Errorf("query user %q roles: %w", userID, err)
	}
	defer roleRows.Close()
	subject := access.Subject{UserID: userID, Roles: make([]access.RoleRef, 0, 4)}
	for roleRows.Next() {
		var role access.RoleRef
		if err := roleRows.Scan(&role.ID, &role.Key, &role.Name); err != nil {
			return access.Subject{}, fmt.Errorf("scan user role: %w", err)
		}
		subject.Roles = append(subject.Roles, role)
	}
	if err := roleRows.Err(); err != nil {
		return access.Subject{}, fmt.Errorf("iterate user roles: %w", err)
	}

	grantRows, err := db.pool.Query(ctx, `SELECT p.key, rp.scope
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		JOIN permissions p ON p.id = rp.permission_id
		WHERE ur.user_id = $1 ORDER BY p.key`, userID)
	if err != nil {
		return access.Subject{}, fmt.Errorf("query user %q permissions: %w", userID, err)
	}
	defer grantRows.Close()
	grants := make([]access.Grant, 0, 48)
	for grantRows.Next() {
		var grant access.Grant
		if err := grantRows.Scan(&grant.Permission, &grant.Scope); err != nil {
			return access.Subject{}, fmt.Errorf("scan user permission: %w", err)
		}
		grants = append(grants, grant)
	}
	if err := grantRows.Err(); err != nil {
		return access.Subject{}, fmt.Errorf("iterate user permissions: %w", err)
	}
	subject.Grants, err = access.Merge(grants...)
	if err != nil {
		return access.Subject{}, fmt.Errorf("merge user permissions: %w", err)
	}
	return subject, nil
}
