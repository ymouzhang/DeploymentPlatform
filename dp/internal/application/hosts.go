package application

import (
	"context"
	"errors"
	"time"

	"DP/internal/domain"
	"DP/internal/remote"
	"DP/internal/security"
)

type HostRepository interface {
	CreateHost(context.Context, domain.Host) (domain.Host, error)
	UpdateHost(context.Context, domain.Host) (domain.Host, error)
	DeleteHost(context.Context, string) error
	GetHost(context.Context, string) (domain.Host, error)
	ListHosts(context.Context, string) ([]domain.Host, error)
	UpsertHosts(context.Context, []domain.Host) (int, int, error)
	RecordHostValidation(context.Context, string, string, string) error
	HostHasServiceInstances(context.Context, string) (bool, error)
	HostHasModels(context.Context, string) (bool, error)
}

type HostService struct {
	store  HostRepository
	cipher *security.PasswordCipher
	remote *remote.Executor
}

func NewHostService(store HostRepository, cipher *security.PasswordCipher, executor *remote.Executor) *HostService {
	return &HostService{store: store, cipher: cipher, remote: executor}
}

func (s *HostService) List(ctx context.Context, ownerID string) ([]domain.HostView, error) {
	hosts, err := s.store.ListHosts(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	result := make([]domain.HostView, 0, len(hosts))
	for _, host := range hosts {
		result = append(result, hostView(host))
	}
	return result, nil
}

func (s *HostService) Create(ctx context.Context, ownerID string, input domain.HostInput) (domain.HostView, error) {
	input.Normalize()
	if err := input.Validate(true); err != nil {
		return domain.HostView{}, err
	}
	encrypted, err := s.cipher.Encrypt(input.SSHPassword)
	if err != nil {
		return domain.HostView{}, err
	}
	host, err := s.store.CreateHost(ctx, domain.Host{OwnerID: ownerID, Name: input.Name, IP: input.IP,
		SSHUser: input.SSHUser, SSHPort: input.SSHPort, SSHPasswordEnc: encrypted, Note: input.Note})
	if errors.Is(err, domain.ErrConflict) {
		return domain.HostView{}, &domain.AppError{Code: "HOST_CONFLICT", Message: "该账号已注册相同 IP 和 SSH 端口的主机"}
	}
	if err != nil {
		return domain.HostView{}, err
	}
	return hostView(host), nil
}

func (s *HostService) Update(ctx context.Context, id string, input domain.HostInput) (domain.HostView, error) {
	input.Normalize()
	if err := input.Validate(false); err != nil {
		return domain.HostView{}, err
	}
	host, err := s.store.GetHost(ctx, id)
	if err != nil {
		return domain.HostView{}, err
	}
	connectionChanged := host.IP != input.IP || host.SSHUser != input.SSHUser || host.SSHPort != input.SSHPort
	host.Name, host.IP, host.SSHUser, host.SSHPort, host.Note = input.Name, input.IP, input.SSHUser, input.SSHPort, input.Note
	if input.SSHPassword != "" {
		host.SSHPasswordEnc, err = s.cipher.Encrypt(input.SSHPassword)
		if err != nil {
			return domain.HostView{}, err
		}
		connectionChanged = true
	}
	if connectionChanged {
		host.HostKeyFingerprint, host.Arch, host.LastValidationAt = "", "", nil
	}
	host, err = s.store.UpdateHost(ctx, host)
	if errors.Is(err, domain.ErrConflict) {
		return domain.HostView{}, &domain.AppError{Code: "HOST_CONFLICT", Message: "该账号已注册相同 IP 和 SSH 端口的主机"}
	}
	if err != nil {
		return domain.HostView{}, err
	}
	return hostView(host), nil
}

func (s *HostService) Delete(ctx context.Context, id string) error {
	if used, err := s.store.HostHasServiceInstances(ctx, id); err != nil {
		return err
	} else if used {
		return &domain.AppError{Code: "HOST_HAS_SERVICES", Message: "主机仍有关联服务实例，不能删除"}
	}
	if used, err := s.store.HostHasModels(ctx, id); err != nil {
		return err
	} else if used {
		return &domain.AppError{Code: "HOST_HAS_MODELS", Message: "主机仍有关联模型，不能删除"}
	}
	if err := s.store.DeleteHost(ctx, id); errors.Is(err, domain.ErrConflict) {
		return &domain.AppError{Code: "HOST_IN_USE", Message: "主机刚被服务实例或模型引用，不能删除"}
	} else {
		return err
	}
}

func (s *HostService) ValidateDraft(ctx context.Context, input domain.HostInput) (remote.ValidationResult, error) {
	input.Normalize()
	if err := input.Validate(true); err != nil {
		return remote.ValidationResult{}, err
	}
	password := []byte(input.SSHPassword)
	defer clear(password)
	return s.remote.ValidateHost(ctx, domain.Host{IP: input.IP, SSHUser: input.SSHUser, SSHPort: input.SSHPort}, password)
}

func (s *HostService) ValidateSaved(ctx context.Context, id string) (remote.ValidationResult, error) {
	host, password, err := s.WithPassword(ctx, id)
	if err != nil {
		return remote.ValidationResult{}, err
	}
	defer clear(password)
	result, err := s.remote.ValidateHost(ctx, host, password)
	if err != nil {
		return result, err
	}
	if err := s.store.RecordHostValidation(ctx, id, result.Fingerprint, result.Arch); err != nil {
		return result, err
	}
	return result, nil
}

func (s *HostService) WithPassword(ctx context.Context, id string) (domain.Host, []byte, error) {
	host, err := s.store.GetHost(ctx, id)
	if err != nil {
		return domain.Host{}, nil, err
	}
	password, err := s.cipher.Decrypt(host.SSHPasswordEnc)
	if err != nil {
		return domain.Host{}, nil, err
	}
	return host, password, nil
}

type HostExportDocument struct {
	SchemaVersion int          `json:"schema_version"`
	ExportedAt    time.Time    `json:"exported_at"`
	Hosts         []ExportHost `json:"hosts"`
}

type ExportHost struct {
	Name                 string `json:"name"`
	IP                   string `json:"ip"`
	SSHUser              string `json:"ssh_user"`
	SSHPort              int    `json:"ssh_port"`
	SSHPasswordEncrypted string `json:"ssh_password_encrypted"`
	Note                 string `json:"note,omitempty"`
}

type HostImportResult struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Total   int `json:"total"`
}

func (s *HostService) Import(ctx context.Context, ownerID string, doc HostExportDocument) (HostImportResult, error) {
	if doc.SchemaVersion != 1 || len(doc.Hosts) == 0 {
		return HostImportResult{}, domain.FieldError("schema_version", "导入文件必须是包含主机的 schema v1 文件")
	}
	items := make([]domain.Host, 0, len(doc.Hosts))
	for _, exported := range doc.Hosts {
		input := domain.HostInput{Name: exported.Name, IP: exported.IP, SSHUser: exported.SSHUser, SSHPort: exported.SSHPort, Note: exported.Note}
		input.Normalize()
		if err := input.Validate(false); err != nil {
			return HostImportResult{}, err
		}
		if exported.SSHPasswordEncrypted == "" {
			return HostImportResult{}, domain.FieldError("ssh_password_encrypted", "主机缺少加密 SSH 密码")
		}
		password, err := s.cipher.Decrypt(exported.SSHPasswordEncrypted)
		if err != nil {
			return HostImportResult{}, domain.FieldError("ssh_password_encrypted", "SSH 密码无法使用当前主密钥解密")
		}
		clear(password)
		items = append(items, domain.Host{OwnerID: ownerID, Name: input.Name, IP: input.IP, SSHUser: input.SSHUser, SSHPort: input.SSHPort, SSHPasswordEnc: exported.SSHPasswordEncrypted, Note: input.Note})
	}
	created, updated, err := s.store.UpsertHosts(ctx, items)
	return HostImportResult{Created: created, Updated: updated, Total: len(items)}, err
}

func (s *HostService) Export(ctx context.Context, ownerID string) (HostExportDocument, error) {
	hosts, err := s.store.ListHosts(ctx, ownerID)
	if err != nil {
		return HostExportDocument{}, err
	}
	doc := HostExportDocument{SchemaVersion: 1, ExportedAt: time.Now().UTC(), Hosts: make([]ExportHost, 0, len(hosts))}
	for _, host := range hosts {
		doc.Hosts = append(doc.Hosts, ExportHost{Name: host.Name, IP: host.IP, SSHUser: host.SSHUser,
			SSHPort: host.SSHPort, SSHPasswordEncrypted: host.SSHPasswordEnc, Note: host.Note})
	}
	return doc, nil
}

func hostView(host domain.Host) domain.HostView {
	hasPassword := host.SSHPasswordEnc != ""
	host.SSHPasswordEnc, host.HostKeyFingerprint = "", ""
	return domain.HostView{Host: host, HasPassword: hasPassword}
}
