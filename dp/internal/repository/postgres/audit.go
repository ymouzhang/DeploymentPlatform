package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"DP/internal/domain"
	"github.com/jackc/pgx/v5"
)

const auditSelect = `SELECT
	id::text,
	occurred_at,
	category,
	action,
	outcome,
	risk_level,
	COALESCE(actor_user_id::text, ''),
	actor_username,
	actor_role_keys,
	COALESCE(owner_id::text, ''),
	owner_username,
	target_type,
	target_id,
	target_label,
	COALESCE(request_id::text, ''),
	COALESCE(operation_id::text, ''),
	COALESCE(host(source_ip), ''),
	user_agent,
	error_code,
	changes
	FROM audit_events`

func (db *DB) CreateAuditEvent(ctx context.Context, event domain.AuditEvent) (domain.AuditEvent, error) {
	if event.ID == "" {
		event.ID = domain.NewID()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.Outcome == "" {
		event.Outcome = "success"
	}
	if event.RiskLevel == "" {
		event.RiskLevel = "normal"
	}
	roleJSON, err := json.Marshal(event.ActorRoles)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("encode audit roles: %w", err)
	}
	changes := event.Changes
	if changes == nil {
		changes = map[string]any{}
	}
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("encode audit changes: %w", err)
	}
	_, err = db.pool.Exec(ctx, `INSERT INTO audit_events (
		id, occurred_at, category, action, outcome, risk_level, actor_user_id,
		actor_username, actor_role_keys, owner_id, owner_username, target_type,
		target_id, target_label, request_id, operation_id, source_ip, user_agent,
		error_code, changes
	) VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, '')::uuid, $8, $9::jsonb,
		NULLIF($10, '')::uuid, $11, $12, $13, $14, NULLIF($15, '')::uuid,
		NULLIF($16, '')::uuid, NULLIF($17, '')::inet, $18, $19, $20::jsonb)`,
		event.ID, event.OccurredAt, event.Category, event.Action, event.Outcome, event.RiskLevel,
		event.ActorUserID, event.ActorUsername, roleJSON, event.OwnerID, event.OwnerUsername,
		event.TargetType, event.TargetID, event.TargetLabel, event.RequestID, event.OperationID,
		event.SourceIP, event.UserAgent, event.ErrorCode, changesJSON)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("insert audit event: %w", err)
	}
	return event, nil
}

func (db *DB) GetAuditEvent(ctx context.Context, id string) (domain.AuditEvent, error) {
	return scanPostgresAuditEvent(db.pool.QueryRow(ctx, auditSelect+` WHERE id = $1`, id))
}

func (db *DB) FindOperationAuditRequest(ctx context.Context, operationID string) (domain.AuditEvent, error) {
	return scanPostgresAuditEvent(db.pool.QueryRow(ctx, auditSelect+`
		WHERE operation_id = $1 AND action LIKE '%.requested'
		ORDER BY occurred_at DESC, id DESC LIMIT 1`, operationID))
}

func (db *DB) HasRecentAuthorizationDenied(
	ctx context.Context,
	actorUserID string,
	permission string,
	since time.Time,
) (bool, error) {
	var exists bool
	err := db.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM audit_events
		WHERE action = 'authorization.denied'
			AND actor_user_id = $1
			AND changes->>'permission' = $2
			AND occurred_at >= $3
	)`, actorUserID, permission, since).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query recent authorization denial: %w", err)
	}
	return exists, nil
}

func (db *DB) ListAuditEvents(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error) {
	where, args := postgresAuditWhere(filter, true)
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit+1)
	rows, err := db.pool.Query(ctx, auditSelect+where+
		fmt.Sprintf(` ORDER BY occurred_at DESC, id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()
	items := make([]domain.AuditEvent, 0, limit+1)
	for rows.Next() {
		item, err := scanPostgresAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return items, nil
}

func (db *DB) AuditSummary(ctx context.Context, filter domain.AuditFilter) (domain.AuditSummary, error) {
	where, args := postgresAuditWhere(filter, false)
	var summary domain.AuditSummary
	err := db.pool.QueryRow(ctx, `SELECT
		count(*),
		count(*) FILTER (WHERE outcome IN ('failure', 'denied')),
		count(*) FILTER (WHERE action = 'auth.login' AND outcome <> 'success'),
		count(*) FILTER (WHERE risk_level = 'high')
		FROM audit_events`+where, args...).Scan(&summary.Total, &summary.Failures,
		&summary.LoginFailures, &summary.HighRisk)
	if err != nil {
		return domain.AuditSummary{}, fmt.Errorf("query audit summary: %w", err)
	}
	return summary, nil
}

func (db *DB) CountRecentLoginFailures(
	ctx context.Context,
	username string,
	sourceIP string,
	since time.Time,
) (int, error) {
	var count int
	err := db.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events
		WHERE action = 'auth.login' AND outcome <> 'success' AND occurred_at >= $1
		AND (actor_username = $2 OR source_ip = NULLIF($3, '')::inet)`, since, username, sourceIP).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count recent login failures: %w", err)
	}
	return count, nil
}

func (db *DB) HasRecentLoginThrottleAudit(
	ctx context.Context,
	username string,
	sourceIP string,
	since time.Time,
) (bool, error) {
	var exists bool
	err := db.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM audit_events WHERE action = 'auth.login' AND error_code = 'LOGIN_THROTTLED'
		AND occurred_at >= $1 AND actor_username = $2 AND source_ip = NULLIF($3, '')::inet
	)`, since, username, sourceIP).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query recent throttle audit: %w", err)
	}
	return exists, nil
}

func (db *DB) DeleteAuditEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	command, err := db.pool.Exec(ctx, `WITH selected AS (
		SELECT id FROM audit_events WHERE occurred_at < $1
		ORDER BY occurred_at, id LIMIT $2 FOR UPDATE SKIP LOCKED
	) DELETE FROM audit_events a USING selected s WHERE a.id = s.id`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("delete audit events: %w", err)
	}
	return command.RowsAffected(), nil
}

func (db *DB) CountAuditEvents(ctx context.Context, filter domain.AuditFilter) (int, error) {
	where, args := postgresAuditWhere(filter, false)
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events`+where, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count audit events: %w", err)
	}
	return count, nil
}

func postgresAuditWhere(filter domain.AuditFilter, includeCursor bool) (string, []any) {
	conditions := make([]string, 0, 10)
	args := make([]any, 0, 16)
	add := func(format string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(format, len(args)))
	}
	if filter.From != nil {
		add("occurred_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("occurred_at <= $%d", *filter.To)
	}
	if filter.ActorID != "" {
		add("actor_user_id = $%d", filter.ActorID)
	}
	if filter.OwnerID != "" {
		add("owner_id = $%d", filter.OwnerID)
	}
	if filter.Category != "" {
		add("category = $%d", filter.Category)
	}
	if filter.Action != "" {
		add("action = $%d", filter.Action)
	}
	if filter.Outcome != "" {
		add("outcome = $%d", filter.Outcome)
	}
	if filter.SourceIP != "" {
		add("source_ip = $%d::inet", filter.SourceIP)
	}
	if filter.Keyword != "" {
		like := "%" + strings.ToLower(filter.Keyword) + "%"
		positions := make([]int, 6)
		for index := range positions {
			args = append(args, like)
			positions[index] = len(args)
		}
		conditions = append(conditions, fmt.Sprintf(`(
			lower(actor_username) LIKE $%d OR lower(owner_username) LIKE $%d OR
			lower(target_label) LIKE $%d OR lower(target_id) LIKE $%d OR
			lower(COALESCE(request_id::text, '')) LIKE $%d OR
			lower(COALESCE(operation_id::text, '')) LIKE $%d
		)`, positions[0], positions[1], positions[2], positions[3], positions[4], positions[5]))
	}
	if includeCursor && filter.CursorTime != nil && filter.CursorID != "" {
		args = append(args, *filter.CursorTime, filter.CursorID)
		conditions = append(conditions, fmt.Sprintf(`(occurred_at < $%d OR
			(occurred_at = $%d AND id < $%d))`, len(args)-1, len(args)-1, len(args)))
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func scanPostgresAuditEvent(row interface{ Scan(...any) error }) (domain.AuditEvent, error) {
	var item domain.AuditEvent
	var roleJSON, changesJSON []byte
	err := row.Scan(&item.ID, &item.OccurredAt, &item.Category, &item.Action, &item.Outcome,
		&item.RiskLevel, &item.ActorUserID, &item.ActorUsername, &roleJSON, &item.OwnerID,
		&item.OwnerUsername, &item.TargetType, &item.TargetID, &item.TargetLabel,
		&item.RequestID, &item.OperationID, &item.SourceIP, &item.UserAgent, &item.ErrorCode,
		&changesJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AuditEvent{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("scan audit event: %w", err)
	}
	if err := json.Unmarshal(roleJSON, &item.ActorRoles); err != nil {
		return domain.AuditEvent{}, fmt.Errorf("decode audit roles: %w", err)
	}
	if err := json.Unmarshal(changesJSON, &item.Changes); err != nil {
		return domain.AuditEvent{}, fmt.Errorf("decode audit changes: %w", err)
	}
	return item, nil
}
