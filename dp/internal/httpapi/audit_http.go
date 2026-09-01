package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"DP/internal/domain"
)

type auditContextKey struct{}

type auditMetadata struct {
	action        string
	outcome       string
	errorCode     string
	actor         domain.User
	actorUsername string
	ownerID       string
	ownerUsername string
	targetType    string
	targetID      string
	targetLabel   string
	operationID   string
	changes       map[string]any
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (a *API) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		category, action, targetType, audited := auditAction(r.Method, r.URL.Path)
		if !audited || a.audit == nil {
			next.ServeHTTP(w, r)
			return
		}
		metadata := &auditMetadata{targetType: targetType, changes: map[string]any{}}
		ctx := context.WithValue(r.Context(), auditContextKey{}, metadata)
		wrapped := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r.WithContext(ctx))
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		if strings.HasSuffix(action, ".requested") && status < 400 {
			return
		}
		outcome := "success"
		if status == http.StatusForbidden {
			outcome = "denied"
		} else if status >= 400 {
			outcome = "failure"
		}
		if metadata.outcome != "" {
			outcome = metadata.outcome
		}
		requestedAction := firstNonEmpty(metadata.action, action)
		if outcome == "denied" && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			category = "authorization"
			metadata.action = "authorization.denied"
			metadata.changes["requested_action"] = requestedAction
			metadata.changes["path"] = r.URL.Path
		}
		actor := metadata.actor
		if actor.ID == "" {
			actor = currentUser(r)
		}
		username := metadata.actorUsername
		if username == "" {
			username = actor.Username
		}
		ownerID, ownerUsername := metadata.ownerID, metadata.ownerUsername
		if ownerID == "" && actor.ID != "" {
			ownerID, ownerUsername = actor.ID, actor.Username
		}
		event := domain.AuditEvent{
			Category: category, Action: firstNonEmpty(metadata.action, action), Outcome: outcome,
			ActorUserID: actor.ID, ActorUsername: username, ActorRoles: roleKeys(actor),
			OwnerID: ownerID, OwnerUsername: ownerUsername,
			TargetType: metadata.targetType, TargetID: metadata.targetID,
			TargetLabel: metadata.targetLabel, RequestID: requestID(ctx),
			OperationID: metadata.operationID, SourceIP: a.sourceIP(r),
			UserAgent: r.UserAgent(), Changes: metadata.changes,
		}
		if metadata.errorCode != "" {
			event.ErrorCode = metadata.errorCode
		} else if status >= 400 {
			event.ErrorCode = "HTTP_" + strconv.Itoa(status)
		}
		a.audit.Record(context.Background(), event)
	})
}

func auditAction(method, path string) (category, action, targetType string, ok bool) {
	switch {
	case method == http.MethodPost && path == "/api/v1/auth/login":
		return "authentication", "auth.login", "session", true
	case method == http.MethodPost && path == "/api/v1/auth/logout":
		return "authentication", "auth.logout", "session", true
	case method == http.MethodPut && path == "/api/v1/auth/password":
		return "authentication", "auth.password.change", "user", true
	case method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/auth/sessions/"):
		return "authentication", "auth.session.revoke", "session", true
	case method == http.MethodPost && path == "/api/v1/users":
		return "account", "account.create", "user", true
	case method == http.MethodPost && path == "/api/v1/roles":
		return "authorization", "role.create", "role", true
	case method == http.MethodPut && strings.HasPrefix(path, "/api/v1/roles/"):
		return "authorization", "role.update", "role", true
	case method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/roles/"):
		return "authorization", "role.delete", "role", true
	case method == http.MethodPut && strings.HasPrefix(path, "/api/v1/users/") && strings.HasSuffix(path, "/roles"):
		return "authorization", "account.role.update", "user", true
	case method == http.MethodPost && path == "/api/v1/communications":
		return "communication", "communication.create", "communication", true
	case method == http.MethodPut && strings.HasPrefix(path, "/api/v1/communications/") && strings.HasSuffix(path, "/read"):
		return "communication", "communication.read", "communication", true
	case method == http.MethodPost && strings.HasPrefix(path, "/api/v1/communications/") && strings.HasSuffix(path, "/messages"):
		return "communication", "communication.message.send", "communication", true
	case method == http.MethodPost && strings.HasPrefix(path, "/api/v1/communications/") && strings.HasSuffix(path, "/close"):
		return "communication", "communication.close", "communication", true
	case method == http.MethodPost && strings.HasPrefix(path, "/api/v1/communications/") && strings.HasSuffix(path, "/reopen"):
		return "communication", "communication.reopen", "communication", true
	case method == http.MethodPut && strings.HasPrefix(path, "/api/v1/users/") && strings.HasSuffix(path, "/password"):
		return "account", "account.password.reset", "user", true
	case method == http.MethodPut && strings.HasPrefix(path, "/api/v1/users/") && strings.HasSuffix(path, "/status"):
		return "account", "account.status.update", "user", true
	case method == http.MethodDelete && strings.Contains(path, "/sessions/") && strings.HasPrefix(path, "/api/v1/users/"):
		return "account", "account.session.revoke", "session", true
	case method == http.MethodPost && strings.HasSuffix(path, "/sessions/revoke") && strings.HasPrefix(path, "/api/v1/users/"):
		return "account", "account.sessions.revoke", "user", true
	case method == http.MethodPost && strings.HasSuffix(path, "/transfer") && strings.HasPrefix(path, "/api/v1/users/"):
		return "account", "account.resources.transfer", "user", true
	case method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/users/"):
		return "account", "account.delete", "user", true
	case method == http.MethodPost && path == "/api/v1/environments":
		return "environment", "environment.create", "environment", true
	case method == http.MethodPost && path == "/api/v1/tags":
		return "environment", "tag.create", "tag", true
	case method == http.MethodPut && strings.HasPrefix(path, "/api/v1/tags/"):
		return "environment", "tag.update", "tag", true
	case method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/tags/"):
		return "environment", "tag.delete", "tag", true
	case method == http.MethodPut && strings.HasPrefix(path, "/api/v1/environments/"):
		return "environment", "environment.update", "environment", true
	case method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/environments/"):
		return "environment", "environment.delete", "environment", true
	case method == http.MethodPost && strings.HasSuffix(path, "/validate") && strings.Contains(path, "/environments"):
		return "environment", "environment.validate", "environment", true
	case method == http.MethodPost && path == "/api/v1/environments/import":
		return "environment", "environment.import", "environment", true
	case method == http.MethodGet && path == "/api/v1/environments/export":
		return "environment", "environment.export", "environment_export", true
	case method == http.MethodPut && strings.Contains(path, "/service-types/") && strings.HasSuffix(path, "/package"):
		return "package", "package.update", "package", true
	case method == http.MethodPut && strings.Contains(path, "/package/versions/") && strings.HasSuffix(path, "/current"):
		return "package", "package.version.activate", "package", true
	case method == http.MethodDelete && strings.Contains(path, "/package/versions/"):
		return "package", "package.version.delete", "package_version", true
	case method == http.MethodDelete && strings.Contains(path, "/service-types/") && strings.HasSuffix(path, "/package"):
		return "package", "package.delete", "package", true
	case method == http.MethodPost && path == "/api/v1/model-uploads":
		return "model", "model.upload.create", "model", true
	case method == http.MethodPost && strings.HasPrefix(path, "/api/v1/model-uploads/") && strings.HasSuffix(path, "/complete"):
		return "model", "model.deploy.request", "model", true
	case method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/model-uploads/"):
		return "model", "model.upload.cancel", "model", true
	case method == http.MethodPost && strings.HasPrefix(path, "/api/v1/models/") && strings.HasSuffix(path, "/retry"):
		return "model", "model.deploy.retry", "model", true
	case method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/models/"):
		return "model", "model.delete.request", "model", true
	case method == http.MethodPut && strings.HasSuffix(path, "/config") && strings.Contains(path, "/services/"):
		return "service", "service.config.update", "service", true
	case method == http.MethodPost && strings.Contains(path, "/config/revisions/") && strings.HasSuffix(path, "/rollback"):
		return "service", "service.config.rollback", "service", true
	case method == http.MethodPost && strings.HasSuffix(path, "/health-check"):
		return "service", "service.health_check", "service", true
	case method == http.MethodPost && strings.Contains(path, "/services/") && strings.HasSuffix(path, "/install"):
		return "service", "service.install.requested", "service", true
	case method == http.MethodPost && strings.Contains(path, "/services/") && strings.HasSuffix(path, "/start"):
		return "service", "service.start.requested", "service", true
	case method == http.MethodPost && strings.Contains(path, "/services/") && strings.HasSuffix(path, "/stop"):
		return "service", "service.stop.requested", "service", true
	case method == http.MethodPost && strings.Contains(path, "/services/") && strings.HasSuffix(path, "/reset"):
		return "service", "service.reset.requested", "service", true
	case method == http.MethodGet && path == "/api/v1/audit-events/export":
		return "audit", "audit.export", "audit_export", true
	case method == http.MethodGet && strings.HasPrefix(path, "/api/v1/audit-events/") && path != "/api/v1/audit-events/summary":
		return "audit", "audit.detail.view", "audit_event", true
	default:
		return "", "", "", false
	}
}

func auditMeta(r *http.Request) *auditMetadata {
	metadata, _ := r.Context().Value(auditContextKey{}).(*auditMetadata)
	return metadata
}

func setAuditActor(r *http.Request, user domain.User, attemptedUsername string) {
	if metadata := auditMeta(r); metadata != nil {
		metadata.actor, metadata.actorUsername = user, attemptedUsername
	}
}

func setAuditAction(r *http.Request, action string) {
	if metadata := auditMeta(r); metadata != nil {
		metadata.action = action
	}
}

func setAuditOutcome(r *http.Request, outcome, errorCode string) {
	if metadata := auditMeta(r); metadata != nil {
		metadata.outcome, metadata.errorCode = outcome, errorCode
	}
}

func setAuditTarget(r *http.Request, owner domain.User, targetType, id, label string, changes map[string]any) {
	if metadata := auditMeta(r); metadata != nil {
		metadata.ownerID, metadata.ownerUsername = owner.ID, owner.Username
		metadata.targetType, metadata.targetID, metadata.targetLabel = targetType, id, label
		if changes != nil {
			permission := metadata.changes["permission"]
			metadata.changes = changes
			if permission != nil {
				metadata.changes["permission"] = permission
			}
		}
	}
}

func (a *API) sourceIP(r *http.Request) string {
	direct := parseAddress(r.RemoteAddr)
	if !direct.IsValid() {
		return ""
	}
	if !a.isTrustedProxy(r.RemoteAddr) {
		return direct.String()
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := parseAddress(strings.TrimSpace(parts[i]))
		if !candidate.IsValid() {
			continue
		}
		trusted := false
		for _, prefix := range a.trustedProxies {
			if prefix.Contains(candidate) {
				trusted = true
				break
			}
		}
		if !trusted {
			return candidate.String()
		}
	}
	return direct.String()
}

func (a *API) isTrustedProxy(remoteAddr string) bool {
	direct := parseAddress(remoteAddr)
	if !direct.IsValid() {
		return false
	}
	for _, prefix := range a.trustedProxies {
		if prefix.Contains(direct) {
			return true
		}
	}
	return false
}

func parseAddress(value string) netip.Addr {
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	address, _ := netip.ParseAddr(value)
	return address.Unmap()
}

func (a *API) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	filter, err := parseAuditFilter(r, true)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	events, err := a.store.ListAuditEvents(r.Context(), filter)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var next string
	if len(events) > filter.Limit {
		events = events[:filter.Limit]
		last := events[len(events)-1]
		next = encodeAuditCursor(last.OccurredAt, last.ID)
	}
	writeData(w, http.StatusOK, map[string]any{"items": events, "next_cursor": next})
}

func (a *API) auditSummary(w http.ResponseWriter, r *http.Request) {
	filter, err := parseAuditFilter(r, false)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	summary, err := a.store.AuditSummary(r.Context(), filter)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, summary)
}

func (a *API) getAuditEvent(w http.ResponseWriter, r *http.Request) {
	event, err := a.store.GetAuditEvent(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "audit_event", event.ID, event.Action, nil)
	writeData(w, http.StatusOK, event)
}

func (a *API) exportAuditEvents(w http.ResponseWriter, r *http.Request) {
	filter, err := parseAuditFilter(r, false)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if filter.From == nil || filter.To == nil || filter.To.Sub(*filter.From) > 31*24*time.Hour {
		a.writeError(w, r, domain.FieldError("time_range", "审计导出时间范围不能超过 31 天"))
		return
	}
	count, err := a.store.CountAuditEvents(r.Context(), filter)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if count > a.auditExportMax {
		a.writeError(w, r, &domain.AppError{Code: "AUDIT_EXPORT_TOO_LARGE", Message: "审计记录过多，请缩小导出范围"})
		return
	}
	setAuditTarget(r, currentUser(r), "audit_export", "", fmt.Sprintf("%d 条审计记录", count), map[string]any{"count": count})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="dp-audit-`+time.Now().Format("20060102-150405")+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"时间", "操作账号", "角色", "事件", "操作对象", "所属账号", "来源 IP", "结果", "风险", "请求 ID", "操作 ID", "错误码"})
	for {
		filter.Limit = 200
		events, listErr := a.store.ListAuditEvents(r.Context(), filter)
		if listErr != nil {
			return
		}
		if len(events) > 200 {
			events = events[:200]
		}
		for _, event := range events {
			_ = writer.Write([]string{event.OccurredAt.Format(time.RFC3339), csvCell(event.ActorUsername), csvCell(strings.Join(event.ActorRoles, "|")), csvCell(event.Action), csvCell(event.TargetLabel), csvCell(event.OwnerUsername), csvCell(event.SourceIP), event.Outcome, event.RiskLevel, csvCell(event.RequestID), csvCell(event.OperationID), csvCell(event.ErrorCode)})
		}
		if len(events) < 200 {
			break
		}
		last := events[len(events)-1]
		filter.CursorTime, filter.CursorID = &last.OccurredAt, last.ID
	}
	writer.Flush()
}

func parseAuditFilter(r *http.Request, cursor bool) (domain.AuditFilter, error) {
	query := r.URL.Query()
	filter := domain.AuditFilter{ActorID: query.Get("actor_id"), OwnerID: query.Get("owner_id"), Category: query.Get("category"), Action: query.Get("action"), Outcome: query.Get("outcome"), SourceIP: query.Get("source_ip"), Keyword: strings.TrimSpace(query.Get("keyword")), Limit: 50}
	now := time.Now().UTC()
	from, to := now.Add(-24*time.Hour), now
	var err error
	if value := query.Get("from"); value != "" {
		from, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return filter, domain.FieldError("from", "开始时间格式不正确")
		}
	}
	if value := query.Get("to"); value != "" {
		to, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return filter, domain.FieldError("to", "结束时间格式不正确")
		}
	}
	if to.Before(from) {
		return filter, domain.FieldError("time_range", "结束时间不能早于开始时间")
	}
	filter.From, filter.To = &from, &to
	if value := query.Get("limit"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 200 {
			return filter, domain.FieldError("limit", "limit 必须为 1 到 200")
		}
		filter.Limit = parsed
	}
	if cursor && query.Get("cursor") != "" {
		cursorTime, cursorID, decodeErr := decodeAuditCursor(query.Get("cursor"))
		if decodeErr != nil {
			return filter, domain.FieldError("cursor", "分页游标无效")
		}
		filter.CursorTime, filter.CursorID = &cursorTime, cursorID
	}
	return filter, nil
}

func encodeAuditCursor(occurred time.Time, id string) string {
	data, _ := json.Marshal([]string{occurred.Format(time.RFC3339Nano), id})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeAuditCursor(value string) (time.Time, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", err
	}
	var parts []string
	if err := json.Unmarshal(data, &parts); err != nil || len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	parsed, err := time.Parse(time.RFC3339Nano, parts[0])
	return parsed, parts[1], err
}

func csvCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
