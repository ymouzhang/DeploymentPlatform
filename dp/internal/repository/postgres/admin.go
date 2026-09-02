package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"DP/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (db *DB) DeleteLoginThrottlesBefore(ctx context.Context, before time.Time) error {
	if _, err := db.pool.Exec(ctx, `DELETE FROM login_throttles WHERE updated_at < $1`, before); err != nil {
		return fmt.Errorf("delete expired login throttles: %w", err)
	}
	return nil
}

func (db *DB) ListStaleUsers(ctx context.Context, before time.Time) ([]domain.User, error) {
	rows, err := db.pool.Query(ctx, userSelect+` WHERE users.enabled = TRUE
		AND users.is_initial_admin = FALSE
		AND users.created_at < $1
		AND NOT EXISTS (
			SELECT 1 FROM audit_events
			WHERE actor_user_id = users.id
				AND action = 'auth.login'
				AND outcome = 'success'
				AND occurred_at >= $1
		)
		ORDER BY users.username`, before)
	if err != nil {
		return nil, fmt.Errorf("query stale users: %w", err)
	}
	defer rows.Close()

	users := make([]domain.User, 0)
	for rows.Next() {
		user, scanErr := scanPostgresUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stale users: %w", err)
	}
	for index := range users {
		users[index], err = db.populateUserAccess(ctx, users[index])
		if err != nil {
			return nil, err
		}
	}
	return users, nil
}

func (db *DB) DashboardMetrics(
	ctx context.Context,
	since time.Time,
	staleBefore time.Time,
) (domain.DashboardMetrics, error) {
	var metrics domain.DashboardMetrics
	err := db.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM users WHERE enabled = TRUE),
		(SELECT count(*) FROM users WHERE enabled = FALSE),
		(SELECT count(*) FROM packages),
		(SELECT count(*) FROM hosts),
		(SELECT count(*) FROM service_instances),
		(SELECT count(*) FROM service_instances WHERE installed = TRUE),
		(SELECT count(*) FROM operations WHERE status IN ($1, $2)),
		(SELECT count(*) FROM operations WHERE status IN ($3, $4, $5) AND created_at >= $6),
		(SELECT count(*) FROM audit_events WHERE action = 'auth.login' AND outcome <> 'success' AND occurred_at >= $6),
		(SELECT count(*) FROM hosts WHERE last_validation_at IS NULL),
		(SELECT count(*) FROM hosts WHERE last_validation_at IS NOT NULL AND last_validation_at < $7),
		(SELECT count(*) FROM audit_events WHERE risk_level = 'high' AND occurred_at >= $6),
		(SELECT count(*) FROM notifications WHERE read_at IS NULL)`,
		domain.OperationQueued,
		domain.OperationRunning,
		domain.OperationFailed,
		domain.OperationTimedOut,
		domain.OperationInterrupted,
		since,
		staleBefore,
	).Scan(
		&metrics.Users,
		&metrics.EnabledUsers,
		&metrics.DisabledUsers,
		&metrics.Packages,
		&metrics.Hosts,
		&metrics.ServiceInstances,
		&metrics.InstalledServices,
		&metrics.ActiveOperations,
		&metrics.FailedOperations24h,
		&metrics.LoginFailures24h,
		&metrics.UnvalidatedHosts,
		&metrics.StaleValidationHosts,
		&metrics.HighRiskAudits24h,
		&metrics.UnreadNotifications,
	)
	if err != nil {
		return domain.DashboardMetrics{}, fmt.Errorf("query dashboard metrics: %w", err)
	}
	return metrics, nil
}

func (db *DB) CountOperationsByTags(
	ctx context.Context,
	tagIDs []string,
	since time.Time,
) (active int, failed int, err error) {
	conditions := make([]string, 0, len(tagIDs))
	args := make([]any, 0, len(tagIDs)+6)
	for _, tagID := range tagIDs {
		args = append(args, tagID)
		conditions = append(conditions, fmt.Sprintf(
			`EXISTS (SELECT 1 FROM operation_tags ot WHERE ot.operation_id = o.id AND ot.tag_id = $%d)`,
			len(args),
		))
	}
	base := "TRUE"
	if len(conditions) > 0 {
		base = strings.Join(conditions, " AND ")
	}

	args = append(args,
		domain.OperationQueued,
		domain.OperationRunning,
		domain.OperationFailed,
		domain.OperationTimedOut,
		domain.OperationInterrupted,
		since,
	)
	statusStart := len(tagIDs) + 1
	query := fmt.Sprintf(`SELECT
		count(*) FILTER (WHERE o.status IN ($%d, $%d)),
		count(*) FILTER (WHERE o.status IN ($%d, $%d, $%d) AND o.created_at >= $%d)
		FROM operations o WHERE %s`,
		statusStart,
		statusStart+1,
		statusStart+2,
		statusStart+3,
		statusStart+4,
		statusStart+5,
		base,
	)
	if err := db.pool.QueryRow(ctx, query, args...).Scan(&active, &failed); err != nil {
		return 0, 0, fmt.Errorf("count operations by tags: %w", err)
	}
	return active, failed, nil
}

func (db *DB) TransferResources(
	ctx context.Context,
	sourceID string,
	targetID string,
) (domain.TransferResult, error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.TransferResult{}, fmt.Errorf("begin resource transfer: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := validatePostgresTransfer(ctx, tx, sourceID, targetID); err != nil {
		return domain.TransferResult{}, err
	}
	tags, err := transferTags(ctx, tx, sourceID)
	if err != nil {
		return domain.TransferResult{}, err
	}

	packageCommand, err := tx.Exec(ctx, `UPDATE packages SET owner_id = $1, updated_at = $2 WHERE owner_id = $3`,
		targetID, time.Now().UTC(), sourceID)
	if err != nil {
		return domain.TransferResult{}, mapTransferWriteError("transfer packages", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE package_versions SET owner_id = $1 WHERE owner_id = $2`, targetID, sourceID); err != nil {
		return domain.TransferResult{}, mapTransferWriteError("transfer package versions", err)
	}
	hostCommand, err := tx.Exec(ctx, `UPDATE hosts SET owner_id=$1, updated_at=$2 WHERE owner_id=$3`, targetID, time.Now().UTC(), sourceID)
	if err != nil {
		return domain.TransferResult{}, mapTransferWriteError("transfer hosts", err)
	}
	service_instanceCommand, err := tx.Exec(ctx, `UPDATE service_instances SET owner_id = $1, updated_at = $2 WHERE owner_id = $3`,
		targetID, time.Now().UTC(), sourceID)
	if err != nil {
		return domain.TransferResult{}, mapTransferWriteError("transfer service_instances", err)
	}
	modelCommand, err := tx.Exec(ctx, `UPDATE models SET owner_id = $1, updated_at = $2 WHERE owner_id = $3`,
		targetID, time.Now().UTC(), sourceID)
	if err != nil {
		return domain.TransferResult{}, mapTransferWriteError("transfer models", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE model_uploads SET owner_id = $1 WHERE owner_id = $2`, targetID, sourceID); err != nil {
		return domain.TransferResult{}, fmt.Errorf("transfer model uploads: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE model_tasks SET owner_id = $1 WHERE owner_id = $2`, targetID, sourceID); err != nil {
		return domain.TransferResult{}, fmt.Errorf("transfer model tasks: %w", err)
	}
	if err := transferServiceInstanceTags(ctx, tx, targetID, tags); err != nil {
		return domain.TransferResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, sourceID); err != nil {
		return domain.TransferResult{}, fmt.Errorf("revoke source user sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TransferResult{}, fmt.Errorf("commit resource transfer: %w", err)
	}

	return domain.TransferResult{
		SourceUserID:     sourceID,
		TargetUserID:     targetID,
		Packages:         int(packageCommand.RowsAffected()),
		Hosts:            int(hostCommand.RowsAffected()),
		ServiceInstances: int(service_instanceCommand.RowsAffected()),
		Models:           int(modelCommand.RowsAffected()),
	}, nil
}

type postgresTransferTag struct {
	service_instanceID string
	sourceTagID        string
	groupName          string
	value              string
}

func validatePostgresTransfer(ctx context.Context, tx pgx.Tx, sourceID, targetID string) error {
	if sourceID == targetID {
		return domain.FieldError("target_user_id", "目标账号不能是源账号")
	}
	rows, err := tx.Query(ctx, `SELECT id::text, enabled FROM users WHERE id IN ($1, $2) FOR UPDATE`, sourceID, targetID)
	if err != nil {
		return fmt.Errorf("lock transfer users: %w", err)
	}
	defer rows.Close()
	found := make(map[string]bool, 2)
	for rows.Next() {
		var id string
		var enabled bool
		if err := rows.Scan(&id, &enabled); err != nil {
			return fmt.Errorf("scan transfer user: %w", err)
		}
		found[id] = enabled
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate transfer users: %w", err)
	}
	if _, ok := found[sourceID]; !ok {
		return domain.ErrNotFound
	}
	targetEnabled, ok := found[targetID]
	if !ok {
		return domain.ErrNotFound
	}
	if !targetEnabled {
		return transferConflict("目标账号已被禁用")
	}

	var activeOperations int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM operations
		WHERE owner_id = $1 AND status IN ($2, $3)`,
		sourceID, domain.OperationQueued, domain.OperationRunning).Scan(&activeOperations); err != nil {
		return fmt.Errorf("count active source operations: %w", err)
	}
	if activeOperations > 0 {
		return transferConflict("源账号仍有执行中的操作")
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM model_tasks
		WHERE owner_id = $1 AND status IN ($2, $3)`,
		sourceID, domain.OperationQueued, domain.OperationRunning).Scan(&activeOperations); err != nil {
		return fmt.Errorf("count active source model tasks: %w", err)
	}
	if activeOperations > 0 {
		return transferConflict("源账号仍有执行中的模型任务")
	}

	var conflicts int
	err = tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM packages source JOIN packages target
			ON target.owner_id = $1 AND target.service_type = source.service_type
			WHERE source.owner_id = $2) +
		(SELECT count(*) FROM hosts source JOIN hosts target
			ON target.owner_id = $1 AND target.ip = source.ip AND target.ssh_port = source.ssh_port
			WHERE source.owner_id = $2) +
		(SELECT count(*) FROM models source JOIN models target
			ON target.owner_id = $1 AND target.host_ip = source.host_ip
				AND target.target_dir = source.target_dir AND target.deleted_at IS NULL
			WHERE source.owner_id = $2 AND source.deleted_at IS NULL)`, targetID, sourceID).Scan(&conflicts)
	if err != nil {
		return fmt.Errorf("detect transfer conflicts: %w", err)
	}
	if conflicts > 0 {
		return transferConflict("目标账号存在同名安装包、相同服务器服务或相同模型目录")
	}
	return nil
}

func transferTags(ctx context.Context, tx pgx.Tx, sourceID string) ([]postgresTransferTag, error) {
	rows, err := tx.Query(ctx, `SELECT et.service_instance_id::text, tag.id::text, tag.group_name, tag.value
		FROM service_instance_tags et
		JOIN resource_tags tag ON tag.id = et.tag_id
		JOIN service_instances service_instance ON service_instance.id = et.service_instance_id
		WHERE service_instance.owner_id = $1`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("query transfer tags: %w", err)
	}
	defer rows.Close()
	tags := make([]postgresTransferTag, 0)
	for rows.Next() {
		var tag postgresTransferTag
		if err := rows.Scan(&tag.service_instanceID, &tag.sourceTagID, &tag.groupName, &tag.value); err != nil {
			return nil, fmt.Errorf("scan transfer tag: %w", err)
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transfer tags: %w", err)
	}
	return tags, nil
}

func transferServiceInstanceTags(
	ctx context.Context,
	tx pgx.Tx,
	targetID string,
	tags []postgresTransferTag,
) error {
	for _, tag := range tags {
		var targetTagID string
		err := tx.QueryRow(ctx, `SELECT id::text FROM resource_tags
			WHERE owner_id = $1 AND lower(group_name) = lower($2) AND lower(value) = lower($3)
				AND deleted_at IS NULL`, targetID, tag.groupName, tag.value).Scan(&targetTagID)
		if errors.Is(err, pgx.ErrNoRows) {
			targetTagID = uuid.NewString()
			_, err = tx.Exec(ctx, `INSERT INTO resource_tags
				(id, owner_id, group_name, value, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $5)`,
				targetTagID, targetID, tag.groupName, tag.value, time.Now().UTC())
		}
		if err != nil {
			return mapTransferWriteError("resolve target resource tag", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM service_instance_tags
			WHERE service_instance_id = $1 AND tag_id = $2`, tag.service_instanceID, tag.sourceTagID); err != nil {
			return fmt.Errorf("remove source service_instance tag: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_instance_tags(service_instance_id, tag_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, tag.service_instanceID, targetTagID); err != nil {
			return fmt.Errorf("assign target service_instance tag: %w", err)
		}
	}
	return nil
}

func transferConflict(message string) error {
	return &domain.AppError{Code: "TRANSFER_CONFLICT", Message: message}
}

func mapTransferWriteError(operation string, err error) error {
	if isPostgresError(err, "23505") {
		return transferConflict("目标账号存在冲突资源")
	}
	return fmt.Errorf("%s: %w", operation, err)
}
