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

func (db *DB) GetServiceConfig(ctx context.Context, environmentID string) (domain.ServiceConfig, error) {
	var item domain.ServiceConfig
	err := db.pool.QueryRow(ctx, `SELECT environment_id::text, content, format, path, port,
		COALESCE(current_revision_id::text, ''), updated_at
		FROM service_configs WHERE environment_id = $1`, environmentID).Scan(
		&item.EnvironmentID, &item.Content, &item.Format, &item.Path, &item.Port,
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
		(environment_id, content, format, path, port, current_revision_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, $7)
		ON CONFLICT (environment_id) DO UPDATE SET
		content = EXCLUDED.content,
		format = EXCLUDED.format,
		path = EXCLUDED.path,
		port = EXCLUDED.port,
		current_revision_id = EXCLUDED.current_revision_id,
		updated_at = EXCLUDED.updated_at`, config.EnvironmentID, config.Content, config.Format,
		config.Path, config.Port, config.CurrentRevisionID, config.UpdatedAt)
	if err != nil {
		return domain.ServiceConfig{}, fmt.Errorf("upsert service config: %w", err)
	}
	return db.GetServiceConfig(ctx, config.EnvironmentID)
}

func (db *DB) DeleteServiceConfig(ctx context.Context, environmentID string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM service_configs WHERE environment_id = $1`, environmentID)
	if err != nil {
		return fmt.Errorf("delete service config: %w", err)
	}
	return nil
}

func (db *DB) ListServicePorts(ctx context.Context) (map[string]int, error) {
	rows, err := db.pool.Query(ctx, `SELECT e.id::text, COALESCE(c.port, p.config_port)
		FROM environments e
		LEFT JOIN service_configs c ON c.environment_id = e.id
		LEFT JOIN packages p ON p.owner_id = e.owner_id AND p.service_type = e.service_type`)
	if err != nil {
		return nil, fmt.Errorf("query service ports: %w", err)
	}
	defer rows.Close()
	ports := make(map[string]int)
	for rows.Next() {
		var environmentID string
		var port sql.NullInt64
		if err := rows.Scan(&environmentID, &port); err != nil {
			return nil, fmt.Errorf("scan service port: %w", err)
		}
		if port.Valid {
			ports[environmentID] = int(port.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service ports: %w", err)
	}
	return ports, nil
}
