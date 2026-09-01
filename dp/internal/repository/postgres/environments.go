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

const environmentSelect = `SELECT
	e.id::text,
	e.owner_id::text,
	COALESCE(u.username, ''),
	e.name,
	host(e.ip),
	e.ssh_user,
	e.ssh_port,
	e.ssh_password_enc,
	e.install_dir,
	e.service_type,
	e.note,
	e.installed,
	e.installed_at,
	COALESCE(e.installed_package_sha256, ''),
	e.health_port,
	e.arch,
	e.host_key_fingerprint,
	e.last_validation_at,
	e.created_at,
	e.updated_at
	FROM environments e
	LEFT JOIN users u ON u.id = e.owner_id`

func (db *DB) ListEnvironments(ctx context.Context) ([]domain.Environment, error) {
	return db.listEnvironments(ctx, "", nil)
}

func (db *DB) ListEnvironmentsByOwner(ctx context.Context, ownerID string) ([]domain.Environment, error) {
	return db.listEnvironments(ctx, ` WHERE e.owner_id = $1`, []any{ownerID})
}

func (db *DB) listEnvironments(
	ctx context.Context,
	where string,
	args []any,
) ([]domain.Environment, error) {
	rows, err := db.pool.Query(ctx, environmentSelect+where+` ORDER BY e.service_type, e.name, e.ip`, args...)
	if err != nil {
		return nil, fmt.Errorf("query environments: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Environment, 0)
	for rows.Next() {
		item, err := scanPostgresEnvironment(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate environments: %w", err)
	}
	if err := db.PopulateEnvironmentTags(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (db *DB) GetEnvironment(ctx context.Context, id string) (domain.Environment, error) {
	item, err := scanPostgresEnvironment(db.pool.QueryRow(ctx, environmentSelect+` WHERE e.id = $1`, id))
	if err != nil {
		return domain.Environment{}, err
	}
	items := []domain.Environment{item}
	if err := db.PopulateEnvironmentTags(ctx, items); err != nil {
		return domain.Environment{}, err
	}
	return items[0], nil
}

func (db *DB) CreateEnvironment(ctx context.Context, env domain.Environment) (domain.Environment, error) {
	return db.CreateEnvironmentWithTags(ctx, env, nil)
}

func (db *DB) CreateEnvironmentWithTags(
	ctx context.Context,
	env domain.Environment,
	tagIDs []string,
) (domain.Environment, error) {
	if env.OwnerID == "" {
		env.OwnerID = domain.InitialAdminID
	}
	now := time.Now().UTC()
	env.ID = domain.NewID()
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Environment{}, fmt.Errorf("begin create environment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO environments (
		id, owner_id, name, ip, ssh_user, ssh_port, ssh_password_enc, install_dir,
		service_type, note, installed, installed_at, installed_package_sha256,
		health_port, arch, host_key_fingerprint, last_validation_at, created_at, updated_at
	) VALUES ($1, $2, $3, $4::inet, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, ''),
		$14, $15, $16, $17, $18, $18)`,
		env.ID, env.OwnerID, env.Name, env.IP, env.SSHUser, env.SSHPort, env.SSHPasswordEnc,
		env.InstallDir, env.ServiceType, env.Note, env.Installed, env.InstalledAt,
		env.InstalledPackageSHA256, env.HealthPort, env.Arch, env.HostKeyFingerprint,
		env.LastValidationAt, now)
	if isPostgresError(err, "23505") {
		return domain.Environment{}, domain.ErrConflict
	}
	if err != nil {
		return domain.Environment{}, fmt.Errorf("insert environment: %w", err)
	}
	if err := replaceEnvironmentTags(ctx, tx, env.ID, env.OwnerID, tagIDs); err != nil {
		return domain.Environment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Environment{}, fmt.Errorf("commit create environment: %w", err)
	}
	return db.GetEnvironment(ctx, env.ID)
}

func (db *DB) UpdateEnvironment(ctx context.Context, env domain.Environment) (domain.Environment, error) {
	return db.UpdateEnvironmentWithTags(ctx, env, nil)
}

func (db *DB) UpdateEnvironmentWithTags(
	ctx context.Context,
	env domain.Environment,
	tagIDs []string,
) (domain.Environment, error) {
	now := time.Now().UTC()
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Environment{}, fmt.Errorf("begin update environment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE environments SET
		name = $1, ip = $2::inet, ssh_user = $3, ssh_port = $4, ssh_password_enc = $5,
		install_dir = $6, service_type = $7, note = $8, installed = $9, installed_at = $10,
		installed_package_sha256 = NULLIF($11, ''), health_port = $12,
		host_key_fingerprint = $13, last_validation_at = $14, updated_at = $15
		WHERE id = $16`,
		env.Name, env.IP, env.SSHUser, env.SSHPort, env.SSHPasswordEnc, env.InstallDir,
		env.ServiceType, env.Note, env.Installed, env.InstalledAt, env.InstalledPackageSHA256,
		env.HealthPort, env.HostKeyFingerprint, env.LastValidationAt, now, env.ID)
	if isPostgresError(err, "23505") {
		return domain.Environment{}, domain.ErrConflict
	}
	if err != nil {
		return domain.Environment{}, fmt.Errorf("update environment: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.Environment{}, domain.ErrNotFound
	}
	if tagIDs != nil {
		if err := replaceEnvironmentTags(ctx, tx, env.ID, env.OwnerID, tagIDs); err != nil {
			return domain.Environment{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Environment{}, fmt.Errorf("commit update environment: %w", err)
	}
	return db.GetEnvironment(ctx, env.ID)
}

func (db *DB) UpsertImportedEnvironments(
	ctx context.Context,
	environments []domain.Environment,
) (created, overwritten int, err error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("begin import environments: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	for _, env := range environments {
		if env.OwnerID == "" {
			env.OwnerID = domain.InitialAdminID
		}
		var environmentID string
		queryErr := tx.QueryRow(ctx, `SELECT id::text FROM environments
			WHERE owner_id = $1 AND ip = $2::inet AND service_type = $3 FOR UPDATE`,
			env.OwnerID, env.IP, env.ServiceType).Scan(&environmentID)
		switch {
		case errors.Is(queryErr, pgx.ErrNoRows):
			environmentID = domain.NewID()
			_, err = tx.Exec(ctx, `INSERT INTO environments (
				id, owner_id, name, ip, ssh_user, ssh_port, ssh_password_enc, install_dir,
				service_type, note, installed, installed_at, installed_package_sha256,
				health_port, host_key_fingerprint, created_at, updated_at
			) VALUES ($1, $2, $3, $4::inet, $5, $6, $7, $8, $9, $10, $11, $12,
				NULLIF($13, ''), $14, $15, $16, $16)`, environmentID, env.OwnerID, env.Name,
				env.IP, env.SSHUser, env.SSHPort, env.SSHPasswordEnc, env.InstallDir,
				env.ServiceType, env.Note, env.Installed, env.InstalledAt,
				env.InstalledPackageSHA256, env.HealthPort, env.HostKeyFingerprint, now)
			created++
		case queryErr != nil:
			err = fmt.Errorf("find imported environment: %w", queryErr)
		default:
			_, err = tx.Exec(ctx, `UPDATE environments SET
				name = $1, ssh_user = $2, ssh_port = $3, ssh_password_enc = $4,
				install_dir = $5, note = $6, installed = $7, installed_at = $8,
				installed_package_sha256 = NULLIF($9, ''), health_port = $10,
				host_key_fingerprint = $11, last_validation_at = NULL, updated_at = $12
				WHERE id = $13`, env.Name, env.SSHUser, env.SSHPort, env.SSHPasswordEnc,
				env.InstallDir, env.Note, env.Installed, env.InstalledAt,
				env.InstalledPackageSHA256, env.HealthPort, env.HostKeyFingerprint, now, environmentID)
			overwritten++
		}
		if err != nil {
			return 0, 0, fmt.Errorf("write imported environment: %w", err)
		}
		if _, err = tx.Exec(ctx, `DELETE FROM environment_tags WHERE environment_id = $1`, environmentID); err != nil {
			return 0, 0, fmt.Errorf("clear imported environment tags: %w", err)
		}
		for _, tag := range env.Tags {
			var tagID string
			queryErr = tx.QueryRow(ctx, `SELECT id::text FROM resource_tags
				WHERE owner_id = $1 AND lower(group_name) = lower($2) AND lower(value) = lower($3)
				AND deleted_at IS NULL`, env.OwnerID, tag.GroupName, tag.Value).Scan(&tagID)
			if errors.Is(queryErr, pgx.ErrNoRows) {
				tagID = domain.NewID()
				_, err = tx.Exec(ctx, `INSERT INTO resource_tags
					(id, owner_id, group_name, value, created_at, updated_at)
					VALUES ($1, $2, $3, $4, $5, $5)`, tagID, env.OwnerID, tag.GroupName, tag.Value, now)
			} else if queryErr != nil {
				err = queryErr
			}
			if err != nil {
				return 0, 0, fmt.Errorf("upsert imported tag: %w", err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO environment_tags (environment_id, tag_id)
				VALUES ($1, $2)`, environmentID, tagID); err != nil {
				return 0, 0, fmt.Errorf("attach imported tag: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit environment import: %w", err)
	}
	return created, overwritten, nil
}

func (db *DB) RecordValidation(ctx context.Context, id, fingerprint, arch string) error {
	now := time.Now().UTC()
	command, err := db.pool.Exec(ctx, `UPDATE environments SET
		host_key_fingerprint = $1, arch = $2, last_validation_at = $3, updated_at = $3
		WHERE id = $4`, fingerprint, arch, now, id)
	return requireAffected("record environment validation", command, err)
}

func (db *DB) UpdateEnvironmentArch(ctx context.Context, id, arch string) error {
	command, err := db.pool.Exec(ctx, `UPDATE environments SET arch = $1, updated_at = $2 WHERE id = $3`,
		arch, time.Now().UTC(), id)
	return requireAffected("update environment architecture", command, err)
}

func (db *DB) MarkInstalled(ctx context.Context, id, sha string, healthPort int) error {
	now := time.Now().UTC()
	command, err := db.pool.Exec(ctx, `UPDATE environments SET installed = TRUE, installed_at = $1,
		installed_package_sha256 = $2, health_port = $3, updated_at = $1 WHERE id = $4`,
		now, sha, healthPort, id)
	return requireAffected("mark environment installed", command, err)
}

func (db *DB) MarkUninstalled(ctx context.Context, id string) error {
	command, err := db.pool.Exec(ctx, `UPDATE environments SET installed = FALSE, installed_at = NULL,
		installed_package_sha256 = NULL, health_port = NULL, updated_at = $1 WHERE id = $2`,
		time.Now().UTC(), id)
	return requireAffected("mark environment uninstalled", command, err)
}

func (db *DB) UpdateHealthPort(ctx context.Context, id string, healthPort int) error {
	command, err := db.pool.Exec(ctx, `UPDATE environments SET health_port = $1, updated_at = $2 WHERE id = $3`,
		healthPort, time.Now().UTC(), id)
	return requireAffected("update environment health port", command, err)
}

func (db *DB) DeleteEnvironment(ctx context.Context, id string) ([]string, error) {
	command, err := db.pool.Exec(ctx, `DELETE FROM environments WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("delete environment: %w", err)
	}
	if command.RowsAffected() == 0 {
		return nil, domain.ErrNotFound
	}
	return nil, nil
}

func (db *DB) PopulateEnvironmentTags(ctx context.Context, environments []domain.Environment) error {
	if len(environments) == 0 {
		return nil
	}
	ids := make([]string, 0, len(environments))
	byID := make(map[string]int, len(environments))
	for index := range environments {
		ids = append(ids, environments[index].ID)
		byID[environments[index].ID] = index
		environments[index].Tags = nil
	}
	rows, err := db.pool.Query(ctx, `SELECT et.environment_id::text, t.id::text, t.group_name, t.value
		FROM environment_tags et JOIN resource_tags t ON t.id = et.tag_id
		WHERE et.environment_id = ANY($1::uuid[]) AND t.deleted_at IS NULL
		ORDER BY lower(t.group_name), lower(t.value), t.id`, ids)
	if err != nil {
		return fmt.Errorf("query environment tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var environmentID string
		var tag domain.ResourceTagRef
		if err := rows.Scan(&environmentID, &tag.ID, &tag.GroupName, &tag.Value); err != nil {
			return fmt.Errorf("scan environment tag: %w", err)
		}
		if index, ok := byID[environmentID]; ok {
			environments[index].Tags = append(environments[index].Tags, tag)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate environment tags: %w", err)
	}
	return nil
}

func replaceEnvironmentTags(
	ctx context.Context,
	tx pgx.Tx,
	environmentID string,
	ownerID string,
	tagIDs []string,
) error {
	if len(tagIDs) > 20 {
		return domain.FieldError("tag_ids", "每个环境最多关联 20 个标签")
	}
	if hasDuplicates(tagIDs) {
		return domain.FieldError("tag_ids", "标签 ID 不能重复")
	}
	if len(tagIDs) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM resource_tags
			WHERE owner_id = $1 AND id = ANY($2::uuid[]) AND deleted_at IS NULL`, ownerID, tagIDs).Scan(&count); err != nil {
			return fmt.Errorf("validate environment tags: %w", err)
		}
		if count != len(tagIDs) {
			return domain.FieldError("tag_ids", "包含不存在或不属于当前账号的标签")
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM environment_tags WHERE environment_id = $1`, environmentID); err != nil {
		return fmt.Errorf("delete environment tags: %w", err)
	}
	for _, tagID := range tagIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO environment_tags (environment_id, tag_id) VALUES ($1, $2)`,
			environmentID, tagID); err != nil {
			return fmt.Errorf("insert environment tag: %w", err)
		}
	}
	return nil
}

func scanPostgresEnvironment(row interface{ Scan(...any) error }) (domain.Environment, error) {
	var item domain.Environment
	var installedAt, validationAt sql.NullTime
	var healthPort sql.NullInt64
	err := row.Scan(
		&item.ID, &item.OwnerID, &item.OwnerUsername, &item.Name, &item.IP, &item.SSHUser,
		&item.SSHPort, &item.SSHPasswordEnc, &item.InstallDir, &item.ServiceType, &item.Note,
		&item.Installed, &installedAt, &item.InstalledPackageSHA256, &healthPort, &item.Arch,
		&item.HostKeyFingerprint, &validationAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Environment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Environment{}, fmt.Errorf("scan environment: %w", err)
	}
	if installedAt.Valid {
		item.InstalledAt = &installedAt.Time
	}
	if validationAt.Valid {
		item.LastValidationAt = &validationAt.Time
	}
	if healthPort.Valid {
		value := int(healthPort.Int64)
		item.HealthPort = &value
	}
	return item, nil
}
