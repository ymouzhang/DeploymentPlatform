package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"DP/internal/domain"
	"github.com/jackc/pgx/v5"
)

const packageSelect = `SELECT
	p.owner_id::text,
	COALESCE(u.username, ''),
	p.service_type,
	COALESCE(p.current_version_id::text, ''),
	p.original_filename,
	p.storage_path,
	p.sha256,
	p.size_bytes,
	p.config_port,
	p.note,
	p.uploaded_at,
	p.updated_at,
	(SELECT count(*) FROM package_versions v WHERE v.owner_id = p.owner_id AND v.service_type = p.service_type),
	(SELECT count(*) FROM environments e WHERE e.owner_id = p.owner_id
		AND e.service_type = p.service_type AND e.installed_package_sha256 = p.sha256)
	FROM packages p
	LEFT JOIN users u ON u.id = p.owner_id`

const packageVersionSelect = `SELECT
	v.id::text,
	v.owner_id::text,
	v.service_type,
	v.original_filename,
	v.storage_path,
	v.sha256,
	v.size_bytes,
	v.config_port,
	v.config_format,
	v.config_path,
	v.config_content,
	v.note,
	COALESCE(v.uploaded_by::text, ''),
	v.uploaded_by_username,
	v.uploaded_at,
	p.current_version_id = v.id,
	(SELECT count(*) FROM environments e WHERE e.owner_id = v.owner_id
		AND e.service_type = v.service_type AND e.installed_package_sha256 = v.sha256)
	FROM package_versions v
	JOIN packages p ON p.owner_id = v.owner_id AND p.service_type = v.service_type`

func (db *DB) GetPackage(ctx context.Context, serviceType string) (domain.Package, error) {
	return db.GetPackageByOwner(ctx, domain.InitialAdminID, serviceType)
}

func (db *DB) GetPackageByOwner(ctx context.Context, ownerID, serviceType string) (domain.Package, error) {
	return scanPostgresPackage(db.pool.QueryRow(ctx, packageSelect+
		` WHERE p.owner_id = $1 AND p.service_type = $2`, ownerID, serviceType))
}

func (db *DB) ListPackages(ctx context.Context) ([]domain.Package, error) {
	return db.ListPackagesByOwner(ctx, "")
}

func (db *DB) ListPackagesByOwner(ctx context.Context, ownerID string) ([]domain.Package, error) {
	query := packageSelect
	args := make([]any, 0, 1)
	if ownerID != "" {
		query += ` WHERE p.owner_id = $1`
		args = append(args, ownerID)
	}
	query += ` ORDER BY p.service_type, p.owner_id`
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query packages: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Package, 0)
	for rows.Next() {
		item, err := scanPostgresPackage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate packages: %w", err)
	}
	return items, nil
}

func (db *DB) UpsertPackage(ctx context.Context, pkg domain.Package) error {
	if pkg.OwnerID == "" {
		pkg.OwnerID = domain.InitialAdminID
	}
	_, err := db.pool.Exec(ctx, `INSERT INTO packages (
		owner_id, service_type, original_filename, storage_path, sha256, size_bytes,
		config_port, note, uploaded_at, updated_at, current_version_id
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, '')::uuid)
	ON CONFLICT (owner_id, service_type) DO UPDATE SET
		original_filename = EXCLUDED.original_filename,
		storage_path = EXCLUDED.storage_path,
		sha256 = EXCLUDED.sha256,
		size_bytes = EXCLUDED.size_bytes,
		config_port = EXCLUDED.config_port,
		note = EXCLUDED.note,
		uploaded_at = EXCLUDED.uploaded_at,
		updated_at = EXCLUDED.updated_at,
		current_version_id = EXCLUDED.current_version_id`, pkg.OwnerID, pkg.ServiceType,
		pkg.OriginalFilename, pkg.StoragePath, pkg.SHA256, pkg.SizeBytes, pkg.ConfigPort,
		pkg.Note, pkg.UploadedAt, pkg.UpdatedAt, pkg.CurrentVersionID)
	if err != nil {
		return fmt.Errorf("upsert package: %w", err)
	}
	return nil
}

func (db *DB) UpdatePackageNote(ctx context.Context, serviceType, note string) (domain.Package, error) {
	return db.UpdatePackageNoteByOwner(ctx, domain.InitialAdminID, serviceType, note)
}

func (db *DB) UpdatePackageNoteByOwner(
	ctx context.Context,
	ownerID string,
	serviceType string,
	note string,
) (domain.Package, error) {
	command, err := db.pool.Exec(ctx, `UPDATE packages SET note = $1, updated_at = $2
		WHERE owner_id = $3 AND service_type = $4`, note, time.Now().UTC(), ownerID, serviceType)
	if err := requireAffected("update package note", command, err); err != nil {
		return domain.Package{}, err
	}
	return db.GetPackageByOwner(ctx, ownerID, serviceType)
}

func (db *DB) DeletePackage(ctx context.Context, serviceType string) error {
	return db.DeletePackageByOwner(ctx, domain.InitialAdminID, serviceType)
}

func (db *DB) DeletePackageByOwner(ctx context.Context, ownerID, serviceType string) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delete package: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `DELETE FROM packages WHERE owner_id = $1 AND service_type = $2`,
		ownerID, serviceType)
	if err := requireAffected("delete package", command, err); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM package_versions WHERE owner_id = $1 AND service_type = $2`,
		ownerID, serviceType); err != nil {
		return fmt.Errorf("delete package versions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete package: %w", err)
	}
	return nil
}

func (db *DB) CountInstalledEnvironments(ctx context.Context, serviceType string) (int, error) {
	return db.CountInstalledEnvironmentsByOwner(ctx, domain.InitialAdminID, serviceType)
}

func (db *DB) CountInstalledEnvironmentsByOwner(
	ctx context.Context,
	ownerID string,
	serviceType string,
) (int, error) {
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM environments
		WHERE owner_id = $1 AND service_type = $2 AND installed`, ownerID, serviceType).Scan(&count); err != nil {
		return 0, fmt.Errorf("count installed environments: %w", err)
	}
	return count, nil
}

func (db *DB) SavePackageVersion(ctx context.Context, version domain.PackageVersion) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin save package version: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO package_versions (
		id, owner_id, service_type, original_filename, storage_path, sha256, size_bytes,
		config_port, config_format, config_path, config_content, note, uploaded_by,
		uploaded_by_username, uploaded_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
		NULLIF($13, '')::uuid, $14, $15)`, version.ID, version.OwnerID, version.ServiceType,
		version.OriginalFilename, version.StoragePath, version.SHA256, version.SizeBytes,
		version.ConfigPort, version.ConfigFormat, version.ConfigPath, version.ConfigContent,
		version.Note, version.UploadedBy, version.UploadedByName, version.UploadedAt)
	if isPostgresError(err, "23505") {
		return &domain.AppError{Code: "PACKAGE_VERSION_EXISTS", Message: "相同内容的安装包版本已经存在"}
	}
	if err != nil {
		return fmt.Errorf("insert package version: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO packages (
		owner_id, service_type, current_version_id, original_filename, storage_path, sha256,
		size_bytes, config_port, note, uploaded_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
	ON CONFLICT (owner_id, service_type) DO UPDATE SET
		current_version_id = EXCLUDED.current_version_id,
		original_filename = EXCLUDED.original_filename,
		storage_path = EXCLUDED.storage_path,
		sha256 = EXCLUDED.sha256,
		size_bytes = EXCLUDED.size_bytes,
		config_port = EXCLUDED.config_port,
		note = EXCLUDED.note,
		uploaded_at = EXCLUDED.uploaded_at,
		updated_at = EXCLUDED.updated_at`, version.OwnerID, version.ServiceType, version.ID,
		version.OriginalFilename, version.StoragePath, version.SHA256, version.SizeBytes,
		version.ConfigPort, version.Note, version.UploadedAt)
	if err != nil {
		return fmt.Errorf("upsert current package: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit package version: %w", err)
	}
	return nil
}

func (db *DB) UpdatePackageVersionConfigTemplate(
	ctx context.Context,
	ownerID string,
	serviceType string,
	id string,
	content []byte,
	format string,
	path string,
	port int,
) error {
	command, err := db.pool.Exec(ctx, `UPDATE package_versions SET
		config_content = $1, config_format = $2, config_path = $3, config_port = $4
		WHERE id = $5 AND owner_id = $6 AND service_type = $7`, content, format, path, port,
		id, ownerID, serviceType)
	return requireAffected("update package config template", command, err)
}

func (db *DB) ListPackageVersions(
	ctx context.Context,
	ownerID string,
	serviceType string,
) ([]domain.PackageVersion, error) {
	rows, err := db.pool.Query(ctx, packageVersionSelect+`
		WHERE v.owner_id = $1 AND v.service_type = $2
		ORDER BY v.uploaded_at DESC, v.id DESC`, ownerID, serviceType)
	if err != nil {
		return nil, fmt.Errorf("query package versions: %w", err)
	}
	defer rows.Close()
	items := make([]domain.PackageVersion, 0)
	for rows.Next() {
		item, err := scanPostgresPackageVersion(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate package versions: %w", err)
	}
	return items, nil
}

func (db *DB) GetPackageVersion(
	ctx context.Context,
	ownerID string,
	serviceType string,
	id string,
) (domain.PackageVersion, error) {
	return scanPostgresPackageVersion(db.pool.QueryRow(ctx, packageVersionSelect+`
		WHERE v.owner_id = $1 AND v.service_type = $2 AND v.id = $3`, ownerID, serviceType, id))
}

func (db *DB) GetPackageVersionBySHA(
	ctx context.Context,
	ownerID string,
	serviceType string,
	sha256 string,
) (domain.PackageVersion, error) {
	return scanPostgresPackageVersion(db.pool.QueryRow(ctx, packageVersionSelect+`
		WHERE v.owner_id = $1 AND v.service_type = $2 AND v.sha256 = $3`, ownerID, serviceType, sha256))
}

func (db *DB) ActivatePackageVersion(ctx context.Context, version domain.PackageVersion) error {
	command, err := db.pool.Exec(ctx, `UPDATE packages SET
		current_version_id = $1, original_filename = $2, storage_path = $3, sha256 = $4,
		size_bytes = $5, config_port = $6, note = $7, uploaded_at = $8, updated_at = $9
		WHERE owner_id = $10 AND service_type = $11`, version.ID, version.OriginalFilename,
		version.StoragePath, version.SHA256, version.SizeBytes, version.ConfigPort, version.Note,
		version.UploadedAt, time.Now().UTC(), version.OwnerID, version.ServiceType)
	return requireAffected("activate package version", command, err)
}

func (db *DB) UpdatePackageVersionNote(
	ctx context.Context,
	ownerID string,
	serviceType string,
	id string,
	note string,
) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin update package version note: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE package_versions SET note = $1
		WHERE id = $2 AND owner_id = $3 AND service_type = $4`, note, id, ownerID, serviceType)
	if err := requireAffected("update package version note", command, err); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE packages SET note = $1, updated_at = $2
		WHERE owner_id = $3 AND service_type = $4 AND current_version_id = $5`, note,
		time.Now().UTC(), ownerID, serviceType, id); err != nil {
		return fmt.Errorf("update current package note: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit package version note: %w", err)
	}
	return nil
}

func (db *DB) DeletePackageVersion(ctx context.Context, ownerID, serviceType, id string) error {
	version, err := db.GetPackageVersion(ctx, ownerID, serviceType, id)
	if err != nil {
		return err
	}
	if version.Current {
		return &domain.AppError{Code: "PACKAGE_VERSION_CURRENT", Message: "当前版本不能删除，请先切换其他版本"}
	}
	if version.ReferencedCount > 0 {
		return &domain.AppError{Code: "PACKAGE_VERSION_IN_USE", Message: "该版本仍被环境引用，不能删除"}
	}
	command, err := db.pool.Exec(ctx, `DELETE FROM package_versions
		WHERE id = $1 AND owner_id = $2 AND service_type = $3`, id, ownerID, serviceType)
	return requireAffected("delete package version", command, err)
}

func (db *DB) PrunablePackageVersions(
	ctx context.Context,
	ownerID string,
	serviceType string,
	retain int,
) ([]domain.PackageVersion, error) {
	items, err := db.ListPackageVersions(ctx, ownerID, serviceType)
	if err != nil {
		return nil, err
	}
	if retain < 1 {
		retain = 1
	}
	result := make([]domain.PackageVersion, 0)
	for index, item := range items {
		if index >= retain && !item.Current && item.ReferencedCount == 0 {
			result = append(result, item)
		}
	}
	return result, nil
}

func scanPostgresPackage(row interface{ Scan(...any) error }) (domain.Package, error) {
	var item domain.Package
	err := row.Scan(&item.OwnerID, &item.OwnerUsername, &item.ServiceType, &item.CurrentVersionID,
		&item.OriginalFilename, &item.StoragePath, &item.SHA256, &item.SizeBytes, &item.ConfigPort,
		&item.Note, &item.UploadedAt, &item.UpdatedAt, &item.VersionCount, &item.ReferencedCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Package{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Package{}, fmt.Errorf("scan package: %w", err)
	}
	return item, nil
}

func scanPostgresPackageVersion(row interface{ Scan(...any) error }) (domain.PackageVersion, error) {
	var item domain.PackageVersion
	err := row.Scan(&item.ID, &item.OwnerID, &item.ServiceType, &item.OriginalFilename,
		&item.StoragePath, &item.SHA256, &item.SizeBytes, &item.ConfigPort, &item.ConfigFormat,
		&item.ConfigPath, &item.ConfigContent, &item.Note, &item.UploadedBy, &item.UploadedByName,
		&item.UploadedAt, &item.Current, &item.ReferencedCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PackageVersion{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.PackageVersion{}, fmt.Errorf("scan package version: %w", err)
	}
	item.ValidationStatus = "passed"
	return item, nil
}
