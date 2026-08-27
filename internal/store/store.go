package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"DP/internal/domain"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	db   *sql.DB
	path string
}

const InitialAdminID = "00000000-0000-4000-8000-000000000001"

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `
		PRAGMA journal_mode=WAL;
		PRAGMA foreign_keys=ON;
		PRAGMA busy_timeout=5000;
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.InterruptActiveOperations(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var version int
		if _, err := fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			return fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		var exists int
		err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&exists)
		if err != nil && !strings.Contains(err.Error(), "no such table") {
			return fmt.Errorf("query migration %d: %w", version, err)
		}
		if exists > 0 {
			continue
		}
		content, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(content)); err == nil {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
				version, formatTime(time.Now()))
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}

func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	buf := make([]byte, 36)
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf)
}

func (s *Store) ListEnvironments(ctx context.Context) ([]domain.Environment, error) {
	rows, err := s.db.QueryContext(ctx, environmentSelect+` ORDER BY service_type, name, ip`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Environment
	for rows.Next() {
		env, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, env)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.PopulateEnvironmentTags(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) ListEnvironmentsByOwner(ctx context.Context, ownerID string) ([]domain.Environment, error) {
	rows, err := s.db.QueryContext(ctx, environmentSelect+` WHERE owner_id = ? ORDER BY service_type, name, ip`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Environment
	for rows.Next() {
		env, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, env)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.PopulateEnvironmentTags(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) GetEnvironment(ctx context.Context, id string) (domain.Environment, error) {
	env, err := scanEnvironment(s.db.QueryRowContext(ctx, environmentSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Environment{}, domain.ErrNotFound
	}
	if err != nil {
		return env, err
	}
	items := []domain.Environment{env}
	if err := s.PopulateEnvironmentTags(ctx, items); err != nil {
		return domain.Environment{}, err
	}
	return items[0], nil
}

func (s *Store) CreateEnvironment(ctx context.Context, env domain.Environment) (domain.Environment, error) {
	return s.CreateEnvironmentWithTags(ctx, env, nil)
}

func (s *Store) CreateEnvironmentWithTags(ctx context.Context, env domain.Environment, tagIDs []string) (domain.Environment, error) {
	if env.OwnerID == "" {
		env.OwnerID = InitialAdminID
	}
	now := time.Now().UTC()
	env.ID = NewID()
	env.CreatedAt = now
	env.UpdatedAt = now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Environment{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO environments (
			id, owner_id, name, ip, ssh_user, ssh_port, ssh_password_enc, install_dir,
			service_type, note, installed, installed_at, installed_package_sha256,
			health_port, arch, host_key_fingerprint, last_validation_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		env.ID, env.OwnerID, env.Name, env.IP, env.SSHUser, env.SSHPort, env.SSHPasswordEnc,
		env.InstallDir, env.ServiceType, env.Note, boolInt(env.Installed), nullableTime(env.InstalledAt),
		env.InstalledPackageSHA256, nullableInt(env.HealthPort), env.Arch, env.HostKeyFingerprint,
		nullableTime(env.LastValidationAt), formatTime(now), formatTime(now),
	)
	if isUniqueError(err) {
		return domain.Environment{}, domain.ErrConflict
	}
	if err != nil {
		return domain.Environment{}, err
	}
	if err := replaceEnvironmentTagsTx(ctx, tx, env.ID, env.OwnerID, tagIDs); err != nil {
		return domain.Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Environment{}, err
	}
	return s.GetEnvironment(ctx, env.ID)
}

func (s *Store) UpdateEnvironment(ctx context.Context, env domain.Environment) (domain.Environment, error) {
	return s.UpdateEnvironmentWithTags(ctx, env, nil)
}

func (s *Store) UpdateEnvironmentWithTags(ctx context.Context, env domain.Environment, tagIDs []string) (domain.Environment, error) {
	env.UpdatedAt = time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Environment{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE environments SET
			name = ?, ip = ?, ssh_user = ?, ssh_port = ?, ssh_password_enc = ?,
			install_dir = ?, service_type = ?, note = ?, installed = ?, installed_at = ?,
			installed_package_sha256 = ?, health_port = ?, host_key_fingerprint = ?,
			last_validation_at = ?, updated_at = ?
		WHERE id = ?`,
		env.Name, env.IP, env.SSHUser, env.SSHPort, env.SSHPasswordEnc, env.InstallDir,
		env.ServiceType, env.Note, boolInt(env.Installed), nullableTime(env.InstalledAt),
		env.InstalledPackageSHA256, nullableInt(env.HealthPort), env.HostKeyFingerprint,
		nullableTime(env.LastValidationAt), formatTime(env.UpdatedAt), env.ID,
	)
	if isUniqueError(err) {
		return domain.Environment{}, domain.ErrConflict
	}
	if err != nil {
		return domain.Environment{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.Environment{}, domain.ErrNotFound
	}
	if tagIDs != nil {
		if err := replaceEnvironmentTagsTx(ctx, tx, env.ID, env.OwnerID, tagIDs); err != nil {
			return domain.Environment{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Environment{}, err
	}
	return s.GetEnvironment(ctx, env.ID)
}

func (s *Store) UpsertImportedEnvironments(ctx context.Context, environments []domain.Environment) (created, overwritten int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	now := time.Now().UTC()
	for _, env := range environments {
		if env.OwnerID == "" {
			env.OwnerID = InitialAdminID
		}
		var existingID, createdAt string
		environmentID := ""
		queryErr := tx.QueryRowContext(ctx,
			`SELECT id, created_at FROM environments WHERE owner_id = ? AND ip = ? AND service_type = ?`,
			env.OwnerID, env.IP, env.ServiceType).Scan(&existingID, &createdAt)
		switch {
		case errors.Is(queryErr, sql.ErrNoRows):
			env.ID = NewID()
			env.CreatedAt = now
			_, err = tx.ExecContext(ctx, `
				INSERT INTO environments (
					id, owner_id, name, ip, ssh_user, ssh_port, ssh_password_enc, install_dir,
					service_type, installed, installed_at, installed_package_sha256,
					health_port, host_key_fingerprint, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				env.ID, env.OwnerID, env.Name, env.IP, env.SSHUser, env.SSHPort, env.SSHPasswordEnc,
				env.InstallDir, env.ServiceType, boolInt(env.Installed), nullableTime(env.InstalledAt),
				env.InstalledPackageSHA256, nullableInt(env.HealthPort), env.HostKeyFingerprint,
				formatTime(now), formatTime(now))
			created++
			environmentID = env.ID
		case queryErr != nil:
			err = queryErr
		default:
			_, err = tx.ExecContext(ctx, `
				UPDATE environments SET
					name = ?, ssh_user = ?, ssh_port = ?, ssh_password_enc = ?,
					install_dir = ?, installed = ?, installed_at = ?,
					installed_package_sha256 = ?, health_port = ?, host_key_fingerprint = ?,
					last_validation_at = NULL, updated_at = ?
				WHERE id = ?`,
				env.Name, env.SSHUser, env.SSHPort, env.SSHPasswordEnc, env.InstallDir,
				boolInt(env.Installed), nullableTime(env.InstalledAt), env.InstalledPackageSHA256,
				nullableInt(env.HealthPort), env.HostKeyFingerprint, formatTime(now), existingID)
			overwritten++
			environmentID = existingID
		}
		if err != nil {
			return 0, 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM environment_tags WHERE environment_id = ?`, environmentID); err != nil {
			return 0, 0, err
		}
		for _, tag := range env.Tags {
			var tagID string
			queryErr = tx.QueryRowContext(ctx, `SELECT id FROM resource_tags WHERE owner_id = ? AND group_name = ? COLLATE NOCASE AND value = ? COLLATE NOCASE AND deleted_at IS NULL`, env.OwnerID, tag.GroupName, tag.Value).Scan(&tagID)
			if errors.Is(queryErr, sql.ErrNoRows) {
				tagID = NewID()
				_, err = tx.ExecContext(ctx, `INSERT INTO resource_tags(id, owner_id, group_name, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, tagID, env.OwnerID, tag.GroupName, tag.Value, formatTime(now), formatTime(now))
			} else if queryErr != nil {
				err = queryErr
			}
			if err != nil {
				return 0, 0, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO environment_tags(environment_id, tag_id) VALUES (?, ?)`, environmentID, tagID); err != nil {
				return 0, 0, err
			}
		}
	}
	err = tx.Commit()
	return created, overwritten, err
}

func (s *Store) RecordValidation(ctx context.Context, id, fingerprint, arch string) error {
	now := formatTime(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `
		UPDATE environments
		SET host_key_fingerprint = ?, arch = ?, last_validation_at = ?, updated_at = ?
		WHERE id = ?`, fingerprint, arch, now, now, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) UpdateEnvironmentArch(ctx context.Context, id, arch string) error {
	now := formatTime(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `
		UPDATE environments SET arch = ?, updated_at = ? WHERE id = ?`, arch, now, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) MarkInstalled(ctx context.Context, id, sha string, healthPort int) error {
	now := formatTime(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `
		UPDATE environments SET installed = 1, installed_at = ?,
			installed_package_sha256 = ?, health_port = ?, updated_at = ?
		WHERE id = ?`, now, sha, healthPort, now, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) MarkUninstalled(ctx context.Context, id string) error {
	now := formatTime(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `
		UPDATE environments SET installed = 0, installed_at = NULL,
			installed_package_sha256 = '', health_port = NULL, updated_at = ?
		WHERE id = ?`, now, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) UpdateHealthPort(ctx context.Context, id string, healthPort int) error {
	now := formatTime(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `
		UPDATE environments SET health_port = ?, updated_at = ? WHERE id = ?`,
		healthPort, now, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetServiceConfig(ctx context.Context, environmentID string) (domain.ServiceConfig, error) {
	var config domain.ServiceConfig
	var updated string
	err := s.db.QueryRowContext(ctx, `
		SELECT environment_id, content, format, path, port, current_revision_id, updated_at
		FROM service_configs WHERE environment_id = ?`, environmentID).
		Scan(&config.EnvironmentID, &config.Content, &config.Format, &config.Path, &config.Port, &config.CurrentRevisionID, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ServiceConfig{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ServiceConfig{}, err
	}
	config.UpdatedAt, _ = parseTime(updated)
	return config, nil
}

func (s *Store) UpsertServiceConfig(ctx context.Context, config domain.ServiceConfig) (domain.ServiceConfig, error) {
	config.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO service_configs (environment_id, content, format, path, port, current_revision_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(environment_id) DO UPDATE SET
			content = excluded.content,
			format = excluded.format,
			path = excluded.path,
			port = excluded.port,
			current_revision_id = excluded.current_revision_id,
			updated_at = excluded.updated_at`,
		config.EnvironmentID, config.Content, config.Format, config.Path, config.Port, config.CurrentRevisionID,
		formatTime(config.UpdatedAt),
	)
	if err != nil {
		return domain.ServiceConfig{}, err
	}
	return s.GetServiceConfig(ctx, config.EnvironmentID)
}

func (s *Store) DeleteServiceConfig(ctx context.Context, environmentID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM service_configs WHERE environment_id = ?`, environmentID)
	return err
}

func (s *Store) ListServicePorts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT environments.id, COALESCE(service_configs.port, packages.config_port)
		FROM environments
		LEFT JOIN service_configs
			ON service_configs.environment_id = environments.id
		LEFT JOIN packages
			ON packages.owner_id = environments.owner_id
			AND packages.service_type = environments.service_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ports := make(map[string]int)
	for rows.Next() {
		var environmentID string
		var port sql.NullInt64
		if err := rows.Scan(&environmentID, &port); err != nil {
			return nil, err
		}
		if port.Valid {
			ports[environmentID] = int(port.Int64)
		}
	}
	return ports, rows.Err()
}

func (s *Store) GetPackage(ctx context.Context, serviceType string) (domain.Package, error) {
	return s.GetPackageByOwner(ctx, InitialAdminID, serviceType)
}

func (s *Store) GetPackageByOwner(ctx context.Context, ownerID, serviceType string) (domain.Package, error) {
	var pkg domain.Package
	var uploaded, updated string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.owner_id, COALESCE(u.username, ''), p.service_type, p.current_version_id,
			p.original_filename, p.storage_path, p.sha256, p.size_bytes, p.config_port, p.note,
			p.uploaded_at, p.updated_at,
			(SELECT COUNT(*) FROM package_versions v WHERE v.owner_id = p.owner_id AND v.service_type = p.service_type),
			(SELECT COUNT(*) FROM environments e WHERE e.owner_id = p.owner_id AND e.service_type = p.service_type AND e.installed_package_sha256 = p.sha256)
		FROM packages p LEFT JOIN users u ON u.id = p.owner_id
		WHERE p.owner_id = ? AND p.service_type = ?`, ownerID, serviceType).
		Scan(&pkg.OwnerID, &pkg.OwnerUsername, &pkg.ServiceType, &pkg.CurrentVersionID,
			&pkg.OriginalFilename, &pkg.StoragePath, &pkg.SHA256, &pkg.SizeBytes,
			&pkg.ConfigPort, &pkg.Note, &uploaded, &updated, &pkg.VersionCount, &pkg.ReferencedCount)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Package{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Package{}, err
	}
	pkg.UploadedAt, _ = parseTime(uploaded)
	pkg.UpdatedAt, _ = parseTime(updated)
	return pkg, nil
}

func (s *Store) ListPackages(ctx context.Context) ([]domain.Package, error) {
	return s.ListPackagesByOwner(ctx, "")
}

func (s *Store) ListPackagesByOwner(ctx context.Context, ownerID string) ([]domain.Package, error) {
	query := `SELECT p.owner_id, COALESCE(u.username, ''), p.service_type, p.current_version_id,
		p.original_filename, p.storage_path, p.sha256, p.size_bytes, p.config_port, p.note,
		p.uploaded_at, p.updated_at,
		(SELECT COUNT(*) FROM package_versions v WHERE v.owner_id = p.owner_id AND v.service_type = p.service_type),
		(SELECT COUNT(*) FROM environments e WHERE e.owner_id = p.owner_id AND e.service_type = p.service_type AND e.installed_package_sha256 = p.sha256)
		FROM packages p LEFT JOIN users u ON u.id = p.owner_id`
	var args []any
	if ownerID != "" {
		query += ` WHERE p.owner_id = ?`
		args = append(args, ownerID)
	}
	query += ` ORDER BY p.service_type`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	packages := make([]domain.Package, 0)
	for rows.Next() {
		var pkg domain.Package
		var uploaded, updated string
		if err := rows.Scan(
			&pkg.OwnerID, &pkg.OwnerUsername, &pkg.ServiceType, &pkg.CurrentVersionID,
			&pkg.OriginalFilename, &pkg.StoragePath, &pkg.SHA256, &pkg.SizeBytes,
			&pkg.ConfigPort, &pkg.Note, &uploaded, &updated, &pkg.VersionCount, &pkg.ReferencedCount,
		); err != nil {
			return nil, err
		}
		pkg.UploadedAt, _ = parseTime(uploaded)
		pkg.UpdatedAt, _ = parseTime(updated)
		packages = append(packages, pkg)
	}
	return packages, rows.Err()
}

func (s *Store) UpsertPackage(ctx context.Context, pkg domain.Package) error {
	if pkg.OwnerID == "" {
		pkg.OwnerID = InitialAdminID
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO packages (
			owner_id, service_type, original_filename, storage_path, sha256, size_bytes,
			config_port, note, uploaded_at, updated_at, current_version_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_id, service_type) DO UPDATE SET
			original_filename = excluded.original_filename,
			storage_path = excluded.storage_path,
			sha256 = excluded.sha256,
			size_bytes = excluded.size_bytes,
			config_port = excluded.config_port,
			note = excluded.note,
			uploaded_at = excluded.uploaded_at,
			updated_at = excluded.updated_at,
			current_version_id = excluded.current_version_id`,
		pkg.OwnerID, pkg.ServiceType, pkg.OriginalFilename, pkg.StoragePath, pkg.SHA256, pkg.SizeBytes,
		pkg.ConfigPort, pkg.Note, formatTime(pkg.UploadedAt), formatTime(pkg.UpdatedAt), pkg.CurrentVersionID)
	return err
}

// UpdatePackageNote 仅更新安装包备注，不影响包文件与时间戳。
func (s *Store) UpdatePackageNote(ctx context.Context, serviceType, note string) (domain.Package, error) {
	return s.UpdatePackageNoteByOwner(ctx, InitialAdminID, serviceType, note)
}

func (s *Store) UpdatePackageNoteByOwner(ctx context.Context, ownerID, serviceType, note string) (domain.Package, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE packages SET note = ? WHERE owner_id = ? AND service_type = ?`, note, ownerID, serviceType)
	if err != nil {
		return domain.Package{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.Package{}, domain.ErrNotFound
	}
	return s.GetPackageByOwner(ctx, ownerID, serviceType)
}

func (s *Store) DeletePackage(ctx context.Context, serviceType string) error {
	return s.DeletePackageByOwner(ctx, InitialAdminID, serviceType)
}

func (s *Store) DeletePackageByOwner(ctx context.Context, ownerID, serviceType string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM packages WHERE owner_id = ? AND service_type = ?`, ownerID, serviceType)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM package_versions WHERE owner_id = ? AND service_type = ?`, ownerID, serviceType); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CountInstalledEnvironments(ctx context.Context, serviceType string) (int, error) {
	return s.CountInstalledEnvironmentsByOwner(ctx, InitialAdminID, serviceType)
}

func (s *Store) CountInstalledEnvironmentsByOwner(ctx context.Context, ownerID, serviceType string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM environments WHERE owner_id = ? AND service_type = ? AND installed = 1`,
		ownerID, serviceType).Scan(&count)
	return count, err
}

// DeleteEnvironment 删除环境，service_configs 由外键级联删除。
// 操作记录和日志作为运维历史保留；operations 使用创建时的资源快照。
func (s *Store) DeleteEnvironment(ctx context.Context, id string) (logPaths []string, err error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM environments WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, domain.ErrNotFound
	}
	return nil, nil
}

func (s *Store) CreateOperation(ctx context.Context, op domain.Operation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if op.OwnerID != "" {
		var ownerID string
		if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM environments WHERE id = ?`, op.EnvironmentID).Scan(&ownerID); errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		} else if err != nil {
			return err
		}
		if ownerID != op.OwnerID {
			return &domain.AppError{Code: "TRANSFER_CONFLICT", Message: "资源归属已变化，请重新发起操作"}
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO operations (
			id, environment_id, request_id, actor_user_id, actor_username, owner_id, owner_username,
			environment_name, environment_ip, service_type, action, status, stage, log_path, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.ID, op.EnvironmentID, op.RequestID, op.ActorUserID, op.ActorUsername, op.OwnerID, op.OwnerUsername,
		op.EnvironmentName, op.EnvironmentIP, op.ServiceType, op.Action, op.Status, op.Stage,
		op.LogPath, formatTime(op.CreatedAt))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_tags(operation_id, tag_id, group_name, value)
		SELECT ?, t.id, t.group_name, t.value FROM environment_tags et JOIN resource_tags t ON t.id = et.tag_id
		WHERE et.environment_id = ?`, op.ID, op.EnvironmentID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LastSuccessfulAction(
	ctx context.Context,
	environmentID string,
) (domain.OperationAction, error) {
	var action string
	err := s.db.QueryRowContext(ctx, `
		SELECT action FROM operations
		WHERE environment_id = ? AND status = ?
		ORDER BY created_at DESC LIMIT 1`,
		environmentID, domain.OperationSucceeded,
	).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return domain.OperationAction(action), nil
}

func (s *Store) UpdateOperation(ctx context.Context, op domain.Operation) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE operations SET status = ?, stage = ?, exit_code = ?, error_code = ?,
			error_message = ?, started_at = ?, finished_at = ?
		WHERE id = ?`,
		op.Status, op.Stage, nullableInt(op.ExitCode), op.ErrorCode, op.ErrorMessage,
		nullableTime(op.StartedAt), nullableTime(op.FinishedAt), op.ID)
	return err
}

func (s *Store) GetOperation(ctx context.Context, id string) (domain.Operation, error) {
	op, err := scanOperation(s.db.QueryRowContext(ctx, operationSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, domain.ErrNotFound
	}
	if err != nil {
		return op, err
	}
	items := []domain.Operation{op}
	if err := s.PopulateOperationTags(ctx, items); err != nil {
		return domain.Operation{}, err
	}
	return items[0], nil
}

// LatestOperations 返回每个环境最近一次生命周期操作，按 created_at、rowid 取最新。
func (s *Store) LatestOperations(ctx context.Context) (map[string]domain.Operation, error) {
	rows, err := s.db.QueryContext(ctx, operationSelect+`
		WHERE o.id = (
			SELECT id FROM operations
			WHERE environment_id = o.environment_id
			ORDER BY created_at DESC, rowid DESC LIMIT 1
		)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	latest := make(map[string]domain.Operation)
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		latest[op.EnvironmentID] = op
	}
	return latest, rows.Err()
}

func (s *Store) InterruptActiveOperations(ctx context.Context) error {
	now := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
		UPDATE operations
		SET status = ?, stage = 'interrupted', error_code = 'SERVER_RESTARTED',
			error_message = '管理服务重启，操作状态无法继续跟踪', finished_at = ?
		WHERE status IN (?, ?)`,
		domain.OperationInterrupted, now, domain.OperationQueued, domain.OperationRunning)
	return err
}

const environmentSelect = `
	SELECT id, owner_id, name, ip, ssh_user, ssh_port, ssh_password_enc, install_dir,
		service_type, note, installed, installed_at, installed_package_sha256,
		health_port, arch, host_key_fingerprint, last_validation_at, created_at, updated_at
	FROM environments`

const operationSelect = `
	SELECT o.id, o.environment_id, o.request_id, o.actor_user_id, o.actor_username, o.owner_id,
		o.owner_username, o.environment_name, o.environment_ip, o.service_type,
		o.action, o.status, o.stage, o.exit_code, o.error_code, o.error_message,
		o.log_path, o.created_at, o.started_at, o.finished_at
	FROM operations AS o`

func scanOperation(row scanner) (domain.Operation, error) {
	var op domain.Operation
	var action, status, created string
	var exit sql.NullInt64
	var started, finished sql.NullString
	err := row.Scan(&op.ID, &op.EnvironmentID, &op.RequestID, &op.ActorUserID, &op.ActorUsername,
		&op.OwnerID, &op.OwnerUsername, &op.EnvironmentName, &op.EnvironmentIP,
		&op.ServiceType, &action, &status, &op.Stage, &exit, &op.ErrorCode,
		&op.ErrorMessage, &op.LogPath, &created, &started, &finished)
	if err != nil {
		return domain.Operation{}, err
	}
	op.Action, op.Status = domain.OperationAction(action), domain.OperationStatus(status)
	if exit.Valid {
		value := int(exit.Int64)
		op.ExitCode = &value
	}
	op.CreatedAt, _ = parseTime(created)
	op.StartedAt, op.FinishedAt = parseNullTime(started), parseNullTime(finished)
	return op, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEnvironment(row scanner) (domain.Environment, error) {
	var env domain.Environment
	var installed int
	var installedAt, validationAt sql.NullString
	var healthPort sql.NullInt64
	var created, updated string
	err := row.Scan(
		&env.ID, &env.OwnerID, &env.Name, &env.IP, &env.SSHUser, &env.SSHPort, &env.SSHPasswordEnc,
		&env.InstallDir, &env.ServiceType, &env.Note, &installed, &installedAt,
		&env.InstalledPackageSHA256, &healthPort, &env.Arch, &env.HostKeyFingerprint,
		&validationAt, &created, &updated,
	)
	if err != nil {
		return domain.Environment{}, err
	}
	env.Installed = installed != 0
	env.InstalledAt = parseNullTime(installedAt)
	env.LastValidationAt = parseNullTime(validationAt)
	if healthPort.Valid {
		value := int(healthPort.Int64)
		env.HealthPort = &value
	}
	env.CreatedAt, _ = parseTime(created)
	env.UpdatedAt, _ = parseTime(updated)
	return env, nil
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }

func parseNullTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isUniqueError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
