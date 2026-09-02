package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"DP/internal/domain"
	"github.com/jackc/pgx/v5"
)

type rowScanner interface{ Scan(...any) error }

const hostSelect = `SELECT h.id::text, h.owner_id::text, COALESCE(u.username, ''), h.name,
	host(h.ip), h.ssh_user, h.ssh_port, h.ssh_password_enc, h.note, h.arch,
	h.host_key_fingerprint, h.last_validation_at, h.created_at, h.updated_at
	FROM hosts h LEFT JOIN users u ON u.id = h.owner_id`

func (db *DB) ListHosts(ctx context.Context, ownerID string) ([]domain.Host, error) {
	query, args := hostSelect+` ORDER BY h.name, h.ip, h.ssh_port`, []any(nil)
	if ownerID != "" {
		query, args = hostSelect+` WHERE h.owner_id = $1 ORDER BY h.name, h.ip, h.ssh_port`, []any{ownerID}
	}
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query hosts: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Host, 0)
	for rows.Next() {
		item, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (db *DB) GetHost(ctx context.Context, id string) (domain.Host, error) {
	return scanHost(db.pool.QueryRow(ctx, hostSelect+` WHERE h.id = $1`, id))
}

func scanHost(row rowScanner) (domain.Host, error) {
	var item domain.Host
	err := row.Scan(&item.ID, &item.OwnerID, &item.OwnerUsername, &item.Name, &item.IP, &item.SSHUser,
		&item.SSHPort, &item.SSHPasswordEnc, &item.Note, &item.Arch, &item.HostKeyFingerprint,
		&item.LastValidationAt, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Host{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Host{}, fmt.Errorf("scan host: %w", err)
	}
	return item, nil
}

func (db *DB) CreateHost(ctx context.Context, host domain.Host) (domain.Host, error) {
	if host.OwnerID == "" {
		host.OwnerID = domain.InitialAdminID
	}
	host.ID = domain.NewID()
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx, `INSERT INTO hosts (id, owner_id, name, ip, ssh_user, ssh_port,
		ssh_password_enc, note, arch, host_key_fingerprint, last_validation_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4::inet,$5,$6,$7,$8,$9,$10,$11,$12,$12)`, host.ID, host.OwnerID,
		host.Name, host.IP, host.SSHUser, host.SSHPort, host.SSHPasswordEnc, host.Note, host.Arch,
		host.HostKeyFingerprint, host.LastValidationAt, now)
	if isPostgresError(err, "23505") {
		return domain.Host{}, domain.ErrConflict
	}
	if err != nil {
		return domain.Host{}, fmt.Errorf("insert host: %w", err)
	}
	return db.GetHost(ctx, host.ID)
}

func (db *DB) UpdateHost(ctx context.Context, host domain.Host) (domain.Host, error) {
	command, err := db.pool.Exec(ctx, `UPDATE hosts SET name=$1, ip=$2::inet, ssh_user=$3,
		ssh_port=$4, ssh_password_enc=$5, note=$6, arch=$7, host_key_fingerprint=$8,
		last_validation_at=$9, updated_at=$10 WHERE id=$11`, host.Name, host.IP, host.SSHUser,
		host.SSHPort, host.SSHPasswordEnc, host.Note, host.Arch, host.HostKeyFingerprint,
		host.LastValidationAt, time.Now().UTC(), host.ID)
	if isPostgresError(err, "23505") {
		return domain.Host{}, domain.ErrConflict
	}
	if err := requireAffected("update host", command, err); err != nil {
		return domain.Host{}, err
	}
	return db.GetHost(ctx, host.ID)
}

func (db *DB) DeleteHost(ctx context.Context, id string) error {
	command, err := db.pool.Exec(ctx, `DELETE FROM hosts WHERE id=$1`, id)
	if isPostgresError(err, "23503") {
		return domain.ErrConflict
	}
	return requireAffected("delete host", command, err)
}

func (db *DB) RecordHostValidation(ctx context.Context, id, fingerprint, arch string) error {
	now := time.Now().UTC()
	command, err := db.pool.Exec(ctx, `UPDATE hosts SET host_key_fingerprint=$1, arch=$2,
		last_validation_at=$3, updated_at=$3 WHERE id=$4`, fingerprint, arch, now, id)
	return requireAffected("record host validation", command, err)
}

func (db *DB) UpdateHostArch(ctx context.Context, id, arch string) error {
	command, err := db.pool.Exec(ctx, `UPDATE hosts SET arch=$1,updated_at=$2 WHERE id=$3`, arch, time.Now().UTC(), id)
	return requireAffected("update host architecture", command, err)
}

func (db *DB) UpsertHosts(ctx context.Context, hosts []domain.Host) (created, updated int, err error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	for _, host := range hosts {
		var id string
		err = tx.QueryRow(ctx, `SELECT id::text FROM hosts WHERE owner_id=$1 AND ip=$2::inet AND ssh_port=$3 FOR UPDATE`, host.OwnerID, host.IP, host.SSHPort).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			id = domain.NewID()
			_, err = tx.Exec(ctx, `INSERT INTO hosts(id,owner_id,name,ip,ssh_user,ssh_port,ssh_password_enc,note,created_at,updated_at) VALUES($1,$2,$3,$4::inet,$5,$6,$7,$8,$9,$9)`, id, host.OwnerID, host.Name, host.IP, host.SSHUser, host.SSHPort, host.SSHPasswordEnc, host.Note, now)
			created++
		} else if err == nil {
			_, err = tx.Exec(ctx, `UPDATE hosts SET name=$1,ssh_user=$2,ssh_password_enc=$3,note=$4,arch='',host_key_fingerprint='',last_validation_at=NULL,updated_at=$5 WHERE id=$6`, host.Name, host.SSHUser, host.SSHPasswordEnc, host.Note, now, id)
			updated++
		}
		if err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return created, updated, nil
}

func (db *DB) HostHasServiceInstances(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := db.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_instances WHERE host_id=$1)`, id).Scan(&exists)
	return exists, err
}
