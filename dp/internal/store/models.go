package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"DP/internal/domain"
)

const modelSelect = `
	SELECT m.id, m.owner_id, m.marker_owner_id, COALESCE(u.username, ''), m.environment_id,
		m.environment_name, m.environment_ip, m.name, m.source, m.target_dir,
		m.original_filename, m.size_bytes, m.expanded_size_bytes, m.file_count,
		m.sha256, m.status, m.error_message, m.created_by, m.created_by_username,
		m.created_at, m.updated_at, m.ready_at, m.deleted_at
	FROM models AS m
	LEFT JOIN users AS u ON u.id = m.owner_id`

func (s *Store) CreateModelUpload(ctx context.Context, model domain.Model, upload domain.ModelUpload) (domain.Model, domain.ModelUpload, error) {
	now := time.Now().UTC()
	if model.ID == "" {
		model.ID = NewID()
	}
	if model.MarkerOwnerID == "" {
		model.MarkerOwnerID = model.OwnerID
	}
	model.CreatedAt, model.UpdatedAt = now, now
	if upload.ID == "" {
		upload.ID = NewID()
	}
	upload.ModelID, upload.OwnerID = model.ID, model.OwnerID
	upload.CreatedAt, upload.UpdatedAt = now, now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model, upload, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO models (
		id, owner_id, marker_owner_id, environment_id, environment_name, environment_ip, name, source,
		target_dir, original_filename, size_bytes, status, created_by,
		created_by_username, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		model.ID, model.OwnerID, model.MarkerOwnerID, model.EnvironmentID, model.EnvironmentName, model.EnvironmentIP,
		model.Name, model.Source, model.TargetDir, model.OriginalFilename, model.SizeBytes,
		model.Status, model.CreatedBy, model.CreatedByUsername, formatTime(now), formatTime(now))
	if isUniqueError(err) {
		return model, upload, &domain.AppError{Code: "MODEL_TARGET_EXISTS", Message: "该主机目标目录已被其他模型占用"}
	}
	if err != nil {
		return model, upload, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO model_uploads (
		id, model_id, owner_id, remote_path, offset, total_bytes, status,
		expires_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?, ?)`, upload.ID, upload.ModelID, upload.OwnerID,
		upload.RemotePath, upload.TotalBytes, upload.Status, formatTime(upload.ExpiresAt),
		formatTime(now), formatTime(now))
	if err != nil {
		return model, upload, err
	}
	if err := tx.Commit(); err != nil {
		return model, upload, err
	}
	return model, upload, nil
}

func (s *Store) ListModels(ctx context.Context, ownerID string) ([]domain.Model, error) {
	query := modelSelect + ` WHERE m.deleted_at IS NULL`
	args := []any{}
	if ownerID != "" {
		query += ` AND m.owner_id = ?`
		args = append(args, ownerID)
	}
	query += ` ORDER BY m.created_at DESC, m.id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Model, 0)
	for rows.Next() {
		item, err := scanModel(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range items {
		task, err := s.LatestModelTask(ctx, items[i].ID)
		if err == nil {
			items[i].LatestTask = &task
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) GetModel(ctx context.Context, id string) (domain.Model, error) {
	item, err := scanModel(s.db.QueryRowContext(ctx, modelSelect+` WHERE m.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return item, domain.ErrNotFound
	}
	if err != nil {
		return item, err
	}
	task, err := s.LatestModelTask(ctx, item.ID)
	if err == nil {
		item.LatestTask = &task
	} else if !errors.Is(err, domain.ErrNotFound) {
		return item, err
	}
	return item, nil
}

func scanModel(row scanner) (domain.Model, error) {
	var item domain.Model
	var status, created, updated string
	var ready, deleted sql.NullString
	err := row.Scan(&item.ID, &item.OwnerID, &item.MarkerOwnerID, &item.OwnerUsername, &item.EnvironmentID,
		&item.EnvironmentName, &item.EnvironmentIP, &item.Name, &item.Source, &item.TargetDir,
		&item.OriginalFilename, &item.SizeBytes, &item.ExpandedSizeBytes, &item.FileCount,
		&item.SHA256, &status, &item.ErrorMessage, &item.CreatedBy, &item.CreatedByUsername,
		&created, &updated, &ready, &deleted)
	if err != nil {
		return item, err
	}
	item.Status = domain.ModelStatus(status)
	item.CreatedAt, _ = parseTime(created)
	item.UpdatedAt, _ = parseTime(updated)
	item.ReadyAt, item.DeletedAt = parseNullTime(ready), parseNullTime(deleted)
	return item, nil
}

func (s *Store) GetModelUpload(ctx context.Context, id string) (domain.ModelUpload, error) {
	var item domain.ModelUpload
	var expires, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, model_id, owner_id, remote_path, offset,
		total_bytes, status, expires_at, created_at, updated_at FROM model_uploads WHERE id = ?`, id).
		Scan(&item.ID, &item.ModelID, &item.OwnerID, &item.RemotePath, &item.Offset,
			&item.TotalBytes, &item.Status, &expires, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return item, domain.ErrNotFound
	}
	if err != nil {
		return item, err
	}
	item.ExpiresAt, _ = parseTime(expires)
	item.CreatedAt, _ = parseTime(created)
	item.UpdatedAt, _ = parseTime(updated)
	return item, nil
}

func (s *Store) GetModelUploadByModel(ctx context.Context, modelID string) (domain.ModelUpload, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM model_uploads WHERE model_id = ?`, modelID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ModelUpload{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ModelUpload{}, err
	}
	return s.GetModelUpload(ctx, id)
}

func (s *Store) SetModelUploadOffset(ctx context.Context, id string, offset int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE model_uploads SET offset = ?, updated_at = ? WHERE id = ? AND status = 'uploading'`, offset, formatTime(time.Now()), id)
	if err != nil {
		return err
	}
	count, _ := res.RowsAffected()
	if count == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) CompleteModelUpload(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE model_uploads SET status = 'completed', updated_at = ? WHERE id = ? AND status = 'uploading'`, formatTime(time.Now()), id)
	if err != nil {
		return err
	}
	count, _ := res.RowsAffected()
	if count == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) CancelModelUpload(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE model_uploads SET status = 'cancelled', updated_at = ? WHERE id = ? AND status = 'uploading'`, formatTime(time.Now()), id)
	if err != nil {
		return err
	}
	count, _ := res.RowsAffected()
	if count == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) ListExpiredModelUploads(ctx context.Context, before time.Time, limit int) ([]domain.ModelUpload, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM model_uploads WHERE status = 'uploading' AND expires_at < ? ORDER BY expires_at LIMIT ?`, formatTime(before), limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	items := make([]domain.ModelUpload, 0, len(ids))
	for _, id := range ids {
		item, err := s.GetModelUpload(ctx, id)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) CreateModelTask(ctx context.Context, task domain.ModelTask) (domain.ModelTask, error) {
	now := time.Now().UTC()
	task.ID, task.CreatedAt = NewID(), now
	_, err := s.db.ExecContext(ctx, `INSERT INTO model_tasks (
		id, model_id, owner_id, actor_user_id, actor_username, action, status, stage, progress, error_code,
		error_message, log_path, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, task.ID, task.ModelID, task.OwnerID,
		task.ActorUserID, task.ActorUsername,
		task.Action, task.Status, task.Stage, task.Progress, task.ErrorCode, task.ErrorMessage,
		task.LogPath, formatTime(now))
	return task, err
}

func (s *Store) UpdateModelTask(ctx context.Context, task domain.ModelTask) error {
	_, err := s.db.ExecContext(ctx, `UPDATE model_tasks SET status = ?, stage = ?, progress = ?,
		error_code = ?, error_message = ?, started_at = ?, finished_at = ? WHERE id = ?`,
		task.Status, task.Stage, task.Progress, task.ErrorCode, task.ErrorMessage,
		nullableTime(task.StartedAt), nullableTime(task.FinishedAt), task.ID)
	return err
}

func (s *Store) GetModelTask(ctx context.Context, id string) (domain.ModelTask, error) {
	return s.getModelTask(ctx, ` WHERE id = ?`, id)
}

func (s *Store) LatestModelTask(ctx context.Context, modelID string) (domain.ModelTask, error) {
	return s.getModelTask(ctx, ` WHERE model_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, modelID)
}

func (s *Store) getModelTask(ctx context.Context, suffix string, arg any) (domain.ModelTask, error) {
	var task domain.ModelTask
	var action, status, created string
	var started, finished sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, model_id, owner_id, actor_user_id, actor_username, action, status, stage,
		progress, error_code, error_message, log_path, created_at, started_at, finished_at FROM model_tasks`+suffix, arg).
		Scan(&task.ID, &task.ModelID, &task.OwnerID, &task.ActorUserID, &task.ActorUsername, &action, &status, &task.Stage, &task.Progress,
			&task.ErrorCode, &task.ErrorMessage, &task.LogPath, &created, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return task, domain.ErrNotFound
	}
	if err != nil {
		return task, err
	}
	task.Action, task.Status = domain.ModelTaskAction(action), domain.OperationStatus(status)
	task.CreatedAt, _ = parseTime(created)
	task.StartedAt, task.FinishedAt = parseNullTime(started), parseNullTime(finished)
	return task, nil
}

func (s *Store) SetModelState(ctx context.Context, id string, status domain.ModelStatus, errorMessage string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE models SET status = ?, error_message = ?, updated_at = ? WHERE id = ?`, status, errorMessage, formatTime(time.Now()), id)
	return err
}

func (s *Store) MarkModelReady(ctx context.Context, id, sha string, expanded, files int64) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE models SET status = ?, sha256 = ?, expanded_size_bytes = ?,
		file_count = ?, error_message = '', ready_at = ?, updated_at = ? WHERE id = ?`,
		domain.ModelReady, sha, expanded, files, formatTime(now), formatTime(now), id)
	return err
}

func (s *Store) MarkModelDeleted(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE models SET status = ?, deleted_at = ?, updated_at = ? WHERE id = ?`, domain.ModelDeleted, formatTime(now), formatTime(now), id)
	return err
}

func (s *Store) InterruptActiveModelTasks(ctx context.Context) error {
	now := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `UPDATE model_tasks SET status = ?, stage = 'interrupted',
		error_code = 'SERVER_RESTARTED', error_message = '管理服务重启，模型任务已中断', finished_at = ?
		WHERE status IN (?, ?)`, domain.OperationInterrupted, now, domain.OperationQueued, domain.OperationRunning)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE models SET status = ?, error_message = '管理服务重启，模型任务已中断', updated_at = ?
		WHERE status IN (?, ?)`, domain.ModelFailed, now, domain.ModelDeploying, domain.ModelDeleting)
	return err
}

func (s *Store) EnvironmentHasModels(ctx context.Context, environmentID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM models WHERE environment_id = ? AND deleted_at IS NULL`, environmentID).Scan(&count)
	return count > 0, err
}
