package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"DP/internal/domain"
	"golang.org/x/sys/unix"
)

func (s *Store) ListOperations(ctx context.Context, filter domain.OperationFilter) ([]domain.Operation, error) {
	where, args := []string{"1=1"}, []any{}
	add := func(condition string, value any) { where = append(where, condition); args = append(args, value) }
	if filter.ActorID != "" {
		add("o.actor_user_id = ?", filter.ActorID)
	}
	if filter.OwnerID != "" {
		add("o.owner_id = ?", filter.OwnerID)
	}
	if filter.Action != "" {
		add("o.action = ?", filter.Action)
	}
	if filter.Status != "" {
		add("o.status = ?", filter.Status)
	}
	if filter.From != nil {
		add("o.created_at >= ?", formatTime(*filter.From))
	}
	if filter.To != nil {
		add("o.created_at <= ?", formatTime(*filter.To))
	}
	if filter.Keyword != "" {
		value := "%" + strings.ToLower(filter.Keyword) + "%"
		where = append(where, `(LOWER(o.environment_name) LIKE ? OR LOWER(o.environment_ip) LIKE ? OR LOWER(o.service_type) LIKE ? OR LOWER(o.error_code) LIKE ? OR LOWER(o.id) LIKE ? OR LOWER(o.request_id) LIKE ?)`)
		args = append(args, value, value, value, value, value, value)
	}
	for _, tagID := range filter.TagIDs {
		add(`EXISTS (SELECT 1 FROM operation_tags ot WHERE ot.operation_id = o.id AND ot.tag_id = ?)`, tagID)
	}
	if filter.CursorTime != nil {
		where = append(where, `(o.created_at < ? OR (o.created_at = ? AND o.id < ?))`)
		stamp := formatTime(*filter.CursorTime)
		args = append(args, stamp, stamp, filter.CursorID)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, operationSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY o.created_at DESC, o.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Operation, 0, limit+1)
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, op)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.PopulateOperationTags(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) DiskUsage() (total, free uint64, err error) {
	var stat unix.Statfs_t
	if err = unix.Statfs(s.path, &stat); err != nil {
		return 0, 0, err
	}
	return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize), nil
}

func (s *Store) DeleteTerminalOperationsBefore(ctx context.Context, before time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, log_path FROM operations
		WHERE finished_at IS NOT NULL AND finished_at < ? AND status NOT IN (?, ?)
		ORDER BY finished_at LIMIT ?`, formatTime(before), domain.OperationQueued, domain.OperationRunning, limit)
	if err != nil {
		return nil, err
	}
	var ids, paths []string
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return nil, err
		}
		ids, paths = append(ids, id), append(paths, path)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM operations WHERE id = ?`, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Store) UserDetail(ctx context.Context, id string, now time.Time) (domain.UserDetail, error) {
	user, err := s.GetUser(ctx, id)
	if err != nil {
		return domain.UserDetail{}, err
	}
	detail := domain.UserDetail{User: user}
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM packages WHERE owner_id = ?),
		(SELECT COUNT(*) FROM environments WHERE owner_id = ?),
		(SELECT COUNT(*) FROM environments WHERE owner_id = ? AND installed = 1),
		(SELECT COUNT(*) FROM operations WHERE owner_id = ? AND created_at >= ?),
		(SELECT COUNT(*) FROM sessions WHERE user_id = ? AND expires_at > ?),
		(SELECT COUNT(*) FROM audit_events WHERE actor_user_id = ? AND action = 'auth.login' AND outcome != 'success'),
		(SELECT COUNT(*) FROM audit_events WHERE actor_user_id = ? AND risk_level = 'high')`,
		id, id, id, id, formatTime(now.Add(-30*24*time.Hour)), id, formatTime(now), id, id).Scan(&detail.PackageCount, &detail.EnvironmentCount,
		&detail.InstalledServiceCount, &detail.RecentOperationCount, &detail.ActiveSessionCount, &detail.LoginFailureCount, &detail.HighRiskCount)
	if err != nil {
		return domain.UserDetail{}, err
	}
	var loginAt, activityAt, sourceIP sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT occurred_at, source_ip FROM audit_events WHERE actor_user_id = ? AND action = 'auth.login' AND outcome = 'success' ORDER BY occurred_at DESC LIMIT 1`, id).Scan(&loginAt, &sourceIP); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.UserDetail{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT occurred_at FROM audit_events WHERE actor_user_id = ? ORDER BY occurred_at DESC LIMIT 1`, id).Scan(&activityAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.UserDetail{}, err
	}
	detail.LastLoginAt, detail.LastActivityAt = parseNullTime(loginAt), parseNullTime(activityAt)
	if sourceIP.Valid {
		detail.LastSourceIP = sourceIP.String
	}
	return detail, nil
}

type TransferPackage struct{ ServiceType, StoragePath string }

func (s *Store) TransferPreview(ctx context.Context, sourceID, targetID string) ([]TransferPackage, int, error) {
	if sourceID == targetID {
		return nil, 0, domain.FieldError("target_user_id", "目标账号不能是源账号")
	}
	if _, err := s.GetUser(ctx, sourceID); err != nil {
		return nil, 0, err
	}
	target, err := s.GetUser(ctx, targetID)
	if err != nil {
		return nil, 0, err
	}
	if !target.Enabled {
		return nil, 0, &domain.AppError{Code: "TRANSFER_CONFLICT", Message: "目标账号已被禁用"}
	}
	var active int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operations WHERE owner_id = ? AND status IN (?, ?)`, sourceID, domain.OperationQueued, domain.OperationRunning).Scan(&active); err != nil {
		return nil, 0, err
	}
	if active > 0 {
		return nil, 0, &domain.AppError{Code: "TRANSFER_CONFLICT", Message: "源账号仍有执行中的操作"}
	}
	var conflicts int
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM packages s JOIN packages t ON t.owner_id = ? AND t.service_type = s.service_type WHERE s.owner_id = ?) +
		(SELECT COUNT(*) FROM environments s JOIN environments t ON t.owner_id = ? AND t.ip = s.ip AND t.service_type = s.service_type WHERE s.owner_id = ?)`, targetID, sourceID, targetID, sourceID).Scan(&conflicts)
	if err != nil {
		return nil, 0, err
	}
	if conflicts > 0 {
		return nil, 0, &domain.AppError{Code: "TRANSFER_CONFLICT", Message: "目标账号存在同名安装包或相同服务器服务"}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT service_type, storage_path FROM packages WHERE owner_id = ? ORDER BY service_type`, sourceID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	packages := []TransferPackage{}
	for rows.Next() {
		var item TransferPackage
		if err := rows.Scan(&item.ServiceType, &item.StoragePath); err != nil {
			return nil, 0, err
		}
		packages = append(packages, item)
	}
	var environments int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM environments WHERE owner_id = ?`, sourceID).Scan(&environments)
	return packages, environments, err
}

func (s *Store) TransferResources(ctx context.Context, sourceID, targetID string, paths map[string]string) (domain.TransferResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TransferResult{}, err
	}
	defer tx.Rollback()
	type tagTransfer struct{ environmentID, sourceTagID, groupName, value string }
	rows, err := tx.QueryContext(ctx, `SELECT et.environment_id, t.id, t.group_name, t.value
		FROM environment_tags et JOIN resource_tags t ON t.id = et.tag_id
		JOIN environments e ON e.id = et.environment_id WHERE e.owner_id = ?`, sourceID)
	if err != nil {
		return domain.TransferResult{}, err
	}
	var tagTransfers []tagTransfer
	for rows.Next() {
		var item tagTransfer
		if err := rows.Scan(&item.environmentID, &item.sourceTagID, &item.groupName, &item.value); err != nil {
			rows.Close()
			return domain.TransferResult{}, err
		}
		tagTransfers = append(tagTransfers, item)
	}
	if err := rows.Close(); err != nil {
		return domain.TransferResult{}, err
	}
	for serviceType, storagePath := range paths {
		if _, err = tx.ExecContext(ctx, `UPDATE packages SET owner_id = ?, storage_path = ? WHERE owner_id = ? AND service_type = ?`, targetID, storagePath, sourceID, serviceType); err != nil {
			return domain.TransferResult{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE package_versions SET owner_id = ?, storage_path = REPLACE(storage_path, ?, ?) WHERE owner_id = ? AND service_type = ?`,
			targetID, "packages/"+sourceID+"/", "packages/"+targetID+"/", sourceID, serviceType); err != nil {
			return domain.TransferResult{}, err
		}
	}
	envResult, err := tx.ExecContext(ctx, `UPDATE environments SET owner_id = ?, updated_at = ? WHERE owner_id = ?`, targetID, formatTime(time.Now().UTC()), sourceID)
	if err != nil {
		return domain.TransferResult{}, err
	}
	now := formatTime(time.Now().UTC())
	for _, item := range tagTransfers {
		var targetTagID string
		queryErr := tx.QueryRowContext(ctx, `SELECT id FROM resource_tags WHERE owner_id = ? AND group_name = ? COLLATE NOCASE AND value = ? COLLATE NOCASE AND deleted_at IS NULL`, targetID, item.groupName, item.value).Scan(&targetTagID)
		if errors.Is(queryErr, sql.ErrNoRows) {
			targetTagID = NewID()
			if _, err = tx.ExecContext(ctx, `INSERT INTO resource_tags(id, owner_id, group_name, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, targetTagID, targetID, item.groupName, item.value, now, now); err != nil {
				return domain.TransferResult{}, err
			}
		} else if queryErr != nil {
			return domain.TransferResult{}, queryErr
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM environment_tags WHERE environment_id = ? AND tag_id = ?`, item.environmentID, item.sourceTagID); err != nil {
			return domain.TransferResult{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO environment_tags(environment_id, tag_id) VALUES (?, ?)`, item.environmentID, targetTagID); err != nil {
			return domain.TransferResult{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, sourceID); err != nil {
		return domain.TransferResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.TransferResult{}, err
	}
	envs, _ := envResult.RowsAffected()
	return domain.TransferResult{SourceUserID: sourceID, TargetUserID: targetID, Packages: len(paths), Environments: int(envs)}, nil
}

func (s *Store) DashboardMetrics(ctx context.Context, since, staleBefore time.Time) (domain.DashboardMetrics, error) {
	var m domain.DashboardMetrics
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM users), (SELECT COUNT(*) FROM users WHERE enabled = 1), (SELECT COUNT(*) FROM users WHERE enabled = 0),
		(SELECT COUNT(*) FROM packages), (SELECT COUNT(*) FROM environments), (SELECT COUNT(*) FROM environments WHERE installed = 1),
		(SELECT COUNT(*) FROM operations WHERE status IN (?, ?)),
		(SELECT COUNT(*) FROM operations WHERE status IN (?, ?, ?) AND created_at >= ?),
		(SELECT COUNT(*) FROM audit_events WHERE action = 'auth.login' AND outcome != 'success' AND occurred_at >= ?),
		(SELECT COUNT(*) FROM environments WHERE last_validation_at IS NULL),
		(SELECT COUNT(*) FROM environments WHERE last_validation_at IS NOT NULL AND last_validation_at < ?),
		(SELECT COUNT(*) FROM audit_events WHERE risk_level = 'high' AND occurred_at >= ?),
		(SELECT COUNT(*) FROM notifications WHERE read_at IS NULL)`, domain.OperationQueued, domain.OperationRunning,
		domain.OperationFailed, domain.OperationTimedOut, domain.OperationInterrupted, formatTime(since), formatTime(since), formatTime(staleBefore), formatTime(since)).Scan(
		&m.Users, &m.EnabledUsers, &m.DisabledUsers, &m.Packages, &m.Environments, &m.InstalledServices,
		&m.ActiveOperations, &m.FailedOperations24h, &m.LoginFailures24h, &m.UnvalidatedEnvironments, &m.StaleValidationEnvironments,
		&m.HighRiskAudits24h, &m.UnreadNotifications)
	return m, err
}

func (s *Store) CountOperationsByTags(ctx context.Context, tagIDs []string, since time.Time) (active, failed int, err error) {
	if len(tagIDs) == 0 {
		err = s.db.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM operations WHERE status IN (?, ?)),
			(SELECT COUNT(*) FROM operations WHERE status IN (?, ?, ?) AND created_at >= ?)`,
			domain.OperationQueued, domain.OperationRunning, domain.OperationFailed, domain.OperationTimedOut,
			domain.OperationInterrupted, formatTime(since)).Scan(&active, &failed)
		return
	}
	where, args := []string{"1=1"}, []any{}
	for _, tagID := range tagIDs {
		where = append(where, `EXISTS (SELECT 1 FROM operation_tags ot WHERE ot.operation_id = o.id AND ot.tag_id = ?)`)
		args = append(args, tagID)
	}
	base := strings.Join(where, " AND ")
	activeArgs := append(append([]any{}, args...), domain.OperationQueued, domain.OperationRunning)
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operations o WHERE `+base+` AND o.status IN (?, ?)`, activeArgs...).Scan(&active); err != nil {
		return
	}
	failedArgs := append(append([]any{}, args...), domain.OperationFailed, domain.OperationTimedOut, domain.OperationInterrupted, formatTime(since))
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operations o WHERE `+base+` AND o.status IN (?, ?, ?) AND o.created_at >= ?`, failedArgs...).Scan(&failed)
	return
}
