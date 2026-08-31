package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"DP/internal/domain"
)

func (a *API) listModels(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.listOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items, err := a.models.List(r.Context(), ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) getModel(w http.ResponseWriter, r *http.Request) {
	item, err := a.authorizeModel(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) createModelUpload(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.createOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, err := a.store.GetUser(r.Context(), ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var input domain.ModelUploadCreateInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	created, err := a.models.CreateUpload(r.Context(), owner, currentUser(r), input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, owner, "model", created.Model.ID, created.Model.Name, map[string]any{
		"environment_id": created.Model.EnvironmentID, "target_dir": created.Model.TargetDir,
		"size_bytes": created.Model.SizeBytes, "source": created.Model.Source,
	})
	writeData(w, http.StatusCreated, created)
}

func (a *API) headModelUpload(w http.ResponseWriter, r *http.Request) {
	upload, err := a.store.GetModelUpload(r.Context(), r.PathValue("id"))
	if err == nil {
		err = a.authorizeOwner(r, upload.OwnerID)
	}
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	upload, err = a.models.UploadOffset(r.Context(), upload.ID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(upload.Offset, 10))
	w.Header().Set("Upload-Length", strconv.FormatInt(upload.TotalBytes, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) patchModelUpload(w http.ResponseWriter, r *http.Request) {
	upload, err := a.store.GetModelUpload(r.Context(), r.PathValue("id"))
	if err == nil {
		err = a.authorizeOwner(r, upload.OwnerID)
	}
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 {
		a.writeError(w, r, domain.FieldError("Upload-Offset", "Upload-Offset 必须是非负整数"))
		return
	}
	if r.ContentLength <= 0 {
		a.writeError(w, r, domain.FieldError("chunk", "分片请求必须提供 Content-Length"))
		return
	}
	next, err := a.models.AppendChunk(r.Context(), upload.ID, offset, r.ContentLength, r.Body)
	w.Header().Set("Upload-Offset", strconv.FormatInt(next, 10))
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) completeModelUpload(w http.ResponseWriter, r *http.Request) {
	upload, err := a.store.GetModelUpload(r.Context(), r.PathValue("id"))
	if err == nil {
		err = a.authorizeOwner(r, upload.OwnerID)
	}
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	task, err := a.models.CompleteUpload(r.Context(), upload.ID, currentUser(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	model, _ := a.models.Get(r.Context(), upload.ModelID)
	owner, _ := a.store.GetUser(r.Context(), upload.OwnerID)
	setAuditTarget(r, owner, "model", model.ID, model.Name, map[string]any{"task_id": task.ID, "action": "deploy"})
	writeData(w, http.StatusAccepted, task)
}

func (a *API) cancelModelUpload(w http.ResponseWriter, r *http.Request) {
	upload, err := a.store.GetModelUpload(r.Context(), r.PathValue("id"))
	if err == nil {
		err = a.authorizeOwner(r, upload.OwnerID)
	}
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	model, _ := a.models.Get(r.Context(), upload.ModelID)
	owner, _ := a.store.GetUser(r.Context(), upload.OwnerID)
	setAuditTarget(r, owner, "model", model.ID, model.Name, map[string]any{"upload_id": upload.ID})
	if err := a.models.CancelUpload(r.Context(), upload.ID); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"cancelled": upload.ID})
}

func (a *API) retryModel(w http.ResponseWriter, r *http.Request) {
	item, err := a.authorizeModel(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), item.OwnerID)
	setAuditTarget(r, owner, "model", item.ID, item.Name, map[string]any{"target_dir": item.TargetDir})
	task, err := a.models.Retry(r.Context(), item.ID, currentUser(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusAccepted, task)
}

func (a *API) deleteModel(w http.ResponseWriter, r *http.Request) {
	item, err := a.authorizeModel(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var input struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	if input.ConfirmName != item.Name {
		a.writeError(w, r, domain.FieldError("confirm_name", "请输入完整模型名称确认删除"))
		return
	}
	task, err := a.models.Delete(r.Context(), item.ID, currentUser(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), item.OwnerID)
	setAuditTarget(r, owner, "model", item.ID, item.Name, map[string]any{"target_dir": item.TargetDir})
	if task.ID == "" {
		writeData(w, http.StatusOK, map[string]string{"deleted": item.ID})
		return
	}
	writeData(w, http.StatusAccepted, task)
}

func (a *API) getModelTask(w http.ResponseWriter, r *http.Request) {
	task, err := a.authorizeModelTask(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, task)
}

func (a *API) modelTaskEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.writeError(w, r, errors.New("streaming is not supported"))
		return
	}
	task, err := a.authorizeModelTask(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	lastID, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	ticker := time.NewTicker(250 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()
	terminalEmpty := 0
	for {
		events, readErr := a.models.ReadEvents(task, lastID)
		if readErr != nil {
			return
		}
		for _, event := range events {
			content, _ := json.Marshal(event)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, content)
			lastID = event.Seq
		}
		if len(events) > 0 {
			flusher.Flush()
			terminalEmpty = 0
		}
		task, err = a.models.GetTask(r.Context(), task.ID)
		if err != nil {
			return
		}
		if isTerminal(task.Status) && len(events) == 0 {
			terminalEmpty++
			if terminalEmpty >= 2 {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-ticker.C:
		}
	}
}

func (a *API) authorizeOwner(r *http.Request, ownerID string) error {
	user := currentUser(r)
	if user.Role != domain.RoleAdmin && user.ID != ownerID {
		return domain.ErrNotFound
	}
	return nil
}

func (a *API) authorizeModel(r *http.Request, id string) (domain.Model, error) {
	item, err := a.models.Get(r.Context(), id)
	if err != nil {
		return item, err
	}
	if err := a.authorizeOwner(r, item.OwnerID); err != nil {
		return domain.Model{}, err
	}
	return item, nil
}

func (a *API) authorizeModelTask(r *http.Request, id string) (domain.ModelTask, error) {
	task, err := a.models.GetTask(r.Context(), id)
	if err != nil {
		return task, err
	}
	if err := a.authorizeOwner(r, task.OwnerID); err != nil {
		return domain.ModelTask{}, err
	}
	return task, nil
}
