package access

import (
	"errors"
	"fmt"
)

var (
	ErrConflict     = errors.New("RBAC conflict")
	ErrInUse        = errors.New("RBAC resource in use")
	ErrInvalidInput = errors.New("invalid RBAC input")
	ErrNotFound     = errors.New("RBAC resource not found")
	ErrProtected    = errors.New("protected RBAC resource")
)

type Scope string

const (
	ScopeOwn Scope = "own"
	ScopeAll Scope = "all"
)

func (s Scope) Valid() bool {
	return s == ScopeOwn || s == ScopeAll
}

type Permission string

const (
	DashboardRead       Permission = "dashboard.read"
	AccountRead         Permission = "account.read"
	AccountCreate       Permission = "account.create"
	AccountUpdate       Permission = "account.update"
	AccountDelete       Permission = "account.delete"
	AccountAssignRoles  Permission = "account.assign_roles"
	AccountTransfer     Permission = "account.transfer"
	RoleRead            Permission = "role.read"
	RoleCreate          Permission = "role.create"
	RoleUpdate          Permission = "role.update"
	RoleDelete          Permission = "role.delete"
	PackageRead         Permission = "package.read"
	PackageWrite        Permission = "package.write"
	PackageDelete       Permission = "package.delete"
	HostRead            Permission = "host.read"
	HostWrite           Permission = "host.write"
	HostDelete          Permission = "host.delete"
	HostValidate        Permission = "host.validate"
	HostImport          Permission = "host.import"
	HostExport          Permission = "host.export"
	TagRead             Permission = "tag.read"
	TagWrite            Permission = "tag.write"
	ModelRead           Permission = "model.read"
	ModelUpload         Permission = "model.upload"
	ModelDelete         Permission = "model.delete"
	ServiceRead         Permission = "service.read"
	ServiceWrite        Permission = "service.write"
	ServiceDelete       Permission = "service.delete"
	ServiceConfigRead   Permission = "service.config.read"
	ServiceConfigWrite  Permission = "service.config.write"
	ServiceInstall      Permission = "service.install"
	ServiceStart        Permission = "service.start"
	ServiceStop         Permission = "service.stop"
	ServiceReset        Permission = "service.reset"
	ServiceHealth       Permission = "service.health"
	ServiceLogRead      Permission = "service.log.read"
	OperationRead       Permission = "operation.read"
	AuditRead           Permission = "audit.read"
	AuditExport         Permission = "audit.export"
	NotificationRead    Permission = "notification.read"
	NotificationUpdate  Permission = "notification.update"
	CommunicationRead   Permission = "communication.read"
	CommunicationCreate Permission = "communication.create"
	CommunicationReply  Permission = "communication.reply"
	CommunicationManage Permission = "communication.manage"
)

const (
	RoleSuperAdmin    = "super_admin"
	RolePlatformAdmin = "platform_admin"
	RoleOperator      = "operator"
	RoleViewer        = "viewer"
)

type Definition struct {
	Key         Permission `json:"key"`
	Resource    string     `json:"resource"`
	Action      string     `json:"action"`
	Description string     `json:"description"`
	Scoped      bool       `json:"scoped"`
}

// Definitions returns the immutable application permission catalog. PostgreSQL
// seeds and HTTP route declarations are verified against this catalog in tests.
func Definitions() []Definition {
	return []Definition{
		{Key: DashboardRead, Resource: "dashboard", Action: "read", Description: "查看管理总览"},
		{Key: AccountRead, Resource: "account", Action: "read", Description: "查看账号和会话"},
		{Key: AccountCreate, Resource: "account", Action: "create", Description: "创建账号"},
		{Key: AccountUpdate, Resource: "account", Action: "update", Description: "修改账号安全状态"},
		{Key: AccountDelete, Resource: "account", Action: "delete", Description: "删除账号"},
		{Key: AccountAssignRoles, Resource: "account", Action: "assign_roles", Description: "分配用户角色"},
		{Key: AccountTransfer, Resource: "account", Action: "transfer", Description: "交接账号资源"},
		{Key: RoleRead, Resource: "role", Action: "read", Description: "查看角色和权限"},
		{Key: RoleCreate, Resource: "role", Action: "create", Description: "创建角色"},
		{Key: RoleUpdate, Resource: "role", Action: "update", Description: "修改角色和权限绑定"},
		{Key: RoleDelete, Resource: "role", Action: "delete", Description: "删除角色"},
		{Key: PackageRead, Resource: "package", Action: "read", Description: "查看安装包", Scoped: true},
		{Key: PackageWrite, Resource: "package", Action: "write", Description: "上传和更新安装包", Scoped: true},
		{Key: PackageDelete, Resource: "package", Action: "delete", Description: "删除安装包", Scoped: true},
		{Key: HostRead, Resource: "host", Action: "read", Description: "查看主机", Scoped: true},
		{Key: HostWrite, Resource: "host", Action: "write", Description: "创建和修改主机", Scoped: true},
		{Key: HostDelete, Resource: "host", Action: "delete", Description: "删除主机", Scoped: true},
		{Key: HostValidate, Resource: "host", Action: "validate", Description: "校验主机 SSH 连接", Scoped: true},
		{Key: HostImport, Resource: "host", Action: "import", Description: "导入主机", Scoped: true},
		{Key: HostExport, Resource: "host", Action: "export", Description: "导出主机", Scoped: true},
		{Key: TagRead, Resource: "tag", Action: "read", Description: "查看标签", Scoped: true},
		{Key: TagWrite, Resource: "tag", Action: "write", Description: "管理标签", Scoped: true},
		{Key: ModelRead, Resource: "model", Action: "read", Description: "查看模型", Scoped: true},
		{Key: ModelUpload, Resource: "model", Action: "upload", Description: "上传和重试模型", Scoped: true},
		{Key: ModelDelete, Resource: "model", Action: "delete", Description: "删除模型", Scoped: true},
		{Key: ServiceRead, Resource: "service", Action: "read", Description: "查看服务", Scoped: true},
		{Key: ServiceWrite, Resource: "service", Action: "write", Description: "创建和修改服务实例", Scoped: true},
		{Key: ServiceDelete, Resource: "service", Action: "delete", Description: "删除服务实例", Scoped: true},
		{Key: ServiceConfigRead, Resource: "service", Action: "config.read", Description: "查看服务配置", Scoped: true},
		{Key: ServiceConfigWrite, Resource: "service", Action: "config.write", Description: "修改服务配置", Scoped: true},
		{Key: ServiceInstall, Resource: "service", Action: "install", Description: "安装服务", Scoped: true},
		{Key: ServiceStart, Resource: "service", Action: "start", Description: "启动服务", Scoped: true},
		{Key: ServiceStop, Resource: "service", Action: "stop", Description: "停止服务", Scoped: true},
		{Key: ServiceReset, Resource: "service", Action: "reset", Description: "重置服务", Scoped: true},
		{Key: ServiceHealth, Resource: "service", Action: "health", Description: "手动检查服务健康", Scoped: true},
		{Key: ServiceLogRead, Resource: "service", Action: "log.read", Description: "查看服务日志", Scoped: true},
		{Key: OperationRead, Resource: "operation", Action: "read", Description: "查看操作和日志", Scoped: true},
		{Key: AuditRead, Resource: "audit", Action: "read", Description: "查看审计日志"},
		{Key: AuditExport, Resource: "audit", Action: "export", Description: "导出审计日志"},
		{Key: NotificationRead, Resource: "notification", Action: "read", Description: "查看风险通知"},
		{Key: NotificationUpdate, Resource: "notification", Action: "update", Description: "处理风险通知"},
		{Key: CommunicationRead, Resource: "communication", Action: "read", Description: "查看通讯事项", Scoped: true},
		{Key: CommunicationCreate, Resource: "communication", Action: "create", Description: "创建通讯事项"},
		{Key: CommunicationReply, Resource: "communication", Action: "reply", Description: "回复和标记通讯已读", Scoped: true},
		{Key: CommunicationManage, Resource: "communication", Action: "manage", Description: "关闭和重新打开通讯"},
	}
}

type Grant struct {
	Permission Permission `json:"permission"`
	Scope      Scope      `json:"scope"`
}

type Role struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	System      bool    `json:"system"`
	Grants      []Grant `json:"grants,omitempty"`
	MemberCount int     `json:"member_count"`
}

func (r Role) HasKey(key string) bool {
	return r.Key == key
}

type RoleRef struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type Subject struct {
	UserID string    `json:"user_id"`
	Roles  []RoleRef `json:"roles"`
	Grants Grants    `json:"permissions"`
}

func (s Subject) HasRole(key string) bool {
	for _, role := range s.Roles {
		if role.Key == key {
			return true
		}
	}
	return false
}

type Grants map[Permission]Scope

func Merge(items ...Grant) (Grants, error) {
	result := make(Grants, len(items))
	for _, item := range items {
		if item.Permission == "" {
			return nil, errors.New("permission is required")
		}
		if !item.Scope.Valid() {
			return nil, fmt.Errorf("permission %q has invalid scope %q", item.Permission, item.Scope)
		}
		if existing, ok := result[item.Permission]; !ok || existing == ScopeOwn && item.Scope == ScopeAll {
			result[item.Permission] = item.Scope
		}
	}
	return result, nil
}

func (g Grants) Scope(permission Permission) (Scope, bool) {
	scope, ok := g[permission]
	return scope, ok
}

func (g Grants) Allows(permission Permission, subjectID, ownerID string) bool {
	scope, ok := g[permission]
	if !ok {
		return false
	}
	if scope == ScopeAll {
		return true
	}
	return scope == ScopeOwn && subjectID != "" && subjectID == ownerID
}

func (g Grants) CanGrant(permission Permission, scope Scope) bool {
	current, ok := g[permission]
	if !ok || !scope.Valid() {
		return false
	}
	return current == ScopeAll || current == scope
}
