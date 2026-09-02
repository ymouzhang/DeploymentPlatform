package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
	"github.com/jackc/pgx/v5"
)

const communicationSelect = `SELECT
	t.id::text,
	t.target_user_id::text,
	t.target_username,
	t.title,
	t.status,
	t.reopen_count,
	t.created_by::text,
	t.created_by_username,
	t.closed_by_username,
	t.closed_at,
	t.last_reopened_by_username,
	t.last_reopened_at,
	t.created_at,
	t.updated_at,
	(SELECT count(*) FROM communication_messages cm
		JOIN communication_message_recipients cr ON cr.message_id = cm.id
		WHERE cm.thread_id = t.id AND cr.recipient_user_id = $1 AND cr.read_at IS NULL),
	COALESCE((SELECT content FROM communication_messages WHERE thread_id = t.id
		ORDER BY created_at DESC, id DESC LIMIT 1), ''),
	(SELECT cr.read_at FROM communication_messages cm
		JOIN communication_message_recipients cr ON cr.message_id = cm.id
		WHERE cm.thread_id = t.id AND cr.recipient_user_id = t.target_user_id
		ORDER BY cm.created_at DESC, cm.id DESC LIMIT 1)
	FROM communication_threads t`

const communicationMessageSelect = `SELECT
	m.id::text, m.thread_id::text, m.type, m.sender_user_id::text,
	m.sender_username, m.sender_role_keys, m.content, m.created_at
	FROM communication_messages m`

const communicationResourceSelect = `SELECT
	r.id::text,
	r.thread_id::text,
	r.resource_type,
	COALESCE(r.resource_id::text, ''),
	r.resource_key,
	r.owner_id::text,
	r.owner_username,
	r.resource_label,
	r.service_type,
	r.host_name,
	COALESCE(host(r.host_ip), ''),
	CASE WHEN r.resource_type = 'package' THEN EXISTS(
		SELECT 1 FROM packages p WHERE p.owner_id = r.owner_id AND p.service_type = r.resource_key
	) WHEN r.resource_type = 'host' THEN EXISTS(
		SELECT 1 FROM hosts h WHERE h.id = r.resource_id AND h.owner_id = r.owner_id
	) ELSE EXISTS(
		SELECT 1 FROM service_instances e WHERE e.id = r.resource_id AND e.owner_id = r.owner_id
	) END
	FROM communication_resource_refs r`

func (db *DB) CreateCommunication(
	ctx context.Context,
	actor domain.User,
	input domain.CommunicationCreateInput,
) (domain.Communication, error) {
	if !hasAllPermission(actor, access.CommunicationCreate) {
		return domain.Communication{}, domain.ErrForbidden
	}
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Communication{}, fmt.Errorf("begin create communication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, err := communicationTargetUser(ctx, tx, input.TargetUserID)
	if err != nil {
		return domain.Communication{}, err
	}
	now := time.Now().UTC()
	threadID := domain.NewID()
	_, err = tx.Exec(ctx, `INSERT INTO communication_threads (
		id, target_user_id, target_username, title, status, created_by,
		created_by_username, created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`, threadID, target.ID,
		target.Username, input.Title, domain.CommunicationOpen, actor.ID, actor.Username, now)
	if err != nil {
		return domain.Communication{}, fmt.Errorf("insert communication thread: %w", err)
	}
	for _, ref := range input.Resources {
		resource, err := postgresCommunicationResourceSnapshot(ctx, tx, target, ref, threadID)
		if err != nil {
			return domain.Communication{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO communication_resource_refs (
			id, thread_id, resource_type, resource_id, resource_key, owner_id, owner_username,
			resource_label, service_type, host_name, host_ip, created_at
		) VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9, $10,
			NULLIF($11, '')::inet, $12)`, resource.ID, threadID, resource.ResourceType,
			resource.ResourceID, resource.ResourceKey, resource.OwnerID, resource.OwnerUsername,
			resource.ResourceLabel, resource.ServiceType, resource.HostName,
			resource.HostIP, now)
		if isPostgresError(err, "23505") {
			return domain.Communication{}, domain.FieldError("resources", "关联资源不能重复")
		}
		if err != nil {
			return domain.Communication{}, fmt.Errorf("insert communication resource: %w", err)
		}
	}
	message, err := insertPostgresCommunicationMessage(
		ctx, tx, threadID, domain.CommunicationAdminMessage, actor, input.Content, now,
	)
	if err != nil {
		return domain.Communication{}, err
	}
	if err := insertPostgresCommunicationRecipient(ctx, tx, message.ID, target); err != nil {
		return domain.Communication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Communication{}, fmt.Errorf("commit communication: %w", err)
	}
	return db.GetCommunication(ctx, actor, threadID)
}

func (db *DB) ListCommunications(
	ctx context.Context,
	actor domain.User,
	filter domain.CommunicationFilter,
) ([]domain.Communication, error) {
	scope, ok := actor.Permissions.Scope(access.CommunicationRead)
	if !ok {
		return nil, domain.ErrForbidden
	}
	where := make([]string, 0, 8)
	args := []any{actor.ID}
	add := func(format string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(format, len(args)))
	}
	if scope != access.ScopeAll {
		add("t.target_user_id = $%d", actor.ID)
	} else if filter.TargetUserID != "" {
		add("t.target_user_id = $%d", filter.TargetUserID)
	}
	if filter.Status != "" {
		add("t.status = $%d", filter.Status)
	}
	if filter.Unread != nil {
		op := "EXISTS"
		if !*filter.Unread {
			op = "NOT EXISTS"
		}
		args = append(args, actor.ID)
		where = append(where, fmt.Sprintf(`%s (
			SELECT 1 FROM communication_messages um
			JOIN communication_message_recipients ur ON ur.message_id = um.id
			WHERE um.thread_id = t.id AND ur.recipient_user_id = $%d AND ur.read_at IS NULL
		)`, op, len(args)))
	}
	if filter.Keyword != "" {
		like := "%" + strings.ToLower(filter.Keyword) + "%"
		args = append(args, like, like)
		where = append(where, fmt.Sprintf(`(lower(t.title) LIKE $%d OR lower(t.target_username) LIKE $%d)`,
			len(args)-1, len(args)))
	}
	if filter.CursorTime != nil && filter.CursorID != "" {
		args = append(args, *filter.CursorTime, filter.CursorID)
		where = append(where, fmt.Sprintf(`(t.updated_at < $%d OR
			(t.updated_at = $%d AND t.id < $%d))`, len(args)-1, len(args)-1, len(args)))
	}
	if len(where) == 0 {
		where = append(where, "TRUE")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	args = append(args, limit+1)
	query := communicationSelect + ` WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY t.updated_at DESC, t.id DESC LIMIT $%d`, len(args))
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query communications: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Communication, 0, limit+1)
	for rows.Next() {
		item, err := scanPostgresCommunication(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate communications: %w", err)
	}
	if err := db.populateCommunicationResources(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (db *DB) GetCommunication(
	ctx context.Context,
	actor domain.User,
	id string,
) (domain.Communication, error) {
	if _, err := db.communicationTarget(ctx, actor, id); err != nil {
		return domain.Communication{}, err
	}
	query := communicationSelect + ` WHERE t.id = $2`
	args := []any{actor.ID, id}
	item, err := scanPostgresCommunication(db.pool.QueryRow(ctx, query, args...))
	if err != nil {
		return domain.Communication{}, err
	}
	items := []domain.Communication{item}
	if err := db.populateCommunicationResources(ctx, items); err != nil {
		return domain.Communication{}, err
	}
	item = items[0]
	item.Messages, err = db.listCommunicationMessages(ctx, actor, id)
	if err != nil {
		return domain.Communication{}, err
	}
	return item, nil
}

func (db *DB) MarkCommunicationRead(
	ctx context.Context,
	actor domain.User,
	id string,
) (domain.Communication, error) {
	if _, err := db.communicationTarget(ctx, actor, id); err != nil {
		return domain.Communication{}, err
	}
	_, err := db.pool.Exec(ctx, `UPDATE communication_message_recipients SET
		read_at = COALESCE(read_at, $1) WHERE recipient_user_id = $2
		AND message_id IN (SELECT id FROM communication_messages WHERE thread_id = $3)`,
		time.Now().UTC(), actor.ID, id)
	if err != nil {
		return domain.Communication{}, fmt.Errorf("mark communication read: %w", err)
	}
	return db.GetCommunication(ctx, actor, id)
}

func (db *DB) CommunicationSummary(
	ctx context.Context,
	actor domain.User,
) (domain.CommunicationSummary, error) {
	if _, ok := actor.Permissions.Scope(access.CommunicationRead); !ok {
		return domain.CommunicationSummary{}, domain.ErrForbidden
	}
	var summary domain.CommunicationSummary
	err := db.pool.QueryRow(ctx, `SELECT count(*) FROM communication_message_recipients
		WHERE recipient_user_id = $1 AND read_at IS NULL`, actor.ID).Scan(&summary.Unread)
	if err != nil {
		return domain.CommunicationSummary{}, fmt.Errorf("query communication summary: %w", err)
	}
	return summary, nil
}

func (db *DB) SendCommunicationMessage(
	ctx context.Context,
	actor domain.User,
	id string,
	content string,
) (domain.CommunicationMessage, error) {
	scope, ok := actor.Permissions.Scope(access.CommunicationReply)
	if !ok {
		return domain.CommunicationMessage{}, domain.ErrForbidden
	}
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.CommunicationMessage{}, fmt.Errorf("begin send communication message: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, status, err := postgresCommunicationTargetTx(ctx, tx, actor, id)
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	if status != domain.CommunicationOpen {
		return domain.CommunicationMessage{}, &domain.AppError{Code: "COMMUNICATION_CLOSED", Message: "事项已关闭，无法继续回复"}
	}
	now := time.Now().UTC()
	messageType := domain.CommunicationAdminMessage
	if scope != access.ScopeAll {
		messageType = domain.CommunicationUserReceipt
		_, err := tx.Exec(ctx, `UPDATE communication_message_recipients SET
			read_at = COALESCE(read_at, $1) WHERE recipient_user_id = $2
			AND message_id IN (SELECT id FROM communication_messages WHERE thread_id = $3)`,
			now, actor.ID, id)
		if err != nil {
			return domain.CommunicationMessage{}, fmt.Errorf("mark messages read before reply: %w", err)
		}
	}
	message, err := insertPostgresCommunicationMessage(ctx, tx, id, messageType, actor, content, now)
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	if scope == access.ScopeAll {
		if err := insertPostgresCommunicationRecipient(ctx, tx, message.ID, target); err != nil {
			return domain.CommunicationMessage{}, err
		}
	} else if err := insertEnabledCommunicationAdministrators(ctx, tx, message.ID); err != nil {
		return domain.CommunicationMessage{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE communication_threads SET updated_at = $1 WHERE id = $2`, now, id); err != nil {
		return domain.CommunicationMessage{}, fmt.Errorf("touch communication thread: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.CommunicationMessage{}, fmt.Errorf("commit communication message: %w", err)
	}
	return db.GetCommunicationMessage(ctx, actor, message.ID)
}

func (db *DB) ChangeCommunicationState(
	ctx context.Context,
	actor domain.User,
	id string,
	reopen bool,
	content string,
) (domain.Communication, error) {
	if !hasAllPermission(actor, access.CommunicationManage) {
		return domain.Communication{}, domain.ErrForbidden
	}
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Communication{}, fmt.Errorf("begin change communication state: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, status, err := postgresCommunicationTargetTx(ctx, tx, actor, id)
	if err != nil {
		return domain.Communication{}, err
	}
	now := time.Now().UTC()
	messageType := domain.CommunicationSystemClosed
	defaultContent := "事项已关闭，无法继续回复"
	if reopen {
		if status == domain.CommunicationOpen {
			return domain.Communication{}, &domain.AppError{Code: "COMMUNICATION_ALREADY_OPEN", Message: "事项已经处于开启状态"}
		}
		command, err := tx.Exec(ctx, `UPDATE communication_threads SET
			status = $1, reopen_count = reopen_count + 1, last_reopened_by = $2,
			last_reopened_by_username = $3, last_reopened_at = $4, updated_at = $4
			WHERE id = $5 AND status = $6`, domain.CommunicationOpen, actor.ID,
			actor.Username, now, id, domain.CommunicationClosed)
		if err != nil {
			return domain.Communication{}, fmt.Errorf("reopen communication: %w", err)
		}
		if command.RowsAffected() == 0 {
			return domain.Communication{}, &domain.AppError{Code: "COMMUNICATION_ALREADY_OPEN", Message: "事项已经处于开启状态"}
		}
		messageType = domain.CommunicationSystemReopen
		defaultContent = "事项已由管理员重新打开，可以继续回复"
	} else {
		if status == domain.CommunicationClosed {
			return domain.Communication{}, &domain.AppError{Code: "COMMUNICATION_ALREADY_CLOSED", Message: "事项已经关闭"}
		}
		command, err := tx.Exec(ctx, `UPDATE communication_threads SET
			status = $1, closed_by = $2, closed_by_username = $3, closed_at = $4, updated_at = $4
			WHERE id = $5 AND status = $6`, domain.CommunicationClosed, actor.ID,
			actor.Username, now, id, domain.CommunicationOpen)
		if err != nil {
			return domain.Communication{}, fmt.Errorf("close communication: %w", err)
		}
		if command.RowsAffected() == 0 {
			return domain.Communication{}, &domain.AppError{Code: "COMMUNICATION_ALREADY_CLOSED", Message: "事项已经关闭"}
		}
	}
	if content != "" {
		defaultContent += "：" + content
	}
	message, err := insertPostgresCommunicationMessage(ctx, tx, id, messageType, actor, defaultContent, now)
	if err != nil {
		return domain.Communication{}, err
	}
	if err := insertPostgresCommunicationRecipient(ctx, tx, message.ID, target); err != nil {
		return domain.Communication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Communication{}, fmt.Errorf("commit communication state: %w", err)
	}
	return db.GetCommunication(ctx, actor, id)
}

func (db *DB) GetCommunicationMessage(
	ctx context.Context,
	actor domain.User,
	id string,
) (domain.CommunicationMessage, error) {
	message, err := scanPostgresCommunicationMessage(db.pool.QueryRow(ctx,
		communicationMessageSelect+` WHERE m.id = $1`, id))
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	if _, err := db.communicationTarget(ctx, actor, message.ThreadID); err != nil {
		return domain.CommunicationMessage{}, err
	}
	message.Recipients, err = db.communicationMessageRecipients(ctx, actor, message.ID)
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	return message, nil
}

func (db *DB) communicationTarget(ctx context.Context, actor domain.User, id string) (domain.User, error) {
	var target domain.User
	err := db.pool.QueryRow(ctx, `SELECT target_user_id::text, target_username
		FROM communication_threads WHERE id = $1`, id).Scan(&target.ID, &target.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("query communication target: %w", err)
	}
	scope, ok := actor.Permissions.Scope(access.CommunicationRead)
	if !ok || scope != access.ScopeAll && actor.ID != target.ID {
		return domain.User{}, domain.ErrForbidden
	}
	return target, nil
}

func postgresCommunicationTargetTx(
	ctx context.Context,
	tx pgx.Tx,
	actor domain.User,
	id string,
) (domain.User, domain.CommunicationStatus, error) {
	var target domain.User
	var status domain.CommunicationStatus
	err := tx.QueryRow(ctx, `SELECT target_user_id::text, target_username, status
		FROM communication_threads WHERE id = $1 FOR UPDATE`, id).Scan(&target.ID, &target.Username, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, "", domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, "", fmt.Errorf("lock communication target: %w", err)
	}
	scope, ok := actor.Permissions.Scope(access.CommunicationReply)
	if !ok || scope != access.ScopeAll && actor.ID != target.ID {
		return domain.User{}, "", domain.ErrForbidden
	}
	return target, status, nil
}

func communicationTargetUser(ctx context.Context, tx pgx.Tx, id string) (domain.User, error) {
	var target domain.User
	var enabled, hasOwn, hasAll bool
	err := tx.QueryRow(ctx, `SELECT u.id::text, u.username, u.enabled,
		EXISTS (SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_id = ur.role_id
			JOIN permissions p ON p.id = rp.permission_id
			WHERE ur.user_id = u.id AND p.key = $2 AND rp.scope = 'own'),
		EXISTS (SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_id = ur.role_id
			JOIN permissions p ON p.id = rp.permission_id
			WHERE ur.user_id = u.id AND p.key = $2 AND rp.scope = 'all')
		FROM users u WHERE u.id = $1 FOR SHARE`, id, access.CommunicationRead).
		Scan(&target.ID, &target.Username, &enabled, &hasOwn, &hasAll)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!enabled || !hasOwn || hasAll) {
		return domain.User{}, &domain.AppError{Code: "COMMUNICATION_TARGET_DISABLED", Message: "目标协作账号不存在、未启用或权限范围不适用"}
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("query communication target user: %w", err)
	}
	return target, nil
}

func postgresCommunicationResourceSnapshot(
	ctx context.Context,
	tx pgx.Tx,
	target domain.User,
	input domain.CommunicationResourceInput,
	threadID string,
) (domain.CommunicationResource, error) {
	item := domain.CommunicationResource{
		ID: domain.NewID(), ThreadID: threadID, ResourceType: input.ResourceType,
		ResourceID: input.ResourceID, ResourceKey: input.ResourceKey,
		OwnerID: target.ID, OwnerUsername: target.Username,
	}
	switch input.ResourceType {
	case "package":
		if input.ResourceKey == "" || input.ResourceID != "" {
			return item, domain.FieldError("resources", "安装包关联必须提供 resource_key 且不能提供 resource_id")
		}
		err := tx.QueryRow(ctx, `SELECT service_type FROM packages
			WHERE owner_id = $1 AND service_type = $2`, target.ID, input.ResourceKey).Scan(&item.ServiceType)
		if errors.Is(err, pgx.ErrNoRows) {
			return item, &domain.AppError{Code: "COMMUNICATION_RESOURCE_INVALID", Message: "关联安装包不存在或不属于目标账号"}
		}
		if err != nil {
			return item, fmt.Errorf("query communication package: %w", err)
		}
		item.ResourceLabel = item.ServiceType
	case "host":
		if input.ResourceID == "" || input.ResourceKey != "" {
			return item, domain.FieldError("resources", "主机关联必须提供 resource_id 且不能提供 resource_key")
		}
		err := tx.QueryRow(ctx, `SELECT name, host(ip) FROM hosts WHERE id=$1 AND owner_id=$2`, input.ResourceID, target.ID).Scan(&item.HostName, &item.HostIP)
		if errors.Is(err, pgx.ErrNoRows) {
			return item, &domain.AppError{Code: "COMMUNICATION_RESOURCE_INVALID", Message: "关联主机不存在或不属于目标账号"}
		}
		if err != nil {
			return item, fmt.Errorf("query communication host: %w", err)
		}
		item.ResourceLabel = item.HostName
	case "service":
		if input.ResourceID == "" || input.ResourceKey != "" {
			return item, domain.FieldError("resources", "服务关联必须提供 resource_id 且不能提供 resource_key")
		}
		var instanceName string
		err := tx.QueryRow(ctx, `SELECT s.name, h.name, host(h.ip), s.service_type FROM service_instances s JOIN hosts h ON h.id=s.host_id
			WHERE s.id = $1 AND s.owner_id = $2`, input.ResourceID, target.ID).
			Scan(&instanceName, &item.HostName, &item.HostIP, &item.ServiceType)
		if errors.Is(err, pgx.ErrNoRows) {
			return item, &domain.AppError{Code: "COMMUNICATION_RESOURCE_INVALID", Message: "关联服务实例不存在或不属于目标账号"}
		}
		if err != nil {
			return item, fmt.Errorf("query communication service_instance: %w", err)
		}
		item.ResourceLabel = item.ServiceType + " · " + instanceName
	default:
		return item, domain.FieldError("resources", "资源类型必须是 package、host 或 service")
	}
	return item, nil
}

func insertPostgresCommunicationMessage(
	ctx context.Context,
	tx pgx.Tx,
	threadID string,
	messageType domain.CommunicationMessageType,
	actor domain.User,
	content string,
	now time.Time,
) (domain.CommunicationMessage, error) {
	roles := make([]string, 0, len(actor.Roles))
	for _, role := range actor.Roles {
		roles = append(roles, role.Key)
	}
	roleJSON, err := json.Marshal(roles)
	if err != nil {
		return domain.CommunicationMessage{}, fmt.Errorf("encode sender roles: %w", err)
	}
	item := domain.CommunicationMessage{
		ID: domain.NewID(), ThreadID: threadID, Type: messageType, SenderUserID: actor.ID,
		SenderUsername: actor.Username, SenderRoles: roles, Content: content, CreatedAt: now,
	}
	_, err = tx.Exec(ctx, `INSERT INTO communication_messages (
		id, thread_id, type, sender_user_id, sender_username, sender_role_keys, content, created_at
	) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)`, item.ID, threadID,
		messageType, actor.ID, actor.Username, roleJSON, content, now)
	if err != nil {
		return domain.CommunicationMessage{}, fmt.Errorf("insert communication message: %w", err)
	}
	return item, nil
}

func insertPostgresCommunicationRecipient(
	ctx context.Context,
	tx pgx.Tx,
	messageID string,
	recipient domain.User,
) error {
	roles := make([]string, 0, len(recipient.Roles))
	for _, role := range recipient.Roles {
		roles = append(roles, role.Key)
	}
	roleJSON, err := json.Marshal(roles)
	if err != nil {
		return fmt.Errorf("encode communication recipient roles: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO communication_message_recipients
		(message_id, recipient_user_id, recipient_username, recipient_role_keys)
		VALUES ($1, $2, $3, $4::jsonb)`, messageID, recipient.ID, recipient.Username, roleJSON)
	if err != nil {
		return fmt.Errorf("insert communication recipient: %w", err)
	}
	return nil
}

func insertEnabledCommunicationAdministrators(ctx context.Context, tx pgx.Tx, messageID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO communication_message_recipients
		(message_id, recipient_user_id, recipient_username, recipient_role_keys)
		SELECT $1::uuid, u.id, u.username,
			COALESCE((SELECT jsonb_agg(r.key ORDER BY r.key)
				FROM user_roles snapshot_ur JOIN roles r ON r.id = snapshot_ur.role_id
				WHERE snapshot_ur.user_id = u.id), '[]'::jsonb)
		FROM users u
		WHERE u.enabled AND EXISTS (
			SELECT 1 FROM user_roles ur
			JOIN role_permissions rp ON rp.role_id = ur.role_id
			JOIN permissions p ON p.id = rp.permission_id
			WHERE ur.user_id = u.id AND p.key = $2 AND rp.scope = 'all'
		)`, messageID, access.CommunicationRead)
	if err != nil {
		return fmt.Errorf("insert communication administrator recipients: %w", err)
	}
	return nil
}

func (db *DB) listCommunicationMessages(
	ctx context.Context,
	actor domain.User,
	threadID string,
) ([]domain.CommunicationMessage, error) {
	rows, err := db.pool.Query(ctx, communicationMessageSelect+`
		WHERE m.thread_id = $1 ORDER BY m.created_at, m.id`, threadID)
	if err != nil {
		return nil, fmt.Errorf("query communication messages: %w", err)
	}
	defer rows.Close()
	items := make([]domain.CommunicationMessage, 0)
	for rows.Next() {
		item, err := scanPostgresCommunicationMessage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate communication messages: %w", err)
	}
	for index := range items {
		items[index].Recipients, err = db.communicationMessageRecipients(ctx, actor, items[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (db *DB) communicationMessageRecipients(
	ctx context.Context,
	actor domain.User,
	messageID string,
) ([]domain.CommunicationRecipient, error) {
	query := `SELECT recipient_user_id::text, recipient_username, recipient_role_keys, read_at
		FROM communication_message_recipients WHERE message_id = $1`
	args := []any{messageID}
	scope, ok := actor.Permissions.Scope(access.CommunicationRead)
	if !ok {
		return nil, domain.ErrForbidden
	}
	if scope != access.ScopeAll {
		query += ` AND recipient_user_id = $2`
		args = append(args, actor.ID)
	}
	query += ` ORDER BY recipient_username, recipient_user_id`
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query communication recipients: %w", err)
	}
	defer rows.Close()
	items := make([]domain.CommunicationRecipient, 0)
	for rows.Next() {
		var item domain.CommunicationRecipient
		var roleJSON []byte
		var readAt sql.NullTime
		if err := rows.Scan(&item.UserID, &item.Username, &roleJSON, &readAt); err != nil {
			return nil, fmt.Errorf("scan communication recipient: %w", err)
		}
		if err := json.Unmarshal(roleJSON, &item.Roles); err != nil {
			return nil, fmt.Errorf("decode communication recipient roles: %w", err)
		}
		if readAt.Valid {
			item.ReadAt = &readAt.Time
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate communication recipients: %w", err)
	}
	return items, nil
}

func (db *DB) populateCommunicationResources(
	ctx context.Context,
	communications []domain.Communication,
) error {
	if len(communications) == 0 {
		return nil
	}
	ids := make([]string, 0, len(communications))
	byID := make(map[string]int, len(communications))
	for index := range communications {
		ids = append(ids, communications[index].ID)
		byID[communications[index].ID] = index
		communications[index].Resources = nil
	}
	rows, err := db.pool.Query(ctx, communicationResourceSelect+`
		WHERE r.thread_id = ANY($1::uuid[]) ORDER BY r.thread_id, r.created_at, r.id`, ids)
	if err != nil {
		return fmt.Errorf("query communication resources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanPostgresCommunicationResource(rows)
		if err != nil {
			return err
		}
		if index, ok := byID[item.ThreadID]; ok {
			communications[index].Resources = append(communications[index].Resources, item)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate communication resources: %w", err)
	}
	return nil
}

func scanPostgresCommunication(row interface{ Scan(...any) error }) (domain.Communication, error) {
	var item domain.Communication
	var closedAt, reopenedAt, userReadAt sql.NullTime
	err := row.Scan(&item.ID, &item.TargetUserID, &item.TargetUsername, &item.Title,
		&item.Status, &item.ReopenCount, &item.CreatedBy, &item.CreatedByUsername,
		&item.ClosedByUsername, &closedAt, &item.LastReopenedByUsername, &reopenedAt,
		&item.CreatedAt, &item.UpdatedAt, &item.UnreadCount, &item.LastMessage, &userReadAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Communication{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Communication{}, fmt.Errorf("scan communication: %w", err)
	}
	if closedAt.Valid {
		item.ClosedAt = &closedAt.Time
	}
	if reopenedAt.Valid {
		item.LastReopenedAt = &reopenedAt.Time
	}
	if userReadAt.Valid {
		item.UserReadAt = &userReadAt.Time
	}
	return item, nil
}

func scanPostgresCommunicationMessage(
	row interface{ Scan(...any) error },
) (domain.CommunicationMessage, error) {
	var item domain.CommunicationMessage
	var roleJSON []byte
	err := row.Scan(&item.ID, &item.ThreadID, &item.Type, &item.SenderUserID,
		&item.SenderUsername, &roleJSON, &item.Content, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CommunicationMessage{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CommunicationMessage{}, fmt.Errorf("scan communication message: %w", err)
	}
	if err := json.Unmarshal(roleJSON, &item.SenderRoles); err != nil {
		return domain.CommunicationMessage{}, fmt.Errorf("decode communication sender roles: %w", err)
	}
	return item, nil
}

func scanPostgresCommunicationResource(
	row interface{ Scan(...any) error },
) (domain.CommunicationResource, error) {
	var item domain.CommunicationResource
	err := row.Scan(&item.ID, &item.ThreadID, &item.ResourceType, &item.ResourceID,
		&item.ResourceKey, &item.OwnerID, &item.OwnerUsername, &item.ResourceLabel,
		&item.ServiceType, &item.HostName, &item.HostIP, &item.Available)
	if err != nil {
		return domain.CommunicationResource{}, fmt.Errorf("scan communication resource: %w", err)
	}
	if item.Available {
		switch item.ResourceType {
		case "package":
			item.Link = "/packages?owner_id=" + url.QueryEscape(item.OwnerID)
		case "host":
			item.Link = "/hosts?owner_id=" + url.QueryEscape(item.OwnerID)
		case "service":
			item.Link = "/services?owner_id=" + url.QueryEscape(item.OwnerID)
		}
	}
	return item, nil
}

func hasAllPermission(user domain.User, permission access.Permission) bool {
	scope, ok := user.Permissions.Scope(permission)
	return ok && scope == access.ScopeAll
}
