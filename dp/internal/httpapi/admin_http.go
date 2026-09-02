package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"DP/internal/access"
	"DP/internal/application"
	"DP/internal/domain"
)

func (a *API) getUserDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := a.auth.UserDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, detail)
}

func (a *API) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	target, _ := a.store.GetUser(r.Context(), r.PathValue("id"))
	if target.ID != "" {
		setAuditTarget(r, target, "user", target.ID, target.Username, map[string]any{"sessions_revoked": true})
	}
	if err := a.auth.RevokeSessions(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"sessions_revoked": true})
}

func (a *API) listUserSessions(w http.ResponseWriter, r *http.Request) {
	items, err := a.auth.ListSessions(r.Context(), r.PathValue("id"), currentSession(r).ID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) revokeUserSession(w http.ResponseWriter, r *http.Request) {
	target, err := a.store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	sessionID := r.PathValue("sessionId")
	setAuditTarget(r, target, "session", sessionID, target.Username, map[string]any{"session_revoked": true})
	if err := a.auth.RevokeUserSession(r.Context(), currentUser(r), currentSession(r).ID, target.ID, sessionID); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"session_revoked": true})
}

func (a *API) transferUserResources(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TargetUserID string `json:"target_user_id"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	source, err := a.store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	target, err := a.store.GetUser(r.Context(), input.TargetUserID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditAction(r, "account.resources.transfer")
	setAuditTarget(r, source, "user", source.ID, source.Username, map[string]any{"target_user_id": target.ID, "target_username": target.Username})
	result, err := a.packages.TransferOwner(r.Context(), source.ID, target.ID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, source, "user", source.ID, source.Username, map[string]any{"target_user_id": target.ID, "target_username": target.Username, "packages": result.Packages, "hosts": result.Hosts, "service_instances": result.ServiceInstances, "models": result.Models})
	writeData(w, http.StatusOK, result)
}

func (a *API) listOperations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := domain.OperationFilter{ActorID: q.Get("actor_id"), OwnerID: q.Get("owner_id"), Action: q.Get("action"), Status: q.Get("status"), Keyword: strings.TrimSpace(q.Get("keyword")), Limit: 50}
	user := currentUser(r)
	scope, ok := user.Permissions.Scope(access.OperationRead)
	if !ok {
		a.writeError(w, r, domain.ErrForbidden)
		return
	}
	tagOwnerID := filter.OwnerID
	if scope == access.ScopeOwn {
		filter.ActorID = ""
		filter.OwnerID = ""
		filter.SubjectID = user.ID
		tagOwnerID = user.ID
	}
	tagIDs, tagErr := a.visibleTagIDs(r, tagOwnerID, access.OperationRead)
	if tagErr != nil {
		a.writeError(w, r, tagErr)
		return
	}
	filter.TagIDs = tagIDs
	if filter.Action != "" && filter.Action != "install" && filter.Action != "start" && filter.Action != "stop" && filter.Action != "reset" {
		a.writeError(w, r, domain.FieldError("action", "不支持的操作类型"))
		return
	}
	if filter.Status != "" && filter.Status != "queued" && filter.Status != "running" && filter.Status != "succeeded" && filter.Status != "failed" && filter.Status != "timed_out" && filter.Status != "interrupted" {
		a.writeError(w, r, domain.FieldError("status", "不支持的操作状态"))
		return
	}
	var err error
	if value := q.Get("from"); value != "" {
		parsed, e := time.Parse(time.RFC3339, value)
		err = e
		filter.From = &parsed
	}
	if err == nil {
		if value := q.Get("to"); value != "" {
			parsed, e := time.Parse(time.RFC3339, value)
			err = e
			filter.To = &parsed
		}
	}
	if err != nil {
		a.writeError(w, r, domain.FieldError("time_range", "时间格式不正确"))
		return
	}
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		a.writeError(w, r, domain.FieldError("time_range", "结束时间不能早于开始时间"))
		return
	}
	if value := q.Get("limit"); value != "" {
		parsed, e := strconv.Atoi(value)
		if e != nil || parsed < 1 || parsed > 200 {
			a.writeError(w, r, domain.FieldError("limit", "limit 必须为 1 到 200"))
			return
		}
		filter.Limit = parsed
	}
	if value := q.Get("cursor"); value != "" {
		parsed, id, e := decodeAuditCursor(value)
		if e != nil {
			a.writeError(w, r, domain.FieldError("cursor", "分页游标无效"))
			return
		}
		filter.CursorTime, filter.CursorID = &parsed, id
	}
	items, err := a.store.ListOperations(r.Context(), filter)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	next := ""
	if len(items) > filter.Limit {
		items = items[:filter.Limit]
		last := items[len(items)-1]
		next = encodeAuditCursor(last.CreatedAt, last.ID)
	}
	writeData(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (a *API) adminDashboard(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	tagIDs, err := a.visibleTagIDs(r, "", access.DashboardRead)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	metrics, err := a.store.DashboardMetrics(r.Context(), now.Add(-24*time.Hour), now.Add(-30*24*time.Hour))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	instances, err := a.store.ListServiceInstances(r.Context())
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if len(tagIDs) > 0 {
		instances = application.FilterServiceInstancesByTagIDs(instances, tagIDs)
		metrics.ServiceInstances, metrics.InstalledServices = len(instances), 0
		for _, instance := range instances {
			if instance.Installed {
				metrics.InstalledServices++
			}
		}
		metrics.ActiveOperations, metrics.FailedOperations24h, err = a.store.CountOperationsByTags(r.Context(), tagIDs, now.Add(-24*time.Hour))
		if err != nil {
			a.writeError(w, r, err)
			return
		}
	}
	metrics.RunningServices, metrics.UnhealthyInstalledServices = 0, 0
	for _, instance := range instances {
		if !instance.Installed {
			continue
		}
		health := a.health.Snapshot(instance.ID)
		if health.Status == "ok" {
			metrics.RunningServices++
		} else {
			metrics.UnhealthyInstalledServices++
		}
	}
	items, err := a.store.RecentNotifications(r.Context(), 8)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	actor := currentUser(r)
	communicationSummary, err := a.communications.Summary(r.Context(), actor)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	unread := true
	communications, err := a.communications.List(r.Context(), actor, domain.CommunicationFilter{Unread: &unread, Limit: 5})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if len(communications) > 5 {
		communications = communications[:5]
	}
	metrics.UnreadCommunications = communicationSummary.Unread
	writeData(w, http.StatusOK, domain.AdminDashboard{Metrics: metrics, Communications: communications, Notifications: items})
}

func (a *API) notificationSummary(w http.ResponseWriter, r *http.Request) {
	result, err := a.store.NotificationSummary(r.Context())
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

func (a *API) listNotifications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := domain.NotificationFilter{RiskLevel: q.Get("risk_level"), Limit: 30}
	if filter.RiskLevel != "" && filter.RiskLevel != "normal" && filter.RiskLevel != "high" {
		a.writeError(w, r, domain.FieldError("risk_level", "风险级别必须是 normal 或 high"))
		return
	}
	if value := q.Get("unread"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			a.writeError(w, r, domain.FieldError("unread", "unread 必须为布尔值"))
			return
		}
		filter.Unread = &parsed
	}
	if value := q.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			a.writeError(w, r, domain.FieldError("limit", "limit 必须为 1 到 100"))
			return
		}
		filter.Limit = parsed
	}
	if value := q.Get("cursor"); value != "" {
		parsed, id, err := decodeAuditCursor(value)
		if err != nil {
			a.writeError(w, r, domain.FieldError("cursor", "分页游标无效"))
			return
		}
		filter.CursorTime, filter.CursorID = &parsed, id
	}
	items, err := a.store.ListNotifications(r.Context(), filter)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	next := ""
	if len(items) > filter.Limit {
		items = items[:filter.Limit]
		last := items[len(items)-1]
		next = encodeAuditCursor(last.CreatedAt, last.ID)
	}
	writeData(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (a *API) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	a.updateNotification(w, r, false)
}
func (a *API) resolveNotification(w http.ResponseWriter, r *http.Request) {
	a.updateNotification(w, r, true)
}
func (a *API) updateNotification(w http.ResponseWriter, r *http.Request, resolve bool) {
	var item domain.Notification
	var err error
	now := time.Now().UTC()
	if resolve {
		item, err = a.store.ResolveNotification(r.Context(), r.PathValue("id"), currentUser(r).ID, now)
	} else {
		item, err = a.store.MarkNotificationRead(r.Context(), r.PathValue("id"), currentUser(r).ID, now)
	}
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}
