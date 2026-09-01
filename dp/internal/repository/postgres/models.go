package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"DP/internal/domain"
	"github.com/jackc/pgx/v5"
)

const modelSelect = `SELECT
	m.id::text,
	m.owner_id::text,
	m.marker_owner_id::text,
	COALESCE(u.username, ''),
	COALESCE(m.environment_id::text, ''),
	m.environment_name,
	host(m.environment_ip),
	m.name,
	m.source,
	m.target_dir,
	m.original_filename,
	m.size_bytes,
	m.expanded_size_bytes,
	m.file_count,
	COALESCE(m.sha256, ''),
	m.status,
	m.error_message,
	COALESCE(m.created_by::text, ''),
	m.created_by_username,
	m.created_at,
	m.updated_at,
	m.ready_at,
	m.deleted_at
	FROM models m
	LEFT JOIN users u ON u.id = m.owner_id`

const modelUploadSelect = `SELECT
	id::text, model_id::text, owner_id::text, remote_path, offset_bytes,
	total_bytes, status, expires_at, created_at, updated_at
	FROM model_uploads`

const modelTaskSelect = `SELECT
	id::text, model_id::text, owner_id::text, COALESCE(actor_user_id::text, ''),
	actor_username, action, status, stage, progress, error_code, error_message,
	log_path, created_at, started_at, finished_at
	FROM model_tasks`

func (db *DB) CreateModelUpload(
	ctx context.Context,
	model domain.Model,
	upload domain.ModelUpload,
) (domain.Model, domain.ModelUpload, error) {
	now := time.Now().UTC()
	if model.ID == "" {
		model.ID = domain.NewID()
	}
	if model.MarkerOwnerID == "" {
		model.MarkerOwnerID = model.OwnerID
	}
	model.CreatedAt = now
	model.UpdatedAt = now
	if upload.ID == "" {
		upload.ID = domain.NewID()
	}
	upload.ModelID = model.ID
	upload.OwnerID = model.OwnerID
	upload.CreatedAt = now
	upload.UpdatedAt = now
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return model, upload, fmt.Errorf("begin create model upload: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO models (
		id, owner_id, marker_owner_id, environment_id, environment_name, environment_ip,
		name, source, target_dir, original_filename, size_bytes, status, created_by,
		created_by_username, created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6::inet, $7, $8, $9, $10, $11, $12,
		NULLIF($13, '')::uuid, $14, $15, $15)`, model.ID, model.OwnerID, model.MarkerOwnerID,
		model.EnvironmentID, model.EnvironmentName, model.EnvironmentIP, model.Name, model.Source,
		model.TargetDir, model.OriginalFilename, model.SizeBytes, model.Status, model.CreatedBy,
		model.CreatedByUsername, now)
	if isPostgresError(err, "23505") {
		return model, upload, &domain.AppError{Code: "MODEL_TARGET_EXISTS", Message: "该主机目标目录已被其他模型占用"}
	}
	if err != nil {
		return model, upload, fmt.Errorf("insert model: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO model_uploads (
		id, model_id, owner_id, remote_path, offset_bytes, total_bytes, status,
		expires_at, created_at, updated_at
	) VALUES ($1, $2, $3, $4, 0, $5, $6, $7, $8, $8)`, upload.ID, upload.ModelID,
		upload.OwnerID, upload.RemotePath, upload.TotalBytes, upload.Status, upload.ExpiresAt, now)
	if err != nil {
		return model, upload, fmt.Errorf("insert model upload: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model, upload, fmt.Errorf("commit model upload: %w", err)
	}
	return model, upload, nil
}

func (db *DB) ListModels(ctx context.Context, ownerID string) ([]domain.Model, error) {
	query := modelSelect + ` WHERE m.deleted_at IS NULL`
	args := make([]any, 0, 1)
	if ownerID != "" {
		query += ` AND m.owner_id = $1`
		args = append(args, ownerID)
	}
	query += ` ORDER BY m.created_at DESC, m.id DESC`
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query models: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Model, 0)
	for rows.Next() {
		item, err := scanPostgresModel(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate models: %w", err)
	}
	for index := range items {
		task, err := db.LatestModelTask(ctx, items[index].ID)
		if err == nil {
			items[index].LatestTask = &task
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	return items, nil
}

func (db *DB) GetModel(ctx context.Context, id string) (domain.Model, error) {
	item, err := scanPostgresModel(db.pool.QueryRow(ctx, modelSelect+` WHERE m.id = $1`, id))
	if err != nil {
		return domain.Model{}, err
	}
	task, err := db.LatestModelTask(ctx, item.ID)
	if err == nil {
		item.LatestTask = &task
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Model{}, err
	}
	return item, nil
}

func (db *DB) GetModelUpload(ctx context.Context, id string) (domain.ModelUpload, error) {
	return scanPostgresModelUpload(db.pool.QueryRow(ctx, modelUploadSelect+` WHERE id = $1`, id))
}

func (db *DB) GetModelUploadByModel(ctx context.Context, modelID string) (domain.ModelUpload, error) {
	return scanPostgresModelUpload(db.pool.QueryRow(ctx, modelUploadSelect+` WHERE model_id = $1`, modelID))
}

func (db *DB) SetModelUploadOffset(ctx context.Context, id string, offset int64) error {
	command, err := db.pool.Exec(ctx, `UPDATE model_uploads SET offset_bytes = $1, updated_at = $2
		WHERE id = $3 AND status = 'uploading'`, offset, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update model upload offset: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (db *DB) CompleteModelUpload(ctx context.Context, id string) error {
	return db.transitionModelUpload(ctx, id, "completed")
}

func (db *DB) CancelModelUpload(ctx context.Context, id string) error {
	return db.transitionModelUpload(ctx, id, "cancelled")
}

func (db *DB) transitionModelUpload(ctx context.Context, id, status string) error {
	command, err := db.pool.Exec(ctx, `UPDATE model_uploads SET status = $1, updated_at = $2
		WHERE id = $3 AND status = 'uploading'`, status, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("transition model upload: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (db *DB) ListExpiredModelUploads(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]domain.ModelUpload, error) {
	rows, err := db.pool.Query(ctx, modelUploadSelect+`
		WHERE status = 'uploading' AND expires_at < $1 ORDER BY expires_at, id LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("query expired model uploads: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ModelUpload, 0)
	for rows.Next() {
		item, err := scanPostgresModelUpload(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired model uploads: %w", err)
	}
	return items, nil
}

func (db *DB) CreateModelTask(ctx context.Context, task domain.ModelTask) (domain.ModelTask, error) {
	task.ID = domain.NewID()
	task.CreatedAt = time.Now().UTC()
	_, err := db.pool.Exec(ctx, `INSERT INTO model_tasks (
		id, model_id, owner_id, actor_user_id, actor_username, action, status, stage,
		progress, error_code, error_message, log_path, created_at
	) VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		task.ID, task.ModelID, task.OwnerID, task.ActorUserID, task.ActorUsername, task.Action,
		task.Status, task.Stage, task.Progress, task.ErrorCode, task.ErrorMessage, task.LogPath,
		task.CreatedAt)
	if err != nil {
		return domain.ModelTask{}, fmt.Errorf("insert model task: %w", err)
	}
	return task, nil
}

func (db *DB) UpdateModelTask(ctx context.Context, task domain.ModelTask) error {
	command, err := db.pool.Exec(ctx, `UPDATE model_tasks SET
		status = $1, stage = $2, progress = $3, error_code = $4, error_message = $5,
		started_at = $6, finished_at = $7 WHERE id = $8`, task.Status, task.Stage, task.Progress,
		task.ErrorCode, task.ErrorMessage, task.StartedAt, task.FinishedAt, task.ID)
	return requireAffected("update model task", command, err)
}

func (db *DB) GetModelTask(ctx context.Context, id string) (domain.ModelTask, error) {
	return scanPostgresModelTask(db.pool.QueryRow(ctx, modelTaskSelect+` WHERE id = $1`, id))
}

func (db *DB) LatestModelTask(ctx context.Context, modelID string) (domain.ModelTask, error) {
	return scanPostgresModelTask(db.pool.QueryRow(ctx, modelTaskSelect+`
		WHERE model_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, modelID))
}

func (db *DB) SetModelState(
	ctx context.Context,
	id string,
	status domain.ModelStatus,
	errorMessage string,
) error {
	command, err := db.pool.Exec(ctx, `UPDATE models SET status = $1, error_message = $2,
		updated_at = $3 WHERE id = $4`, status, errorMessage, time.Now().UTC(), id)
	return requireAffected("update model state", command, err)
}

func (db *DB) MarkModelReady(ctx context.Context, id, sha string, expanded, files int64) error {
	now := time.Now().UTC()
	command, err := db.pool.Exec(ctx, `UPDATE models SET status = $1, sha256 = $2,
		expanded_size_bytes = $3, file_count = $4, error_message = '', ready_at = $5,
		updated_at = $5 WHERE id = $6`, domain.ModelReady, sha, expanded, files, now, id)
	return requireAffected("mark model ready", command, err)
}

func (db *DB) MarkModelDeleted(ctx context.Context, id string) error {
	now := time.Now().UTC()
	command, err := db.pool.Exec(ctx, `UPDATE models SET status = $1, deleted_at = $2,
		updated_at = $2 WHERE id = $3`, domain.ModelDeleted, now, id)
	return requireAffected("mark model deleted", command, err)
}

func (db *DB) InterruptActiveModelTasks(ctx context.Context) error {
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx, `UPDATE model_tasks SET status = $1, stage = 'interrupted',
		error_code = 'SERVER_RESTARTED', error_message = '管理服务重启，模型任务已中断', finished_at = $2
		WHERE status = ANY($3::text[])`, domain.OperationInterrupted, now,
		[]string{string(domain.OperationQueued), string(domain.OperationRunning)})
	if err != nil {
		return fmt.Errorf("interrupt active model tasks: %w", err)
	}
	_, err = db.pool.Exec(ctx, `UPDATE models SET status = $1,
		error_message = '管理服务重启，模型任务已中断', updated_at = $2
		WHERE status = ANY($3::text[])`, domain.ModelFailed, now,
		[]string{string(domain.ModelDeploying), string(domain.ModelDeleting)})
	if err != nil {
		return fmt.Errorf("interrupt active models: %w", err)
	}
	return nil
}

func (db *DB) EnvironmentHasModels(ctx context.Context, environmentID string) (bool, error) {
	var exists bool
	if err := db.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM models WHERE environment_id = $1 AND deleted_at IS NULL
	)`, environmentID).Scan(&exists); err != nil {
		return false, fmt.Errorf("query environment models: %w", err)
	}
	return exists, nil
}

func scanPostgresModel(row interface{ Scan(...any) error }) (domain.Model, error) {
	var item domain.Model
	var readyAt, deletedAt sql.NullTime
	err := row.Scan(&item.ID, &item.OwnerID, &item.MarkerOwnerID, &item.OwnerUsername,
		&item.EnvironmentID, &item.EnvironmentName, &item.EnvironmentIP, &item.Name,
		&item.Source, &item.TargetDir, &item.OriginalFilename, &item.SizeBytes,
		&item.ExpandedSizeBytes, &item.FileCount, &item.SHA256, &item.Status,
		&item.ErrorMessage, &item.CreatedBy, &item.CreatedByUsername, &item.CreatedAt,
		&item.UpdatedAt, &readyAt, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Model{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Model{}, fmt.Errorf("scan model: %w", err)
	}
	if readyAt.Valid {
		item.ReadyAt = &readyAt.Time
	}
	if deletedAt.Valid {
		item.DeletedAt = &deletedAt.Time
	}
	return item, nil
}

func scanPostgresModelUpload(row interface{ Scan(...any) error }) (domain.ModelUpload, error) {
	var item domain.ModelUpload
	err := row.Scan(&item.ID, &item.ModelID, &item.OwnerID, &item.RemotePath, &item.Offset,
		&item.TotalBytes, &item.Status, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ModelUpload{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ModelUpload{}, fmt.Errorf("scan model upload: %w", err)
	}
	return item, nil
}

func scanPostgresModelTask(row interface{ Scan(...any) error }) (domain.ModelTask, error) {
	var item domain.ModelTask
	var startedAt, finishedAt sql.NullTime
	err := row.Scan(&item.ID, &item.ModelID, &item.OwnerID, &item.ActorUserID,
		&item.ActorUsername, &item.Action, &item.Status, &item.Stage, &item.Progress,
		&item.ErrorCode, &item.ErrorMessage, &item.LogPath, &item.CreatedAt, &startedAt, &finishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ModelTask{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ModelTask{}, fmt.Errorf("scan model task: %w", err)
	}
	if startedAt.Valid {
		item.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		item.FinishedAt = &finishedAt.Time
	}
	return item, nil
}
