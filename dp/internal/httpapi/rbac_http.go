package httpapi

import (
	"net/http"
	"strings"

	"DP/internal/application"
	"DP/internal/domain"
	"github.com/google/uuid"
)

func (a *API) listPermissions(w http.ResponseWriter, r *http.Request) {
	items, err := a.roles.ListPermissions(r.Context(), currentSubject(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) listRoles(w http.ResponseWriter, r *http.Request) {
	items, err := a.roles.ListRoles(r.Context(), currentSubject(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) getRole(w http.ResponseWriter, r *http.Request) {
	id, err := rolePathID(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	item, err := a.roles.GetRole(r.Context(), currentSubject(r), id)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) createRole(w http.ResponseWriter, r *http.Request) {
	var input application.RoleCreateInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "role", "", strings.TrimSpace(input.Name), map[string]any{
		"key": input.Key, "grants": input.Grants,
	})
	item, err := a.roles.CreateRole(r.Context(), currentSubject(r), input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "role", item.ID, item.Name, map[string]any{
		"key": item.Key, "grants": item.Grants,
	})
	writeData(w, http.StatusCreated, item)
}

func (a *API) updateRole(w http.ResponseWriter, r *http.Request) {
	id, err := rolePathID(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var input application.RoleUpdateInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "role", id, strings.TrimSpace(input.Name), map[string]any{
		"grants": input.Grants,
	})
	item, err := a.roles.UpdateRole(r.Context(), currentSubject(r), id, input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) deleteRole(w http.ResponseWriter, r *http.Request) {
	id, err := rolePathID(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "role", id, "", nil)
	if err := a.roles.DeleteRole(r.Context(), currentSubject(r), id); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"deleted": id})
}

func (a *API) replaceUserRoles(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if _, err := uuid.Parse(userID); err != nil {
		a.writeError(w, r, domain.FieldError("id", "账号 ID 格式不正确"))
		return
	}
	var input struct {
		RoleIDs []string `json:"role_ids"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "user", userID, "", map[string]any{"role_ids": input.RoleIDs})
	if err := a.roles.ReplaceUserRoles(r.Context(), currentSubject(r), userID, input.RoleIDs); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"user_id": userID, "role_ids": input.RoleIDs})
}

func rolePathID(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		return "", domain.FieldError("id", "角色 ID 格式不正确")
	}
	return id, nil
}
