package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"DP/internal/access"
	"DP/internal/domain"
)

func (a *API) communicationSummary(w http.ResponseWriter, r *http.Request) {
	result, err := a.communications.Summary(r.Context(), currentUser(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

func (a *API) listCommunications(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := domain.CommunicationFilter{TargetUserID: strings.TrimSpace(query.Get("target_user_id")),
		Status: domain.CommunicationStatus(query.Get("status")), Keyword: strings.TrimSpace(query.Get("keyword")), Limit: 30}
	if filter.Status != "" && filter.Status != domain.CommunicationOpen && filter.Status != domain.CommunicationClosed {
		a.writeError(w, r, domain.FieldError("status", "事项状态必须是 open 或 closed"))
		return
	}
	if value := query.Get("unread"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			a.writeError(w, r, domain.FieldError("unread", "unread 必须是布尔值"))
			return
		}
		filter.Unread = &parsed
	}
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			a.writeError(w, r, domain.FieldError("limit", "limit 必须在 1–100 之间"))
			return
		}
		filter.Limit = parsed
	}
	if value := query.Get("cursor"); value != "" {
		parsed, id, err := decodeAuditCursor(value)
		if err != nil {
			a.writeError(w, r, domain.FieldError("cursor", "分页游标无效"))
			return
		}
		filter.CursorTime, filter.CursorID = &parsed, id
	}
	items, err := a.communications.List(r.Context(), currentUser(r), filter)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	next := ""
	if len(items) > filter.Limit {
		last := items[filter.Limit-1]
		next = encodeAuditCursor(last.UpdatedAt, last.ID)
		items = items[:filter.Limit]
	}
	writeData(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (a *API) createCommunication(w http.ResponseWriter, r *http.Request) {
	var input domain.CommunicationCreateInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "communication", "", strings.TrimSpace(input.Title), map[string]any{
		"target_user_id": input.TargetUserID, "resource_count": len(input.Resources), "content_length": len([]rune(input.Content)),
	})
	item, err := a.communications.Create(r.Context(), currentUser(r), input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	target := domain.User{ID: item.TargetUserID, Username: item.TargetUsername}
	setAuditTarget(r, target, "communication", item.ID, item.Title, map[string]any{
		"target_user_id": item.TargetUserID, "resource_count": len(item.Resources), "content_length": len([]rune(input.Content)),
	})
	writeData(w, http.StatusCreated, item)
}

func (a *API) getCommunication(w http.ResponseWriter, r *http.Request) {
	item, err := a.communications.Get(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) markCommunicationRead(w http.ResponseWriter, r *http.Request) {
	item, err := a.communications.MarkRead(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, domain.User{ID: item.TargetUserID, Username: item.TargetUsername}, "communication", item.ID, item.Title, map[string]any{"read": true})
	writeData(w, http.StatusOK, item)
}

func (a *API) sendCommunicationMessage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	item, err := a.communications.Get(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if scope, _ := currentUser(r).Permissions.Scope(access.CommunicationReply); scope == access.ScopeAll {
		setAuditAction(r, "communication.message.admin.send")
	} else {
		setAuditAction(r, "communication.receipt.user.send")
	}
	setAuditTarget(r, domain.User{ID: item.TargetUserID, Username: item.TargetUsername}, "communication", item.ID, item.Title,
		map[string]any{"content_length": len([]rune(input.Content))})
	message, err := a.communications.Send(r.Context(), currentUser(r), item.ID, input.Content)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, message)
}

func (a *API) closeCommunication(w http.ResponseWriter, r *http.Request) {
	a.changeCommunicationState(w, r, false)
}

func (a *API) reopenCommunication(w http.ResponseWriter, r *http.Request) {
	a.changeCommunicationState(w, r, true)
}

func (a *API) changeCommunicationState(w http.ResponseWriter, r *http.Request, reopen bool) {
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	item, err := a.communications.Get(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if reopen {
		setAuditAction(r, "communication.reopen")
	} else {
		setAuditAction(r, "communication.close")
	}
	setAuditTarget(r, domain.User{ID: item.TargetUserID, Username: item.TargetUsername}, "communication", item.ID, item.Title,
		map[string]any{"reopen": reopen, "content_length": len([]rune(input.Content))})
	updated, err := a.communications.ChangeState(r.Context(), currentUser(r), item.ID, reopen, input.Content)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, updated)
}
