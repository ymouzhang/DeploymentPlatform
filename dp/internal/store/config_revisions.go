package store

import (
	"context"
	"database/sql"
	"errors"

	"DP/internal/domain"
)

func (s *Store) SaveServiceConfigRevision(ctx context.Context, config domain.ServiceConfig, revision domain.ServiceConfigRevision, updateHealthPort bool) (domain.ServiceConfigRevision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO service_config_revisions (
		id, environment_id, content, format, path, port, source, restored_from_id,
		created_by, created_by_username, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, revision.ID, revision.EnvironmentID,
		revision.Content, revision.Format, revision.Path, revision.Port, revision.Source,
		nullableString(revision.RestoredFromID), revision.CreatedBy, revision.CreatedByName,
		formatTime(revision.CreatedAt))
	if err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO service_configs (
		environment_id, content, format, path, port, current_revision_id, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(environment_id) DO UPDATE SET content=excluded.content, format=excluded.format,
		path=excluded.path, port=excluded.port, current_revision_id=excluded.current_revision_id,
		updated_at=excluded.updated_at`, config.EnvironmentID, config.Content, config.Format,
		config.Path, config.Port, revision.ID, formatTime(revision.CreatedAt))
	if err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	if updateHealthPort {
		result, err := tx.ExecContext(ctx, `UPDATE environments SET health_port = ?, updated_at = ? WHERE id = ?`,
			config.Port, formatTime(revision.CreatedAt), config.EnvironmentID)
		if err != nil {
			return domain.ServiceConfigRevision{}, err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return domain.ServiceConfigRevision{}, domain.ErrNotFound
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	revision.Current = true
	return revision, nil
}

func (s *Store) ListServiceConfigRevisions(ctx context.Context, environmentID string) ([]domain.ServiceConfigRevision, error) {
	rows, err := s.db.QueryContext(ctx, configRevisionSelect+` WHERE r.environment_id=? ORDER BY r.created_at DESC, r.id DESC`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ServiceConfigRevision{}
	for rows.Next() {
		item, err := scanConfigRevision(rows, false)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetServiceConfigRevision(ctx context.Context, environmentID, id string) (domain.ServiceConfigRevision, error) {
	item, err := scanConfigRevision(s.db.QueryRowContext(ctx, configRevisionSelect+` WHERE r.environment_id=? AND r.id=?`, environmentID, id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ServiceConfigRevision{}, domain.ErrNotFound
	}
	return item, err
}

const configRevisionSelect = `SELECT r.id, r.environment_id, r.content, r.format, r.path, r.port,
	r.source, r.restored_from_id, r.created_by, r.created_by_username, r.created_at,
	CASE WHEN c.current_revision_id=r.id THEN 1 ELSE 0 END
	FROM service_config_revisions r LEFT JOIN service_configs c ON c.environment_id=r.environment_id`

func scanConfigRevision(row scanner, includeContent bool) (domain.ServiceConfigRevision, error) {
	var item domain.ServiceConfigRevision
	var restored sql.NullString
	var created string
	var current int
	err := row.Scan(&item.ID, &item.EnvironmentID, &item.Content, &item.Format, &item.Path,
		&item.Port, &item.Source, &restored, &item.CreatedBy, &item.CreatedByName, &created, &current)
	if err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	if !includeContent {
		item.Content = ""
	}
	item.RestoredFromID = restored.String
	item.CreatedAt, _ = parseTime(created)
	item.Current = current != 0
	return item, nil
}
