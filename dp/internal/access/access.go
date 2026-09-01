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
	EnvironmentRead     Permission = "environment.read"
	EnvironmentWrite    Permission = "environment.write"
	EnvironmentDelete   Permission = "environment.delete"
	EnvironmentValidate Permission = "environment.validate"
	EnvironmentImport   Permission = "environment.import"
	EnvironmentExport   Permission = "environment.export"
	TagRead             Permission = "tag.read"
	TagWrite            Permission = "tag.write"
	ModelRead           Permission = "model.read"
	ModelUpload         Permission = "model.upload"
	ModelDelete         Permission = "model.delete"
	ServiceRead         Permission = "service.read"
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
