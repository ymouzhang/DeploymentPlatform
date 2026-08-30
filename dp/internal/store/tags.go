package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"DP/internal/domain"
)

const tagSelect = `
	SELECT t.id, t.owner_id, COALESCE(u.username, ''), t.group_name, t.value,
		(SELECT COUNT(*) FROM environment_tags et WHERE et.tag_id = t.id),
		t.created_at, t.updated_at
	FROM resource_tags t JOIN users u ON u.id = t.owner_id`

func (s *Store) ListResourceTags(ctx context.Context, ownerID string) ([]domain.ResourceTag, error) {
	query, args := tagSelect+` WHERE t.deleted_at IS NULL`, []any{}
	if ownerID != "" {
		query += ` AND t.owner_id = ?`
		args = append(args, ownerID)
	}
	query += ` ORDER BY u.username COLLATE NOCASE, t.group_name COLLATE NOCASE, t.value COLLATE NOCASE`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ResourceTag{}
	for rows.Next() {
		item, err := scanResourceTag(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetResourceTag(ctx context.Context, id string) (domain.ResourceTag, error) {
	item, err := scanResourceTag(s.db.QueryRowContext(ctx, tagSelect+` WHERE t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ResourceTag{}, domain.ErrNotFound
	}
	return item, err
}

func scanResourceTag(row scanner) (domain.ResourceTag, error) {
	var item domain.ResourceTag
	var created, updated string
	err := row.Scan(&item.ID, &item.OwnerID, &item.OwnerUsername, &item.GroupName, &item.Value, &item.EnvironmentCount, &created, &updated)
	if err != nil {
		return item, err
	}
	item.CreatedAt, _ = parseTime(created)
	item.UpdatedAt, _ = parseTime(updated)
	return item, nil
}

func (s *Store) CreateResourceTag(ctx context.Context, ownerID string, input domain.ResourceTagInput) (domain.ResourceTag, error) {
	now, id := time.Now().UTC(), NewID()
	_, err := s.db.ExecContext(ctx, `INSERT INTO resource_tags(id, owner_id, group_name, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, id, ownerID, input.GroupName, input.Value, formatTime(now), formatTime(now))
	if isUniqueError(err) {
		return domain.ResourceTag{}, domain.ErrConflict
	}
	if err != nil {
		return domain.ResourceTag{}, err
	}
	return s.GetResourceTag(ctx, id)
}

func (s *Store) UpdateResourceTag(ctx context.Context, id string, input domain.ResourceTagInput) (domain.ResourceTag, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE resource_tags SET group_name = ?, value = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, input.GroupName, input.Value, formatTime(time.Now().UTC()), id)
	if isUniqueError(err) {
		return domain.ResourceTag{}, domain.ErrConflict
	}
	if err != nil {
		return domain.ResourceTag{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ResourceTag{}, domain.ErrNotFound
	}
	return s.GetResourceTag(ctx, id)
}

func (s *Store) DeleteResourceTag(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM environment_tags WHERE tag_id = ?`, id); err != nil {
		return err
	}
	now := formatTime(time.Now().UTC())
	result, err := tx.ExecContext(ctx, `UPDATE resource_tags SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, now, now, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) ValidateTagIDs(ctx context.Context, ownerID string, tagIDs []string) error {
	if len(tagIDs) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for _, id := range tagIDs {
		if _, ok := seen[id]; ok {
			return domain.FieldError("tag_ids", "标签 ID 不能重复")
		}
		seen[id] = struct{}{}
		var actualOwner string
		if err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM resource_tags WHERE id = ? AND deleted_at IS NULL`, id).Scan(&actualOwner); errors.Is(err, sql.ErrNoRows) {
			return domain.FieldError("tag_ids", "标签不存在或不可用")
		} else if err != nil {
			return err
		}
		if ownerID != "" && actualOwner != ownerID {
			return domain.FieldError("tag_ids", "环境只能关联所属账号的标签")
		}
	}
	return nil
}

func (s *Store) ReplaceEnvironmentTags(ctx context.Context, environmentID string, tagIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ownerID string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM environments WHERE id = ?`, environmentID).Scan(&ownerID); errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	} else if err != nil {
		return err
	}
	if err := replaceEnvironmentTagsTx(ctx, tx, environmentID, ownerID, tagIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceEnvironmentTagsTx(ctx context.Context, tx *sql.Tx, environmentID, ownerID string, tagIDs []string) error {
	if len(tagIDs) > 20 {
		return domain.FieldError("tag_ids", "每个环境最多关联 20 个标签")
	}
	seen := map[string]struct{}{}
	for _, id := range tagIDs {
		if _, exists := seen[id]; exists {
			return domain.FieldError("tag_ids", "标签 ID 不能重复")
		}
		seen[id] = struct{}{}
		var tagOwner string
		if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM resource_tags WHERE id = ? AND deleted_at IS NULL`, id).Scan(&tagOwner); errors.Is(err, sql.ErrNoRows) {
			return domain.FieldError("tag_ids", "标签不存在或不可用")
		} else if err != nil {
			return err
		}
		if tagOwner != ownerID {
			return domain.FieldError("tag_ids", "环境只能关联所属账号的标签")
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM environment_tags WHERE environment_id = ?`, environmentID); err != nil {
		return err
	}
	for _, id := range tagIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO environment_tags(environment_id, tag_id) VALUES (?, ?)`, environmentID, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) PopulateEnvironmentTags(ctx context.Context, environments []domain.Environment) error {
	if len(environments) == 0 {
		return nil
	}
	index := make(map[string]int, len(environments))
	args := make([]any, len(environments))
	marks := make([]string, len(environments))
	for i := range environments {
		index[environments[i].ID], args[i], marks[i] = i, environments[i].ID, "?"
		environments[i].Tags = []domain.ResourceTagRef{}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT et.environment_id, t.id, t.group_name, t.value FROM environment_tags et JOIN resource_tags t ON t.id = et.tag_id WHERE et.environment_id IN (`+strings.Join(marks, ",")+`) ORDER BY t.group_name COLLATE NOCASE, t.value COLLATE NOCASE`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var envID string
		var tag domain.ResourceTagRef
		if err := rows.Scan(&envID, &tag.ID, &tag.GroupName, &tag.Value); err != nil {
			return err
		}
		if i, ok := index[envID]; ok {
			environments[i].Tags = append(environments[i].Tags, tag)
		}
	}
	return rows.Err()
}

func (s *Store) PopulateOperationTags(ctx context.Context, operations []domain.Operation) error {
	if len(operations) == 0 {
		return nil
	}
	index := make(map[string]int, len(operations))
	args := make([]any, len(operations))
	marks := make([]string, len(operations))
	for i := range operations {
		index[operations[i].ID], args[i], marks[i] = i, operations[i].ID, "?"
		operations[i].Tags = []domain.ResourceTagRef{}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT operation_id, tag_id, group_name, value FROM operation_tags WHERE operation_id IN (`+strings.Join(marks, ",")+`) ORDER BY group_name COLLATE NOCASE, value COLLATE NOCASE`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var operationID string
		var tag domain.ResourceTagRef
		if err := rows.Scan(&operationID, &tag.ID, &tag.GroupName, &tag.Value); err != nil {
			return err
		}
		if i, ok := index[operationID]; ok {
			operations[i].Tags = append(operations[i].Tags, tag)
		}
	}
	return rows.Err()
}

func matchesAllTags(tags []domain.ResourceTagRef, ids map[string]struct{}) bool {
	if len(ids) == 0 {
		return true
	}
	found := 0
	for _, tag := range tags {
		if _, ok := ids[tag.ID]; ok {
			found++
		}
	}
	return found == len(ids)
}

func FilterEnvironmentsByTagIDs(environments []domain.Environment, tagIDs []string) []domain.Environment {
	if len(tagIDs) == 0 {
		return environments
	}
	ids := make(map[string]struct{}, len(tagIDs))
	for _, id := range tagIDs {
		ids[id] = struct{}{}
	}
	result := make([]domain.Environment, 0, len(environments))
	for _, env := range environments {
		if matchesAllTags(env.Tags, ids) {
			result = append(result, env)
		}
	}
	return result
}
