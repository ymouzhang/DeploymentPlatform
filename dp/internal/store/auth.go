package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
)

const pendingAdminUsername = "__dp_pending_admin__"

func (s *Store) PendingInitialAdmin(ctx context.Context) (domain.User, bool, error) {
	user, err := s.GetUserByUsername(ctx, pendingAdminUsername)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, false, nil
	}
	return user, err == nil, err
}

func (s *Store) InitializeAdmin(ctx context.Context, id, username, passwordHash string) (domain.User, error) {
	now := formatTime(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `
		UPDATE users SET username = ?, password_hash = ?, must_change_password = 1, updated_at = ?
		WHERE id = ? AND username = ?`, username, passwordHash, now, id, pendingAdminUsername)
	if isUniqueError(err) {
		return domain.User{}, domain.ErrConflict
	}
	if err != nil {
		return domain.User{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.User{}, domain.ErrNotFound
	}
	return s.GetUser(ctx, id)
}

func (s *Store) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	now := time.Now().UTC()
	user.ID = NewID()
	user.CreatedAt, user.UpdatedAt = now, now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, role, enabled, must_change_password, is_initial_admin, created_at, updated_at, created_by)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		user.ID, user.Username, user.PasswordHash, user.Role, boolInt(user.Enabled),
		boolInt(user.IsInitialAdmin), formatTime(now), formatTime(now), user.CreatedBy)
	if isUniqueError(err) {
		return domain.User{}, domain.ErrConflict
	}
	if err != nil {
		return domain.User{}, err
	}
	return s.GetUser(ctx, user.ID)
}

func (s *Store) GetUser(ctx context.Context, id string) (domain.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, userSelect+` WHERE id = ?`, id))
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (domain.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, userSelect+` WHERE username = ?`, username))
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.db.QueryContext(ctx, userSelect+` ORDER BY is_initial_admin DESC, username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]domain.User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) UpdateUserPassword(ctx context.Context, id, passwordHash string) error {
	return s.UpdateUserPasswordAndRevokeSessions(ctx, id, passwordHash, false)
}

func (s *Store) UpdateUserPasswordAndRevokeSessions(ctx context.Context, id, passwordHash string, mustChange bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
		passwordHash, boolInt(mustChange), formatTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateUserEnabled(ctx context.Context, id string, enabled bool) (domain.User, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE users SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolInt(enabled), formatTime(time.Now().UTC()), id)
	if err != nil {
		return domain.User{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.User{}, domain.ErrNotFound
	}
	return s.GetUser(ctx, id)
}

func (s *Store) UserBusinessCounts(ctx context.Context, id string) (packages, environments int, err error) {
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM packages WHERE owner_id = ?`, id).Scan(&packages); err != nil {
		return 0, 0, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM environments WHERE owner_id = ?`, id).Scan(&environments)
	return packages, environments, err
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM resource_tags WHERE owner_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) CreateSession(ctx context.Context, tokenHash, userID, sourceIP, userAgent string, expiresAt time.Time) (domain.Session, error) {
	now := time.Now().UTC()
	session := domain.Session{ID: NewID(), UserID: userID, SourceIP: sourceIP, UserAgent: userAgent,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: expiresAt}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, id, user_id, source_ip, user_agent, last_seen_at, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, tokenHash, session.ID, userID, sourceIP, userAgent,
		formatTime(now), formatTime(expiresAt), formatTime(now))
	return session, err
}

func (s *Store) UserForSession(ctx context.Context, tokenHash string, now time.Time) (domain.User, domain.Session, error) {
	var user domain.User
	var session domain.Session
	var role string
	var enabled, mustChange, initial int
	var created, updated, sessionCreated, lastSeen, expires string
	err := s.db.QueryRowContext(ctx, `
		SELECT users.id, users.username, users.password_hash, users.role, users.enabled,
			users.must_change_password, users.is_initial_admin, users.created_at, users.updated_at, users.created_by,
			COALESCE((SELECT creator.username FROM users creator WHERE creator.id = users.created_by), ''),
			sessions.id, sessions.user_id, sessions.source_ip, sessions.user_agent,
			sessions.created_at, sessions.last_seen_at, sessions.expires_at
		FROM sessions JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.expires_at > ? AND users.enabled = 1`,
		tokenHash, formatTime(now)).Scan(&user.ID, &user.Username, &user.PasswordHash, &role, &enabled,
		&mustChange, &initial, &created, &updated, &user.CreatedBy, &user.CreatedByUsername,
		&session.ID, &session.UserID, &session.SourceIP, &session.UserAgent, &sessionCreated, &lastSeen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.Session{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, domain.Session{}, err
	}
	user.Role, user.Enabled, user.MustChangePassword, user.IsInitialAdmin = domain.UserRole(role), enabled != 0, mustChange != 0, initial != 0
	user.CreatedAt, _ = parseTime(created)
	user.UpdatedAt, _ = parseTime(updated)
	session.CreatedAt, _ = parseTime(sessionCreated)
	session.LastSeenAt, _ = parseTime(lastSeen)
	session.ExpiresAt, _ = parseTime(expires)
	if now.Sub(session.LastSeenAt) >= 5*time.Minute {
		if _, touchErr := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, formatTime(now), tokenHash); touchErr != nil {
			return domain.User{}, domain.Session{}, touchErr
		}
		session.LastSeenAt = now
	}
	return user, session, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, formatTime(now))
	return err
}

func (s *Store) ListUserSessions(ctx context.Context, userID string, now time.Time) ([]domain.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, source_ip, user_agent, created_at, last_seen_at, expires_at
		FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC, created_at DESC`, userID, formatTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Session{}
	for rows.Next() {
		item, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteUserSessionByID(ctx context.Context, userID, sessionID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id = ?`, userID, sessionID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanSession(row scanner) (domain.Session, error) {
	var item domain.Session
	var created, lastSeen, expires string
	if err := row.Scan(&item.ID, &item.UserID, &item.SourceIP, &item.UserAgent, &created, &lastSeen, &expires); err != nil {
		return domain.Session{}, err
	}
	item.CreatedAt, _ = parseTime(created)
	item.LastSeenAt, _ = parseTime(lastSeen)
	item.ExpiresAt, _ = parseTime(expires)
	return item, nil
}

func (s *Store) LoginThrottleUntil(ctx context.Context, keys []string, now time.Time) (time.Time, error) {
	var latest time.Time
	for _, key := range keys {
		var value string
		err := s.db.QueryRowContext(ctx, `SELECT blocked_until FROM login_throttles WHERE scope_key = ?`, key).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return time.Time{}, err
		}
		until, _ := parseTime(value)
		if until.After(now) && until.After(latest) {
			latest = until
		}
	}
	return latest, nil
}

func (s *Store) RecordLoginFailure(ctx context.Context, keys []string, now time.Time) (time.Time, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	var latest time.Time
	for _, key := range keys {
		count := 0
		windowStart := now
		var startText string
		err := tx.QueryRowContext(ctx, `SELECT failure_count, window_started_at FROM login_throttles WHERE scope_key = ?`, key).Scan(&count, &startText)
		if err == nil {
			windowStart, _ = parseTime(startText)
			if now.Sub(windowStart) >= 10*time.Minute {
				count, windowStart = 0, now
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, err
		}
		count++
		blockedUntil := now
		if count >= 5 {
			seconds := math.Min(30*math.Pow(2, float64(count-5)), 15*60)
			blockedUntil = now.Add(time.Duration(seconds) * time.Second)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO login_throttles(scope_key, failure_count, window_started_at, blocked_until, updated_at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT(scope_key) DO UPDATE SET failure_count=excluded.failure_count,
			window_started_at=excluded.window_started_at, blocked_until=excluded.blocked_until, updated_at=excluded.updated_at`,
			key, count, formatTime(windowStart), formatTime(blockedUntil), formatTime(now))
		if err != nil {
			return time.Time{}, err
		}
		if blockedUntil.After(latest) {
			latest = blockedUntil
		}
	}
	return latest, tx.Commit()
}

func (s *Store) ClearLoginThrottle(ctx context.Context, keys []string) error {
	for _, key := range keys {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM login_throttles WHERE scope_key = ?`, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteLoginThrottlesBefore(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM login_throttles WHERE updated_at < ?`, formatTime(before))
	return err
}

func (s *Store) ListStaleUsers(ctx context.Context, before time.Time) ([]domain.User, error) {
	rows, err := s.db.QueryContext(ctx, userSelect+` WHERE users.enabled = 1 AND users.is_initial_admin = 0
		AND users.created_at < ? AND NOT EXISTS (
			SELECT 1 FROM audit_events WHERE actor_user_id = users.id AND action = 'auth.login'
			AND outcome = 'success' AND occurred_at >= ?
		) ORDER BY users.username`, formatTime(before), formatTime(before))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.User{}
	for rows.Next() {
		item, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const userSelect = `
	SELECT users.id, users.username, users.password_hash, users.role, users.enabled,
		users.must_change_password, users.is_initial_admin, users.created_at, users.updated_at, users.created_by,
		COALESCE((SELECT creator.username FROM users creator WHERE creator.id = users.created_by), '')
	FROM users`

func scanUser(row scanner) (domain.User, error) {
	var user domain.User
	var role string
	var enabled, mustChange, initial int
	var created, updated string
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &role, &enabled, &mustChange, &initial, &created, &updated, &user.CreatedBy, &user.CreatedByUsername)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	user.Role = domain.UserRole(role)
	if user.Role == domain.RoleAdmin {
		user.Roles = []access.RoleRef{{Key: access.RolePlatformAdmin, Name: "平台管理员"}}
		user.Permissions = access.Grants{
			access.CommunicationRead: access.ScopeAll, access.CommunicationCreate: access.ScopeAll,
			access.CommunicationReply: access.ScopeAll, access.CommunicationManage: access.ScopeAll,
		}
	} else {
		user.Roles = []access.RoleRef{{Key: access.RoleOperator, Name: "运维人员"}}
		user.Permissions = access.Grants{
			access.CommunicationRead: access.ScopeOwn, access.CommunicationReply: access.ScopeOwn,
		}
	}
	user.Enabled = enabled != 0
	user.MustChangePassword = mustChange != 0
	user.IsInitialAdmin = initial != 0
	user.CreatedAt, _ = parseTime(created)
	user.UpdatedAt, _ = parseTime(updated)
	return user, nil
}
