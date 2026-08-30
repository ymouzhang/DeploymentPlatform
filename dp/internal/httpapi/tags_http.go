package httpapi

import (
	"net/http"
	"strings"

	"DP/internal/domain"
)

func tagLabels(tags []domain.ResourceTagRef) []string {
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		result = append(result, tag.GroupName+" / "+tag.Value)
	}
	return result
}

func requestedTagIDs(r *http.Request) ([]string, error) {
	values := r.URL.Query()["tag_id"]
	if len(values) > 20 {
		return nil, domain.FieldError("tag_id", "最多同时筛选 20 个标签")
	}
	seen, result := map[string]struct{}{}, make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, domain.FieldError("tag_id", "标签 ID 不能为空")
		}
		if _, exists := seen[value]; exists {
			return nil, domain.FieldError("tag_id", "标签 ID 不能重复")
		}
		seen[value], result = struct{}{}, append(result, value)
	}
	return result, nil
}

func (a *API) visibleTagIDs(r *http.Request, ownerID string) ([]string, error) {
	ids, err := requestedTagIDs(r)
	if err != nil || len(ids) == 0 {
		return ids, err
	}
	user := currentUser(r)
	for _, id := range ids {
		tag, err := a.store.GetResourceTag(r.Context(), id)
		if err != nil {
			return nil, domain.FieldError("tag_id", "标签不存在或不可用")
		}
		if user.Role != domain.RoleAdmin && tag.OwnerID != user.ID {
			return nil, domain.FieldError("tag_id", "标签不存在或不可用")
		}
		if ownerID != "" && tag.OwnerID != ownerID {
			return nil, domain.FieldError("tag_id", "标签不属于当前数据范围")
		}
	}
	return ids, nil
}

func (a *API) listResourceTags(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.listOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items, err := a.store.ListResourceTags(r.Context(), ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) createResourceTag(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.createOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var input domain.ResourceTagInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeError(w, r, err)
		return
	}
	item, err := a.store.CreateResourceTag(r.Context(), ownerID, input)
	if err != nil {
		if err == domain.ErrConflict {
			err = &domain.AppError{Code: "TAG_EXISTS", Message: "该账号下已存在相同标签"}
		}
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "tag", item.ID, item.GroupName+" / "+item.Value, map[string]any{"group_name": item.GroupName, "value": item.Value})
	writeData(w, http.StatusCreated, item)
}

func (a *API) authorizeResourceTag(r *http.Request, id string) (domain.ResourceTag, error) {
	item, err := a.store.GetResourceTag(r.Context(), id)
	if err != nil {
		return item, err
	}
	user := currentUser(r)
	if user.Role != domain.RoleAdmin && item.OwnerID != user.ID {
		return domain.ResourceTag{}, domain.ErrNotFound
	}
	return item, nil
}

func (a *API) updateResourceTag(w http.ResponseWriter, r *http.Request) {
	original, err := a.authorizeResourceTag(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var input domain.ResourceTagInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeError(w, r, err)
		return
	}
	item, err := a.store.UpdateResourceTag(r.Context(), original.ID, input)
	if err != nil {
		if err == domain.ErrConflict {
			err = &domain.AppError{Code: "TAG_EXISTS", Message: "该账号下已存在相同标签"}
		}
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), original.OwnerID)
	setAuditTarget(r, owner, "tag", item.ID, item.GroupName+" / "+item.Value, map[string]any{"before": map[string]string{"group_name": original.GroupName, "value": original.Value}, "after": map[string]string{"group_name": item.GroupName, "value": item.Value}})
	writeData(w, http.StatusOK, item)
}

func (a *API) deleteResourceTag(w http.ResponseWriter, r *http.Request) {
	item, err := a.authorizeResourceTag(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), item.OwnerID)
	setAuditTarget(r, owner, "tag", item.ID, item.GroupName+" / "+item.Value, map[string]any{"environment_count": item.EnvironmentCount})
	if err := a.store.DeleteResourceTag(r.Context(), item.ID); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"deleted": item.ID})
}

func (a *API) replaceEnvironmentTags(w http.ResponseWriter, r *http.Request) {
	env, err := a.authorizeEnvironment(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var input struct {
		TagIDs []string `json:"tag_ids"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	if len(input.TagIDs) > 20 {
		a.writeError(w, r, domain.FieldError("tag_ids", "每个环境最多关联 20 个标签"))
		return
	}
	if err := a.store.ValidateTagIDs(r.Context(), env.OwnerID, input.TagIDs); err != nil {
		a.writeError(w, r, err)
		return
	}
	if err := a.store.ReplaceEnvironmentTags(r.Context(), env.ID, input.TagIDs); err != nil {
		a.writeError(w, r, err)
		return
	}
	updated, err := a.store.GetEnvironment(r.Context(), env.ID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), env.OwnerID)
	setAuditTarget(r, owner, "environment", env.ID, env.Name, map[string]any{"before": tagLabels(env.Tags), "after": tagLabels(updated.Tags)})
	writeData(w, http.StatusOK, domain.EnvironmentView{Environment: updated, HasPassword: true})
}
