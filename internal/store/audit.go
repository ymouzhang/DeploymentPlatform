package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"DP/internal/domain"
)

func (s *Store) CreateAuditEvent(ctx context.Context, event domain.AuditEvent) (domain.AuditEvent, error) {
	if event.ID == "" {
		event.ID = NewID()
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
	changes, err := json.Marshal(event.Changes)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("encode audit changes: %w", err)
	}
	if event.Changes == nil {
		changes = []byte("{}")
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO audit_events (
			id, occurred_at, category, action, outcome, risk_level,
			actor_user_id, actor_username, actor_role, owner_id, owner_username,
			target_type, target_id, target_label, request_id, operation_id,
			source_ip, user_agent, error_code, changes_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, formatTime(event.OccurredAt), event.Category, event.Action, event.Outcome,
		event.RiskLevel, nullableString(event.ActorUserID), event.ActorUsername, event.ActorRole,
		nullableString(event.OwnerID), event.OwnerUsername, event.TargetType, event.TargetID,
		event.TargetLabel, event.RequestID, nullableString(event.OperationID), event.SourceIP,
		event.UserAgent, event.ErrorCode, string(changes),
	)
	return event, err
}

func (s *Store) GetAuditEvent(ctx context.Context, id string) (domain.AuditEvent, error) {
	return scanAuditEvent(s.db.QueryRowContext(ctx, auditSelect+` WHERE id = ?`, id))
}

func (s *Store) FindOperationAuditRequest(ctx context.Context, operationID string) (domain.AuditEvent, error) {
	return scanAuditEvent(s.db.QueryRowContext(ctx, auditSelect+`
		WHERE operation_id = ? AND action LIKE '%.requested'
		ORDER BY occurred_at DESC LIMIT 1`, operationID))
}

func (s *Store) ListAuditEvents(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error) {
	where, args := auditWhere(filter, true)
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, auditSelect+where+`
		ORDER BY occurred_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.AuditEvent, 0, limit+1)
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) AuditSummary(ctx context.Context, filter domain.AuditFilter) (domain.AuditSummary, error) {
	where, args := auditWhere(filter, false)
	var summary domain.AuditSummary
	err := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN outcome IN ('failure', 'denied') THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN action = 'auth.login' AND outcome != 'success' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN risk_level = 'high' THEN 1 ELSE 0 END), 0)
		FROM audit_events`+where, args...).Scan(
		&summary.Total, &summary.Failures, &summary.LoginFailures, &summary.HighRisk)
	return summary, err
}

func (s *Store) CountRecentLoginFailures(ctx context.Context, username, sourceIP string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events
		WHERE action = 'auth.login' AND outcome != 'success' AND occurred_at >= ?
		AND (actor_username = ? OR source_ip = ?)`, formatTime(since), username, sourceIP).Scan(&count)
	return count, err
}

func (s *Store) HasRecentLoginThrottleAudit(ctx context.Context, username, sourceIP string, since time.Time) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events
		WHERE action = 'auth.login' AND error_code = 'LOGIN_THROTTLED' AND occurred_at >= ?
		AND actor_username = ? AND source_ip = ?`, formatTime(since), username, sourceIP).Scan(&count)
	return count > 0, err
}

func (s *Store) DeleteAuditEventsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM audit_events WHERE id IN (
		SELECT id FROM audit_events WHERE occurred_at < ? ORDER BY occurred_at LIMIT ?
	)`, formatTime(before), limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) CountAuditEvents(ctx context.Context, filter domain.AuditFilter) (int, error) {
	where, args := auditWhere(filter, false)
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`+where, args...).Scan(&count)
	return count, err
}

func auditWhere(filter domain.AuditFilter, includeCursor bool) (string, []any) {
	conditions := make([]string, 0, 10)
	args := make([]any, 0, 12)
	add := func(condition string, value any) {
		conditions = append(conditions, condition)
		args = append(args, value)
	}
	if filter.From != nil {
		add("occurred_at >= ?", formatTime(*filter.From))
	}
	if filter.To != nil {
		add("occurred_at <= ?", formatTime(*filter.To))
	}
	if filter.ActorID != "" {
		add("actor_user_id = ?", filter.ActorID)
	}
	if filter.OwnerID != "" {
		add("owner_id = ?", filter.OwnerID)
	}
	if filter.Category != "" {
		add("category = ?", filter.Category)
	}
	if filter.Action != "" {
		add("action = ?", filter.Action)
	}
	if filter.Outcome != "" {
		add("outcome = ?", filter.Outcome)
	}
	if filter.SourceIP != "" {
		add("source_ip = ?", filter.SourceIP)
	}
	if filter.Keyword != "" {
		like := "%" + strings.ToLower(filter.Keyword) + "%"
		conditions = append(conditions, `(LOWER(actor_username) LIKE ? OR LOWER(owner_username) LIKE ?
			OR LOWER(target_label) LIKE ? OR LOWER(target_id) LIKE ? OR LOWER(request_id) LIKE ?
			OR LOWER(COALESCE(operation_id, '')) LIKE ?)`)
		for range 6 {
			args = append(args, like)
		}
	}
	if includeCursor && filter.CursorTime != nil && filter.CursorID != "" {
		conditions = append(conditions, "(occurred_at < ? OR (occurred_at = ? AND id < ?))")
		formatted := formatTime(*filter.CursorTime)
		args = append(args, formatted, formatted, filter.CursorID)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

const auditSelect = `SELECT id, occurred_at, category, action, outcome, risk_level,
	actor_user_id, actor_username, actor_role, owner_id, owner_username,
	target_type, target_id, target_label, request_id, operation_id, source_ip,
	user_agent, error_code, changes_json FROM audit_events`

func scanAuditEvent(row scanner) (domain.AuditEvent, error) {
	var event domain.AuditEvent
	var occurred, changes string
	var actorID, ownerID, operationID sql.NullString
	err := row.Scan(&event.ID, &occurred, &event.Category, &event.Action, &event.Outcome,
		&event.RiskLevel, &actorID, &event.ActorUsername, &event.ActorRole, &ownerID,
		&event.OwnerUsername, &event.TargetType, &event.TargetID, &event.TargetLabel,
		&event.RequestID, &operationID, &event.SourceIP, &event.UserAgent, &event.ErrorCode, &changes)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AuditEvent{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AuditEvent{}, err
	}
	event.ActorUserID, event.OwnerID, event.OperationID = actorID.String, ownerID.String, operationID.String
	event.OccurredAt, _ = parseTime(occurred)
	if err := json.Unmarshal([]byte(changes), &event.Changes); err != nil {
		event.Changes = map[string]any{}
	}
	return event, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
