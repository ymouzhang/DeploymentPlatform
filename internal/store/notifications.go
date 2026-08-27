package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"DP/internal/domain"
)

func (s *Store) CreateNotification(ctx context.Context, item domain.Notification) (domain.Notification, error) {
	if item.ID == "" {
		item.ID = NewID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO notifications (
		id, created_at, risk_level, category, title, message, target_type, target_id,
		target_label, owner_id, owner_username, operation_id, link, dedupe_key
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(dedupe_key) WHERE dedupe_key <> '' AND resolved_at IS NULL DO NOTHING`, item.ID, formatTime(item.CreatedAt),
		item.RiskLevel, item.Category, item.Title, item.Message, item.TargetType, item.TargetID,
		item.TargetLabel, nullableString(item.OwnerID), item.OwnerUsername, nullableString(item.OperationID), item.Link, item.DedupeKey)
	if err != nil {
		return domain.Notification{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 && item.DedupeKey != "" {
		return scanNotification(s.db.QueryRowContext(ctx, notificationSelect+` WHERE dedupe_key = ? AND resolved_at IS NULL`, item.DedupeKey))
	}
	return item, nil
}

func (s *Store) CreateNotificationIfUnresolved(ctx context.Context, key string, item domain.Notification) error {
	item.DedupeKey = key
	if item.TargetType == "" {
		item.TargetType, item.TargetID = "system", key
	}
	_, err := s.CreateNotification(ctx, item)
	return err
}

func (s *Store) DeleteResolvedNotificationsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM notifications WHERE id IN (
		SELECT id FROM notifications WHERE resolved_at IS NOT NULL AND resolved_at < ? ORDER BY resolved_at LIMIT ?
	)`, formatTime(before), limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) ListNotifications(ctx context.Context, filter domain.NotificationFilter) ([]domain.Notification, error) {
	where, args := []string{"1=1"}, []any{}
	if filter.Unread != nil {
		if *filter.Unread {
			where = append(where, "read_at IS NULL")
		} else {
			where = append(where, "read_at IS NOT NULL")
		}
	}
	if filter.RiskLevel != "" {
		where = append(where, "risk_level = ?")
		args = append(args, filter.RiskLevel)
	}
	if filter.CursorTime != nil {
		stamp := formatTime(*filter.CursorTime)
		where = append(where, `(created_at < ? OR (created_at = ? AND id < ?))`)
		args = append(args, stamp, stamp, filter.CursorID)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, notificationSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Notification, 0, limit+1)
	for rows.Next() {
		item, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RecentNotifications(ctx context.Context, limit int) ([]domain.Notification, error) {
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	rows, err := s.db.QueryContext(ctx, notificationSelect+` WHERE resolved_at IS NULL ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Notification{}
	for rows.Next() {
		item, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) NotificationSummary(ctx context.Context) (domain.NotificationSummary, error) {
	var result domain.NotificationSummary
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN read_at IS NULL THEN 1 ELSE 0 END), 0), COALESCE(SUM(CASE WHEN resolved_at IS NULL THEN 1 ELSE 0 END), 0) FROM notifications`).Scan(&result.Unread, &result.Unresolved)
	return result, err
}

func (s *Store) MarkNotificationRead(ctx context.Context, id, actorID string, now time.Time) (domain.Notification, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = COALESCE(read_at, ?), read_by = COALESCE(read_by, ?) WHERE id = ?`, formatTime(now), actorID, id)
	if err != nil {
		return domain.Notification{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.Notification{}, domain.ErrNotFound
	}
	return s.getNotification(ctx, id)
}

func (s *Store) ResolveNotification(ctx context.Context, id, actorID string, now time.Time) (domain.Notification, error) {
	stamp := formatTime(now)
	result, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = COALESCE(read_at, ?), read_by = COALESCE(read_by, ?), resolved_at = COALESCE(resolved_at, ?), resolved_by = COALESCE(resolved_by, ?) WHERE id = ?`, stamp, actorID, stamp, actorID, id)
	if err != nil {
		return domain.Notification{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.Notification{}, domain.ErrNotFound
	}
	return s.getNotification(ctx, id)
}

func (s *Store) ResolveNotificationByDedupeKey(ctx context.Context, key, actorID string, now time.Time) error {
	stamp := formatTime(now)
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = COALESCE(read_at, ?),
		read_by = COALESCE(read_by, ?), resolved_at = COALESCE(resolved_at, ?),
		resolved_by = COALESCE(resolved_by, ?) WHERE dedupe_key = ? AND resolved_at IS NULL`,
		stamp, actorID, stamp, actorID, key)
	return err
}

func (s *Store) getNotification(ctx context.Context, id string) (domain.Notification, error) {
	item, err := scanNotification(s.db.QueryRowContext(ctx, notificationSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Notification{}, domain.ErrNotFound
	}
	return item, err
}

const notificationSelect = `SELECT id, created_at, risk_level, category, title, message,
	target_type, target_id, target_label, owner_id, owner_username, operation_id, link,
	read_at, read_by, resolved_at, resolved_by, dedupe_key FROM notifications`

func scanNotification(row scanner) (domain.Notification, error) {
	var item domain.Notification
	var created string
	var ownerID, operationID, readAt, readBy, resolvedAt, resolvedBy sql.NullString
	err := row.Scan(&item.ID, &created, &item.RiskLevel, &item.Category, &item.Title, &item.Message,
		&item.TargetType, &item.TargetID, &item.TargetLabel, &ownerID, &item.OwnerUsername,
		&operationID, &item.Link, &readAt, &readBy, &resolvedAt, &resolvedBy, &item.DedupeKey)
	if err != nil {
		return domain.Notification{}, err
	}
	item.CreatedAt, _ = parseTime(created)
	item.ReadAt, item.ResolvedAt = parseNullTime(readAt), parseNullTime(resolvedAt)
	item.OwnerID, item.OperationID, item.ReadBy, item.ResolvedBy = ownerID.String, operationID.String, readBy.String, resolvedBy.String
	item.Read, item.Resolved = item.ReadAt != nil, item.ResolvedAt != nil
	return item, nil
}
