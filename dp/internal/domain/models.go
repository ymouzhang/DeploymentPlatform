package domain

import (
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"DP/internal/access"
)

var serviceTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

var (
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrAlreadyInstalled    = errors.New("service already installed")
	ErrNotInstalled        = errors.New("service not installed")
	ErrOperationInProgress = errors.New("operation in progress")
	ErrTimedOut            = errors.New("operation timed out")
	ErrUnauthorized        = errors.New("unauthorized")
	ErrForbidden           = errors.New("forbidden")
)

type User struct {
	ID                 string           `json:"id"`
	Username           string           `json:"username"`
	PasswordHash       string           `json:"-"`
	Roles              []access.RoleRef `json:"roles"`
	Permissions        access.Grants    `json:"permissions"`
	Enabled            bool             `json:"enabled"`
	MustChangePassword bool             `json:"must_change_password"`
	IsInitialAdmin     bool             `json:"is_initial_admin"`
	CreatedBy          string           `json:"created_by,omitempty"`
	CreatedByUsername  string           `json:"created_by_username,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"-"`
	SourceIP   string    `json:"source_ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

type Environment struct {
	ID                     string           `json:"id"`
	OwnerID                string           `json:"owner_id"`
	OwnerUsername          string           `json:"owner_username,omitempty"`
	Name                   string           `json:"name"`
	IP                     string           `json:"ip"`
	SSHUser                string           `json:"ssh_user"`
	SSHPort                int              `json:"ssh_port"`
	SSHPasswordEnc         string           `json:"-"`
	InstallDir             string           `json:"install_dir"`
	ServiceType            string           `json:"service_type"`
	Note                   string           `json:"note"`
	Installed              bool             `json:"installed"`
	InstalledAt            *time.Time       `json:"installed_at"`
	InstalledPackageSHA256 string           `json:"installed_package_sha256,omitempty"`
	HealthPort             *int             `json:"health_port,omitempty"`
	Arch                   string           `json:"arch"`
	HostKeyFingerprint     string           `json:"-"`
	LastValidationAt       *time.Time       `json:"last_validation_at"`
	CreatedAt              time.Time        `json:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
	Tags                   []ResourceTagRef `json:"tags"`
}

type EnvironmentView struct {
	Environment
	HasPassword bool `json:"has_password"`
}

type EnvironmentInput struct {
	Name        string   `json:"name"`
	IP          string   `json:"ip"`
	SSHUser     string   `json:"ssh_user"`
	SSHPort     int      `json:"ssh_port"`
	SSHPassword string   `json:"ssh_password,omitempty"`
	InstallDir  string   `json:"install_dir"`
	ServiceType string   `json:"service_type"`
	Note        string   `json:"note"`
	TagIDs      []string `json:"tag_ids"`
}

type ResourceTagRef struct {
	ID        string `json:"id"`
	GroupName string `json:"group_name"`
	Value     string `json:"value"`
}

type ResourceTag struct {
	ResourceTagRef
	OwnerID          string    `json:"owner_id"`
	OwnerUsername    string    `json:"owner_username,omitempty"`
	EnvironmentCount int       `json:"environment_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ResourceTagInput struct {
	GroupName string `json:"group_name"`
	Value     string `json:"value"`
}

func (in *ResourceTagInput) Normalize() {
	in.GroupName = strings.TrimSpace(in.GroupName)
	in.Value = strings.TrimSpace(in.Value)
}

func (in ResourceTagInput) Validate() error {
	if utf8.RuneCountInString(in.GroupName) < 1 || utf8.RuneCountInString(in.GroupName) > 32 {
		return FieldError("group_name", "标签分组长度必须为 1 到 32 个字符")
	}
	if utf8.RuneCountInString(in.Value) < 1 || utf8.RuneCountInString(in.Value) > 32 {
		return FieldError("value", "标签值长度必须为 1 到 32 个字符")
	}
	if strings.ContainsAny(in.GroupName+in.Value, "\x00\r\n") {
		return FieldError("value", "标签不能包含换行或空字符")
	}
	return nil
}

// MaxNoteLength 是备注字段允许的最大字符数（环境、安装包共用）。
const MaxNoteLength = 200

// ValidateNote 校验备注长度。
func ValidateNote(note string) error {
	if utf8.RuneCountInString(note) > MaxNoteLength {
		return FieldError("note", fmt.Sprintf("备注不能超过 %d 个字符", MaxNoteLength))
	}
	return nil
}

func (in *EnvironmentInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.IP = strings.TrimSpace(in.IP)
	in.SSHUser = strings.TrimSpace(in.SSHUser)
	in.InstallDir = strings.TrimSpace(in.InstallDir)
	in.ServiceType = strings.ToLower(strings.TrimSpace(in.ServiceType))
	in.Note = strings.TrimSpace(in.Note)
	if in.SSHUser == "" {
		in.SSHUser = "aaron"
	}
	if in.SSHPort == 0 {
		in.SSHPort = 22
	}
	if parsed := net.ParseIP(in.IP); parsed != nil {
		in.IP = parsed.String()
	}
}

func (in EnvironmentInput) Validate(requirePassword bool) error {
	if in.Name == "" {
		return FieldError("name", "环境名称不能为空")
	}
	if net.ParseIP(in.IP) == nil {
		return FieldError("ip", "服务器 IP 格式不正确")
	}
	if in.SSHUser == "" {
		return FieldError("ssh_user", "SSH 用户不能为空")
	}
	if in.SSHPort < 1 || in.SSHPort > 65535 {
		return FieldError("ssh_port", "SSH 端口必须在 1 到 65535 之间")
	}
	if requirePassword && in.SSHPassword == "" {
		return FieldError("ssh_password", "SSH 密码不能为空")
	}
	if !strings.HasPrefix(in.InstallDir, "/") || path.Clean(in.InstallDir) != in.InstallDir ||
		strings.ContainsAny(in.InstallDir, "\x00\r\n") {
		return FieldError("install_dir", "安装目录必须是规范的绝对路径")
	}
	if err := ValidateServiceType(in.ServiceType); err != nil {
		return err
	}
	if err := ValidateNote(in.Note); err != nil {
		return err
	}
	if len(in.TagIDs) > 20 {
		return FieldError("tag_ids", "每个环境最多关联 20 个标签")
	}
	seenTags := make(map[string]struct{}, len(in.TagIDs))
	for _, id := range in.TagIDs {
		if strings.TrimSpace(id) == "" {
			return FieldError("tag_ids", "标签 ID 不能为空")
		}
		if _, exists := seenTags[id]; exists {
			return FieldError("tag_ids", "标签 ID 不能重复")
		}
		seenTags[id] = struct{}{}
	}
	return nil
}

func ValidateServiceType(value string) error {
	if !serviceTypePattern.MatchString(value) {
		return FieldError(
			"service_type",
			"服务类型必须以小写字母开头，只能包含小写字母、数字和连字符，长度不超过 63",
		)
	}
	return nil
}

type FieldValidationError struct {
	Field   string
	Message string
}

func (e *FieldValidationError) Error() string { return e.Message }

func FieldError(field, message string) error {
	return &FieldValidationError{Field: field, Message: message}
}

type Package struct {
	OwnerID          string    `json:"owner_id"`
	OwnerUsername    string    `json:"owner_username,omitempty"`
	ServiceType      string    `json:"service_type"`
	CurrentVersionID string    `json:"current_version_id"`
	VersionCount     int       `json:"version_count"`
	ReferencedCount  int       `json:"referenced_environment_count"`
	OriginalFilename string    `json:"original_filename"`
	StoragePath      string    `json:"-"`
	SHA256           string    `json:"sha256"`
	SizeBytes        int64     `json:"size_bytes"`
	ConfigPort       int       `json:"config_port"`
	Note             string    `json:"note"`
	UploadedAt       time.Time `json:"uploaded_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type PackageVersion struct {
	ID               string    `json:"id"`
	OwnerID          string    `json:"owner_id"`
	ServiceType      string    `json:"service_type"`
	OriginalFilename string    `json:"original_filename"`
	StoragePath      string    `json:"-"`
	SHA256           string    `json:"sha256"`
	SizeBytes        int64     `json:"size_bytes"`
	ConfigPort       int       `json:"config_port"`
	ConfigFormat     string    `json:"config_format"`
	ConfigPath       string    `json:"config_path"`
	ConfigContent    []byte    `json:"-"`
	ValidationStatus string    `json:"validation_status"`
	Note             string    `json:"note"`
	UploadedBy       string    `json:"uploaded_by,omitempty"`
	UploadedByName   string    `json:"uploaded_by_username"`
	UploadedAt       time.Time `json:"uploaded_at"`
	Current          bool      `json:"current"`
	ReferencedCount  int       `json:"referenced_environment_count"`
}

type ServiceConfig struct {
	EnvironmentID     string    `json:"environment_id"`
	Content           string    `json:"content"`
	Format            string    `json:"format"`
	Path              string    `json:"path"`
	Port              int       `json:"port"`
	Inherited         bool      `json:"inherited"`
	CurrentRevisionID string    `json:"current_revision_id,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
	PackageContent    string    `json:"package_content"`
	PackageVersionID  string    `json:"package_version_id"`
	PackageFilename   string    `json:"package_filename"`
	PackageChanged    bool      `json:"package_changed"`
	PackageUpdated    bool      `json:"package_updated"`
}

type ServiceConfigPreview struct {
	CurrentContent  string `json:"current_content"`
	ProposedContent string `json:"proposed_content"`
	Changed         bool   `json:"changed"`
	Format          string `json:"format"`
	Path            string `json:"path"`
	Port            int    `json:"port"`
}

type ServiceConfigRevision struct {
	ID             string    `json:"id"`
	EnvironmentID  string    `json:"environment_id"`
	Content        string    `json:"content,omitempty"`
	Format         string    `json:"format"`
	Path           string    `json:"path"`
	Port           int       `json:"port"`
	Source         string    `json:"source"`
	RestoredFromID string    `json:"restored_from_id,omitempty"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedByName  string    `json:"created_by_username"`
	CreatedAt      time.Time `json:"created_at"`
	Current        bool      `json:"current"`
}

type OperationAction string
type OperationStatus string

const (
	ActionInstall OperationAction = "install"
	ActionStart   OperationAction = "start"
	ActionStop    OperationAction = "stop"
	ActionReset   OperationAction = "reset"

	OperationQueued      OperationStatus = "queued"
	OperationRunning     OperationStatus = "running"
	OperationSucceeded   OperationStatus = "succeeded"
	OperationFailed      OperationStatus = "failed"
	OperationTimedOut    OperationStatus = "timed_out"
	OperationInterrupted OperationStatus = "interrupted"
)

type Operation struct {
	ID              string           `json:"id"`
	EnvironmentID   string           `json:"environment_id"`
	RequestID       string           `json:"request_id,omitempty"`
	ActorUserID     string           `json:"actor_user_id,omitempty"`
	ActorUsername   string           `json:"actor_username,omitempty"`
	OwnerID         string           `json:"owner_id,omitempty"`
	OwnerUsername   string           `json:"owner_username,omitempty"`
	EnvironmentName string           `json:"environment_name,omitempty"`
	EnvironmentIP   string           `json:"environment_ip,omitempty"`
	ServiceType     string           `json:"service_type,omitempty"`
	Action          OperationAction  `json:"action"`
	Status          OperationStatus  `json:"status"`
	Stage           string           `json:"stage"`
	ExitCode        *int             `json:"exit_code"`
	ErrorCode       string           `json:"error_code,omitempty"`
	ErrorMessage    string           `json:"error_message,omitempty"`
	LogPath         string           `json:"-"`
	CreatedAt       time.Time        `json:"created_at"`
	StartedAt       *time.Time       `json:"started_at"`
	FinishedAt      *time.Time       `json:"finished_at"`
	Tags            []ResourceTagRef `json:"tags"`
}

type OperationFilter struct {
	ActorID, OwnerID, Action, Status, Keyword string
	From, To, CursorTime                      *time.Time
	CursorID                                  string
	TagIDs                                    []string
	Limit                                     int
}

type ModelStatus string
type ModelTaskAction string

const (
	ModelUploading ModelStatus = "uploading"
	ModelDeploying ModelStatus = "deploying"
	ModelReady     ModelStatus = "ready"
	ModelFailed    ModelStatus = "failed"
	ModelDeleting  ModelStatus = "deleting"
	ModelDeleted   ModelStatus = "deleted"

	ModelTaskDeploy ModelTaskAction = "deploy"
	ModelTaskDelete ModelTaskAction = "delete"
)

type Model struct {
	ID                string      `json:"id"`
	OwnerID           string      `json:"owner_id"`
	MarkerOwnerID     string      `json:"-"`
	OwnerUsername     string      `json:"owner_username,omitempty"`
	EnvironmentID     string      `json:"environment_id"`
	EnvironmentName   string      `json:"environment_name"`
	EnvironmentIP     string      `json:"environment_ip"`
	Name              string      `json:"name"`
	Source            string      `json:"source"`
	TargetDir         string      `json:"target_dir"`
	OriginalFilename  string      `json:"original_filename"`
	SizeBytes         int64       `json:"size_bytes"`
	ExpandedSizeBytes int64       `json:"expanded_size_bytes"`
	FileCount         int64       `json:"file_count"`
	SHA256            string      `json:"sha256,omitempty"`
	Status            ModelStatus `json:"status"`
	ErrorMessage      string      `json:"error_message,omitempty"`
	CreatedBy         string      `json:"created_by,omitempty"`
	CreatedByUsername string      `json:"created_by_username,omitempty"`
	CreatedAt         time.Time   `json:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
	ReadyAt           *time.Time  `json:"ready_at,omitempty"`
	DeletedAt         *time.Time  `json:"deleted_at,omitempty"`
	LatestTask        *ModelTask  `json:"latest_task,omitempty"`
}

type ModelUpload struct {
	ID         string    `json:"id"`
	ModelID    string    `json:"model_id"`
	OwnerID    string    `json:"owner_id"`
	RemotePath string    `json:"-"`
	Offset     int64     `json:"offset"`
	TotalBytes int64     `json:"total_bytes"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ModelTask struct {
	ID            string          `json:"id"`
	ModelID       string          `json:"model_id"`
	OwnerID       string          `json:"owner_id"`
	ActorUserID   string          `json:"actor_user_id,omitempty"`
	ActorUsername string          `json:"actor_username,omitempty"`
	Action        ModelTaskAction `json:"action"`
	Status        OperationStatus `json:"status"`
	Stage         string          `json:"stage"`
	Progress      int             `json:"progress"`
	ErrorCode     string          `json:"error_code,omitempty"`
	ErrorMessage  string          `json:"error_message,omitempty"`
	LogPath       string          `json:"-"`
	CreatedAt     time.Time       `json:"created_at"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
}

type ModelUploadCreateInput struct {
	Name             string `json:"name"`
	EnvironmentID    string `json:"environment_id"`
	TargetDir        string `json:"target_dir"`
	OriginalFilename string `json:"original_filename"`
	TotalBytes       int64  `json:"total_bytes"`
}

func (in *ModelUploadCreateInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.EnvironmentID = strings.TrimSpace(in.EnvironmentID)
	in.TargetDir = strings.TrimSpace(in.TargetDir)
	in.OriginalFilename = strings.TrimSpace(in.OriginalFilename)
}

func (in ModelUploadCreateInput) Validate(maxBytes int64) error {
	if utf8.RuneCountInString(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 128 || strings.ContainsAny(in.Name, "\x00\r\n") {
		return FieldError("name", "模型名称长度必须为 1 到 128 个字符且不能包含换行")
	}
	if in.EnvironmentID == "" {
		return FieldError("environment_id", "请选择目标环境")
	}
	if !strings.HasSuffix(strings.ToLower(in.OriginalFilename), ".tar.gz") {
		return FieldError("original_filename", "模型文件必须是 .tar.gz 压缩包")
	}
	if in.TotalBytes <= 0 || in.TotalBytes > maxBytes {
		return FieldError("total_bytes", fmt.Sprintf("模型压缩包大小必须大于 0 且不超过 %d 字节", maxBytes))
	}
	if !strings.HasPrefix(in.TargetDir, "/") || path.Clean(in.TargetDir) != in.TargetDir || strings.ContainsAny(in.TargetDir, "\x00\r\n") {
		return FieldError("target_dir", "目标目录必须是规范的绝对路径")
	}
	switch in.TargetDir {
	case "/", "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/tmp", "/usr", "/var":
		return FieldError("target_dir", "目标目录不能是系统关键目录")
	}
	return nil
}

type Notification struct {
	ID            string     `json:"id"`
	DedupeKey     string     `json:"-"`
	RiskLevel     string     `json:"risk_level"`
	Category      string     `json:"category"`
	Title         string     `json:"title"`
	Message       string     `json:"message"`
	TargetType    string     `json:"target_type,omitempty"`
	TargetID      string     `json:"target_id,omitempty"`
	TargetLabel   string     `json:"target_label,omitempty"`
	OwnerID       string     `json:"owner_id,omitempty"`
	OwnerUsername string     `json:"owner_username,omitempty"`
	OperationID   string     `json:"operation_id,omitempty"`
	Link          string     `json:"link"`
	CreatedAt     time.Time  `json:"created_at"`
	ReadAt        *time.Time `json:"read_at,omitempty"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	ReadBy        string     `json:"read_by,omitempty"`
	ResolvedBy    string     `json:"resolved_by,omitempty"`
	Read          bool       `json:"read"`
	Resolved      bool       `json:"resolved"`
}

type NotificationFilter struct {
	Unread     *bool
	RiskLevel  string
	CursorTime *time.Time
	CursorID   string
	Limit      int
}

type NotificationSummary struct {
	Unread     int `json:"unread"`
	Unresolved int `json:"unresolved"`
}

type CommunicationStatus string
type CommunicationMessageType string

const (
	CommunicationOpen   CommunicationStatus = "open"
	CommunicationClosed CommunicationStatus = "closed"

	CommunicationAdminMessage CommunicationMessageType = "admin_message"
	CommunicationUserReceipt  CommunicationMessageType = "user_receipt"
	CommunicationSystemClosed CommunicationMessageType = "system_closed"
	CommunicationSystemReopen CommunicationMessageType = "system_reopened"
)

type CommunicationResourceInput struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id,omitempty"`
	ResourceKey  string `json:"resource_key,omitempty"`
}

type CommunicationCreateInput struct {
	TargetUserID string                       `json:"target_user_id"`
	Title        string                       `json:"title"`
	Content      string                       `json:"content"`
	Resources    []CommunicationResourceInput `json:"resources"`
}

type CommunicationRecipient struct {
	UserID   string     `json:"user_id"`
	Username string     `json:"username"`
	Roles    []string   `json:"roles,omitempty"`
	ReadAt   *time.Time `json:"read_at"`
}

type CommunicationMessage struct {
	ID             string                   `json:"id"`
	ThreadID       string                   `json:"-"`
	Type           CommunicationMessageType `json:"type"`
	SenderUserID   string                   `json:"sender_user_id,omitempty"`
	SenderUsername string                   `json:"sender_username"`
	SenderRoles    []string                 `json:"sender_roles,omitempty"`
	Content        string                   `json:"content"`
	CreatedAt      time.Time                `json:"created_at"`
	Recipients     []CommunicationRecipient `json:"recipients"`
}

type CommunicationResource struct {
	ID              string `json:"id"`
	ThreadID        string `json:"-"`
	ResourceType    string `json:"resource_type"`
	ResourceID      string `json:"resource_id,omitempty"`
	ResourceKey     string `json:"resource_key,omitempty"`
	OwnerID         string `json:"owner_id,omitempty"`
	OwnerUsername   string `json:"owner_username"`
	ResourceLabel   string `json:"resource_label"`
	ServiceType     string `json:"service_type"`
	EnvironmentName string `json:"environment_name"`
	EnvironmentIP   string `json:"environment_ip"`
	Available       bool   `json:"available"`
	Link            string `json:"link,omitempty"`
}

type Communication struct {
	ID                     string                  `json:"id"`
	TargetUserID           string                  `json:"target_user_id,omitempty"`
	TargetUsername         string                  `json:"target_username"`
	Title                  string                  `json:"title"`
	Status                 CommunicationStatus     `json:"status"`
	ReopenCount            int                     `json:"reopen_count"`
	CreatedBy              string                  `json:"created_by,omitempty"`
	CreatedByUsername      string                  `json:"created_by_username"`
	ClosedByUsername       string                  `json:"closed_by_username"`
	ClosedAt               *time.Time              `json:"closed_at"`
	LastReopenedByUsername string                  `json:"last_reopened_by_username"`
	LastReopenedAt         *time.Time              `json:"last_reopened_at"`
	CreatedAt              time.Time               `json:"created_at"`
	UpdatedAt              time.Time               `json:"updated_at"`
	UnreadCount            int                     `json:"unread_count"`
	LastMessage            string                  `json:"last_message,omitempty"`
	UserReadAt             *time.Time              `json:"user_read_at"`
	Resources              []CommunicationResource `json:"resources"`
	Messages               []CommunicationMessage  `json:"messages,omitempty"`
}

type CommunicationFilter struct {
	TargetUserID string
	Status       CommunicationStatus
	Unread       *bool
	Keyword      string
	CursorTime   *time.Time
	CursorID     string
	Limit        int
}

type CommunicationSummary struct {
	Unread int `json:"unread"`
}

type UserDetail struct {
	User
	PackageCount          int        `json:"package_count"`
	EnvironmentCount      int        `json:"environment_count"`
	ModelCount            int        `json:"model_count"`
	InstalledServiceCount int        `json:"installed_service_count"`
	RecentOperationCount  int        `json:"recent_operation_count"`
	ActiveSessionCount    int        `json:"active_session_count"`
	LoginFailureCount     int        `json:"login_failure_count"`
	HighRiskCount         int        `json:"high_risk_count"`
	LastLoginAt           *time.Time `json:"last_login_at,omitempty"`
	LastActivityAt        *time.Time `json:"last_activity_at,omitempty"`
	LastSourceIP          string     `json:"last_source_ip"`
}

type TransferResult struct {
	SourceUserID string `json:"source_user_id"`
	TargetUserID string `json:"target_user_id"`
	Packages     int    `json:"packages"`
	Environments int    `json:"environments"`
	Models       int    `json:"models"`
}

type DashboardMetrics struct {
	Users                       int `json:"users"`
	EnabledUsers                int `json:"enabled_users"`
	DisabledUsers               int `json:"disabled_users"`
	Packages                    int `json:"packages"`
	Environments                int `json:"environments"`
	InstalledServices           int `json:"installed_services"`
	RunningServices             int `json:"running_services"`
	ActiveOperations            int `json:"active_operations"`
	FailedOperations24h         int `json:"failed_operations_24h"`
	LoginFailures24h            int `json:"login_failures_24h"`
	UnvalidatedEnvironments     int `json:"unvalidated_environments"`
	StaleValidationEnvironments int `json:"stale_validation_environments"`
	UnhealthyInstalledServices  int `json:"unhealthy_installed_services"`
	HighRiskAudits24h           int `json:"high_risk_audits_24h"`
	UnreadNotifications         int `json:"unread_notifications"`
	UnreadCommunications        int `json:"unread_communications"`
}

type AdminDashboard struct {
	Metrics        DashboardMetrics `json:"metrics"`
	Communications []Communication  `json:"communications"`
	Notifications  []Notification   `json:"notifications"`
}

type OperationEvent struct {
	Seq     int64     `json:"seq"`
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Stream  string    `json:"stream,omitempty"`
	Message string    `json:"message,omitempty"`
	Status  string    `json:"status,omitempty"`
	Stage   string    `json:"stage,omitempty"`
}

type AuditEvent struct {
	ID            string         `json:"id"`
	OccurredAt    time.Time      `json:"occurred_at"`
	Category      string         `json:"category"`
	Action        string         `json:"action"`
	Outcome       string         `json:"outcome"`
	RiskLevel     string         `json:"risk_level"`
	ActorUserID   string         `json:"actor_user_id,omitempty"`
	ActorUsername string         `json:"actor_username"`
	ActorRoles    []string       `json:"actor_roles,omitempty"`
	OwnerID       string         `json:"owner_id,omitempty"`
	OwnerUsername string         `json:"owner_username,omitempty"`
	TargetType    string         `json:"target_type,omitempty"`
	TargetID      string         `json:"target_id,omitempty"`
	TargetLabel   string         `json:"target_label,omitempty"`
	RequestID     string         `json:"request_id"`
	OperationID   string         `json:"operation_id,omitempty"`
	SourceIP      string         `json:"source_ip,omitempty"`
	UserAgent     string         `json:"user_agent,omitempty"`
	ErrorCode     string         `json:"error_code,omitempty"`
	Changes       map[string]any `json:"changes,omitempty"`
}

type AuditFilter struct {
	From       *time.Time
	To         *time.Time
	ActorID    string
	OwnerID    string
	Category   string
	Action     string
	Outcome    string
	SourceIP   string
	Keyword    string
	CursorTime *time.Time
	CursorID   string
	Limit      int
}

type AuditSummary struct {
	Total         int `json:"total"`
	Failures      int `json:"failures"`
	LoginFailures int `json:"login_failures"`
	HighRisk      int `json:"high_risk"`
}

type HealthResult struct {
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at"`
}

type ServiceView struct {
	Environment EnvironmentView `json:"environment"`
	Health      HealthResult    `json:"health"`
	ServicePort *int            `json:"service_port,omitempty"`
	Busy        bool            `json:"busy"`
	// LastOperation 是该环境最近一次生命周期操作，用于区分“从未安装”与“安装失败”。
	LastOperation *OperationSummary `json:"last_operation,omitempty"`
}

type OperationSummary struct {
	Action       OperationAction `json:"action"`
	Status       OperationStatus `json:"status"`
	ErrorMessage string          `json:"error_message,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at"`
}

type AppError struct {
	Code    string
	Message string
	Err     error
}

func (e *AppError) Error() string {
	if e.Err == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

func (e *AppError) Unwrap() error { return e.Err }

type ExistingInstallationError struct {
	PackageSHA256 string
	HealthPort    int
}

func (e *ExistingInstallationError) Error() string {
	return "remote installation marker already exists"
}

func (e *ExistingInstallationError) Unwrap() error { return ErrAlreadyInstalled }

func ParsePort(raw any) (int, error) {
	var value int64
	switch v := raw.(type) {
	case float64:
		if v != float64(int64(v)) {
			return 0, errors.New("port must be an integer")
		}
		value = int64(v)
	case int:
		value = int64(v)
	case int64:
		value = v
	case uint64:
		if v > uint64(^uint(0)>>1) {
			return 0, errors.New("port is too large")
		}
		value = int64(v)
	case string:
		parsed, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return 0, errors.New("port must be numeric")
		}
		value = parsed
	default:
		return 0, errors.New("port is missing or invalid")
	}
	if value < 1 || value > 65535 {
		return 0, errors.New("port must be between 1 and 65535")
	}
	return int(value), nil
}
