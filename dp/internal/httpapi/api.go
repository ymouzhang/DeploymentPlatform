package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"DP/internal/application"
	"DP/internal/archive"
	"DP/internal/audit"
	"DP/internal/domain"
	"DP/internal/health"
	modelmanager "DP/internal/model"
	"DP/internal/operation"
	"DP/internal/realtime"
	"DP/internal/store"
)

type API struct {
	auth           *application.AuthService
	communications *application.CommunicationService
	realtime       *realtime.Hub
	realtimeBeat   time.Duration
	environments   *application.EnvironmentService
	configs        *application.ServiceConfigService
	serviceLogs    *application.ServiceLogService
	packages       *archive.Manager
	operations     *operation.Manager
	models         *modelmanager.Manager
	health         *health.Monitor
	store          *store.Store
	audit          *audit.Service
	uploadMax      int64
	auditExportMax int
	trustedProxies []netip.Prefix
	log            *slog.Logger
}

func New(
	authService *application.AuthService,
	communicationService *application.CommunicationService,
	realtimeHub *realtime.Hub,
	environments *application.EnvironmentService,
	configs *application.ServiceConfigService,
	serviceLogs *application.ServiceLogService,
	packages *archive.Manager,
	operations *operation.Manager,
	models *modelmanager.Manager,
	healthMonitor *health.Monitor,
	store *store.Store,
	auditService *audit.Service,
	uploadMax int64,
	auditExportMax int,
	trustedProxyCIDRs string,
	log *slog.Logger,
) *API {
	trustedProxies := make([]netip.Prefix, 0)
	for _, value := range strings.Split(trustedProxyCIDRs, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			log.Warn("ignore invalid trusted proxy CIDR", "cidr", value)
			continue
		}
		trustedProxies = append(trustedProxies, prefix)
	}
	return &API{
		auth: authService, communications: communicationService, realtime: realtimeHub, realtimeBeat: 15 * time.Second,
		environments: environments, configs: configs, serviceLogs: serviceLogs,
		packages: packages, operations: operations, models: models,
		health: healthMonitor, store: store, audit: auditService, uploadMax: uploadMax,
		auditExportMax: auditExportMax, trustedProxies: trustedProxies, log: log,
	}
}

func (a *API) Handler(frontend fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.HandleFunc("GET /api/v1/auth/me", a.me)
	mux.HandleFunc("PUT /api/v1/auth/password", a.changePassword)
	mux.HandleFunc("GET /api/v1/auth/sessions", a.listOwnSessions)
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{sessionId}", a.revokeOwnSession)
	mux.HandleFunc("GET /api/v1/users", a.listUsers)
	mux.HandleFunc("POST /api/v1/users", a.createUser)
	mux.HandleFunc("PUT /api/v1/users/{id}/password", a.resetUserPassword)
	mux.HandleFunc("PUT /api/v1/users/{id}/status", a.updateUserStatus)
	mux.HandleFunc("GET /api/v1/users/{id}", a.getUserDetail)
	mux.HandleFunc("GET /api/v1/users/{id}/sessions", a.listUserSessions)
	mux.HandleFunc("DELETE /api/v1/users/{id}/sessions/{sessionId}", a.revokeUserSession)
	mux.HandleFunc("POST /api/v1/users/{id}/sessions/revoke", a.revokeUserSessions)
	mux.HandleFunc("POST /api/v1/users/{id}/transfer", a.transferUserResources)
	mux.HandleFunc("DELETE /api/v1/users/{id}", a.deleteUser)
	mux.HandleFunc("GET /api/v1/service-types", a.serviceTypes)
	mux.HandleFunc("GET /api/v1/packages", a.listPackages)
	mux.HandleFunc("GET /api/v1/environments", a.listEnvironments)
	mux.HandleFunc("POST /api/v1/environments", a.createEnvironment)
	mux.HandleFunc("PUT /api/v1/environments/{id}", a.updateEnvironment)
	mux.HandleFunc("DELETE /api/v1/environments/{id}", a.deleteEnvironment)
	mux.HandleFunc("POST /api/v1/environments/validate", a.validateDraftEnvironment)
	mux.HandleFunc("POST /api/v1/environments/{id}/validate", a.validateSavedEnvironment)
	mux.HandleFunc("GET /api/v1/environments/export", a.exportEnvironments)
	mux.HandleFunc("POST /api/v1/environments/import", a.importEnvironments)
	mux.HandleFunc("PUT /api/v1/environments/{id}/tags", a.replaceEnvironmentTags)
	mux.HandleFunc("GET /api/v1/tags", a.listResourceTags)
	mux.HandleFunc("POST /api/v1/tags", a.createResourceTag)
	mux.HandleFunc("PUT /api/v1/tags/{id}", a.updateResourceTag)
	mux.HandleFunc("DELETE /api/v1/tags/{id}", a.deleteResourceTag)
	mux.HandleFunc("GET /api/v1/service-types/{type}/package", a.getPackage)
	mux.HandleFunc("PUT /api/v1/service-types/{type}/package", a.uploadPackage)
	mux.HandleFunc("DELETE /api/v1/service-types/{type}/package", a.deletePackage)
	mux.HandleFunc("GET /api/v1/service-types/{type}/package/versions", a.listPackageVersions)
	mux.HandleFunc("PUT /api/v1/service-types/{type}/package/versions/{versionId}/current", a.activatePackageVersion)
	mux.HandleFunc("DELETE /api/v1/service-types/{type}/package/versions/{versionId}", a.deletePackageVersion)
	mux.HandleFunc("GET /api/v1/services", a.listServices)
	mux.HandleFunc("GET /api/v1/services/{id}/config", a.getServiceConfig)
	mux.HandleFunc("PUT /api/v1/services/{id}/config", a.updateServiceConfig)
	mux.HandleFunc("POST /api/v1/services/{id}/config/preview", a.previewServiceConfig)
	mux.HandleFunc("GET /api/v1/services/{id}/config/revisions", a.listServiceConfigRevisions)
	mux.HandleFunc("GET /api/v1/services/{id}/config/revisions/{revisionId}", a.getServiceConfigRevision)
	mux.HandleFunc("POST /api/v1/services/{id}/config/revisions/{revisionId}/rollback", a.rollbackServiceConfigRevision)
	mux.HandleFunc("POST /api/v1/services/{id}/install", a.startAction(domain.ActionInstall))
	mux.HandleFunc("POST /api/v1/services/{id}/start", a.startAction(domain.ActionStart))
	mux.HandleFunc("POST /api/v1/services/{id}/stop", a.startAction(domain.ActionStop))
	mux.HandleFunc("POST /api/v1/services/{id}/reset", a.startAction(domain.ActionReset))
	mux.HandleFunc("POST /api/v1/services/{id}/health-check", a.checkHealth)
	mux.HandleFunc("GET /api/v1/services/{id}/logs/stream", a.streamServiceLogs)
	mux.HandleFunc("GET /api/v1/models", a.listModels)
	mux.HandleFunc("GET /api/v1/models/{id}", a.getModel)
	mux.HandleFunc("POST /api/v1/model-uploads", a.createModelUpload)
	mux.HandleFunc("HEAD /api/v1/model-uploads/{id}", a.headModelUpload)
	mux.HandleFunc("PATCH /api/v1/model-uploads/{id}", a.patchModelUpload)
	mux.HandleFunc("POST /api/v1/model-uploads/{id}/complete", a.completeModelUpload)
	mux.HandleFunc("DELETE /api/v1/model-uploads/{id}", a.cancelModelUpload)
	mux.HandleFunc("POST /api/v1/models/{id}/retry", a.retryModel)
	mux.HandleFunc("DELETE /api/v1/models/{id}", a.deleteModel)
	mux.HandleFunc("GET /api/v1/model-tasks/{id}", a.getModelTask)
	mux.HandleFunc("GET /api/v1/model-tasks/{id}/events", a.modelTaskEvents)
	mux.HandleFunc("GET /api/v1/operations/{id}", a.getOperation)
	mux.HandleFunc("GET /api/v1/operations/{id}/events", a.operationEvents)
	mux.HandleFunc("GET /api/v1/operations", a.listOperations)
	mux.HandleFunc("GET /api/v1/admin/dashboard", a.adminDashboard)
	mux.HandleFunc("GET /api/v1/notifications/summary", a.notificationSummary)
	mux.HandleFunc("GET /api/v1/notifications", a.listNotifications)
	mux.HandleFunc("PUT /api/v1/notifications/{id}/read", a.markNotificationRead)
	mux.HandleFunc("PUT /api/v1/notifications/{id}/resolve", a.resolveNotification)
	mux.HandleFunc("GET /api/v1/communications/summary", a.communicationSummary)
	mux.HandleFunc("GET /api/v1/events", a.realtimeEvents)
	mux.HandleFunc("GET /api/v1/communications", a.listCommunications)
	mux.HandleFunc("POST /api/v1/communications", a.createCommunication)
	mux.HandleFunc("GET /api/v1/communications/{id}", a.getCommunication)
	mux.HandleFunc("PUT /api/v1/communications/{id}/read", a.markCommunicationRead)
	mux.HandleFunc("POST /api/v1/communications/{id}/messages", a.sendCommunicationMessage)
	mux.HandleFunc("POST /api/v1/communications/{id}/close", a.closeCommunication)
	mux.HandleFunc("POST /api/v1/communications/{id}/reopen", a.reopenCommunication)
	mux.HandleFunc("GET /api/v1/audit-events/summary", a.auditSummary)
	mux.HandleFunc("GET /api/v1/audit-events/export", a.exportAuditEvents)
	mux.HandleFunc("GET /api/v1/audit-events/{id}", a.getAuditEvent)
	mux.HandleFunc("GET /api/v1/audit-events", a.listAuditEvents)
	mux.Handle("/", spaHandler(frontend))
	return a.recoverMiddleware(a.requestMiddleware(a.originMiddleware(a.authMiddleware(a.auditMiddleware(mux)))))
}

func (a *API) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	result := a.health.Health(ctx)
	status := http.StatusOK
	if result.Status != "ok" {
		status = http.StatusServiceUnavailable
	}
	writeProbe(w, status, result)
}

func (a *API) serviceTypes(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.listOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	packages, err := a.store.ListPackagesByOwner(r.Context(), ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result := make([]map[string]any, 0, len(packages))
	for _, pkg := range packages {
		result = append(result, map[string]any{
			"name": pkg.ServiceType, "display_name": pkg.ServiceType,
			"package_format": ".tar.gz",
		})
	}
	writeData(w, http.StatusOK, result)
}

func (a *API) listPackages(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.listOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	packages, err := a.store.ListPackagesByOwner(r.Context(), ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, packages)
}

func (a *API) listEnvironments(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.listOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	tagIDs, err := a.visibleTagIDs(r, ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items, err := a.environments.ListFiltered(r.Context(), ownerID, tagIDs)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var input domain.EnvironmentInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	ownerID, err := a.createOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.environments.Create(r.Context(), ownerID, input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "environment", result.ID, result.Name,
		map[string]any{"ip": result.IP, "ssh_user": result.SSHUser, "ssh_port": result.SSHPort,
			"install_dir": result.InstallDir, "service_type": result.ServiceType, "note": result.Note, "tags": tagLabels(result.Tags)})
	writeData(w, http.StatusCreated, result)
}

func (a *API) deleteEnvironment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	env, err := a.authorizeEnvironment(r, id)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), env.OwnerID)
	setAuditTarget(r, owner, "environment", env.ID, env.Name,
		map[string]any{"ip": env.IP, "service_type": env.ServiceType})
	if env.Installed {
		a.writeError(w, r, &domain.AppError{
			Code:    "ENVIRONMENT_INSTALLED",
			Message: "该环境已安装服务，请先重置后再删除",
		})
		return
	}
	if a.operations.Busy(id) {
		a.writeError(w, r, domain.ErrOperationInProgress)
		return
	}
	if used, checkErr := a.store.EnvironmentHasModels(r.Context(), id); checkErr != nil {
		a.writeError(w, r, checkErr)
		return
	} else if used {
		a.writeError(w, r, &domain.AppError{Code: "ENVIRONMENT_HAS_MODELS", Message: "该环境仍有关联模型，请先删除模型"})
		return
	}
	_, err = a.store.DeleteEnvironment(r.Context(), id)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"deleted": id})
}

func (a *API) updateEnvironment(w http.ResponseWriter, r *http.Request) {
	original, err := a.authorizeEnvironment(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), original.OwnerID)
	setAuditTarget(r, owner, "environment", original.ID, original.Name, nil)
	var input domain.EnvironmentInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	input.Normalize()
	if input.IP != original.IP || input.SSHUser != original.SSHUser || input.SSHPort != original.SSHPort {
		if used, checkErr := a.store.EnvironmentHasModels(r.Context(), original.ID); checkErr != nil {
			a.writeError(w, r, checkErr)
			return
		} else if used {
			a.writeError(w, r, &domain.AppError{Code: "ENVIRONMENT_HAS_MODELS", Message: "该环境仍有关联模型，不能修改 IP、SSH 用户或 SSH 端口"})
			return
		}
	}
	result, err := a.environments.Update(r.Context(), r.PathValue("id"), input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, owner, "environment", result.ID, result.Name, map[string]any{
		"before":               map[string]any{"name": original.Name, "ip": original.IP, "ssh_user": original.SSHUser, "ssh_port": original.SSHPort, "install_dir": original.InstallDir, "service_type": original.ServiceType, "note": original.Note, "tags": tagLabels(original.Tags)},
		"after":                map[string]any{"name": result.Name, "ip": result.IP, "ssh_user": result.SSHUser, "ssh_port": result.SSHPort, "install_dir": result.InstallDir, "service_type": result.ServiceType, "note": result.Note, "tags": tagLabels(result.Tags)},
		"ssh_password_changed": input.SSHPassword != "",
	})
	writeData(w, http.StatusOK, result)
}

func (a *API) validateDraftEnvironment(w http.ResponseWriter, r *http.Request) {
	var input domain.EnvironmentInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.environments.ValidateDraft(r.Context(), currentUser(r).ID, input)
	setAuditTarget(r, currentUser(r), "environment", "", input.Name, map[string]any{"ip": input.IP, "service_type": input.ServiceType})
	if err != nil {
		setAuditOutcome(r, "failure", "VALIDATION_FAILED")
	}
	writeValidation(w, result, err)
}

func (a *API) validateSavedEnvironment(w http.ResponseWriter, r *http.Request) {
	env, authorizeErr := a.authorizeEnvironment(r, r.PathValue("id"))
	if authorizeErr != nil {
		a.writeError(w, r, authorizeErr)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), env.OwnerID)
	setAuditTarget(r, owner, "environment", env.ID, env.Name, map[string]any{"ip": env.IP, "service_type": env.ServiceType})
	result, err := a.environments.ValidateSaved(r.Context(), r.PathValue("id"))
	if errors.Is(err, domain.ErrNotFound) {
		a.writeError(w, r, err)
		return
	}
	if err != nil {
		code := "VALIDATION_FAILED"
		if strings.Contains(err.Error(), "主机指纹已变化") {
			code = "HOST_KEY_CHANGED"
		}
		setAuditOutcome(r, "failure", code)
	}
	writeValidation(w, result, err)
}

func (a *API) exportEnvironments(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.packageOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	document, err := a.environments.Export(r.Context(), ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "environment_export", ownerID, owner.Username, map[string]any{"count": len(document.Environments)})
	filename := "dp-environments-" + time.Now().Format("20060102") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(document)
}

func (a *API) importEnvironments(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	var document application.ExportDocument
	if err := decodeJSON(r, &document); err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.environments.Import(r.Context(), currentUser(r).ID, document)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "environment", "", "批量导入环境", map[string]any{"created": result.Created, "overwritten": result.Overwritten, "total": result.Total})
	writeData(w, http.StatusOK, result)
}

func (a *API) getPackage(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.packageOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	pkg, err := a.packages.GetForOwner(r.Context(), ownerID, r.PathValue("type"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, pkg)
}

func (a *API) uploadPackage(w http.ResponseWriter, r *http.Request) {
	ownerID, scopeErr := a.packageOwnerScope(r)
	if scopeErr != nil {
		a.writeError(w, r, scopeErr)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	existing, existingErr := a.packages.GetForOwner(r.Context(), ownerID, r.PathValue("type"))
	if errors.Is(existingErr, domain.ErrNotFound) && !owner.Enabled {
		a.writeError(w, r, &domain.AppError{Code: "ACCOUNT_DISABLED", Message: "不能为已禁用账号新增资源"})
		return
	}
	setAuditTarget(r, owner, "package", ownerID+":"+r.PathValue("type"), r.PathValue("type"), nil)
	r.Body = http.MaxBytesReader(w, r.Body, a.uploadMax+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		a.writeError(w, r, domain.FieldError("file", "无法读取上传文件或文件过大"))
		return
	}
	// 流式读取 multipart，避免 ParseMultipartForm 将大文件溢出到 /tmp 临时文件。
	// note 与 file 字段顺序不固定：note 在 file 之后到达时，先按原备注上传，再单独更新备注。
	var (
		note     *string
		pkg      domain.Package
		uploaded bool
	)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			a.writeError(w, r, domain.FieldError("file", "无法读取上传文件或文件过大"))
			return
		}
		switch part.FormName() {
		case "note":
			value, err := io.ReadAll(io.LimitReader(part, 4<<10))
			_ = part.Close()
			if err != nil {
				a.writeError(w, r, domain.FieldError("note", "无法读取备注"))
				return
			}
			text := string(value)
			if err := domain.ValidateNote(text); err != nil {
				a.writeError(w, r, err)
				return
			}
			note = &text
			if uploaded {
				pkg, err = a.packages.UpdateNoteForOwner(r.Context(), ownerID, r.PathValue("type"), text)
				if err != nil {
					a.writeError(w, r, err)
					return
				}
			}
		case "file":
			filename := part.FileName()
			if filename == "" {
				a.writeError(w, r, domain.FieldError("file", "请选择安装包"))
				return
			}
			pkg, err = a.packages.UploadVersionForOwner(r.Context(), ownerID, r.PathValue("type"), filename, part, note, currentUser(r))
			_ = part.Close()
			if err != nil {
				a.writeError(w, r, err)
				return
			}
			uploaded = true
		}
	}
	if !uploaded {
		if note == nil {
			a.writeError(w, r, domain.FieldError("file", "请选择安装包或填写备注"))
			return
		}
		var err error
		pkg, err = a.packages.UpdateNoteForOwner(r.Context(), ownerID, r.PathValue("type"), *note)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
	}
	if uploaded {
		if existingErr == nil {
			setAuditAction(r, "package.replace")
		} else {
			setAuditAction(r, "package.upload")
		}
	} else {
		setAuditAction(r, "package.note.update")
	}
	setAuditTarget(r, owner, "package", ownerID+":"+pkg.ServiceType, pkg.ServiceType,
		map[string]any{"original_filename": pkg.OriginalFilename, "sha256": pkg.SHA256,
			"size_bytes": pkg.SizeBytes, "note_changed": note != nil, "previous_sha256": existing.SHA256})
	writeData(w, http.StatusOK, pkg)
}

func (a *API) listPackageVersions(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.packageOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items, err := a.packages.ListVersions(r.Context(), ownerID, r.PathValue("type"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) activatePackageVersion(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.packageOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "package", ownerID+":"+r.PathValue("type"), r.PathValue("type"), map[string]any{"version_id": r.PathValue("versionId")})
	pkg, err := a.packages.ActivateVersion(r.Context(), ownerID, r.PathValue("type"), r.PathValue("versionId"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, pkg)
}

func (a *API) deletePackageVersion(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.packageOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "package_version", r.PathValue("versionId"), r.PathValue("type"), map[string]any{"version_id": r.PathValue("versionId")})
	if err := a.packages.DeleteVersion(r.Context(), ownerID, r.PathValue("type"), r.PathValue("versionId")); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"deleted": r.PathValue("versionId")})
}

func (a *API) deletePackage(w http.ResponseWriter, r *http.Request) {
	serviceType := r.PathValue("type")
	ownerID, err := a.packageOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	pkg, _ := a.packages.GetForOwner(r.Context(), ownerID, serviceType)
	setAuditTarget(r, owner, "package", ownerID+":"+serviceType, serviceType, map[string]any{"sha256": pkg.SHA256})
	if err := a.packages.DeleteForOwner(r.Context(), ownerID, serviceType); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"deleted": serviceType})
}

func (a *API) listServices(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.listOwnerScope(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	tagIDs, err := a.visibleTagIDs(r, ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	environments, err := a.environments.ListFiltered(r.Context(), ownerID, tagIDs)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	ports, err := a.store.ListServicePorts(r.Context())
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	latestOps, err := a.store.LatestOperations(r.Context())
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	serviceType := r.URL.Query().Get("service_type")
	result := make([]domain.ServiceView, 0, len(environments))
	for _, env := range environments {
		if serviceType != "" && env.ServiceType != serviceType {
			continue
		}
		var servicePort *int
		if port, ok := ports[env.ID]; ok {
			servicePort = &port
		}
		healthResult := domain.HealthResult{Status: "unknown"}
		if env.Installed {
			healthResult = a.health.Snapshot(env.ID)
		}
		var lastOperation *domain.OperationSummary
		if op, ok := latestOps[env.ID]; ok {
			lastOperation = &domain.OperationSummary{
				Action:       op.Action,
				Status:       op.Status,
				ErrorMessage: op.ErrorMessage,
				FinishedAt:   op.FinishedAt,
			}
		}
		result = append(result, domain.ServiceView{
			Environment:   env,
			Health:        healthResult,
			ServicePort:   servicePort,
			Busy:          a.operations.Busy(env.ID),
			LastOperation: lastOperation,
		})
	}
	writeData(w, http.StatusOK, result)
}

func (a *API) getServiceConfig(w http.ResponseWriter, r *http.Request) {
	if _, err := a.authorizeEnvironment(r, r.PathValue("id")); err != nil {
		a.writeError(w, r, err)
		return
	}
	config, err := a.configs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, config)
}

func (a *API) updateServiceConfig(w http.ResponseWriter, r *http.Request) {
	env, err := a.authorizeEnvironment(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), env.OwnerID)
	setAuditTarget(r, owner, "service", env.ID, env.Name, map[string]any{"config_changed": true})
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	config, err := a.configs.Update(r.Context(), r.PathValue("id"), []byte(input.Content), currentUser(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, owner, "service", env.ID, env.Name,
		map[string]any{"config_changed": true, "revision_id": config.CurrentRevisionID, "format": config.Format, "path": config.Path, "port": config.Port})
	writeData(w, http.StatusOK, config)
}

func (a *API) previewServiceConfig(w http.ResponseWriter, r *http.Request) {
	if _, err := a.authorizeEnvironment(r, r.PathValue("id")); err != nil {
		a.writeError(w, r, err)
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	preview, err := a.configs.Preview(r.Context(), r.PathValue("id"), []byte(input.Content))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, preview)
}

func (a *API) listServiceConfigRevisions(w http.ResponseWriter, r *http.Request) {
	if _, err := a.authorizeEnvironment(r, r.PathValue("id")); err != nil {
		a.writeError(w, r, err)
		return
	}
	items, err := a.configs.ListRevisions(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) getServiceConfigRevision(w http.ResponseWriter, r *http.Request) {
	if _, err := a.authorizeEnvironment(r, r.PathValue("id")); err != nil {
		a.writeError(w, r, err)
		return
	}
	item, err := a.configs.GetRevision(r.Context(), r.PathValue("id"), r.PathValue("revisionId"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) rollbackServiceConfigRevision(w http.ResponseWriter, r *http.Request) {
	env, err := a.authorizeEnvironment(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), env.OwnerID)
	setAuditTarget(r, owner, "service", env.ID, env.Name, map[string]any{"restored_from_revision_id": r.PathValue("revisionId")})
	item, err := a.configs.Rollback(r.Context(), env.ID, r.PathValue("revisionId"), currentUser(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, owner, "service", env.ID, env.Name, map[string]any{"revision_id": item.ID, "restored_from_revision_id": item.RestoredFromID, "port": item.Port})
	writeData(w, http.StatusOK, item)
}

func (a *API) startAction(action domain.OperationAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		env, err := a.authorizeEnvironment(r, r.PathValue("id"))
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		owner, _ := a.store.GetUser(r.Context(), env.OwnerID)
		setAuditTarget(r, owner, "service", env.ID, env.Name, map[string]any{"ip": env.IP, "service_type": env.ServiceType})
		event := domain.AuditEvent{
			Category: "service", Action: "service." + string(action) + ".requested", Outcome: "success",
			ActorUserID: currentUser(r).ID, ActorUsername: currentUser(r).Username,
			ActorRole: string(currentUser(r).Role), OwnerID: owner.ID, OwnerUsername: owner.Username,
			TargetType: "service", TargetID: env.ID, TargetLabel: env.Name,
			RequestID: requestID(r.Context()), SourceIP: a.sourceIP(r), UserAgent: r.UserAgent(),
			Changes: map[string]any{"ip": env.IP, "service_type": env.ServiceType},
		}
		op, err := a.operations.StartWithAudit(r.Context(), r.PathValue("id"), action, event)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		writeData(w, http.StatusAccepted, map[string]any{
			"operation_id": op.ID, "status": op.Status,
		})
	}
}

func (a *API) checkHealth(w http.ResponseWriter, r *http.Request) {
	env, err := a.authorizeEnvironment(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), env.OwnerID)
	setAuditTarget(r, owner, "service", env.ID, env.Name, map[string]any{"ip": env.IP, "service_type": env.ServiceType})
	result, err := a.health.CheckNow(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

func (a *API) getOperation(w http.ResponseWriter, r *http.Request) {
	op, err := a.authorizeOperation(r, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, op)
}

func (a *API) operationEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.writeError(w, r, errors.New("streaming is not supported"))
		return
	}
	op, err := a.authorizeOperation(r, r.PathValue("id"))
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
	terminalEmptyPolls := 0
	for {
		events, readErr := a.operations.ReadEvents(op, lastID)
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
			terminalEmptyPolls = 0
		}
		op, err = a.operations.Get(r.Context(), op.ID)
		if err != nil {
			return
		}
		if isTerminal(op.Status) && len(events) == 0 {
			terminalEmptyPolls++
			if terminalEmptyPolls >= 2 {
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

func writeValidation(w http.ResponseWriter, result any, err error) {
	payload := map[string]any{"data": result}
	if err != nil {
		payload["validation_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, payload)
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 20<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return domain.FieldError("body", "请求 JSON 格式错误: "+err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return domain.FieldError("body", "请求只能包含一个 JSON 值")
	}
	return nil
}

func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message, details := classifyHTTPError(err)
	outcome := "failure"
	if status == http.StatusForbidden {
		outcome = "denied"
	}
	setAuditOutcome(r, outcome, code)
	if status >= 500 {
		a.log.Error("request failed", "request_id", requestID(r.Context()), "error", err)
	}
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code": code, "message": message, "details": details,
		},
		"request_id": requestID(r.Context()),
	})
}

func classifyHTTPError(err error) (int, string, string, any) {
	var fieldErr *domain.FieldValidationError
	if errors.As(err, &fieldErr) {
		return http.StatusUnprocessableEntity, "VALIDATION_ERROR", fieldErr.Message,
			map[string]string{"field": fieldErr.Field}
	}
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		status := http.StatusUnprocessableEntity
		switch appErr.Code {
		case "INVALID_CREDENTIALS":
			status = http.StatusUnauthorized
		case "ACCOUNT_DISABLED":
			status = http.StatusForbidden
		case "PASSWORD_CHANGE_REQUIRED":
			status = http.StatusForbidden
		case "LOGIN_THROTTLED":
			status = http.StatusTooManyRequests
		case "COMMUNICATION_CLOSED", "COMMUNICATION_ALREADY_OPEN", "COMMUNICATION_ALREADY_CLOSED", "COMMUNICATION_TARGET_DISABLED":
			status = http.StatusConflict
		case "PACKAGE_NOT_FOUND", "PACKAGE_IN_USE", "PACKAGE_VERSION_EXISTS", "PACKAGE_VERSION_CURRENT", "PACKAGE_VERSION_IN_USE", "PACKAGE_VERSION_INCOMPATIBLE", "CONFIG_REVISION_CURRENT", "ENVIRONMENT_INSTALLED", "ENVIRONMENT_HAS_MODELS", "USER_IN_USE", "USER_PROTECTED", "USERNAME_CONFLICT", "TRANSFER_CONFLICT", "TAG_EXISTS", "MODEL_TARGET_EXISTS", "MODEL_UPLOAD_EXISTS", "UPLOAD_OFFSET_MISMATCH", "MODEL_UPLOAD_INCOMPLETE", "MODEL_UPLOAD_CLOSED", "MODEL_UPLOAD_EXPIRED", "MODEL_OPERATION_IN_PROGRESS", "MODEL_NOT_RETRYABLE", "MODEL_UPLOAD_NOT_AVAILABLE":
			status = http.StatusConflict
		}
		return status, appErr.Code, appErr.Message, nil
	}
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized, "UNAUTHORIZED", "请先登录", nil
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "FORBIDDEN", "没有权限执行该操作", nil
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "NOT_FOUND", "请求的资源不存在", nil
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "ENVIRONMENT_CONFLICT", "服务器 IP 与服务类型已存在", nil
	case errors.Is(err, domain.ErrAlreadyInstalled):
		return http.StatusConflict, "ALREADY_INSTALLED", "该服务已安装，不允许重复安装", nil
	case errors.Is(err, domain.ErrNotInstalled):
		return http.StatusConflict, "NOT_INSTALLED", "该服务尚未安装", nil
	case errors.Is(err, domain.ErrOperationInProgress):
		return http.StatusConflict, "OPERATION_IN_PROGRESS", "该环境已有操作正在执行", nil
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误", nil
	}
}

func writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"data": data})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProbe(w http.ResponseWriter, status int, result health.ProbeResult) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

type contextKey string

const requestIDKey contextKey = "request_id"

func (a *API) requestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := store.NewID()
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"font-src 'self' data:; img-src 'self' data:; connect-src 'self'; "+
				"worker-src 'self' blob:; object-src 'none'; frame-ancestors 'none'")
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(ctx))
		a.log.Debug("request", "request_id", id, "method", r.Method,
			"path", r.URL.Path, "duration", time.Since(start))
	})
}

func (a *API) originMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if !a.sameOriginRequest(r) {
				a.writeError(w, r, domain.FieldError("origin", "拒绝跨站变更请求"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) sameOriginRequest(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	publicScheme := "http"
	if r.TLS != nil {
		publicScheme = "https"
	}
	publicHost := r.Host
	if a.isTrustedProxy(r.RemoteAddr) {
		if forwardedHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
			publicHost = forwardedHost
		}
		if forwardedProto := firstForwardedValue(r.Header.Get("X-Forwarded-Proto")); forwardedProto == "http" || forwardedProto == "https" {
			publicScheme = forwardedProto
		}
	}
	return strings.EqualFold(parsed.Scheme, publicScheme) && strings.EqualFold(parsed.Host, publicHost)
}

func firstForwardedValue(value string) string {
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

func (a *API) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.log.Error("panic in request", "request_id", requestID(r.Context()), "panic", recovered)
				a.writeError(w, r, errors.New("panic"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func isTerminal(status domain.OperationStatus) bool {
	switch status {
	case domain.OperationSucceeded, domain.OperationFailed, domain.OperationTimedOut, domain.OperationInterrupted:
		return true
	default:
		return false
	}
}

func spaHandler(frontend fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(frontend))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requestPath == "." || requestPath == "" {
			requestPath = "index.html"
		}
		if _, err := fs.Stat(frontend, requestPath); err != nil {
			requestPath = "index.html"
		}
		if extension := path.Ext(requestPath); extension != "" {
			if mediaType := mime.TypeByExtension(extension); mediaType != "" {
				w.Header().Set("Content-Type", mediaType)
			}
		}
		if requestPath == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
			content, err := fs.ReadFile(frontend, "index.html")
			if err != nil {
				http.Error(w, "frontend is unavailable", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
			return
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		clone := r.Clone(r.Context())
		clone.URL.Path = "/" + requestPath
		fileServer.ServeHTTP(w, clone)
	})
}
