package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"DP/internal/domain"
	"github.com/jackc/pgx/v5"
)

const tagSelect = `SELECT
	t.id::text,
	t.owner_id::text,
	COALESCE(u.username, ''),
	t.group_name,
	t.value,
	(SELECT count(*) FROM service_instance_tags et WHERE et.tag_id = t.id),
	t.created_at,
	t.updated_at
	FROM resource_tags t
	JOIN users u ON u.id = t.owner_id`

func (db *DB) ListResourceTags(ctx context.Context, ownerID string) ([]domain.ResourceTag, error) {
	query := tagSelect + ` WHERE t.deleted_at IS NULL`
	args := make([]any, 0, 1)
	if ownerID != "" {
		query += ` AND t.owner_id = $1`
		args = append(args, ownerID)
	}
	query += ` ORDER BY lower(u.username), lower(t.group_name), lower(t.value), t.id`
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query resource tags: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ResourceTag, 0)
	for rows.Next() {
		item, err := scanPostgresResourceTag(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource tags: %w", err)
	}
	return items, nil
}

func (db *DB) GetResourceTag(ctx context.Context, id string) (domain.ResourceTag, error) {
	return scanPostgresResourceTag(db.pool.QueryRow(ctx, tagSelect+` WHERE t.id = $1`, id))
}

func (db *DB) CreateResourceTag(
	ctx context.Context,
	ownerID string,
	input domain.ResourceTagInput,
) (domain.ResourceTag, error) {
	id := domain.NewID()
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx, `INSERT INTO resource_tags
		(id, owner_id, group_name, value, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`, id, ownerID, input.GroupName, input.Value, now)
	if isPostgresError(err, "23505") {
		return domain.ResourceTag{}, domain.ErrConflict
	}
	if err != nil {
		return domain.ResourceTag{}, fmt.Errorf("insert resource tag: %w", err)
	}
	return db.GetResourceTag(ctx, id)
}

func (db *DB) UpdateResourceTag(
	ctx context.Context,
	id string,
	input domain.ResourceTagInput,
) (domain.ResourceTag, error) {
	command, err := db.pool.Exec(ctx, `UPDATE resource_tags SET
		group_name = $1, value = $2, updated_at = $3
		WHERE id = $4 AND deleted_at IS NULL`, input.GroupName, input.Value, time.Now().UTC(), id)
	if isPostgresError(err, "23505") {
		return domain.ResourceTag{}, domain.ErrConflict
	}
	if err := requireAffected("update resource tag", command, err); err != nil {
		return domain.ResourceTag{}, err
	}
	return db.GetResourceTag(ctx, id)
}

func (db *DB) DeleteResourceTag(ctx context.Context, id string) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delete resource tag: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM service_instance_tags WHERE tag_id = $1`, id); err != nil {
		return fmt.Errorf("detach resource tag: %w", err)
	}
	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `UPDATE resource_tags SET deleted_at = $1, updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL`, now, id)
	if err := requireAffected("delete resource tag", command, err); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete resource tag: %w", err)
	}
	return nil
}

func (db *DB) ValidateTagIDs(ctx context.Context, ownerID string, tagIDs []string) error {
	if len(tagIDs) == 0 {
		return nil
	}
	if hasDuplicates(tagIDs) {
		return domain.FieldError("tag_ids", "标签 ID 不能重复")
	}
	rows, err := db.pool.Query(ctx, `SELECT id::text, owner_id::text FROM resource_tags
		WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL`, tagIDs)
	if err != nil {
		return fmt.Errorf("query resource tags for validation: %w", err)
	}
	defer rows.Close()
	found := 0
	for rows.Next() {
		var id, actualOwner string
		if err := rows.Scan(&id, &actualOwner); err != nil {
			return fmt.Errorf("scan resource tag owner: %w", err)
		}
		found++
		if ownerID != "" && actualOwner != ownerID {
			return domain.FieldError("tag_ids", "服务实例只能关联所属账号的标签")
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate resource tag owners: %w", err)
	}
	if found != len(tagIDs) {
		return domain.FieldError("tag_ids", "标签不存在或不可用")
	}
	return nil
}

func (db *DB) ReplaceServiceInstanceTags(ctx context.Context, service_instanceID string, tagIDs []string) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin replace service_instance tags: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ownerID string
	err = tx.QueryRow(ctx, `SELECT owner_id::text FROM service_instances WHERE id = $1 FOR UPDATE`, service_instanceID).
		Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock service_instance for tags: %w", err)
	}
	if err := replaceServiceInstanceTags(ctx, tx, service_instanceID, ownerID, tagIDs); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit service_instance tags: %w", err)
	}
	return nil
}

func (db *DB) PopulateOperationTags(ctx context.Context, operations []domain.Operation) error {
	if len(operations) == 0 {
		return nil
	}
	ids := make([]string, 0, len(operations))
	byID := make(map[string]int, len(operations))
	for index := range operations {
		ids = append(ids, operations[index].ID)
		byID[operations[index].ID] = index
		operations[index].Tags = nil
	}
	rows, err := db.pool.Query(ctx, `SELECT operation_id::text, tag_id::text, group_name, value
		FROM operation_tags WHERE operation_id = ANY($1::uuid[])
		ORDER BY lower(group_name), lower(value), tag_id`, ids)
	if err != nil {
		return fmt.Errorf("query operation tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var operationID string
		var tag domain.ResourceTagRef
		if err := rows.Scan(&operationID, &tag.ID, &tag.GroupName, &tag.Value); err != nil {
			return fmt.Errorf("scan operation tag: %w", err)
		}
		if index, ok := byID[operationID]; ok {
			operations[index].Tags = append(operations[index].Tags, tag)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate operation tags: %w", err)
	}
	return nil
}

func scanPostgresResourceTag(row interface{ Scan(...any) error }) (domain.ResourceTag, error) {
	var item domain.ResourceTag
	err := row.Scan(&item.ID, &item.OwnerID, &item.OwnerUsername, &item.GroupName, &item.Value,
		&item.ServiceInstanceCount, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ResourceTag{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ResourceTag{}, fmt.Errorf("scan resource tag: %w", err)
	}
	return item, nil
}
