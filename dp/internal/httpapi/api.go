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

	"DP/internal/access"
	"DP/internal/application"
	"DP/internal/archive"
	"DP/internal/audit"
	"DP/internal/domain"
	"DP/internal/health"
	modelmanager "DP/internal/model"
	"DP/internal/operation"
	"DP/internal/realtime"
)

type Repository interface {
	AuditSummary(context.Context, domain.AuditFilter) (domain.AuditSummary, error)
	CountAuditEvents(context.Context, domain.AuditFilter) (int, error)
	CountOperationsByTags(context.Context, []string, time.Time) (int, int, error)
	CreateResourceTag(context.Context, string, domain.ResourceTagInput) (domain.ResourceTag, error)
	DashboardMetrics(context.Context, time.Time, time.Time) (domain.DashboardMetrics, error)
	DeleteResourceTag(context.Context, string) error
	GetAuditEvent(context.Context, string) (domain.AuditEvent, error)
	GetHost(context.Context, string) (domain.Host, error)
	GetServiceInstance(context.Context, string) (domain.ServiceInstance, error)
	GetModelUpload(context.Context, string) (domain.ModelUpload, error)
	GetResourceTag(context.Context, string) (domain.ResourceTag, error)
	GetUser(context.Context, string) (domain.User, error)
	LatestOperations(context.Context) (map[string]domain.Operation, error)
	ListAuditEvents(context.Context, domain.AuditFilter) ([]domain.AuditEvent, error)
	ListHosts(context.Context, string) ([]domain.Host, error)
	ListServiceInstances(context.Context) ([]domain.ServiceInstance, error)
	ListNotifications(context.Context, domain.NotificationFilter) ([]domain.Notification, error)
	ListOperations(context.Context, domain.OperationFilter) ([]domain.Operation, error)
	ListPackagesByOwner(context.Context, string) ([]domain.Package, error)
	ListResourceTags(context.Context, string) ([]domain.ResourceTag, error)
	MarkNotificationRead(context.Context, string, string, time.Time) (domain.Notification, error)
	NotificationSummary(context.Context) (domain.NotificationSummary, error)
	RecentNotifications(context.Context, int) ([]domain.Notification, error)
	ReplaceServiceInstanceTags(context.Context, string, []string) error
	ResolveNotification(context.Context, string, string, time.Time) (domain.Notification, error)
	UpdateResourceTag(context.Context, string, domain.ResourceTagInput) (domain.ResourceTag, error)
	ValidateTagIDs(context.Context, string, []string) error
}

type API struct {
	auth           *application.AuthService
	roles          *application.RoleService
	communications *application.CommunicationService
	realtime       *realtime.Hub
	realtimeBeat   time.Duration
	hosts          *application.HostService
	instances      *application.ServiceInstanceService
	configs        *application.ServiceConfigService
	serviceLogs    *application.ServiceLogService
	packages       *archive.Manager
	operations     *operation.Manager
	models         *modelmanager.Manager
	health         *health.Monitor
	store          Repository
	audit          *audit.Service
	uploadMax      int64
	auditExportMax int
	trustedProxies []netip.Prefix
	log            *slog.Logger
}

func New(
	authService *application.AuthService,
	roleService *application.RoleService,
	communicationService *application.CommunicationService,
	realtimeHub *realtime.Hub,
	hosts *application.HostService,
	instances *application.ServiceInstanceService,
	configs *application.ServiceConfigService,
	serviceLogs *application.ServiceLogService,
	packages *archive.Manager,
	operations *operation.Manager,
	models *modelmanager.Manager,
	healthMonitor *health.Monitor,
	store Repository,
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
		auth: authService, roles: roleService, communications: communicationService, realtime: realtimeHub, realtimeBeat: 15 * time.Second,
		hosts: hosts, instances: instances, configs: configs, serviceLogs: serviceLogs,
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
	mux.HandleFunc("GET /api/v1/events", a.realtimeEvents)
	for _, route := range a.protectedRoutes() {
		mux.Handle(route.pattern, a.requirePermission(route.permission, route.handler))
	}
	mux.Handle("/", spaHandler(frontend))
	return a.recoverMiddleware(a.requestMiddleware(a.originMiddleware(a.authMiddleware(a.auditMiddleware(mux)))))
}

type protectedRoute struct {
	pattern    string
	permission access.Permission
	handler    http.HandlerFunc
}

func (a *API) protectedRoutes() []protectedRoute {
	return []protectedRoute{
		{"GET /api/v1/users", access.AccountRead, a.listUsers},
		{"POST /api/v1/users", access.AccountCreate, a.createUser},
		{"PUT /api/v1/users/{id}/password", access.AccountUpdate, a.resetUserPassword},
		{"PUT /api/v1/users/{id}/status", access.AccountUpdate, a.updateUserStatus},
		{"GET /api/v1/users/{id}", access.AccountRead, a.getUserDetail},
		{"GET /api/v1/users/{id}/sessions", access.AccountRead, a.listUserSessions},
		{"DELETE /api/v1/users/{id}/sessions/{sessionId}", access.AccountUpdate, a.revokeUserSession},
		{"POST /api/v1/users/{id}/sessions/revoke", access.AccountUpdate, a.revokeUserSessions},
		{"POST /api/v1/users/{id}/transfer", access.AccountTransfer, a.transferUserResources},
		{"DELETE /api/v1/users/{id}", access.AccountDelete, a.deleteUser},
		{"GET /api/v1/permissions", access.RoleRead, a.listPermissions},
		{"GET /api/v1/roles", access.RoleRead, a.listRoles},
		{"POST /api/v1/roles", access.RoleCreate, a.createRole},
		{"GET /api/v1/roles/{id}", access.RoleRead, a.getRole},
		{"PUT /api/v1/roles/{id}", access.RoleUpdate, a.updateRole},
		{"DELETE /api/v1/roles/{id}", access.RoleDelete, a.deleteRole},
		{"PUT /api/v1/users/{id}/roles", access.AccountAssignRoles, a.replaceUserRoles},
		{"GET /api/v1/service-types", access.PackageRead, a.serviceTypes},
		{"GET /api/v1/packages", access.PackageRead, a.listPackages},
		{"GET /api/v1/hosts", access.HostRead, a.listHosts},
		{"POST /api/v1/hosts", access.HostWrite, a.createHost},
		{"PUT /api/v1/hosts/{id}", access.HostWrite, a.updateHost},
		{"DELETE /api/v1/hosts/{id}", access.HostDelete, a.deleteHost},
		{"POST /api/v1/hosts/validate", access.HostValidate, a.validateDraftHost},
		{"POST /api/v1/hosts/{id}/validate", access.HostValidate, a.validateSavedHost},
		{"GET /api/v1/hosts/export", access.HostExport, a.exportHosts},
		{"POST /api/v1/hosts/import", access.HostImport, a.importHosts},
		{"GET /api/v1/tags", access.TagRead, a.listResourceTags},
		{"POST /api/v1/tags", access.TagWrite, a.createResourceTag},
		{"PUT /api/v1/tags/{id}", access.TagWrite, a.updateResourceTag},
		{"DELETE /api/v1/tags/{id}", access.TagWrite, a.deleteResourceTag},
		{"GET /api/v1/service-types/{type}/package", access.PackageRead, a.getPackage},
		{"PUT /api/v1/service-types/{type}/package", access.PackageWrite, a.uploadPackage},
		{"DELETE /api/v1/service-types/{type}/package", access.PackageDelete, a.deletePackage},
		{"GET /api/v1/service-types/{type}/package/versions", access.PackageRead, a.listPackageVersions},
		{"PUT /api/v1/service-types/{type}/package/versions/{versionId}/current", access.PackageWrite, a.activatePackageVersion},
		{"DELETE /api/v1/service-types/{type}/package/versions/{versionId}", access.PackageDelete, a.deletePackageVersion},
		{"GET /api/v1/services", access.ServiceRead, a.listServices},
		{"POST /api/v1/services", access.ServiceWrite, a.createServiceInstance},
		{"PUT /api/v1/services/{id}", access.ServiceWrite, a.updateServiceInstance},
		{"DELETE /api/v1/services/{id}", access.ServiceDelete, a.deleteServiceInstance},
		{"PUT /api/v1/services/{id}/tags", access.TagWrite, a.replaceServiceInstanceTags},
		{"GET /api/v1/services/{id}/config", access.ServiceConfigRead, a.getServiceConfig},
		{"PUT /api/v1/services/{id}/config", access.ServiceConfigWrite, a.updateServiceConfig},
		{"POST /api/v1/services/{id}/config/preview", access.ServiceConfigWrite, a.previewServiceConfig},
		{"GET /api/v1/services/{id}/config/revisions", access.ServiceConfigRead, a.listServiceConfigRevisions},
		{"GET /api/v1/services/{id}/config/revisions/{revisionId}", access.ServiceConfigRead, a.getServiceConfigRevision},
		{"POST /api/v1/services/{id}/config/revisions/{revisionId}/rollback", access.ServiceConfigWrite, a.rollbackServiceConfigRevision},
		{"POST /api/v1/services/{id}/install", access.ServiceInstall, a.startAction(domain.ActionInstall)},
		{"POST /api/v1/services/{id}/start", access.ServiceStart, a.startAction(domain.ActionStart)},
		{"POST /api/v1/services/{id}/stop", access.ServiceStop, a.startAction(domain.ActionStop)},
		{"POST /api/v1/services/{id}/reset", access.ServiceReset, a.startAction(domain.ActionReset)},
		{"POST /api/v1/services/{id}/health-check", access.ServiceHealth, a.checkHealth},
		{"GET /api/v1/services/{id}/logs/stream", access.ServiceLogRead, a.streamServiceLogs},
		{"GET /api/v1/models", access.ModelRead, a.listModels},
		{"GET /api/v1/models/{id}", access.ModelRead, a.getModel},
		{"POST /api/v1/model-uploads", access.ModelUpload, a.createModelUpload},
		{"HEAD /api/v1/model-uploads/{id}", access.ModelUpload, a.headModelUpload},
		{"PATCH /api/v1/model-uploads/{id}", access.ModelUpload, a.patchModelUpload},
		{"POST /api/v1/model-uploads/{id}/complete", access.ModelUpload, a.completeModelUpload},
		{"DELETE /api/v1/model-uploads/{id}", access.ModelUpload, a.cancelModelUpload},
		{"POST /api/v1/models/{id}/retry", access.ModelUpload, a.retryModel},
		{"DELETE /api/v1/models/{id}", access.ModelDelete, a.deleteModel},
		{"GET /api/v1/model-tasks/{id}", access.ModelRead, a.getModelTask},
		{"POST /api/v1/model-tasks/{id}/cancel", access.ModelUpload, a.cancelModelTask},
		{"GET /api/v1/model-tasks/{id}/events", access.ModelRead, a.modelTaskEvents},
		{"GET /api/v1/operations/{id}", access.OperationRead, a.getOperation},
		{"GET /api/v1/operations/{id}/events", access.OperationRead, a.operationEvents},
		{"GET /api/v1/operations", access.OperationRead, a.listOperations},
		{"GET /api/v1/admin/dashboard", access.DashboardRead, a.adminDashboard},
		{"GET /api/v1/notifications/summary", access.NotificationRead, a.notificationSummary},
		{"GET /api/v1/notifications", access.NotificationRead, a.listNotifications},
		{"PUT /api/v1/notifications/{id}/read", access.NotificationUpdate, a.markNotificationRead},
		{"PUT /api/v1/notifications/{id}/resolve", access.NotificationUpdate, a.resolveNotification},
		{"GET /api/v1/communications/summary", access.CommunicationRead, a.communicationSummary},
		{"GET /api/v1/communications", access.CommunicationRead, a.listCommunications},
		{"POST /api/v1/communications", access.CommunicationCreate, a.createCommunication},
		{"GET /api/v1/communications/{id}", access.CommunicationRead, a.getCommunication},
		{"PUT /api/v1/communications/{id}/read", access.CommunicationReply, a.markCommunicationRead},
		{"POST /api/v1/communications/{id}/messages", access.CommunicationReply, a.sendCommunicationMessage},
		{"POST /api/v1/communications/{id}/close", access.CommunicationManage, a.closeCommunication},
		{"POST /api/v1/communications/{id}/reopen", access.CommunicationManage, a.reopenCommunication},
		{"GET /api/v1/audit-events/summary", access.AuditRead, a.auditSummary},
		{"GET /api/v1/audit-events/export", access.AuditExport, a.exportAuditEvents},
		{"GET /api/v1/audit-events/{id}", access.AuditRead, a.getAuditEvent},
		{"GET /api/v1/audit-events", access.AuditRead, a.listAuditEvents},
	}
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
	ownerID, err := a.listOwnerScope(r, access.PackageRead)
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
	ownerID, err := a.listOwnerScope(r, access.PackageRead)
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

func (a *API) listHosts(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.listOwnerScope(r, access.HostRead)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items, err := a.hosts.List(r.Context(), ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) createHost(w http.ResponseWriter, r *http.Request) {
	var input domain.HostInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	ownerID, err := a.createOwnerScope(r, access.HostWrite)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.hosts.Create(r.Context(), ownerID, input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "host", result.ID, result.Name,
		map[string]any{"ip": result.IP, "ssh_user": result.SSHUser, "ssh_port": result.SSHPort, "note": result.Note})
	writeData(w, http.StatusCreated, result)
}

func (a *API) deleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	host, err := a.authorizeHost(r, id, access.HostDelete)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), host.OwnerID)
	setAuditTarget(r, owner, "host", host.ID, host.Name, map[string]any{"ip": host.IP})
	err = a.hosts.Delete(r.Context(), id)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"deleted": id})
}

func (a *API) updateHost(w http.ResponseWriter, r *http.Request) {
	original, err := a.authorizeHost(r, r.PathValue("id"), access.HostWrite)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), original.OwnerID)
	setAuditTarget(r, owner, "host", original.ID, original.Name, nil)
	var input domain.HostInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.hosts.Update(r.Context(), r.PathValue("id"), input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, owner, "host", result.ID, result.Name, map[string]any{
		"before":               map[string]any{"name": original.Name, "ip": original.IP, "ssh_user": original.SSHUser, "ssh_port": original.SSHPort, "note": original.Note},
		"after":                map[string]any{"name": result.Name, "ip": result.IP, "ssh_user": result.SSHUser, "ssh_port": result.SSHPort, "note": result.Note},
		"ssh_password_changed": input.SSHPassword != "",
	})
	writeData(w, http.StatusOK, result)
}

func (a *API) validateDraftHost(w http.ResponseWriter, r *http.Request) {
	var input domain.HostInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	ownerID, err := a.createOwnerScope(r, access.HostValidate)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.hosts.ValidateDraft(r.Context(), input)
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "host", "", input.Name, map[string]any{"ip": input.IP})
	if err != nil {
		setAuditOutcome(r, "failure", "VALIDATION_FAILED")
	}
	writeValidation(w, result, err)
}

func (a *API) validateSavedHost(w http.ResponseWriter, r *http.Request) {
	host, authorizeErr := a.authorizeHost(r, r.PathValue("id"), access.HostValidate)
	if authorizeErr != nil {
		a.writeError(w, r, authorizeErr)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), host.OwnerID)
	setAuditTarget(r, owner, "host", host.ID, host.Name, map[string]any{"ip": host.IP})
	result, err := a.hosts.ValidateSaved(r.Context(), r.PathValue("id"))
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

func (a *API) exportHosts(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.packageOwnerScope(r, access.HostExport)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	document, err := a.hosts.Export(r.Context(), ownerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "host_export", ownerID, owner.Username, map[string]any{"count": len(document.Hosts)})
	filename := "dp-hosts-" + time.Now().Format("20060102") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(document)
}

func (a *API) importHosts(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	var document application.HostExportDocument
	if err := decodeJSON(r, &document); err != nil {
		a.writeError(w, r, err)
		return
	}
	ownerID, err := a.createOwnerScope(r, access.HostImport)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.hosts.Import(r.Context(), ownerID, document)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "host", "", "批量导入主机", map[string]any{"created": result.Created, "updated": result.Updated})
	writeData(w, http.StatusOK, result)
}

func (a *API) createServiceInstance(w http.ResponseWriter, r *http.Request) {
	var input domain.ServiceInstanceInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	ownerID, err := a.createOwnerScope(r, access.ServiceWrite)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.instances.Create(r.Context(), ownerID, input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), ownerID)
	setAuditTarget(r, owner, "service", result.ID, result.Name, map[string]any{
		"host_id": result.HostID, "service_type": result.ServiceType, "install_dir": result.InstallDir,
	})
	writeData(w, http.StatusCreated, result)
}

func (a *API) updateServiceInstance(w http.ResponseWriter, r *http.Request) {
	original, err := a.authorizeServiceInstance(r, r.PathValue("id"), access.ServiceWrite)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	var input domain.ServiceInstanceInput
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.instances.Update(r.Context(), original.ID, input)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), original.OwnerID)
	setAuditTarget(r, owner, "service", result.ID, result.Name, map[string]any{
		"before": map[string]any{"host_id": original.HostID, "service_type": original.ServiceType, "install_dir": original.InstallDir},
		"after":  map[string]any{"host_id": result.HostID, "service_type": result.ServiceType, "install_dir": result.InstallDir},
	})
	writeData(w, http.StatusOK, result)
}

func (a *API) deleteServiceInstance(w http.ResponseWriter, r *http.Request) {
	instance, err := a.authorizeServiceInstance(r, r.PathValue("id"), access.ServiceDelete)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if a.operations.Busy(instance.ID) {
		a.writeError(w, r, domain.ErrOperationInProgress)
		return
	}
	if err := a.instances.Delete(r.Context(), instance.ID); err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), instance.OwnerID)
	setAuditTarget(r, owner, "service", instance.ID, instance.Name, nil)
	writeData(w, http.StatusOK, map[string]string{"deleted": instance.ID})
}

func (a *API) getPackage(w http.ResponseWriter, r *http.Request) {
	ownerID, err := a.packageOwnerScope(r, access.PackageRead)
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
	ownerID, scopeErr := a.packageOwnerScope(r, access.PackageWrite)
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
	ownerID, err := a.packageOwnerScope(r, access.PackageRead)
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
	ownerID, err := a.packageOwnerScope(r, access.PackageWrite)
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
	ownerID, err := a.packageOwnerScope(r, access.PackageDelete)
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
	ownerID, err := a.packageOwnerScope(r, access.PackageDelete)
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
	ownerID, err := a.listOwnerScope(r, access.ServiceRead)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	tagIDs, err := a.visibleTagIDs(r, ownerID, access.ServiceRead)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	instances, err := a.instances.ListFiltered(r.Context(), ownerID, tagIDs)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	ports, err := a.configs.ListAPIPorts(r.Context(), instances)
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
	result := make([]domain.ServiceView, 0, len(instances))
	for _, instance := range instances {
		if serviceType != "" && instance.ServiceType != serviceType {
			continue
		}
		var apiPort *int
		if port, ok := ports[instance.ID]; ok {
			apiPort = &port
		}
		healthResult := domain.HealthResult{Status: "unknown"}
		if instance.Installed {
			healthResult = a.health.Snapshot(instance.ID)
		}
		var lastOperation *domain.OperationSummary
		if op, ok := latestOps[instance.ID]; ok {
			lastOperation = &domain.OperationSummary{
				Action:       op.Action,
				Status:       op.Status,
				ErrorMessage: op.ErrorMessage,
				FinishedAt:   op.FinishedAt,
			}
		}
		result = append(result, domain.ServiceView{
			ServiceInstance: instance,
			Health:          healthResult,
			APIPort:         apiPort,
			Busy:            a.operations.Busy(instance.ID),
			LastOperation:   lastOperation,
		})
	}
	writeData(w, http.StatusOK, result)
}

func (a *API) getServiceConfig(w http.ResponseWriter, r *http.Request) {
	if _, err := a.authorizeServiceInstance(r, r.PathValue("id"), access.ServiceConfigRead); err != nil {
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
	instance, err := a.authorizeServiceInstance(r, r.PathValue("id"), access.ServiceConfigWrite)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), instance.OwnerID)
	setAuditTarget(r, owner, "service", instance.ID, instance.Name, map[string]any{"config_changed": true})
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
	setAuditTarget(r, owner, "service", instance.ID, instance.Name,
		map[string]any{"config_changed": true, "revision_id": config.CurrentRevisionID, "format": config.Format, "path": config.Path, "port": config.Port})
	writeData(w, http.StatusOK, config)
}

func (a *API) previewServiceConfig(w http.ResponseWriter, r *http.Request) {
	if _, err := a.authorizeServiceInstance(r, r.PathValue("id"), access.ServiceConfigWrite); err != nil {
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
	if _, err := a.authorizeServiceInstance(r, r.PathValue("id"), access.ServiceConfigRead); err != nil {
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
	if _, err := a.authorizeServiceInstance(r, r.PathValue("id"), access.ServiceConfigRead); err != nil {
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
	instance, err := a.authorizeServiceInstance(r, r.PathValue("id"), access.ServiceConfigWrite)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), instance.OwnerID)
	setAuditTarget(r, owner, "service", instance.ID, instance.Name, map[string]any{"restored_from_revision_id": r.PathValue("revisionId")})
	item, err := a.configs.Rollback(r.Context(), instance.ID, r.PathValue("revisionId"), currentUser(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, owner, "service", instance.ID, instance.Name, map[string]any{"revision_id": item.ID, "restored_from_revision_id": item.RestoredFromID, "port": item.Port})
	writeData(w, http.StatusOK, item)
}

func (a *API) startAction(action domain.OperationAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instance, err := a.authorizeServiceInstance(r, r.PathValue("id"), serviceActionPermission(action))
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		owner, _ := a.store.GetUser(r.Context(), instance.OwnerID)
		setAuditTarget(r, owner, "service", instance.ID, instance.Name, map[string]any{"ip": instance.Host.IP, "service_type": instance.ServiceType})
		event := domain.AuditEvent{
			Category: "service", Action: "service." + string(action) + ".requested", Outcome: "success",
			ActorUserID: currentUser(r).ID, ActorUsername: currentUser(r).Username,
			ActorRoles: roleKeys(currentUser(r)), OwnerID: owner.ID, OwnerUsername: owner.Username,
			TargetType: "service", TargetID: instance.ID, TargetLabel: instance.Name,
			RequestID: requestID(r.Context()), SourceIP: a.sourceIP(r), UserAgent: r.UserAgent(),
			Changes: map[string]any{"ip": instance.Host.IP, "service_type": instance.ServiceType},
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

func serviceActionPermission(action domain.OperationAction) access.Permission {
	switch action {
	case domain.ActionInstall:
		return access.ServiceInstall
	case domain.ActionStart:
		return access.ServiceStart
	case domain.ActionStop:
		return access.ServiceStop
	case domain.ActionReset:
		return access.ServiceReset
	default:
		return ""
	}
}

func (a *API) checkHealth(w http.ResponseWriter, r *http.Request) {
	instance, err := a.authorizeServiceInstance(r, r.PathValue("id"), access.ServiceHealth)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	owner, _ := a.store.GetUser(r.Context(), instance.OwnerID)
	setAuditTarget(r, owner, "service", instance.ID, instance.Name, map[string]any{"ip": instance.Host.IP, "service_type": instance.ServiceType})
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
		case "ACCOUNT_DISABLED", "GRANT_FORBIDDEN":
			status = http.StatusForbidden
		case "PASSWORD_CHANGE_REQUIRED":
			status = http.StatusForbidden
		case "LOGIN_THROTTLED":
			status = http.StatusTooManyRequests
		case "COMMUNICATION_CLOSED", "COMMUNICATION_ALREADY_OPEN", "COMMUNICATION_ALREADY_CLOSED", "COMMUNICATION_TARGET_DISABLED":
			status = http.StatusConflict
		case "PACKAGE_NOT_FOUND", "PACKAGE_IN_USE", "PACKAGE_VERSION_EXISTS", "PACKAGE_VERSION_CURRENT", "PACKAGE_VERSION_IN_USE", "PACKAGE_VERSION_INCOMPATIBLE", "CONFIG_REVISION_CURRENT", "SERVICE_INSTALLED", "SERVICE_INSTANCE_CONFLICT", "HOST_CONFLICT", "HOST_HAS_SERVICES", "HOST_HAS_MODELS", "HOST_IN_USE", "USER_IN_USE", "USER_PROTECTED", "USERNAME_CONFLICT", "TRANSFER_CONFLICT", "TAG_EXISTS", "MODEL_TARGET_EXISTS", "MODEL_UPLOAD_EXISTS", "UPLOAD_OFFSET_MISMATCH", "MODEL_UPLOAD_INCOMPLETE", "MODEL_UPLOAD_CLOSED", "MODEL_UPLOAD_EXPIRED", "MODEL_OPERATION_IN_PROGRESS", "MODEL_NOT_RETRYABLE", "MODEL_UPLOAD_NOT_AVAILABLE", "MODEL_TASK_NOT_CANCELLABLE", "MODEL_TASK_ALREADY_COMMITTED", "MODEL_CANCEL_CLEANUP_FAILED", "ROLE_KEY_CONFLICT", "ROLE_IN_USE", "ROLE_PROTECTED":
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
		return http.StatusConflict, "CONFLICT", "资源状态冲突", nil
	case errors.Is(err, domain.ErrAlreadyInstalled):
		return http.StatusConflict, "ALREADY_INSTALLED", "该服务已安装，不允许重复安装", nil
	case errors.Is(err, domain.ErrNotInstalled):
		return http.StatusConflict, "NOT_INSTALLED", "该服务尚未安装", nil
	case errors.Is(err, domain.ErrOperationInProgress):
		return http.StatusConflict, "OPERATION_IN_PROGRESS", "该服务实例已有操作正在执行", nil
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
		id := domain.NewID()
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
