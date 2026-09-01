package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	pendingAdminUsername = "__dp_pending_admin__"
	initialAdminID       = "00000000-0000-4000-8000-000000000001"
)

const userSelect = `SELECT
	users.id::text,
	users.username,
	users.password_hash,
	users.enabled,
	users.must_change_password,
	users.is_initial_admin,
	users.created_at,
	users.updated_at,
	COALESCE(users.created_by::text, ''),
	COALESCE(creator.username, '')
	FROM users
	LEFT JOIN users creator ON creator.id = users.created_by`

func (db *DB) PendingInitialAdmin(ctx context.Context) (domain.User, bool, error) {
	user, err := db.GetUserByUsername(ctx, pendingAdminUsername)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, false, nil
	}
	return user, err == nil, err
}

func (db *DB) InitializeAdmin(
	ctx context.Context,
	id string,
	username string,
	passwordHash string,
) (domain.User, error) {
	command, err := db.pool.Exec(ctx, `UPDATE users
		SET username = $1, password_hash = $2, must_change_password = TRUE, updated_at = $3
		WHERE id = $4 AND username = $5`, username, passwordHash, time.Now().UTC(), id, pendingAdminUsername)
	if err != nil {
		return domain.User{}, mapUserWriteError("initialize administrator", err)
	}
	if command.RowsAffected() == 0 {
		return domain.User{}, domain.ErrNotFound
	}
	return db.GetUser(ctx, id)
}

func (db *DB) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	if len(user.Roles) == 0 {
		return domain.User{}, domain.FieldError("role_ids", "至少选择一个角色")
	}
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.User{}, fmt.Errorf("begin create user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	user.ID = uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO users
		(id, username, password_hash, enabled, must_change_password, is_initial_admin, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, TRUE, FALSE, $5, $6, $6)`,
		user.ID, user.Username, user.PasswordHash, user.Enabled, nullableUUID(user.CreatedBy), now)
	if err != nil {
		return domain.User{}, mapUserWriteError("insert user", err)
	}
	seen := make(map[string]struct{}, len(user.Roles))
	for _, role := range user.Roles {
		if role.ID == "" {
			return domain.User{}, domain.FieldError("role_ids", "角色 ID 不能为空")
		}
		if _, exists := seen[role.ID]; exists {
			return domain.User{}, domain.FieldError("role_ids", "角色不能重复")
		}
		seen[role.ID] = struct{}{}
		_, err = tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id, assigned_by, assigned_at)
			VALUES ($1, $2, $3, $4)`, user.ID, role.ID, nullableUUID(user.CreatedBy), now)
		if isPostgresError(err, "23503") {
			return domain.User{}, domain.FieldError("role_ids", "包含未知角色")
		}
		if err != nil {
			return domain.User{}, mapUserWriteError("insert initial user role", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit create user: %w", err)
	}
	return db.GetUser(ctx, user.ID)
}

func (db *DB) GetUser(ctx context.Context, id string) (domain.User, error) {
	user, err := scanPostgresUser(db.pool.QueryRow(ctx, userSelect+` WHERE users.id = $1`, id))
	if err != nil {
		return domain.User{}, err
	}
	return db.populateUserAccess(ctx, user)
}

func (db *DB) GetUserByUsername(ctx context.Context, username string) (domain.User, error) {
	user, err := scanPostgresUser(db.pool.QueryRow(ctx, userSelect+` WHERE users.username = $1`, username))
	if err != nil {
		return domain.User{}, err
	}
	return db.populateUserAccess(ctx, user)
}

func (db *DB) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := db.pool.Query(ctx, userSelect+` ORDER BY users.is_initial_admin DESC, users.username`)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()
	items := make([]domain.User, 0)
	for rows.Next() {
		user, err := scanPostgresUser(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	for index := range items {
		items[index], err = db.populateUserAccess(ctx, items[index])
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (db *DB) UpdateUserPasswordAndRevokeSessions(
	ctx context.Context,
	id string,
	passwordHash string,
	mustChange bool,
) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin update password: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE users SET password_hash = $1, must_change_password = $2, updated_at = $3
		WHERE id = $4`, passwordHash, mustChange, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id); err != nil {
		return fmt.Errorf("revoke sessions after password update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password update: %w", err)
	}
	return nil
}

func (db *DB) UpdateUserEnabled(ctx context.Context, id string, enabled bool) (domain.User, error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.User{}, fmt.Errorf("begin update user status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, _roleAssignmentLockID); err != nil {
		return domain.User{}, fmt.Errorf("lock role assignments: %w", err)
	}

	var currentEnabled bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM users WHERE id = $1 FOR UPDATE`, id).Scan(&currentEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("lock user status: %w", err)
	}
	if !currentEnabled && enabled {
		var hasRole bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_roles WHERE user_id = $1)`, id).Scan(&hasRole); err != nil {
			return domain.User{}, fmt.Errorf("check user roles before enable: %w", err)
		}
		if !hasRole {
			return domain.User{}, fmt.Errorf("enabled user requires a role: %w", access.ErrInvalidInput)
		}
	}
	if currentEnabled && !enabled {
		if err := ensureEnabledSuperAdminRemains(ctx, tx, id); err != nil {
			return domain.User{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET enabled = $1, updated_at = $2 WHERE id = $3`,
		enabled, time.Now().UTC(), id); err != nil {
		return domain.User{}, fmt.Errorf("update user status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit user status: %w", err)
	}
	return db.GetUser(ctx, id)
}

func (db *DB) UserBusinessCounts(ctx context.Context, id string) (packages, environments int, err error) {
	err = db.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM packages WHERE owner_id = $1),
		(SELECT count(*) FROM environments WHERE owner_id = $1)`, id).Scan(&packages, &environments)
	if err != nil {
		return 0, 0, fmt.Errorf("count user business resources: %w", err)
	}
	return packages, environments, nil
}

func (db *DB) DeleteUser(ctx context.Context, id string) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delete user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, _roleAssignmentLockID); err != nil {
		return fmt.Errorf("lock role assignments: %w", err)
	}
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM users WHERE id = $1 FOR UPDATE`, id).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock user before delete: %w", err)
	}
	if enabled {
		if err := ensureEnabledSuperAdminRemains(ctx, tx, id); err != nil {
			return err
		}
	}
	command, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return mapUserWriteError("delete user", err)
	}
	if command.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete user: %w", err)
	}
	return nil
}

func ensureEnabledSuperAdminRemains(ctx context.Context, tx pgx.Tx, excludedUserID string) error {
	var isSuperAdmin bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1 AND r.key = $2
	)`, excludedUserID, access.RoleSuperAdmin).Scan(&isSuperAdmin)
	if err != nil {
		return fmt.Errorf("check super administrator role: %w", err)
	}
	if !isSuperAdmin {
		return nil
	}
	var remaining int
	err = tx.QueryRow(ctx, `SELECT count(DISTINCT u.id)
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE u.enabled AND r.key = $1 AND u.id <> $2`, access.RoleSuperAdmin, excludedUserID).
		Scan(&remaining)
	if err != nil {
		return fmt.Errorf("count remaining super administrators: %w", err)
	}
	if remaining == 0 {
		return fmt.Errorf("last enabled super administrator: %w", access.ErrProtected)
	}
	return nil
}

func (db *DB) CreateSession(
	ctx context.Context,
	tokenHash string,
	userID string,
	sourceIP string,
	userAgent string,
	expiresAt time.Time,
) (domain.Session, error) {
	now := time.Now().UTC()
	session := domain.Session{
		ID: uuid.NewString(), UserID: userID, SourceIP: sourceIP, UserAgent: userAgent,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: expiresAt,
	}
	_, err := db.pool.Exec(ctx, `INSERT INTO sessions
		(token_hash, id, user_id, source_ip, user_agent, last_seen_at, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $6)`, tokenHash, session.ID, userID, sourceIP,
		userAgent, now, expiresAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("insert session: %w", err)
	}
	return session, nil
}

func (db *DB) UserForSession(
	ctx context.Context,
	tokenHash string,
	now time.Time,
) (domain.User, domain.Session, error) {
	var user domain.User
	var session domain.Session
	err := db.pool.QueryRow(ctx, `SELECT
		u.id::text, u.username, u.password_hash, u.enabled, u.must_change_password,
		u.is_initial_admin, u.created_at, u.updated_at, COALESCE(u.created_by::text, ''),
		COALESCE(creator.username, ''),
		s.id::text, s.user_id::text, s.source_ip, s.user_agent, s.created_at, s.last_seen_at, s.expires_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN users creator ON creator.id = u.created_by
		WHERE s.token_hash = $1 AND s.expires_at > $2 AND u.enabled`, tokenHash, now).
		Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Enabled, &user.MustChangePassword,
			&user.IsInitialAdmin, &user.CreatedAt, &user.UpdatedAt, &user.CreatedBy, &user.CreatedByUsername,
			&session.ID, &session.UserID, &session.SourceIP, &session.UserAgent, &session.CreatedAt,
			&session.LastSeenAt, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.Session{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, domain.Session{}, fmt.Errorf("query session user: %w", err)
	}
	user, err = db.populateUserAccess(ctx, user)
	if err != nil {
		return domain.User{}, domain.Session{}, err
	}
	if now.Sub(session.LastSeenAt) >= 5*time.Minute {
		if _, err := db.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = $1 WHERE token_hash = $2`, now, tokenHash); err != nil {
			return domain.User{}, domain.Session{}, fmt.Errorf("touch session: %w", err)
		}
		session.LastSeenAt = now
	}
	return user, session, nil
}

func (db *DB) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (db *DB) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

func (db *DB) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= $1`, now)
	return err
}

func (db *DB) ListUserSessions(ctx context.Context, userID string, now time.Time) ([]domain.Session, error) {
	rows, err := db.pool.Query(ctx, `SELECT id::text, user_id::text, source_ip, user_agent,
		created_at, last_seen_at, expires_at FROM sessions
		WHERE user_id = $1 AND expires_at > $2 ORDER BY last_seen_at DESC, created_at DESC`, userID, now)
	if err != nil {
		return nil, fmt.Errorf("query user sessions: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Session, 0)
	for rows.Next() {
		var item domain.Session
		if err := rows.Scan(&item.ID, &item.UserID, &item.SourceIP, &item.UserAgent,
			&item.CreatedAt, &item.LastSeenAt, &item.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan user session: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user sessions: %w", err)
	}
	return items, nil
}

func (db *DB) DeleteUserSessionByID(ctx context.Context, userID, sessionID string) error {
	command, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id = $2`, userID, sessionID)
	if err != nil {
		return fmt.Errorf("delete user session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (db *DB) LoginThrottleUntil(ctx context.Context, keys []string, now time.Time) (time.Time, error) {
	var latest time.Time
	for _, key := range keys {
		var blockedUntil time.Time
		err := db.pool.QueryRow(ctx, `SELECT blocked_until FROM login_throttles WHERE scope_key = $1`, key).
			Scan(&blockedUntil)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return time.Time{}, fmt.Errorf("query login throttle: %w", err)
		}
		if blockedUntil.After(now) && blockedUntil.After(latest) {
			latest = blockedUntil
		}
	}
	return latest, nil
}

func (db *DB) RecordLoginFailure(ctx context.Context, keys []string, now time.Time) (time.Time, error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return time.Time{}, fmt.Errorf("begin login throttle update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var latest time.Time
	for _, key := range keys {
		count := 0
		windowStart := now
		err := tx.QueryRow(ctx, `SELECT failure_count, window_started_at FROM login_throttles
			WHERE scope_key = $1 FOR UPDATE`, key).Scan(&count, &windowStart)
		if errors.Is(err, pgx.ErrNoRows) {
			count = 0
			windowStart = now
		} else if err != nil {
			return time.Time{}, fmt.Errorf("lock login throttle: %w", err)
		}
		if now.Sub(windowStart) >= 10*time.Minute {
			count = 0
			windowStart = now
		}
		count++
		blockedUntil := now
		if count >= 5 {
			seconds := math.Min(30*math.Pow(2, float64(count-5)), 15*60)
			blockedUntil = now.Add(time.Duration(seconds) * time.Second)
		}
		_, err = tx.Exec(ctx, `INSERT INTO login_throttles
			(scope_key, failure_count, window_started_at, blocked_until, updated_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (scope_key) DO UPDATE SET
			failure_count = EXCLUDED.failure_count,
			window_started_at = EXCLUDED.window_started_at,
			blocked_until = EXCLUDED.blocked_until,
			updated_at = EXCLUDED.updated_at`, key, count, windowStart, blockedUntil, now)
		if err != nil {
			return time.Time{}, fmt.Errorf("upsert login throttle: %w", err)
		}
		if blockedUntil.After(latest) {
			latest = blockedUntil
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, fmt.Errorf("commit login throttle update: %w", err)
	}
	return latest, nil
}

func (db *DB) ClearLoginThrottle(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := db.pool.Exec(ctx, `DELETE FROM login_throttles WHERE scope_key = ANY($1::text[])`, keys)
	if err != nil {
		return fmt.Errorf("clear login throttles: %w", err)
	}
	return nil
}

func (db *DB) UserDetail(ctx context.Context, id string, now time.Time) (domain.UserDetail, error) {
	user, err := db.GetUser(ctx, id)
	if err != nil {
		return domain.UserDetail{}, err
	}
	detail := domain.UserDetail{User: user}
	err = db.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM packages WHERE owner_id = $1),
		(SELECT count(*) FROM environments WHERE owner_id = $1),
		(SELECT count(*) FROM models WHERE owner_id = $1 AND deleted_at IS NULL),
		(SELECT count(*) FROM environments WHERE owner_id = $1 AND installed),
		(SELECT count(*) FROM operations WHERE owner_id = $1 AND created_at >= $2),
		(SELECT count(*) FROM sessions WHERE user_id = $1 AND expires_at > $3),
		(SELECT count(*) FROM audit_events WHERE actor_user_id = $1 AND action = 'auth.login' AND outcome <> 'success'),
		(SELECT count(*) FROM audit_events WHERE actor_user_id = $1 AND risk_level = 'high')`,
		id, now.Add(-30*24*time.Hour), now).Scan(
		&detail.PackageCount, &detail.EnvironmentCount, &detail.ModelCount,
		&detail.InstalledServiceCount, &detail.RecentOperationCount, &detail.ActiveSessionCount,
		&detail.LoginFailureCount, &detail.HighRiskCount,
	)
	if err != nil {
		return domain.UserDetail{}, fmt.Errorf("query user detail: %w", err)
	}
	var loginAt, activityAt *time.Time
	if err := db.pool.QueryRow(ctx, `SELECT occurred_at FROM audit_events
		WHERE actor_user_id = $1 AND action = 'auth.login' AND outcome = 'success'
		ORDER BY occurred_at DESC LIMIT 1`, id).Scan(&loginAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.UserDetail{}, fmt.Errorf("query last login: %w", err)
	}
	var sourceIP string
	if err := db.pool.QueryRow(ctx, `SELECT source_ip::text FROM audit_events
		WHERE actor_user_id = $1 AND action = 'auth.login' AND outcome = 'success'
		ORDER BY occurred_at DESC LIMIT 1`, id).Scan(&sourceIP); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.UserDetail{}, fmt.Errorf("query last login source: %w", err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT occurred_at FROM audit_events
		WHERE actor_user_id = $1 ORDER BY occurred_at DESC LIMIT 1`, id).Scan(&activityAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.UserDetail{}, fmt.Errorf("query last activity: %w", err)
	}
	detail.LastLoginAt = loginAt
	detail.LastActivityAt = activityAt
	detail.LastSourceIP = sourceIP
	return detail, nil
}

func (db *DB) populateUserAccess(ctx context.Context, user domain.User) (domain.User, error) {
	subject, err := db.AccessForUser(ctx, user.ID)
	if err != nil {
		return domain.User{}, err
	}
	user.Roles = subject.Roles
	user.Permissions = subject.Grants
	return user, nil
}

func scanPostgresUser(row interface{ Scan(...any) error }) (domain.User, error) {
	var user domain.User
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.PasswordHash,
		&user.Enabled,
		&user.MustChangePassword,
		&user.IsInitialAdmin,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.CreatedBy,
		&user.CreatedByUsername,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("scan user: %w", err)
	}
	return user, nil
}

func mapUserWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%s: %w", operation, domain.ErrConflict)
		case "23503":
			return fmt.Errorf("%s: %w", operation, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
