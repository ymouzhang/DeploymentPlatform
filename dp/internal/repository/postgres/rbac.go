package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"DP/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const _roleAssignmentLockID int64 = 0x445052424143

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

func (db *DB) CreateRole(
	ctx context.Context,
	actorID string,
	role access.Role,
) (access.Role, error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return access.Role{}, fmt.Errorf("begin create role: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	role.ID = uuid.NewString()
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO roles
		(id, key, name, description, system, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, FALSE, $5, $6, $6)`,
		role.ID, role.Key, role.Name, role.Description, nullableUUID(actorID), now)
	if err != nil {
		return access.Role{}, mapRoleWriteError("insert role", err)
	}
	if err := replaceRoleGrants(ctx, tx, role.ID, role.Grants); err != nil {
		return access.Role{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return access.Role{}, fmt.Errorf("commit create role: %w", err)
	}
	return db.GetRole(ctx, role.ID)
}

func (db *DB) UpdateRole(ctx context.Context, role access.Role) (access.Role, error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return access.Role{}, fmt.Errorf("begin update role: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var system bool
	err = tx.QueryRow(ctx, `SELECT system FROM roles WHERE id = $1 FOR UPDATE`, role.ID).Scan(&system)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Role{}, fmt.Errorf("role %q: %w", role.ID, ErrNotFound)
	}
	if err != nil {
		return access.Role{}, fmt.Errorf("lock role %q: %w", role.ID, err)
	}
	if system {
		return access.Role{}, fmt.Errorf("role %q: %w", role.ID, ErrProtected)
	}
	_, err = tx.Exec(ctx, `UPDATE roles SET name = $1, description = $2, updated_at = $3 WHERE id = $4`,
		role.Name, role.Description, time.Now().UTC(), role.ID)
	if err != nil {
		return access.Role{}, mapRoleWriteError("update role", err)
	}
	if err := replaceRoleGrants(ctx, tx, role.ID, role.Grants); err != nil {
		return access.Role{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return access.Role{}, fmt.Errorf("commit update role: %w", err)
	}
	return db.GetRole(ctx, role.ID)
}

func (db *DB) DeleteRole(ctx context.Context, roleID string) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delete role: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var system bool
	err = tx.QueryRow(ctx, `SELECT system FROM roles WHERE id = $1 FOR UPDATE`, roleID).Scan(&system)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("role %q: %w", roleID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("lock role %q: %w", roleID, err)
	}
	if system {
		return fmt.Errorf("role %q: %w", roleID, ErrProtected)
	}
	var members int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_roles WHERE role_id = $1`, roleID).Scan(&members); err != nil {
		return fmt.Errorf("count role members: %w", err)
	}
	if members > 0 {
		return fmt.Errorf("role %q has %d members: %w", roleID, members, ErrInUse)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return fmt.Errorf("delete role permissions: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE id = $1`, roleID); err != nil {
		return mapRoleWriteError("delete role", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete role: %w", err)
	}
	return nil
}

func (db *DB) ReplaceUserRoles(
	ctx context.Context,
	actorID string,
	userID string,
	roleIDs []string,
) error {
	if hasDuplicates(roleIDs) {
		return fmt.Errorf("role IDs contain duplicates: %w", ErrInvalidInput)
	}

	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin replace user roles: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, _roleAssignmentLockID); err != nil {
		return fmt.Errorf("lock role assignments: %w", err)
	}

	var enabled bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("user %q: %w", userID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("lock user %q: %w", userID, err)
	}

	desiredKeys, err := roleKeys(ctx, tx, roleIDs)
	if err != nil {
		return err
	}
	if enabled && len(roleIDs) == 0 {
		return fmt.Errorf("enabled user requires a role: %w", ErrInvalidInput)
	}
	currentKeys, err := currentRoleKeys(ctx, tx, userID)
	if err != nil {
		return err
	}
	hadSuper := currentKeys[access.RoleSuperAdmin]
	wantsSuper := desiredKeys[access.RoleSuperAdmin]
	if hadSuper != wantsSuper {
		actorKeys, queryErr := currentRoleKeys(ctx, tx, actorID)
		if queryErr != nil {
			return queryErr
		}
		if !actorKeys[access.RoleSuperAdmin] {
			return fmt.Errorf("only super administrator may change super administrator assignment: %w", ErrProtected)
		}
	}
	if actorID == userID && hadSuper && !wantsSuper {
		return fmt.Errorf("cannot remove own super administrator role: %w", ErrProtected)
	}
	if enabled && hadSuper && !wantsSuper {
		var remaining int
		err = tx.QueryRow(ctx, `SELECT count(DISTINCT u.id)
			FROM users u
			JOIN user_roles ur ON ur.user_id = u.id
			JOIN roles r ON r.id = ur.role_id
			WHERE u.enabled AND r.key = $1 AND u.id <> $2`, access.RoleSuperAdmin, userID).Scan(&remaining)
		if err != nil {
			return fmt.Errorf("count remaining super administrators: %w", err)
		}
		if remaining == 0 {
			return fmt.Errorf("last enabled super administrator: %w", ErrProtected)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete current user roles: %w", err)
	}
	for _, roleID := range roleIDs {
		_, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id, assigned_by, assigned_at)
			VALUES ($1, $2, $3, $4)`, userID, roleID, nullableUUID(actorID), time.Now().UTC())
		if err != nil {
			return mapRoleWriteError("insert user role", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit replace user roles: %w", err)
	}
	return nil
}

func replaceRoleGrants(ctx context.Context, tx pgx.Tx, roleID string, grants []access.Grant) error {
	if hasDuplicateGrants(grants) {
		return fmt.Errorf("role grants contain duplicates: %w", ErrInvalidInput)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return fmt.Errorf("delete role permissions: %w", err)
	}
	for _, grant := range grants {
		var scoped bool
		var permissionID string
		err := tx.QueryRow(ctx, `SELECT id::text, scoped FROM permissions WHERE key = $1`, grant.Permission).
			Scan(&permissionID, &scoped)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("permission %q: %w", grant.Permission, ErrInvalidInput)
		}
		if err != nil {
			return fmt.Errorf("query permission %q: %w", grant.Permission, err)
		}
		if !grant.Scope.Valid() || !scoped && grant.Scope != access.ScopeAll {
			return fmt.Errorf("permission %q scope %q: %w", grant.Permission, grant.Scope, ErrInvalidInput)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_id, scope)
			VALUES ($1, $2, $3)`, roleID, permissionID, grant.Scope); err != nil {
			return mapRoleWriteError("insert role permission", err)
		}
	}
	return nil
}

func roleKeys(ctx context.Context, tx pgx.Tx, roleIDs []string) (map[string]bool, error) {
	keys := make(map[string]bool, len(roleIDs))
	if len(roleIDs) == 0 {
		return keys, nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text, key FROM roles WHERE id = ANY($1::uuid[])`, roleIDs)
	if err != nil {
		return nil, fmt.Errorf("query requested roles: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, fmt.Errorf("scan requested role: %w", err)
		}
		keys[key] = true
		count++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate requested roles: %w", err)
	}
	if count != len(roleIDs) {
		return nil, fmt.Errorf("one or more roles do not exist: %w", ErrInvalidInput)
	}
	return keys, nil
}

func currentRoleKeys(ctx context.Context, tx pgx.Tx, userID string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT r.key FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("query current user roles: %w", err)
	}
	defer rows.Close()
	keys := make(map[string]bool)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan current user role: %w", err)
		}
		keys[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current user roles: %w", err)
	}
	return keys, nil
}

func hasDuplicateGrants(grants []access.Grant) bool {
	seen := make(map[access.Permission]struct{}, len(grants))
	for _, grant := range grants {
		if _, ok := seen[grant.Permission]; ok {
			return true
		}
		seen[grant.Permission] = struct{}{}
	}
	return false
}

func hasDuplicates(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func nullableUUID(value string) any {
	if _, err := uuid.Parse(value); err != nil {
		return nil
	}
	return value
}

func mapRoleWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		case "23503":
			return fmt.Errorf("%s: %w", operation, ErrInvalidInput)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
