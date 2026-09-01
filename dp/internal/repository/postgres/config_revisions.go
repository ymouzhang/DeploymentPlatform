package postgres

import (
	"context"
	"errors"
	"fmt"

	"DP/internal/domain"
	"github.com/jackc/pgx/v5"
)

const configRevisionSelect = `SELECT
	r.id::text,
	r.environment_id::text,
	r.content,
	r.format,
	r.path,
	r.port,
	r.source,
	COALESCE(r.restored_from_id::text, ''),
	COALESCE(r.created_by::text, ''),
	r.created_by_username,
	r.created_at,
	c.current_revision_id = r.id
	FROM service_config_revisions r
	LEFT JOIN service_configs c ON c.environment_id = r.environment_id`

func (db *DB) SaveServiceConfigRevision(
	ctx context.Context,
	config domain.ServiceConfig,
	revision domain.ServiceConfigRevision,
	updateHealthPort bool,
) (domain.ServiceConfigRevision, error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.ServiceConfigRevision{}, fmt.Errorf("begin save config revision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO service_config_revisions (
		id, environment_id, content, format, path, port, source, restored_from_id,
		created_by, created_by_username, created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid,
		NULLIF($9, '')::uuid, $10, $11)`, revision.ID, revision.EnvironmentID,
		revision.Content, revision.Format, revision.Path, revision.Port, revision.Source,
		revision.RestoredFromID, revision.CreatedBy, revision.CreatedByName, revision.CreatedAt)
	if err != nil {
		return domain.ServiceConfigRevision{}, fmt.Errorf("insert config revision: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO service_configs (
		environment_id, content, format, path, port, current_revision_id, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (environment_id) DO UPDATE SET
		content = EXCLUDED.content,
		format = EXCLUDED.format,
		path = EXCLUDED.path,
		port = EXCLUDED.port,
		current_revision_id = EXCLUDED.current_revision_id,
		updated_at = EXCLUDED.updated_at`, config.EnvironmentID, config.Content, config.Format,
		config.Path, config.Port, revision.ID, revision.CreatedAt)
	if err != nil {
		return domain.ServiceConfigRevision{}, fmt.Errorf("upsert current config revision: %w", err)
	}
	if updateHealthPort {
		command, err := tx.Exec(ctx, `UPDATE environments SET health_port = $1, updated_at = $2 WHERE id = $3`,
			config.Port, revision.CreatedAt, config.EnvironmentID)
		if err := requireAffected("update config health port", command, err); err != nil {
			return domain.ServiceConfigRevision{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ServiceConfigRevision{}, fmt.Errorf("commit config revision: %w", err)
	}
	revision.Current = true
	return revision, nil
}

func (db *DB) ListServiceConfigRevisions(
	ctx context.Context,
	environmentID string,
) ([]domain.ServiceConfigRevision, error) {
	rows, err := db.pool.Query(ctx, configRevisionSelect+`
		WHERE r.environment_id = $1 ORDER BY r.created_at DESC, r.id DESC`, environmentID)
	if err != nil {
		return nil, fmt.Errorf("query config revisions: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ServiceConfigRevision, 0)
	for rows.Next() {
		item, err := scanPostgresConfigRevision(rows, false)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate config revisions: %w", err)
	}
	return items, nil
}

func (db *DB) GetServiceConfigRevision(
	ctx context.Context,
	environmentID string,
	id string,
) (domain.ServiceConfigRevision, error) {
	return scanPostgresConfigRevision(db.pool.QueryRow(ctx, configRevisionSelect+`
		WHERE r.environment_id = $1 AND r.id = $2`, environmentID, id), true)
}

func scanPostgresConfigRevision(
	row interface{ Scan(...any) error },
	includeContent bool,
) (domain.ServiceConfigRevision, error) {
	var item domain.ServiceConfigRevision
	err := row.Scan(&item.ID, &item.EnvironmentID, &item.Content, &item.Format, &item.Path,
		&item.Port, &item.Source, &item.RestoredFromID, &item.CreatedBy, &item.CreatedByName,
		&item.CreatedAt, &item.Current)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ServiceConfigRevision{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ServiceConfigRevision{}, fmt.Errorf("scan config revision: %w", err)
	}
	if !includeContent {
		item.Content = ""
	}
	return item, nil
}
