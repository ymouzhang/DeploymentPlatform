package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"

	"DP/internal/domain"
)

func (s *Store) CreateCommunication(ctx context.Context, actor domain.User, input domain.CommunicationCreateInput) (domain.Communication, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Communication{}, err
	}
	defer tx.Rollback()
	target, err := scanUser(tx.QueryRowContext(ctx, userSelect+` WHERE id = ?`, input.TargetUserID))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Communication{}, &domain.AppError{Code: "COMMUNICATION_TARGET_DISABLED", Message: "目标普通账号不存在或未启用"}
		}
		return domain.Communication{}, err
	}
	if target.Role != domain.RoleUser || !target.Enabled {
		return domain.Communication{}, &domain.AppError{Code: "COMMUNICATION_TARGET_DISABLED", Message: "目标普通账号不存在或未启用"}
	}
	now := time.Now().UTC()
	threadID := NewID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO communication_threads(
		id, target_user_id, target_username, title, status, created_by, created_by_username, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, threadID, target.ID, target.Username, input.Title,
		domain.CommunicationOpen, actor.ID, actor.Username, formatTime(now), formatTime(now)); err != nil {
		return domain.Communication{}, err
	}
	for _, ref := range input.Resources {
		resource, err := communicationResourceSnapshot(ctx, tx, target, ref, threadID)
		if err != nil {
			return domain.Communication{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO communication_resource_refs(
			id, thread_id, resource_type, resource_id, resource_key, owner_id, owner_username,
			resource_label, service_type, environment_name, environment_ip, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, resource.ID, threadID, resource.ResourceType,
			resource.ResourceID, resource.ResourceKey, resource.OwnerID, resource.OwnerUsername,
			resource.ResourceLabel, resource.ServiceType, resource.EnvironmentName, resource.EnvironmentIP, formatTime(now)); err != nil {
			if isUniqueError(err) {
				return domain.Communication{}, domain.FieldError("resources", "关联资源不能重复")
			}
			return domain.Communication{}, err
		}
	}
	message, err := insertCommunicationMessage(ctx, tx, threadID, domain.CommunicationAdminMessage, actor, input.Content, now)
	if err != nil {
		return domain.Communication{}, err
	}
	if err := insertCommunicationRecipient(ctx, tx, message.ID, target); err != nil {
		return domain.Communication{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Communication{}, err
	}
	return s.GetCommunication(ctx, actor, threadID)
}

func (s *Store) ListCommunications(ctx context.Context, actor domain.User, filter domain.CommunicationFilter) ([]domain.Communication, error) {
	where := []string{"1=1"}
	args := []any{actor.ID}
	if actor.Role != domain.RoleAdmin {
		where = append(where, "t.target_user_id = ?")
		args = append(args, actor.ID)
	} else if filter.TargetUserID != "" {
		where = append(where, "t.target_user_id = ?")
		args = append(args, filter.TargetUserID)
	}
	if filter.Status != "" {
		where = append(where, "t.status = ?")
		args = append(args, filter.Status)
	}
	if filter.Unread != nil {
		op := "EXISTS"
		if !*filter.Unread {
			op = "NOT EXISTS"
		}
		where = append(where, op+` (SELECT 1 FROM communication_messages um
			JOIN communication_message_recipients ur ON ur.message_id = um.id
			WHERE um.thread_id = t.id AND ur.recipient_user_id = ? AND ur.read_at IS NULL)`)
		args = append(args, actor.ID)
	}
	if filter.Keyword != "" {
		where = append(where, "(LOWER(t.title) LIKE ? OR LOWER(t.target_username) LIKE ?)")
		like := "%" + strings.ToLower(filter.Keyword) + "%"
		args = append(args, like, like)
	}
	if filter.CursorTime != nil && filter.CursorID != "" {
		where = append(where, "(t.updated_at < ? OR (t.updated_at = ? AND t.id < ?))")
		stamp := formatTime(*filter.CursorTime)
		args = append(args, stamp, stamp, filter.CursorID)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, communicationSelect+` WHERE `+strings.Join(where, " AND ")+`
		ORDER BY t.updated_at DESC, t.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	items := []domain.Communication{}
	for rows.Next() {
		item, err := scanCommunication(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := s.populateCommunicationResources(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) GetCommunication(ctx context.Context, actor domain.User, id string) (domain.Communication, error) {
	query := communicationSelect + ` WHERE t.id = ?`
	args := []any{actor.ID, id}
	if actor.Role != domain.RoleAdmin {
		query += ` AND t.target_user_id = ?`
		args = append(args, actor.ID)
	}
	item, err := scanCommunication(s.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		return domain.Communication{}, err
	}
	items := []domain.Communication{item}
	if err := s.populateCommunicationResources(ctx, items); err != nil {
		return domain.Communication{}, err
	}
	item = items[0]
	messages, err := s.listCommunicationMessages(ctx, actor, id)
	if err != nil {
		return domain.Communication{}, err
	}
	item.Messages = messages
	return item, nil
}

func (s *Store) MarkCommunicationRead(ctx context.Context, actor domain.User, id string) (domain.Communication, error) {
	if _, err := s.communicationTarget(ctx, actor, id); err != nil {
		return domain.Communication{}, err
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `UPDATE communication_message_recipients SET read_at = COALESCE(read_at, ?)
		WHERE recipient_user_id = ? AND message_id IN (SELECT id FROM communication_messages WHERE thread_id = ?)`, now, actor.ID, id)
	if err != nil {
		return domain.Communication{}, err
	}
	return s.GetCommunication(ctx, actor, id)
}

func (s *Store) CommunicationSummary(ctx context.Context, actor domain.User) (domain.CommunicationSummary, error) {
	var result domain.CommunicationSummary
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM communication_message_recipients
		WHERE recipient_user_id = ? AND read_at IS NULL`, actor.ID).Scan(&result.Unread)
	return result, err
}

func (s *Store) SendCommunicationMessage(ctx context.Context, actor domain.User, id, content string) (domain.CommunicationMessage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	defer tx.Rollback()
	target, status, err := communicationTargetTx(ctx, tx, actor, id)
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	if status != domain.CommunicationOpen {
		return domain.CommunicationMessage{}, &domain.AppError{Code: "COMMUNICATION_CLOSED", Message: "事项已关闭，无法继续回复"}
	}
	now := time.Now().UTC()
	typeValue := domain.CommunicationAdminMessage
	if actor.Role != domain.RoleAdmin {
		typeValue = domain.CommunicationUserReceipt
		if _, err := tx.ExecContext(ctx, `UPDATE communication_message_recipients SET read_at = COALESCE(read_at, ?)
			WHERE recipient_user_id = ? AND message_id IN (SELECT id FROM communication_messages WHERE thread_id = ?)`, formatTime(now), actor.ID, id); err != nil {
			return domain.CommunicationMessage{}, err
		}
	}
	message, err := insertCommunicationMessage(ctx, tx, id, typeValue, actor, content, now)
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	if actor.Role == domain.RoleAdmin {
		if err := insertCommunicationRecipient(ctx, tx, message.ID, target); err != nil {
			return domain.CommunicationMessage{}, err
		}
	} else if err := insertEnabledAdminRecipients(ctx, tx, message.ID); err != nil {
		return domain.CommunicationMessage{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE communication_threads SET updated_at = ? WHERE id = ?`, formatTime(now), id); err != nil {
		return domain.CommunicationMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.CommunicationMessage{}, err
	}
	return s.GetCommunicationMessage(ctx, actor, message.ID)
}

func (s *Store) ChangeCommunicationState(ctx context.Context, actor domain.User, id string, reopen bool, content string) (domain.Communication, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Communication{}, err
	}
	defer tx.Rollback()
	target, status, err := communicationTargetTx(ctx, tx, actor, id)
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
		result, err := tx.ExecContext(ctx, `UPDATE communication_threads SET status = ?, reopen_count = reopen_count + 1,
			last_reopened_by = ?, last_reopened_by_username = ?, last_reopened_at = ?, updated_at = ?
			WHERE id = ? AND status = ?`, domain.CommunicationOpen, actor.ID, actor.Username, formatTime(now), formatTime(now), id, domain.CommunicationClosed)
		if err != nil {
			return domain.Communication{}, err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return domain.Communication{}, &domain.AppError{Code: "COMMUNICATION_ALREADY_OPEN", Message: "事项已经处于开启状态"}
		}
		messageType, defaultContent = domain.CommunicationSystemReopen, "事项已由管理员重新打开，可以继续回复"
	} else {
		if status == domain.CommunicationClosed {
			return domain.Communication{}, &domain.AppError{Code: "COMMUNICATION_ALREADY_CLOSED", Message: "事项已经关闭"}
		}
		result, err := tx.ExecContext(ctx, `UPDATE communication_threads SET status = ?, closed_by = ?, closed_by_username = ?,
			closed_at = ?, updated_at = ? WHERE id = ? AND status = ?`, domain.CommunicationClosed, actor.ID,
			actor.Username, formatTime(now), formatTime(now), id, domain.CommunicationOpen)
		if err != nil {
			return domain.Communication{}, err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return domain.Communication{}, &domain.AppError{Code: "COMMUNICATION_ALREADY_CLOSED", Message: "事项已经关闭"}
		}
	}
	if content != "" {
		defaultContent += "：" + content
	}
	message, err := insertCommunicationMessage(ctx, tx, id, messageType, actor, defaultContent, now)
	if err != nil {
		return domain.Communication{}, err
	}
	if err := insertCommunicationRecipient(ctx, tx, message.ID, target); err != nil {
		return domain.Communication{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Communication{}, err
	}
	return s.GetCommunication(ctx, actor, id)
}

func (s *Store) GetCommunicationMessage(ctx context.Context, actor domain.User, id string) (domain.CommunicationMessage, error) {
	message, err := scanCommunicationMessage(s.db.QueryRowContext(ctx, communicationMessageSelect+` WHERE m.id = ?`, id))
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	if _, err := s.communicationTarget(ctx, actor, message.ThreadID); err != nil {
		return domain.CommunicationMessage{}, err
	}
	recipients, err := s.communicationMessageRecipients(ctx, actor, message.ID)
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	message.Recipients = recipients
	return message, nil
}

func (s *Store) communicationTarget(ctx context.Context, actor domain.User, id string) (domain.User, error) {
	var targetID, username string
	err := s.db.QueryRowContext(ctx, `SELECT target_user_id, target_username FROM communication_threads WHERE id = ?`, id).Scan(&targetID, &username)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && actor.Role != domain.RoleAdmin && actor.ID != targetID) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	return domain.User{ID: targetID, Username: username, Role: domain.RoleUser}, nil
}

func communicationTargetTx(ctx context.Context, tx *sql.Tx, actor domain.User, id string) (domain.User, domain.CommunicationStatus, error) {
	var target domain.User
	var status domain.CommunicationStatus
	err := tx.QueryRowContext(ctx, `SELECT target_user_id, target_username, status FROM communication_threads WHERE id = ?`, id).
		Scan(&target.ID, &target.Username, &status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && actor.Role != domain.RoleAdmin && actor.ID != target.ID) {
		return domain.User{}, "", domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, "", err
	}
	target.Role = domain.RoleUser
	return target, status, nil
}

func communicationResourceSnapshot(ctx context.Context, tx *sql.Tx, target domain.User, input domain.CommunicationResourceInput, threadID string) (domain.CommunicationResource, error) {
	item := domain.CommunicationResource{ID: NewID(), ThreadID: threadID, ResourceType: input.ResourceType,
		ResourceID: input.ResourceID, ResourceKey: input.ResourceKey, OwnerID: target.ID, OwnerUsername: target.Username}
	switch input.ResourceType {
	case "package":
		if input.ResourceKey == "" || input.ResourceID != "" {
			return item, domain.FieldError("resources", "安装包关联必须提供 resource_key 且不能提供 resource_id")
		}
		if err := tx.QueryRowContext(ctx, `SELECT service_type FROM packages WHERE owner_id = ? AND service_type = ?`, target.ID, input.ResourceKey).Scan(&item.ServiceType); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return item, &domain.AppError{Code: "COMMUNICATION_RESOURCE_INVALID", Message: "关联安装包不存在或不属于目标账号"}
			}
			return item, err
		}
		item.ResourceLabel = item.ServiceType
	case "environment", "service":
		if input.ResourceID == "" || input.ResourceKey != "" {
			return item, domain.FieldError("resources", "环境或服务关联必须提供 resource_id 且不能提供 resource_key")
		}
		err := tx.QueryRowContext(ctx, `SELECT name, ip, service_type FROM environments WHERE id = ? AND owner_id = ?`, input.ResourceID, target.ID).
			Scan(&item.EnvironmentName, &item.EnvironmentIP, &item.ServiceType)
		if errors.Is(err, sql.ErrNoRows) {
			return item, &domain.AppError{Code: "COMMUNICATION_RESOURCE_INVALID", Message: "关联环境或服务不存在或不属于目标账号"}
		}
		if err != nil {
			return item, err
		}
		item.ResourceLabel = item.EnvironmentName
		if input.ResourceType == "service" {
			item.ResourceLabel = item.ServiceType + " · " + item.EnvironmentName
		}
	default:
		return item, domain.FieldError("resources", "资源类型必须是 package、environment 或 service")
	}
	return item, nil
}

func insertCommunicationMessage(ctx context.Context, tx *sql.Tx, threadID string, messageType domain.CommunicationMessageType, actor domain.User, content string, now time.Time) (domain.CommunicationMessage, error) {
	item := domain.CommunicationMessage{ID: NewID(), ThreadID: threadID, Type: messageType, SenderUserID: actor.ID,
		SenderUsername: actor.Username, SenderRole: actor.Role, Content: content, CreatedAt: now, Recipients: []domain.CommunicationRecipient{}}
	_, err := tx.ExecContext(ctx, `INSERT INTO communication_messages(id, thread_id, type, sender_user_id,
		sender_username, sender_role, content, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, threadID,
		messageType, actor.ID, actor.Username, actor.Role, content, formatTime(now))
	return item, err
}

func insertCommunicationRecipient(ctx context.Context, tx *sql.Tx, messageID string, recipient domain.User) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO communication_message_recipients(message_id, recipient_user_id,
		recipient_username, recipient_role) VALUES (?, ?, ?, ?)`, messageID, recipient.ID, recipient.Username, recipient.Role)
	return err
}

func insertEnabledAdminRecipients(ctx context.Context, tx *sql.Tx, messageID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO communication_message_recipients(message_id, recipient_user_id,
		recipient_username, recipient_role) SELECT ?, id, username, role FROM users WHERE role = 'admin' AND enabled = 1`, messageID)
	return err
}

func (s *Store) listCommunicationMessages(ctx context.Context, actor domain.User, threadID string) ([]domain.CommunicationMessage, error) {
	rows, err := s.db.QueryContext(ctx, communicationMessageSelect+` WHERE m.thread_id = ? ORDER BY m.created_at, m.id`, threadID)
	if err != nil {
		return nil, err
	}
	items := []domain.CommunicationMessage{}
	for rows.Next() {
		item, err := scanCommunicationMessage(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range items {
		recipients, err := s.communicationMessageRecipients(ctx, actor, items[i].ID)
		if err != nil {
			return nil, err
		}
		items[i].Recipients = recipients
	}
	return items, nil
}

func (s *Store) communicationMessageRecipients(ctx context.Context, actor domain.User, messageID string) ([]domain.CommunicationRecipient, error) {
	query := `SELECT recipient_user_id, recipient_username, recipient_role, read_at
		FROM communication_message_recipients WHERE message_id = ?`
	args := []any{messageID}
	if actor.Role != domain.RoleAdmin {
		query += ` AND recipient_user_id = ?`
		args = append(args, actor.ID)
	}
	query += ` ORDER BY recipient_username`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.CommunicationRecipient{}
	for rows.Next() {
		var item domain.CommunicationRecipient
		var role string
		var readAt sql.NullString
		if err := rows.Scan(&item.UserID, &item.Username, &role, &readAt); err != nil {
			return nil, err
		}
		item.Role = domain.UserRole(role)
		item.ReadAt = parseNullTime(readAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) populateCommunicationResources(ctx context.Context, communications []domain.Communication) error {
	for i := range communications {
		rows, err := s.db.QueryContext(ctx, communicationResourceSelect+` WHERE r.thread_id = ? ORDER BY r.created_at, r.id`, communications[i].ID)
		if err != nil {
			return err
		}
		resources := []domain.CommunicationResource{}
		for rows.Next() {
			item, err := scanCommunicationResource(rows)
			if err != nil {
				rows.Close()
				return err
			}
			resources = append(resources, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		communications[i].Resources = resources
	}
	return nil
}

func scanCommunication(row scanner) (domain.Communication, error) {
	var item domain.Communication
	var status string
	var closedAt, reopenedAt, userReadAt sql.NullString
	var created, updated string
	err := row.Scan(&item.ID, &item.TargetUserID, &item.TargetUsername, &item.Title, &status, &item.ReopenCount,
		&item.CreatedBy, &item.CreatedByUsername, &item.ClosedByUsername, &closedAt,
		&item.LastReopenedByUsername, &reopenedAt, &created, &updated, &item.UnreadCount, &item.LastMessage, &userReadAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Communication{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Communication{}, err
	}
	item.Status = domain.CommunicationStatus(status)
	item.ClosedAt, item.LastReopenedAt, item.UserReadAt = parseNullTime(closedAt), parseNullTime(reopenedAt), parseNullTime(userReadAt)
	item.CreatedAt, _ = parseTime(created)
	item.UpdatedAt, _ = parseTime(updated)
	item.Resources = []domain.CommunicationResource{}
	return item, nil
}

func scanCommunicationMessage(row scanner) (domain.CommunicationMessage, error) {
	var item domain.CommunicationMessage
	var messageType, role, created string
	err := row.Scan(&item.ID, &item.ThreadID, &messageType, &item.SenderUserID, &item.SenderUsername, &role, &item.Content, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CommunicationMessage{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CommunicationMessage{}, err
	}
	item.Type, item.SenderRole = domain.CommunicationMessageType(messageType), domain.UserRole(role)
	item.CreatedAt, _ = parseTime(created)
	item.Recipients = []domain.CommunicationRecipient{}
	return item, nil
}

func scanCommunicationResource(row scanner) (domain.CommunicationResource, error) {
	var item domain.CommunicationResource
	var available int
	if err := row.Scan(&item.ID, &item.ThreadID, &item.ResourceType, &item.ResourceID, &item.ResourceKey,
		&item.OwnerID, &item.OwnerUsername, &item.ResourceLabel, &item.ServiceType, &item.EnvironmentName,
		&item.EnvironmentIP, &available); err != nil {
		return item, err
	}
	item.Available = available != 0
	if item.Available {
		switch item.ResourceType {
		case "package":
			item.Link = "/packages?owner_id=" + url.QueryEscape(item.OwnerID)
		case "environment":
			item.Link = "/environments?owner_id=" + url.QueryEscape(item.OwnerID)
		case "service":
			item.Link = "/services?owner_id=" + url.QueryEscape(item.OwnerID)
		}
	}
	return item, nil
}

const communicationSelect = `SELECT t.id, t.target_user_id, t.target_username, t.title, t.status, t.reopen_count,
	t.created_by, t.created_by_username, t.closed_by_username, t.closed_at,
	t.last_reopened_by_username, t.last_reopened_at, t.created_at, t.updated_at,
	(SELECT COUNT(*) FROM communication_messages cm JOIN communication_message_recipients cr ON cr.message_id = cm.id
	 WHERE cm.thread_id = t.id AND cr.recipient_user_id = ? AND cr.read_at IS NULL),
	COALESCE((SELECT content FROM communication_messages WHERE thread_id = t.id ORDER BY created_at DESC, id DESC LIMIT 1), ''),
	(SELECT cr.read_at FROM communication_messages cm JOIN communication_message_recipients cr ON cr.message_id = cm.id
	 WHERE cm.thread_id = t.id AND cr.recipient_user_id = t.target_user_id
	 ORDER BY cm.created_at DESC, cm.id DESC LIMIT 1)
	FROM communication_threads t`

const communicationMessageSelect = `SELECT m.id, m.thread_id, m.type, m.sender_user_id, m.sender_username,
	m.sender_role, m.content, m.created_at FROM communication_messages m`

const communicationResourceSelect = `SELECT r.id, r.thread_id, r.resource_type, r.resource_id, r.resource_key,
	r.owner_id, r.owner_username, r.resource_label, r.service_type, r.environment_name, r.environment_ip,
	CASE WHEN r.resource_type = 'package' THEN EXISTS(SELECT 1 FROM packages p WHERE p.owner_id = r.owner_id AND p.service_type = r.resource_key)
	ELSE EXISTS(SELECT 1 FROM environments e WHERE e.id = r.resource_id AND e.owner_id = r.owner_id) END
	FROM communication_resource_refs r`
