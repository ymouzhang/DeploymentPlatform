package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"DP/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (db *DB) GetServiceConfig(ctx context.Context, service_instanceID string) (domain.ServiceConfig, error) {
	var item domain.ServiceConfig
	err := db.pool.QueryRow(ctx, `SELECT service_instance_id::text, content, format, path, port,
		COALESCE(current_revision_id::text, ''), updated_at
		FROM service_configs WHERE service_instance_id = $1`, service_instanceID).Scan(
		&item.ServiceInstanceID, &item.Content, &item.Format, &item.Path, &item.Port,
		&item.CurrentRevisionID, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ServiceConfig{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ServiceConfig{}, fmt.Errorf("query service config: %w", err)
	}
	return item, nil
}

func (db *DB) UpsertServiceConfig(ctx context.Context, config domain.ServiceConfig) (domain.ServiceConfig, error) {
	config.UpdatedAt = time.Now().UTC()
	_, err := db.pool.Exec(ctx, `INSERT INTO service_configs
		(service_instance_id, content, format, path, port, current_revision_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, $7)
		ON CONFLICT (service_instance_id) DO UPDATE SET
		content = EXCLUDED.content,
		format = EXCLUDED.format,
		path = EXCLUDED.path,
		port = EXCLUDED.port,
		current_revision_id = EXCLUDED.current_revision_id,
		updated_at = EXCLUDED.updated_at`, config.ServiceInstanceID, config.Content, config.Format,
		config.Path, config.Port, config.CurrentRevisionID, config.UpdatedAt)
	if err != nil {
		return domain.ServiceConfig{}, fmt.Errorf("upsert service config: %w", err)
	}
	return db.GetServiceConfig(ctx, config.ServiceInstanceID)
}

func (db *DB) DeleteServiceConfig(ctx context.Context, service_instanceID string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM service_configs WHERE service_instance_id = $1`, service_instanceID)
	if err != nil {
		return fmt.Errorf("delete service config: %w", err)
	}
	return nil
}
