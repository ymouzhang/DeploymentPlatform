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

const service_instanceSelect = `SELECT s.id::text, s.owner_id::text, COALESCE(u.username,''),
	s.host_id::text, s.name, s.install_dir, s.service_type, s.note, s.installed, s.installed_at,
	COALESCE(s.installed_package_sha256,''), s.health_port, s.created_at, s.updated_at,
	h.id::text, h.owner_id::text, COALESCE(u.username,''), h.name, host(h.ip), h.ssh_user,
	h.ssh_port, h.ssh_password_enc, h.note, h.arch, h.host_key_fingerprint,
	h.last_validation_at, h.created_at, h.updated_at
	FROM service_instances s JOIN hosts h ON h.id=s.host_id
	LEFT JOIN users u ON u.id=s.owner_id`

func (db *DB) ListServiceInstances(ctx context.Context) ([]domain.ServiceInstance, error) {
	return db.listServiceInstances(ctx, "", nil)
}

func (db *DB) ListServiceInstancesByOwner(ctx context.Context, ownerID string) ([]domain.ServiceInstance, error) {
	return db.listServiceInstances(ctx, ` WHERE s.owner_id=$1`, []any{ownerID})
}

func (db *DB) listServiceInstances(ctx context.Context, where string, args []any) ([]domain.ServiceInstance, error) {
	rows, err := db.pool.Query(ctx, service_instanceSelect+where+` ORDER BY s.service_type,s.name,h.ip`, args...)
	if err != nil {
		return nil, fmt.Errorf("query service instances: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ServiceInstance, 0)
	for rows.Next() {
		item, err := scanServiceInstance(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := db.PopulateServiceInstanceTags(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (db *DB) GetServiceInstance(ctx context.Context, id string) (domain.ServiceInstance, error) {
	item, err := scanServiceInstance(db.pool.QueryRow(ctx, service_instanceSelect+` WHERE s.id=$1`, id))
	if err != nil {
		return domain.ServiceInstance{}, err
	}
	items := []domain.ServiceInstance{item}
	if err := db.PopulateServiceInstanceTags(ctx, items); err != nil {
		return domain.ServiceInstance{}, err
	}
	return items[0], nil
}

func scanServiceInstance(row rowScanner) (domain.ServiceInstance, error) {
	var item domain.ServiceInstance
	var installedAt, validationAt sql.NullTime
	var healthPort sql.NullInt64
	err := row.Scan(&item.ID, &item.OwnerID, &item.OwnerUsername, &item.HostID, &item.Name,
		&item.InstallDir, &item.ServiceType, &item.Note, &item.Installed, &installedAt,
		&item.InstalledPackageSHA256, &healthPort, &item.CreatedAt, &item.UpdatedAt,
		&item.Host.ID, &item.Host.OwnerID, &item.Host.OwnerUsername, &item.Host.Name, &item.Host.IP,
		&item.Host.SSHUser, &item.Host.SSHPort, &item.Host.SSHPasswordEnc, &item.Host.Note,
		&item.Host.Arch, &item.Host.HostKeyFingerprint, &validationAt, &item.Host.CreatedAt, &item.Host.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ServiceInstance{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ServiceInstance{}, fmt.Errorf("scan service instance: %w", err)
	}
	if installedAt.Valid {
		item.InstalledAt = &installedAt.Time
	}
	if validationAt.Valid {
		item.Host.LastValidationAt = &validationAt.Time
	}
	if healthPort.Valid {
		value := int(healthPort.Int64)
		item.HealthPort = &value
	}
	return item, nil
}

func (db *DB) CreateServiceInstance(ctx context.Context, item domain.ServiceInstance) (domain.ServiceInstance, error) {
	return db.CreateServiceInstanceWithTags(ctx, item, nil)
}

func (db *DB) CreateServiceInstanceWithTags(ctx context.Context, item domain.ServiceInstance, tagIDs []string) (domain.ServiceInstance, error) {
	if item.OwnerID == "" {
		item.OwnerID = domain.InitialAdminID
	}
	item.ID = domain.NewID()
	now := time.Now().UTC()
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.ServiceInstance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if item.HostID == "" {
		return domain.ServiceInstance{}, domain.FieldError("host_id", "服务实例必须关联已注册主机")
	}
	_, err = tx.Exec(ctx, `INSERT INTO service_instances (id,owner_id,host_id,name,install_dir,
		service_type,note,installed,installed_at,installed_package_sha256,health_port,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$12)`, item.ID, item.OwnerID,
		item.HostID, item.Name, item.InstallDir, item.ServiceType, item.Note, item.Installed, item.InstalledAt,
		item.InstalledPackageSHA256, item.HealthPort, now)
	if isPostgresError(err, "23505") {
		return domain.ServiceInstance{}, domain.ErrConflict
	}
	if err != nil {
		return domain.ServiceInstance{}, fmt.Errorf("insert service instance: %w", err)
	}
	if err := replaceServiceInstanceTags(ctx, tx, item.ID, item.OwnerID, tagIDs); err != nil {
		return domain.ServiceInstance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ServiceInstance{}, err
	}
	return db.GetServiceInstance(ctx, item.ID)
}

func (db *DB) UpdateServiceInstance(ctx context.Context, item domain.ServiceInstance) (domain.ServiceInstance, error) {
	return db.UpdateServiceInstanceWithTags(ctx, item, nil)
}

func (db *DB) UpdateServiceInstanceWithTags(ctx context.Context, item domain.ServiceInstance, tagIDs []string) (domain.ServiceInstance, error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.ServiceInstance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE service_instances SET host_id=$1,name=$2,install_dir=$3,service_type=$4,
		note=$5,installed=$6,installed_at=$7,installed_package_sha256=NULLIF($8,''),health_port=$9,
		updated_at=$10 WHERE id=$11`, item.HostID, item.Name, item.InstallDir, item.ServiceType, item.Note, item.Installed,
		item.InstalledAt, item.InstalledPackageSHA256, item.HealthPort, time.Now().UTC(), item.ID)
	if isPostgresError(err, "23505") {
		return domain.ServiceInstance{}, domain.ErrConflict
	}
	if err := requireAffected("update service instance", command, err); err != nil {
		return domain.ServiceInstance{}, err
	}
	if tagIDs != nil {
		if err := replaceServiceInstanceTags(ctx, tx, item.ID, item.OwnerID, tagIDs); err != nil {
			return domain.ServiceInstance{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ServiceInstance{}, err
	}
	return db.GetServiceInstance(ctx, item.ID)
}

func (db *DB) MarkInstalled(ctx context.Context, id, sha string, healthPort int) error {
	now := time.Now().UTC()
	command, err := db.pool.Exec(ctx, `UPDATE service_instances SET installed=TRUE,installed_at=$1,installed_package_sha256=$2,health_port=$3,updated_at=$1 WHERE id=$4`, now, sha, healthPort, id)
	return requireAffected("mark service instance installed", command, err)
}
func (db *DB) MarkUninstalled(ctx context.Context, id string) error {
	command, err := db.pool.Exec(ctx, `UPDATE service_instances SET installed=FALSE,installed_at=NULL,installed_package_sha256=NULL,health_port=NULL,updated_at=$1 WHERE id=$2`, time.Now().UTC(), id)
	return requireAffected("mark service instance uninstalled", command, err)
}
func (db *DB) UpdateHealthPort(ctx context.Context, id string, port int) error {
	command, err := db.pool.Exec(ctx, `UPDATE service_instances SET health_port=$1,updated_at=$2 WHERE id=$3`, port, time.Now().UTC(), id)
	return requireAffected("update service instance health port", command, err)
}
func (db *DB) DeleteServiceInstance(ctx context.Context, id string) ([]string, error) {
	command, err := db.pool.Exec(ctx, `DELETE FROM service_instances WHERE id=$1`, id)
	if err != nil {
		return nil, fmt.Errorf("delete service instance: %w", err)
	}
	if command.RowsAffected() == 0 {
		return nil, domain.ErrNotFound
	}
	return nil, nil
}

func (db *DB) PopulateServiceInstanceTags(ctx context.Context, items []domain.ServiceInstance) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	byID := make(map[string]int, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
		byID[items[i].ID] = i
		items[i].Tags = nil
	}
	rows, err := db.pool.Query(ctx, `SELECT st.service_instance_id::text,t.id::text,t.group_name,t.value FROM service_instance_tags st JOIN resource_tags t ON t.id=st.tag_id WHERE st.service_instance_id=ANY($1::uuid[]) AND t.deleted_at IS NULL ORDER BY lower(t.group_name),lower(t.value),t.id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var tag domain.ResourceTagRef
		if err := rows.Scan(&id, &tag.ID, &tag.GroupName, &tag.Value); err != nil {
			return err
		}
		if i, ok := byID[id]; ok {
			items[i].Tags = append(items[i].Tags, tag)
		}
	}
	return rows.Err()
}

func replaceServiceInstanceTags(ctx context.Context, tx pgx.Tx, id, ownerID string, tagIDs []string) error {
	if len(tagIDs) > 20 {
		return domain.FieldError("tag_ids", "每个服务实例最多关联 20 个标签")
	}
	if hasDuplicates(tagIDs) {
		return domain.FieldError("tag_ids", "标签 ID 不能重复")
	}
	if len(tagIDs) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM resource_tags WHERE owner_id=$1 AND id=ANY($2::uuid[]) AND deleted_at IS NULL`, ownerID, tagIDs).Scan(&count); err != nil {
			return err
		}
		if count != len(tagIDs) {
			return domain.FieldError("tag_ids", "包含不存在或不属于当前账号的标签")
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM service_instance_tags WHERE service_instance_id=$1`, id); err != nil {
		return err
	}
	for _, tagID := range tagIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO service_instance_tags(service_instance_id,tag_id) VALUES($1,$2)`, id, tagID); err != nil {
			return err
		}
	}
	return nil
}
