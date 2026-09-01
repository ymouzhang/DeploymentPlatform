package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"DP/internal/domain"
	"github.com/jackc/pgx/v5"
)

const notificationSelect = `SELECT
	id::text, created_at, risk_level, category, title, message, target_type,
	target_id, target_label, COALESCE(owner_id::text, ''), owner_username,
	COALESCE(operation_id::text, ''), link, read_at, COALESCE(read_by::text, ''),
	resolved_at, COALESCE(resolved_by::text, ''), dedupe_key
	FROM notifications`

func (db *DB) CreateNotification(
	ctx context.Context,
	item domain.Notification,
) (domain.Notification, error) {
	if item.ID == "" {
		item.ID = domain.NewID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	command, err := db.pool.Exec(ctx, `INSERT INTO notifications (
		id, created_at, risk_level, category, title, message, target_type, target_id,
		target_label, owner_id, owner_username, operation_id, link, dedupe_key
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, '')::uuid,
		$11, NULLIF($12, '')::uuid, $13, $14)
	ON CONFLICT (dedupe_key) WHERE dedupe_key <> '' AND resolved_at IS NULL DO NOTHING`,
		item.ID, item.CreatedAt, item.RiskLevel, item.Category, item.Title, item.Message,
		item.TargetType, item.TargetID, item.TargetLabel, item.OwnerID, item.OwnerUsername,
		item.OperationID, item.Link, item.DedupeKey)
	if err != nil {
		return domain.Notification{}, fmt.Errorf("insert notification: %w", err)
	}
	if command.RowsAffected() == 0 && item.DedupeKey != "" {
		return scanPostgresNotification(db.pool.QueryRow(ctx, notificationSelect+`
			WHERE dedupe_key = $1 AND resolved_at IS NULL`, item.DedupeKey))
	}
	return item, nil
}

func (db *DB) CreateNotificationIfUnresolved(
	ctx context.Context,
	key string,
	item domain.Notification,
) error {
	item.DedupeKey = key
	if item.TargetType == "" {
		item.TargetType = "system"
		item.TargetID = key
	}
	_, err := db.CreateNotification(ctx, item)
	return err
}

func (db *DB) DeleteResolvedNotificationsBefore(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	command, err := db.pool.Exec(ctx, `WITH selected AS (
		SELECT id FROM notifications WHERE resolved_at IS NOT NULL AND resolved_at < $1
		ORDER BY resolved_at, id LIMIT $2 FOR UPDATE SKIP LOCKED
	) DELETE FROM notifications n USING selected s WHERE n.id = s.id`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("delete resolved notifications: %w", err)
	}
	return command.RowsAffected(), nil
}

func (db *DB) ListNotifications(
	ctx context.Context,
	filter domain.NotificationFilter,
) ([]domain.Notification, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 5)
	if filter.Unread != nil {
		if *filter.Unread {
			where = append(where, "read_at IS NULL")
		} else {
			where = append(where, "read_at IS NOT NULL")
		}
	}
	if filter.RiskLevel != "" {
		args = append(args, filter.RiskLevel)
		where = append(where, fmt.Sprintf("risk_level = $%d", len(args)))
	}
	if filter.CursorTime != nil {
		args = append(args, *filter.CursorTime, filter.CursorID)
		where = append(where, fmt.Sprintf(`(created_at < $%d OR
			(created_at = $%d AND id < $%d))`, len(args)-1, len(args)-1, len(args)))
	}
	if len(where) == 0 {
		where = append(where, "TRUE")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	args = append(args, limit+1)
	query := notificationSelect + ` WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(args))
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query notifications: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Notification, 0, limit+1)
	for rows.Next() {
		item, err := scanPostgresNotification(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}
	return items, nil
}

func (db *DB) RecentNotifications(ctx context.Context, limit int) ([]domain.Notification, error) {
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	rows, err := db.pool.Query(ctx, notificationSelect+`
		WHERE resolved_at IS NULL ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent notifications: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Notification, 0, limit)
	for rows.Next() {
		item, err := scanPostgresNotification(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent notifications: %w", err)
	}
	return items, nil
}

func (db *DB) NotificationSummary(ctx context.Context) (domain.NotificationSummary, error) {
	var summary domain.NotificationSummary
	err := db.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE read_at IS NULL),
		count(*) FILTER (WHERE resolved_at IS NULL)
		FROM notifications`).Scan(&summary.Unread, &summary.Unresolved)
	if err != nil {
		return domain.NotificationSummary{}, fmt.Errorf("query notification summary: %w", err)
	}
	return summary, nil
}

func (db *DB) MarkNotificationRead(
	ctx context.Context,
	id string,
	actorID string,
	now time.Time,
) (domain.Notification, error) {
	command, err := db.pool.Exec(ctx, `UPDATE notifications SET
		read_at = COALESCE(read_at, $1), read_by = COALESCE(read_by, $2::uuid)
		WHERE id = $3`, now, nullableUUID(actorID), id)
	if err := requireAffected("mark notification read", command, err); err != nil {
		return domain.Notification{}, err
	}
	return db.getNotification(ctx, id)
}

func (db *DB) ResolveNotification(
	ctx context.Context,
	id string,
	actorID string,
	now time.Time,
) (domain.Notification, error) {
	command, err := db.pool.Exec(ctx, `UPDATE notifications SET
		read_at = COALESCE(read_at, $1), read_by = COALESCE(read_by, $2::uuid),
		resolved_at = COALESCE(resolved_at, $1), resolved_by = COALESCE(resolved_by, $2::uuid)
		WHERE id = $3`, now, nullableUUID(actorID), id)
	if err := requireAffected("resolve notification", command, err); err != nil {
		return domain.Notification{}, err
	}
	return db.getNotification(ctx, id)
}

func (db *DB) ResolveNotificationByDedupeKey(
	ctx context.Context,
	key string,
	actorID string,
	now time.Time,
) error {
	_, err := db.pool.Exec(ctx, `UPDATE notifications SET
		read_at = COALESCE(read_at, $1), read_by = COALESCE(read_by, $2::uuid),
		resolved_at = COALESCE(resolved_at, $1), resolved_by = COALESCE(resolved_by, $2::uuid)
		WHERE dedupe_key = $3 AND resolved_at IS NULL`, now, nullableUUID(actorID), key)
	if err != nil {
		return fmt.Errorf("resolve notification by key: %w", err)
	}
	return nil
}

func (db *DB) getNotification(ctx context.Context, id string) (domain.Notification, error) {
	return scanPostgresNotification(db.pool.QueryRow(ctx, notificationSelect+` WHERE id = $1`, id))
}

func scanPostgresNotification(row interface{ Scan(...any) error }) (domain.Notification, error) {
	var item domain.Notification
	var readAt, resolvedAt sql.NullTime
	err := row.Scan(&item.ID, &item.CreatedAt, &item.RiskLevel, &item.Category, &item.Title,
		&item.Message, &item.TargetType, &item.TargetID, &item.TargetLabel, &item.OwnerID,
		&item.OwnerUsername, &item.OperationID, &item.Link, &readAt, &item.ReadBy,
		&resolvedAt, &item.ResolvedBy, &item.DedupeKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Notification{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Notification{}, fmt.Errorf("scan notification: %w", err)
	}
	if readAt.Valid {
		item.ReadAt = &readAt.Time
	}
	if resolvedAt.Valid {
		item.ResolvedAt = &resolvedAt.Time
	}
	item.Read = item.ReadAt != nil
	item.Resolved = item.ResolvedAt != nil
	return item, nil
}
