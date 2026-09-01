package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"DP/internal/domain"
	"DP/internal/remote"
	"DP/internal/security"
)

type EnvironmentRepository interface {
	CreateEnvironmentWithTags(context.Context, domain.Environment, []string) (domain.Environment, error)
	DeleteServiceConfig(context.Context, string) error
	GetEnvironment(context.Context, string) (domain.Environment, error)
	GetPackageByOwner(context.Context, string, string) (domain.Package, error)
	ListEnvironments(context.Context) ([]domain.Environment, error)
	ListEnvironmentsByOwner(context.Context, string) ([]domain.Environment, error)
	RecordValidation(context.Context, string, string, string) error
	UpdateEnvironmentWithTags(context.Context, domain.Environment, []string) (domain.Environment, error)
	UpsertImportedEnvironments(context.Context, []domain.Environment) (int, int, error)
	ValidateTagIDs(context.Context, string, []string) error
}

type EnvironmentService struct {
	store  EnvironmentRepository
	cipher *security.PasswordCipher
	remote *remote.Executor
}

func NewEnvironmentService(
	store EnvironmentRepository,
	cipher *security.PasswordCipher,
	remoteExecutor *remote.Executor,
) *EnvironmentService {
	return &EnvironmentService{store: store, cipher: cipher, remote: remoteExecutor}
}

func (s *EnvironmentService) List(ctx context.Context, ownerID string) ([]domain.EnvironmentView, error) {
	return s.ListFiltered(ctx, ownerID, nil)
}

func (s *EnvironmentService) ListFiltered(ctx context.Context, ownerID string, tagIDs []string) ([]domain.EnvironmentView, error) {
	var environments []domain.Environment
	var err error
	if ownerID == "" {
		environments, err = s.store.ListEnvironments(ctx)
	} else {
		environments, err = s.store.ListEnvironmentsByOwner(ctx, ownerID)
	}
	if err != nil {
		return nil, err
	}
	environments = FilterEnvironmentsByTagIDs(environments, tagIDs)
	result := make([]domain.EnvironmentView, 0, len(environments))
	for _, env := range environments {
		result = append(result, viewEnvironment(env))
	}
	return result, nil
}

func (s *EnvironmentService) Create(ctx context.Context, ownerID string, input domain.EnvironmentInput) (domain.EnvironmentView, error) {
	input.Normalize()
	if err := input.Validate(true); err != nil {
		return domain.EnvironmentView{}, err
	}
	if err := s.requireServicePackage(ctx, ownerID, input.ServiceType); err != nil {
		return domain.EnvironmentView{}, err
	}
	if err := s.store.ValidateTagIDs(ctx, ownerID, input.TagIDs); err != nil {
		return domain.EnvironmentView{}, err
	}
	encrypted, err := s.cipher.Encrypt(input.SSHPassword)
	if err != nil {
		return domain.EnvironmentView{}, err
	}
	env, err := s.store.CreateEnvironmentWithTags(ctx, domain.Environment{
		OwnerID: ownerID,
		Name:    input.Name, IP: input.IP, SSHUser: input.SSHUser, SSHPort: input.SSHPort,
		SSHPasswordEnc: encrypted, InstallDir: input.InstallDir, ServiceType: input.ServiceType,
		Note: input.Note,
	}, input.TagIDs)
	if err != nil {
		return domain.EnvironmentView{}, err
	}
	return viewEnvironment(env), nil
}

func (s *EnvironmentService) Update(
	ctx context.Context,
	id string,
	input domain.EnvironmentInput,
) (domain.EnvironmentView, error) {
	input.Normalize()
	if err := input.Validate(false); err != nil {
		return domain.EnvironmentView{}, err
	}
	env, err := s.store.GetEnvironment(ctx, id)
	if err != nil {
		return domain.EnvironmentView{}, err
	}
	if err := s.requireServicePackage(ctx, env.OwnerID, input.ServiceType); err != nil {
		return domain.EnvironmentView{}, err
	}
	if input.TagIDs != nil {
		if err := s.store.ValidateTagIDs(ctx, env.OwnerID, input.TagIDs); err != nil {
			return domain.EnvironmentView{}, err
		}
	}
	originalIP, originalType, originalPort := env.IP, env.ServiceType, env.SSHPort
	if env.Installed &&
		(input.IP != env.IP || input.ServiceType != env.ServiceType || input.InstallDir != env.InstallDir) {
		return domain.EnvironmentView{}, domain.FieldError(
			"environment",
			"已安装服务不允许修改服务器 IP、服务类型或安装目录",
		)
	}
	env.Name = input.Name
	env.IP = input.IP
	env.SSHUser = input.SSHUser
	env.SSHPort = input.SSHPort
	env.InstallDir = input.InstallDir
	env.ServiceType = input.ServiceType
	env.Note = input.Note
	if input.SSHPassword != "" {
		env.SSHPasswordEnc, err = s.cipher.Encrypt(input.SSHPassword)
		if err != nil {
			return domain.EnvironmentView{}, err
		}
	}
	if originalIP != env.IP || originalType != env.ServiceType || originalPort != env.SSHPort {
		env.HostKeyFingerprint = ""
		env.LastValidationAt = nil
	}
	updated, err := s.store.UpdateEnvironmentWithTags(ctx, env, input.TagIDs)
	if err != nil {
		return domain.EnvironmentView{}, err
	}
	if originalType != updated.ServiceType {
		if err := s.store.DeleteServiceConfig(ctx, updated.ID); err != nil {
			return domain.EnvironmentView{}, err
		}
	}
	return viewEnvironment(updated), nil
}

func (s *EnvironmentService) ValidateDraft(
	ctx context.Context,
	ownerID string,
	input domain.EnvironmentInput,
) (remote.ValidationResult, error) {
	input.Normalize()
	if err := input.Validate(true); err != nil {
		return remote.ValidationResult{}, err
	}
	if err := s.requireServicePackage(ctx, ownerID, input.ServiceType); err != nil {
		return remote.ValidationResult{}, err
	}
	env := domain.Environment{
		Name: input.Name, IP: input.IP, SSHUser: input.SSHUser, SSHPort: input.SSHPort,
		InstallDir: input.InstallDir, ServiceType: input.ServiceType,
	}
	password := []byte(input.SSHPassword)
	defer clear(password)
	return s.remote.Validate(ctx, env, password)
}

func (s *EnvironmentService) ValidateSaved(ctx context.Context, id string) (remote.ValidationResult, error) {
	env, password, err := s.environmentWithPassword(ctx, id)
	if err != nil {
		return remote.ValidationResult{}, err
	}
	defer clear(password)
	result, err := s.remote.Validate(ctx, env, password)
	if err != nil {
		return result, err
	}
	if err := s.store.RecordValidation(ctx, id, result.Fingerprint, result.Arch); err != nil {
		return result, err
	}
	return result, nil
}

func (s *EnvironmentService) environmentWithPassword(
	ctx context.Context,
	id string,
) (domain.Environment, []byte, error) {
	env, err := s.store.GetEnvironment(ctx, id)
	if err != nil {
		return domain.Environment{}, nil, err
	}
	password, err := s.cipher.Decrypt(env.SSHPasswordEnc)
	if err != nil {
		return domain.Environment{}, nil, err
	}
	return env, password, nil
}

type ExportDocument struct {
	SchemaVersion int                 `json:"schema_version"`
	ExportedAt    time.Time           `json:"exported_at"`
	Environments  []ExportEnvironment `json:"environments"`
}

type ExportEnvironment struct {
	Name                   string                    `json:"name"`
	IP                     string                    `json:"ip"`
	SSHUser                string                    `json:"ssh_user"`
	SSHPort                int                       `json:"ssh_port"`
	SSHPasswordEncrypted   string                    `json:"ssh_password_encrypted"`
	InstallDir             string                    `json:"install_dir"`
	ServiceType            string                    `json:"service_type"`
	Installed              bool                      `json:"installed"`
	InstalledAt            *time.Time                `json:"installed_at,omitempty"`
	InstalledPackageSHA256 string                    `json:"installed_package_sha256,omitempty"`
	HealthPort             *int                      `json:"health_port,omitempty"`
	SSHHostKeyFingerprint  string                    `json:"host_key_fingerprint,omitempty"`
	Tags                   []domain.ResourceTagInput `json:"tags,omitempty"`
}

func (s *EnvironmentService) Export(ctx context.Context, ownerID string) (ExportDocument, error) {
	environments, err := s.store.ListEnvironmentsByOwner(ctx, ownerID)
	if err != nil {
		return ExportDocument{}, err
	}
	document := ExportDocument{
		SchemaVersion: 2, ExportedAt: time.Now().UTC(),
		Environments: make([]ExportEnvironment, 0, len(environments)),
	}
	for _, env := range environments {
		tags := make([]domain.ResourceTagInput, 0, len(env.Tags))
		for _, tag := range env.Tags {
			tags = append(tags, domain.ResourceTagInput{GroupName: tag.GroupName, Value: tag.Value})
		}
		document.Environments = append(document.Environments, ExportEnvironment{
			Name: env.Name, IP: env.IP, SSHUser: env.SSHUser, SSHPort: env.SSHPort,
			SSHPasswordEncrypted: env.SSHPasswordEnc, InstallDir: env.InstallDir,
			ServiceType: env.ServiceType, Installed: env.Installed,
			InstalledAt: env.InstalledAt, InstalledPackageSHA256: env.InstalledPackageSHA256,
			HealthPort: env.HealthPort, SSHHostKeyFingerprint: env.HostKeyFingerprint, Tags: tags,
		})
	}
	return document, nil
}

type ImportResult struct {
	Created     int `json:"created"`
	Overwritten int `json:"overwritten"`
	Total       int `json:"total"`
}

func (s *EnvironmentService) Import(ctx context.Context, ownerID string, document ExportDocument) (ImportResult, error) {
	if document.SchemaVersion != 1 && document.SchemaVersion != 2 {
		return ImportResult{}, domain.FieldError("schema_version", "不支持该导入文件版本")
	}
	if len(document.Environments) == 0 {
		return ImportResult{}, domain.FieldError("environments", "导入文件中没有环境信息")
	}
	environments := make([]domain.Environment, 0, len(document.Environments))
	seen := make(map[string]struct{}, len(document.Environments))
	for index, item := range document.Environments {
		input := domain.EnvironmentInput{
			Name: item.Name, IP: item.IP, SSHUser: item.SSHUser, SSHPort: item.SSHPort,
			InstallDir: item.InstallDir, ServiceType: item.ServiceType,
		}
		input.Normalize()
		if err := input.Validate(false); err != nil {
			return ImportResult{}, fmt.Errorf("第 %d 条环境信息无效: %w", index+1, err)
		}
		if err := s.requireServicePackage(ctx, ownerID, input.ServiceType); err != nil {
			return ImportResult{}, fmt.Errorf("第 %d 条环境信息无效: %w", index+1, err)
		}
		if item.SSHPasswordEncrypted == "" {
			return ImportResult{}, fmt.Errorf("第 %d 条环境信息缺少加密密码", index+1)
		}
		password, err := s.cipher.Decrypt(item.SSHPasswordEncrypted)
		if err != nil {
			return ImportResult{}, fmt.Errorf("第 %d 条环境密码无法解密，请检查主密钥: %w", index+1, err)
		}
		clear(password)
		key := input.IP + "\x00" + input.ServiceType
		if _, exists := seen[key]; exists {
			return ImportResult{}, fmt.Errorf("导入文件中第 %d 条环境与其他记录冲突", index+1)
		}
		seen[key] = struct{}{}
		if item.Installed && item.HealthPort == nil {
			return ImportResult{}, fmt.Errorf("第 %d 条已安装环境缺少 health_port", index+1)
		}
		tags := make([]domain.ResourceTagRef, 0, len(item.Tags))
		seenTags := map[string]struct{}{}
		for _, tagInput := range item.Tags {
			tagInput.Normalize()
			if err := tagInput.Validate(); err != nil {
				return ImportResult{}, fmt.Errorf("第 %d 条环境标签无效: %w", index+1, err)
			}
			tagKey := strings.ToLower(tagInput.GroupName) + "\x00" + strings.ToLower(tagInput.Value)
			if _, exists := seenTags[tagKey]; exists {
				return ImportResult{}, fmt.Errorf("第 %d 条环境包含重复标签", index+1)
			}
			seenTags[tagKey] = struct{}{}
			tags = append(tags, domain.ResourceTagRef{GroupName: tagInput.GroupName, Value: tagInput.Value})
		}
		if len(tags) > 20 {
			return ImportResult{}, fmt.Errorf("第 %d 条环境最多包含 20 个标签", index+1)
		}
		environments = append(environments, domain.Environment{
			OwnerID: ownerID,
			Name:    input.Name, IP: input.IP, SSHUser: input.SSHUser, SSHPort: input.SSHPort,
			SSHPasswordEnc: item.SSHPasswordEncrypted, InstallDir: input.InstallDir,
			ServiceType: input.ServiceType, Installed: item.Installed, InstalledAt: item.InstalledAt,
			InstalledPackageSHA256: item.InstalledPackageSHA256, HealthPort: item.HealthPort,
			HostKeyFingerprint: item.SSHHostKeyFingerprint, Tags: tags,
		})
	}
	created, overwritten, err := s.store.UpsertImportedEnvironments(ctx, environments)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return ImportResult{}, err
		}
		return ImportResult{}, fmt.Errorf("导入环境信息: %w", err)
	}
	return ImportResult{Created: created, Overwritten: overwritten, Total: len(environments)}, nil
}

func (s *EnvironmentService) requireServicePackage(ctx context.Context, ownerID, serviceType string) error {
	if _, err := s.store.GetPackageByOwner(ctx, ownerID, serviceType); errors.Is(err, domain.ErrNotFound) {
		return domain.FieldError("service_type", "该服务类型尚未上传安装包")
	} else if err != nil {
		return err
	}
	return nil
}

func viewEnvironment(env domain.Environment) domain.EnvironmentView {
	env.SSHPasswordEnc = ""
	env.HostKeyFingerprint = ""
	return domain.EnvironmentView{Environment: env, HasPassword: true}
}
