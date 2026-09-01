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

const operationSelect = `SELECT
	o.id::text,
	o.environment_id::text,
	COALESCE(o.request_id::text, ''),
	COALESCE(o.actor_user_id::text, ''),
	o.actor_username,
	COALESCE(o.owner_id::text, ''),
	o.owner_username,
	o.environment_name,
	COALESCE(host(o.environment_ip), ''),
	o.service_type,
	o.action,
	o.status,
	o.stage,
	o.exit_code,
	o.error_code,
	o.error_message,
	o.log_path,
	o.created_at,
	o.started_at,
	o.finished_at
	FROM operations o`

func (db *DB) CreateOperation(ctx context.Context, operation domain.Operation) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin create operation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if operation.OwnerID != "" {
		var ownerID string
		err := tx.QueryRow(ctx, `SELECT owner_id::text FROM environments WHERE id = $1 FOR SHARE`,
			operation.EnvironmentID).Scan(&ownerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("verify operation owner: %w", err)
		}
		if ownerID != operation.OwnerID {
			return &domain.AppError{Code: "TRANSFER_CONFLICT", Message: "资源归属已变化，请重新发起操作"}
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO operations (
		id, environment_id, request_id, actor_user_id, actor_username, owner_id, owner_username,
		environment_name, environment_ip, service_type, action, status, stage, log_path, created_at
	) VALUES ($1, $2, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5,
		NULLIF($6, '')::uuid, $7, $8, NULLIF($9, '')::inet, $10, $11, $12, $13, $14, $15)`,
		operation.ID, operation.EnvironmentID, operation.RequestID, operation.ActorUserID,
		operation.ActorUsername, operation.OwnerID, operation.OwnerUsername, operation.EnvironmentName,
		operation.EnvironmentIP, operation.ServiceType, operation.Action, operation.Status,
		operation.Stage, operation.LogPath, operation.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert operation: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO operation_tags (operation_id, tag_id, group_name, value)
		SELECT $1, t.id, t.group_name, t.value
		FROM environment_tags et JOIN resource_tags t ON t.id = et.tag_id
		WHERE et.environment_id = $2`, operation.ID, operation.EnvironmentID)
	if err != nil {
		return fmt.Errorf("snapshot operation tags: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit create operation: %w", err)
	}
	return nil
}

func (db *DB) LastSuccessfulAction(
	ctx context.Context,
	environmentID string,
) (domain.OperationAction, error) {
	var action domain.OperationAction
	err := db.pool.QueryRow(ctx, `SELECT action FROM operations
		WHERE environment_id = $1 AND status = $2
		ORDER BY created_at DESC, id DESC LIMIT 1`, environmentID, domain.OperationSucceeded).Scan(&action)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("query last successful action: %w", err)
	}
	return action, nil
}

func (db *DB) UpdateOperation(ctx context.Context, operation domain.Operation) error {
	command, err := db.pool.Exec(ctx, `UPDATE operations SET
		status = $1, stage = $2, exit_code = $3, error_code = $4,
		error_message = $5, started_at = $6, finished_at = $7 WHERE id = $8`,
		operation.Status, operation.Stage, operation.ExitCode, operation.ErrorCode,
		operation.ErrorMessage, operation.StartedAt, operation.FinishedAt, operation.ID)
	return requireAffected("update operation", command, err)
}

func (db *DB) GetOperation(ctx context.Context, id string) (domain.Operation, error) {
	item, err := scanPostgresOperation(db.pool.QueryRow(ctx, operationSelect+` WHERE o.id = $1`, id))
	if err != nil {
		return domain.Operation{}, err
	}
	items := []domain.Operation{item}
	if err := db.PopulateOperationTags(ctx, items); err != nil {
		return domain.Operation{}, err
	}
	return items[0], nil
}

func (db *DB) LatestOperations(ctx context.Context) (map[string]domain.Operation, error) {
	rows, err := db.pool.Query(ctx, operationSelect+` WHERE o.id IN (
		SELECT DISTINCT ON (environment_id) id FROM operations
		ORDER BY environment_id, created_at DESC, id DESC
	)`)
	if err != nil {
		return nil, fmt.Errorf("query latest operations: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Operation, 0)
	for rows.Next() {
		item, err := scanPostgresOperation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest operations: %w", err)
	}
	if err := db.PopulateOperationTags(ctx, items); err != nil {
		return nil, err
	}
	latest := make(map[string]domain.Operation, len(items))
	for _, item := range items {
		latest[item.EnvironmentID] = item
	}
	return latest, nil
}

func (db *DB) InterruptActiveOperations(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `UPDATE operations SET
		status = $1, stage = 'interrupted', error_code = 'SERVER_RESTARTED',
		error_message = '管理服务重启，操作状态无法继续跟踪', finished_at = $2
		WHERE status = ANY($3::text[])`, domain.OperationInterrupted, time.Now().UTC(),
		[]string{string(domain.OperationQueued), string(domain.OperationRunning)})
	if err != nil {
		return fmt.Errorf("interrupt active operations: %w", err)
	}
	return nil
}

func (db *DB) ListOperations(ctx context.Context, filter domain.OperationFilter) ([]domain.Operation, error) {
	where := make([]string, 0, 12)
	args := make([]any, 0, 16)
	add := func(format string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(format, len(args)))
	}
	if filter.ActorID != "" {
		add("o.actor_user_id = $%d", filter.ActorID)
	}
	if filter.OwnerID != "" {
		add("o.owner_id = $%d", filter.OwnerID)
	}
	if filter.SubjectID != "" {
		args = append(args, filter.SubjectID)
		position := len(args)
		where = append(where, fmt.Sprintf("(o.owner_id = $%d OR o.actor_user_id = $%d)", position, position))
	}
	if filter.Action != "" {
		add("o.action = $%d", filter.Action)
	}
	if filter.Status != "" {
		add("o.status = $%d", filter.Status)
	}
	if filter.From != nil {
		add("o.created_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("o.created_at <= $%d", *filter.To)
	}
	if filter.Keyword != "" {
		value := "%" + strings.ToLower(filter.Keyword) + "%"
		positions := make([]int, 6)
		for index := range positions {
			args = append(args, value)
			positions[index] = len(args)
		}
		where = append(where, fmt.Sprintf(`(
			lower(o.environment_name) LIKE $%d OR lower(host(o.environment_ip)) LIKE $%d OR
			lower(o.service_type) LIKE $%d OR lower(o.error_code) LIKE $%d OR
			lower(o.id::text) LIKE $%d OR lower(COALESCE(o.request_id::text, '')) LIKE $%d
		)`, positions[0], positions[1], positions[2], positions[3], positions[4], positions[5]))
	}
	for _, tagID := range filter.TagIDs {
		add(`EXISTS (SELECT 1 FROM operation_tags ot WHERE ot.operation_id = o.id AND ot.tag_id = $%d)`, tagID)
	}
	if filter.CursorTime != nil {
		args = append(args, *filter.CursorTime, filter.CursorID)
		where = append(where, fmt.Sprintf(`(o.created_at < $%d OR (o.created_at = $%d AND o.id < $%d))`,
			len(args)-1, len(args)-1, len(args)))
	}
	if len(where) == 0 {
		where = append(where, "TRUE")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit+1)
	query := operationSelect + ` WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY o.created_at DESC, o.id DESC LIMIT $%d`, len(args))
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query operations: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Operation, 0, limit+1)
	for rows.Next() {
		item, err := scanPostgresOperation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operations: %w", err)
	}
	if err := db.PopulateOperationTags(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (db *DB) DeleteTerminalOperationsBefore(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := db.pool.Query(ctx, `WITH selected AS (
		SELECT id FROM operations
		WHERE finished_at IS NOT NULL AND finished_at < $1
			AND status <> ALL($2::text[])
		ORDER BY finished_at, id LIMIT $3
		FOR UPDATE SKIP LOCKED
	)
	DELETE FROM operations o USING selected s WHERE o.id = s.id RETURNING o.log_path`, before,
		[]string{string(domain.OperationQueued), string(domain.OperationRunning)}, limit)
	if err != nil {
		return nil, fmt.Errorf("delete terminal operations: %w", err)
	}
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("scan deleted operation path: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deleted operation paths: %w", err)
	}
	return paths, nil
}

func scanPostgresOperation(row interface{ Scan(...any) error }) (domain.Operation, error) {
	var item domain.Operation
	var exitCode sql.NullInt64
	var startedAt, finishedAt sql.NullTime
	err := row.Scan(&item.ID, &item.EnvironmentID, &item.RequestID, &item.ActorUserID,
		&item.ActorUsername, &item.OwnerID, &item.OwnerUsername, &item.EnvironmentName,
		&item.EnvironmentIP, &item.ServiceType, &item.Action, &item.Status, &item.Stage,
		&exitCode, &item.ErrorCode, &item.ErrorMessage, &item.LogPath, &item.CreatedAt,
		&startedAt, &finishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Operation{}, fmt.Errorf("scan operation: %w", err)
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		item.ExitCode = &value
	}
	if startedAt.Valid {
		item.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		item.FinishedAt = &finishedAt.Time
	}
	return item, nil
}
