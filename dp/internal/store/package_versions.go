package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"DP/internal/domain"
)

func (s *Store) SavePackageVersion(ctx context.Context, version domain.PackageVersion) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO package_versions (
		id, owner_id, service_type, original_filename, storage_path, sha256, size_bytes,
		config_port, config_format, config_path, note, uploaded_by, uploaded_by_username, uploaded_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, version.ID, version.OwnerID,
		version.ServiceType, version.OriginalFilename, version.StoragePath, version.SHA256,
		version.SizeBytes, version.ConfigPort, version.ConfigFormat, version.ConfigPath,
		version.Note, version.UploadedBy, version.UploadedByName, formatTime(version.UploadedAt))
	if isUniqueError(err) {
		return &domain.AppError{Code: "PACKAGE_VERSION_EXISTS", Message: "相同内容的安装包版本已经存在"}
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO packages (
		owner_id, service_type, current_version_id, original_filename, storage_path, sha256,
		size_bytes, config_port, note, uploaded_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(owner_id, service_type) DO UPDATE SET
		current_version_id=excluded.current_version_id, original_filename=excluded.original_filename,
		storage_path=excluded.storage_path, sha256=excluded.sha256, size_bytes=excluded.size_bytes,
		config_port=excluded.config_port, note=excluded.note, uploaded_at=excluded.uploaded_at,
		updated_at=excluded.updated_at`, version.OwnerID, version.ServiceType, version.ID,
		version.OriginalFilename, version.StoragePath, version.SHA256, version.SizeBytes,
		version.ConfigPort, version.Note, formatTime(version.UploadedAt), formatTime(version.UploadedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListPackageVersions(ctx context.Context, ownerID, serviceType string) ([]domain.PackageVersion, error) {
	rows, err := s.db.QueryContext(ctx, packageVersionSelect+`
		WHERE v.owner_id = ? AND v.service_type = ? ORDER BY v.uploaded_at DESC, v.id DESC`, ownerID, serviceType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.PackageVersion{}
	for rows.Next() {
		item, err := scanPackageVersion(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetPackageVersion(ctx context.Context, ownerID, serviceType, id string) (domain.PackageVersion, error) {
	item, err := scanPackageVersion(s.db.QueryRowContext(ctx, packageVersionSelect+`
		WHERE v.owner_id = ? AND v.service_type = ? AND v.id = ?`, ownerID, serviceType, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PackageVersion{}, domain.ErrNotFound
	}
	return item, err
}

func (s *Store) ActivatePackageVersion(ctx context.Context, version domain.PackageVersion) error {
	result, err := s.db.ExecContext(ctx, `UPDATE packages SET current_version_id=?, original_filename=?,
		storage_path=?, sha256=?, size_bytes=?, config_port=?, note=?, uploaded_at=?, updated_at=?
		WHERE owner_id=? AND service_type=?`, version.ID, version.OriginalFilename,
		version.StoragePath, version.SHA256, version.SizeBytes, version.ConfigPort, version.Note,
		formatTime(version.UploadedAt), formatTime(time.Now().UTC()), version.OwnerID, version.ServiceType)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) UpdatePackageVersionNote(ctx context.Context, ownerID, serviceType, id, note string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE package_versions SET note=? WHERE id=? AND owner_id=? AND service_type=?`, note, id, ownerID, serviceType)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE packages SET note=?, updated_at=? WHERE owner_id=? AND service_type=? AND current_version_id=?`, note, formatTime(time.Now().UTC()), ownerID, serviceType, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeletePackageVersion(ctx context.Context, ownerID, serviceType, id string) error {
	version, err := s.GetPackageVersion(ctx, ownerID, serviceType, id)
	if err != nil {
		return err
	}
	if version.Current {
		return &domain.AppError{Code: "PACKAGE_VERSION_CURRENT", Message: "当前版本不能删除，请先切换其他版本"}
	}
	if version.ReferencedCount > 0 {
		return &domain.AppError{Code: "PACKAGE_VERSION_IN_USE", Message: "该版本仍被环境引用，不能删除"}
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM package_versions WHERE id=? AND owner_id=? AND service_type=?`, id, ownerID, serviceType)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) PrunablePackageVersions(ctx context.Context, ownerID, serviceType string, retain int) ([]domain.PackageVersion, error) {
	items, err := s.ListPackageVersions(ctx, ownerID, serviceType)
	if err != nil {
		return nil, err
	}
	if retain < 1 {
		retain = 1
	}
	result := []domain.PackageVersion{}
	for index, item := range items {
		if index >= retain && !item.Current && item.ReferencedCount == 0 {
			result = append(result, item)
		}
	}
	return result, nil
}

const packageVersionSelect = `SELECT v.id, v.owner_id, v.service_type, v.original_filename,
	v.storage_path, v.sha256, v.size_bytes, v.config_port, v.config_format, v.config_path,
	v.note, v.uploaded_by, v.uploaded_by_username, v.uploaded_at,
	CASE WHEN p.current_version_id = v.id THEN 1 ELSE 0 END,
	(SELECT COUNT(*) FROM environments e WHERE e.owner_id=v.owner_id AND e.service_type=v.service_type AND e.installed_package_sha256=v.sha256)
	FROM package_versions v JOIN packages p ON p.owner_id=v.owner_id AND p.service_type=v.service_type`

func scanPackageVersion(row scanner) (domain.PackageVersion, error) {
	var item domain.PackageVersion
	var uploaded string
	var current int
	err := row.Scan(&item.ID, &item.OwnerID, &item.ServiceType, &item.OriginalFilename,
		&item.StoragePath, &item.SHA256, &item.SizeBytes, &item.ConfigPort, &item.ConfigFormat,
		&item.ConfigPath, &item.Note, &item.UploadedBy, &item.UploadedByName, &uploaded,
		&current, &item.ReferencedCount)
	if err != nil {
		return domain.PackageVersion{}, err
	}
	item.UploadedAt, _ = parseTime(uploaded)
	item.Current = current != 0
	item.ValidationStatus = "passed"
	return item, nil
}
