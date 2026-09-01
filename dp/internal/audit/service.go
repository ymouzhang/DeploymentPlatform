package audit

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"DP/internal/domain"
	"golang.org/x/sys/unix"
)

type Repository interface {
	CountRecentLoginFailures(context.Context, string, string, time.Time) (int, error)
	CreateAuditEvent(context.Context, domain.AuditEvent) (domain.AuditEvent, error)
	CreateNotification(context.Context, domain.Notification) (domain.Notification, error)
	CreateNotificationIfUnresolved(context.Context, string, domain.Notification) error
	DeleteAuditEventsBefore(context.Context, time.Time, int) (int64, error)
	DeleteLoginThrottlesBefore(context.Context, time.Time) error
	DeleteResolvedNotificationsBefore(context.Context, time.Time, int) (int64, error)
	DeleteTerminalOperationsBefore(context.Context, time.Time, int) ([]string, error)
	FindOperationAuditRequest(context.Context, string) (domain.AuditEvent, error)
	HasRecentLoginThrottleAudit(context.Context, string, string, time.Time) (bool, error)
	ListStaleUsers(context.Context, time.Time) ([]domain.User, error)
	ResolveNotificationByDedupeKey(context.Context, string, string, time.Time) error
}

type Service struct {
	store                 Repository
	retention             time.Duration
	log                   *slog.Logger
	notificationRetention time.Duration
	operationRetention    time.Duration
	staleAccountThreshold time.Duration
	dataDir               string
}

func (s *Service) ConfigureMaintenance(dataDir string, notificationDays, operationDays, staleAccountDays int) {
	if notificationDays <= 0 {
		notificationDays = 180
	}
	if operationDays <= 0 {
		operationDays = 180
	}
	if staleAccountDays <= 0 {
		staleAccountDays = 90
	}
	s.dataDir = dataDir
	s.notificationRetention = time.Duration(notificationDays) * 24 * time.Hour
	s.operationRetention = time.Duration(operationDays) * 24 * time.Hour
	s.staleAccountThreshold = time.Duration(staleAccountDays) * 24 * time.Hour
}

func NewService(db Repository, retentionDays int, log *slog.Logger) *Service {
	if retentionDays <= 0 {
		retentionDays = 180
	}
	return &Service{store: db, retention: time.Duration(retentionDays) * 24 * time.Hour, log: log}
}

func (s *Service) Record(ctx context.Context, event domain.AuditEvent) {
	event.UserAgent = truncate(event.UserAgent, 512)
	if event.Action == "auth.login" && event.ErrorCode == "LOGIN_THROTTLED" {
		recent, err := s.store.HasRecentLoginThrottleAudit(ctx, event.ActorUsername, event.SourceIP, time.Now().UTC().Add(-30*time.Second))
		if err == nil && recent {
			return
		}
	}
	if event.RiskLevel == "" {
		event.RiskLevel = riskLevel(event)
	}
	if event.Action == "auth.login" && event.Outcome != "success" {
		count, err := s.store.CountRecentLoginFailures(ctx, event.ActorUsername, event.SourceIP, time.Now().UTC().Add(-10*time.Minute))
		if err == nil && count >= 4 {
			event.RiskLevel = "high"
		}
	}
	created, err := s.store.CreateAuditEvent(ctx, event)
	if err != nil {
		s.log.Error("record audit event", "action", event.Action, "request_id", event.RequestID, "error", err)
		return
	}
	if created.Action == "auth.login" && created.Outcome == "success" && created.ActorUserID != "" {
		if err := s.store.ResolveNotificationByDedupeKey(ctx, "stale-account:"+created.ActorUserID, "system", created.OccurredAt); err != nil {
			s.log.Error("resolve stale account notification", "user_id", created.ActorUserID, "error", err)
		}
	}
	if notification, ok := notificationFor(created); ok {
		if _, err := s.store.CreateNotification(ctx, notification); err != nil {
			s.log.Error("create risk notification", "action", event.Action, "error", err)
		}
	}
}

func notificationFor(event domain.AuditEvent) (domain.Notification, bool) {
	item := domain.Notification{CreatedAt: event.OccurredAt, RiskLevel: event.RiskLevel,
		TargetType: event.TargetType, TargetID: event.TargetID, TargetLabel: event.TargetLabel,
		OwnerID: event.OwnerID, OwnerUsername: event.OwnerUsername, OperationID: event.OperationID}
	if item.RiskLevel == "" {
		item.RiskLevel = "normal"
	}
	switch {
	case event.Action == "auth.login" && event.Outcome != "success" && event.RiskLevel == "high":
		item.Category, item.Title, item.Message, item.Link = "security", "连续登录失败", "账号 "+event.ActorUsername+" 在短时间内连续登录失败", "/audit?category=authentication&outcome=failure"
		item.DedupeKey = "login-failure:" + strings.ToLower(event.ActorUsername) + ":" + event.SourceIP
	case event.Action == "account.disable":
		item.Category, item.Title, item.Message, item.Link = "account", "账号已禁用", "账号 "+event.TargetLabel+" 已被禁用并强制下线", "/users?user_id="+event.TargetID
	case event.Action == "account.delete":
		item.Category, item.Title, item.Message, item.Link = "account", "账号已删除", "账号 "+event.TargetLabel+" 已被删除", "/audit?category=account"
	case event.Action == "account.password.reset":
		item.Category, item.Title, item.Message, item.Link = "account", "管理员重置密码", "账号 "+event.TargetLabel+" 的密码已由管理员重置", "/users?user_id="+event.TargetID
	case event.Action == "account.resources.transfer":
		item.Category, item.Title, item.Message, item.Link = "account", "账号资源已交接", "账号 "+event.TargetLabel+" 的业务资源已完成交接", "/users?user_id="+event.TargetID
	case event.Action == "environment.validate" && event.ErrorCode == "HOST_KEY_CHANGED":
		item.Category, item.Title, item.Message, item.Link = "security", "SSH 主机指纹变化", "环境 "+event.TargetLabel+" 的 SSH 主机指纹与已信任值不一致", "/environments?owner_id="+event.OwnerID
		item.DedupeKey = "host-key:" + event.TargetID
	case strings.HasPrefix(event.Action, "service.") && strings.HasSuffix(event.Action, ".completed") && event.Outcome != "success":
		item.Category, item.Title, item.Message, item.Link = "operation", "服务操作失败", event.TargetLabel+" 操作失败："+event.ErrorCode, "/operations?operation_id="+event.OperationID
		item.DedupeKey = "operation-failure:" + event.OperationID
	case event.Action == "package.delete" || event.Action == "environment.delete":
		item.Category, item.Title, item.Message, item.Link = "resource", "资源已删除", event.TargetLabel+" 已被删除", "/audit"
	case strings.HasSuffix(event.Action, ".export"):
		item.Category, item.Title, item.Message, item.Link = "resource", "数据已导出", event.ActorUsername+" 执行了数据导出", "/audit"
	case event.RiskLevel == "high" && event.ActorUserID != "" && event.OwnerID != "" && event.ActorUserID != event.OwnerID:
		item.Category, item.Title, item.Message, item.Link = "resource", "跨账号资源变更", event.ActorUsername+" 变更了 "+event.OwnerUsername+" 的资源 "+event.TargetLabel, "/audit?owner_id="+event.OwnerID
	default:
		return domain.Notification{}, false
	}
	return item, true
}

func (s *Service) CompleteOperation(ctx context.Context, operation domain.Operation) {
	requested, err := s.store.FindOperationAuditRequest(ctx, operation.ID)
	if err != nil {
		s.log.Error("find requested audit event", "operation_id", operation.ID, "error", err)
		return
	}
	action := strings.TrimSuffix(requested.Action, ".requested") + ".completed"
	outcome := "success"
	if operation.Status != domain.OperationSucceeded {
		outcome = "failure"
	}
	changes := map[string]any{"status": operation.Status, "stage": operation.Stage}
	if operation.ExitCode != nil {
		changes["exit_code"] = *operation.ExitCode
	}
	s.Record(ctx, domain.AuditEvent{
		Category: requested.Category, Action: action, Outcome: outcome,
		ActorUserID: requested.ActorUserID, ActorUsername: requested.ActorUsername,
		ActorRole: requested.ActorRole, OwnerID: requested.OwnerID,
		OwnerUsername: requested.OwnerUsername, TargetType: requested.TargetType,
		TargetID: requested.TargetID, TargetLabel: requested.TargetLabel,
		RequestID: requested.RequestID, OperationID: operation.ID,
		SourceIP: requested.SourceIP, UserAgent: requested.UserAgent,
		ErrorCode: operation.ErrorCode, Changes: changes,
	})
}

func (s *Service) Run(ctx context.Context) {
	s.cleanup(ctx)
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanup(ctx)
		}
	}
}

func (s *Service) cleanup(ctx context.Context) {
	s.checkDisk(ctx)
	s.checkStaleAccounts(ctx)
	if err := s.store.DeleteLoginThrottlesBefore(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
		s.log.Error("clean login throttles", "error", err)
	}
	s.cleanupNotifications(ctx)
	s.cleanupOperations(ctx)
	before := time.Now().UTC().Add(-s.retention)
	for {
		deleted, err := s.store.DeleteAuditEventsBefore(ctx, before, 1000)
		if err != nil {
			s.log.Error("clean audit events", "error", err)
			_ = s.store.CreateNotificationIfUnresolved(context.Background(), "audit-cleanup", domain.Notification{
				RiskLevel: "high", Category: "system", Title: "审计日志清理失败",
				Message: "审计日志保留策略执行失败，请检查数据库状态和磁盘权限", Link: "/dashboard",
			})
			return
		}
		if deleted < 1000 {
			return
		}
	}
}

func (s *Service) checkStaleAccounts(ctx context.Context) {
	if s.staleAccountThreshold <= 0 {
		return
	}
	users, err := s.store.ListStaleUsers(ctx, time.Now().UTC().Add(-s.staleAccountThreshold))
	if err != nil {
		s.log.Error("check stale accounts", "error", err)
		return
	}
	for _, user := range users {
		err := s.store.CreateNotificationIfUnresolved(ctx, "stale-account:"+user.ID, domain.Notification{
			RiskLevel: "normal", Category: "account", Title: "账号长期未登录",
			Message:    "账号 " + user.Username + " 已超过安全阈值未成功登录，请确认是否仍需启用",
			TargetType: "user", TargetID: user.ID, TargetLabel: user.Username,
			OwnerID: user.ID, OwnerUsername: user.Username, Link: "/users?user_id=" + user.ID,
		})
		if err != nil {
			s.log.Error("create stale account notification", "user_id", user.ID, "error", err)
		}
	}
}

func (s *Service) cleanupNotifications(ctx context.Context) {
	if s.notificationRetention == 0 {
		return
	}
	before := time.Now().UTC().Add(-s.notificationRetention)
	for {
		deleted, err := s.store.DeleteResolvedNotificationsBefore(ctx, before, 1000)
		if err != nil {
			s.log.Error("clean notifications", "error", err)
			return
		}
		if deleted < 1000 {
			return
		}
	}
}

func (s *Service) cleanupOperations(ctx context.Context) {
	if s.operationRetention == 0 || s.dataDir == "" {
		return
	}
	before := time.Now().UTC().Add(-s.operationRetention)
	for {
		paths, err := s.store.DeleteTerminalOperationsBefore(ctx, before, 500)
		if err != nil {
			s.log.Error("clean operations", "error", err)
			return
		}
		for _, relative := range paths {
			clean := filepath.Clean(relative)
			if filepath.IsAbs(clean) || filepath.Dir(clean) != "operations" {
				s.log.Error("reject unsafe operation log path", "path", relative)
				continue
			}
			path := filepath.Join(s.dataDir, clean)
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.log.Error("clean operation log", "path", relative, "error", err)
				_ = s.store.CreateNotificationIfUnresolved(context.Background(), "operation-log-cleanup", domain.Notification{
					RiskLevel: "high", Category: "system", Title: "操作日志清理失败",
					Message: "过期操作记录已清理，但部分 JSONL 日志文件删除失败", Link: "/dashboard",
				})
			}
		}
		if len(paths) < 500 {
			return
		}
	}
}

func (s *Service) checkDisk(ctx context.Context) {
	total, free, err := diskUsage(s.dataDir)
	if err != nil {
		s.log.Warn("check data disk usage", "error", err)
		return
	}
	const oneGiB = uint64(1 << 30)
	if free >= oneGiB && (total == 0 || free*100/total >= 10) {
		return
	}
	_ = s.store.CreateNotificationIfUnresolved(ctx, "disk-space", domain.Notification{
		RiskLevel: "high", Category: "system", Title: "数据目录空间不足",
		Message: "DP 数据目录可用空间低于 1 GiB 或 10%，请及时扩容或清理", Link: "/dashboard",
	})
}

func diskUsage(path string) (total uint64, free uint64, err error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize), nil
}

func riskLevel(event domain.AuditEvent) string {
	if event.Outcome == "denied" || strings.HasPrefix(event.Action, "account.password.reset") ||
		event.ErrorCode == "HOST_KEY_CHANGED" ||
		event.Action == "account.disable" || event.Action == "account.delete" ||
		event.Action == "account.resources.transfer" ||
		event.Action == "package.delete" || event.Action == "environment.delete" ||
		strings.HasSuffix(event.Action, ".export") ||
		(event.ActorUserID != "" && event.OwnerID != "" && event.ActorUserID != event.OwnerID &&
			(event.TargetType == "package" || event.TargetType == "environment" || event.TargetType == "service")) {
		return "high"
	}
	return "normal"
}

func truncate(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}
