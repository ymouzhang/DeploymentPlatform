package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"DP/internal/archive"
	"DP/internal/domain"
	"DP/internal/security"
)

type configWriter interface {
	WriteConfig(context.Context, domain.Environment, []byte, string, []byte) (string, error)
}

type ServiceConfigService struct {
	store    ServiceConfigRepository
	packages *archive.Manager
	cipher   *security.PasswordCipher
	remote   configWriter
}

type ServiceConfigRepository interface {
	GetEnvironment(context.Context, string) (domain.Environment, error)
	GetServiceConfig(context.Context, string) (domain.ServiceConfig, error)
	GetServiceConfigRevision(context.Context, string, string) (domain.ServiceConfigRevision, error)
	ListServiceConfigRevisions(context.Context, string) ([]domain.ServiceConfigRevision, error)
	RecordValidation(context.Context, string, string, string) error
	SaveServiceConfigRevision(context.Context, domain.ServiceConfig, domain.ServiceConfigRevision, bool) (domain.ServiceConfigRevision, error)
}

func NewServiceConfigService(
	store ServiceConfigRepository,
	packages *archive.Manager,
	cipher *security.PasswordCipher,
	remoteWriter configWriter,
) *ServiceConfigService {
	return &ServiceConfigService{
		store: store, packages: packages, cipher: cipher, remote: remoteWriter,
	}
}

func (s *ServiceConfigService) Get(ctx context.Context, environmentID string) (domain.ServiceConfig, error) {
	env, err := s.store.GetEnvironment(ctx, environmentID)
	if err != nil {
		return domain.ServiceConfig{}, err
	}
	packageContent, pkg, packageInspection, err := s.packages.ReadConfigForOwner(ctx, env.OwnerID, env.ServiceType)
	if err != nil {
		return domain.ServiceConfig{}, err
	}
	config, configErr := s.store.GetServiceConfig(ctx, environmentID)
	if configErr != nil && !errors.Is(configErr, domain.ErrNotFound) {
		return domain.ServiceConfig{}, configErr
	}
	if errors.Is(configErr, domain.ErrNotFound) {
		currentContent, currentInspection := packageContent, packageInspection
		if env.Installed && env.InstalledPackageSHA256 != "" && env.InstalledPackageSHA256 != pkg.SHA256 {
			currentContent, _, currentInspection, err = s.packages.ReadConfigVersionForOwner(
				ctx, env.OwnerID, env.ServiceType, env.InstalledPackageSHA256)
			if err != nil {
				return domain.ServiceConfig{}, err
			}
		}
		config = domain.ServiceConfig{EnvironmentID: environmentID, Content: string(currentContent),
			Format: currentInspection.ConfigType, Path: archive.RelativeConfigPath(currentInspection),
			Port: currentInspection.Port, Inherited: true}
	}
	config.PackageContent = string(packageContent)
	config.PackageVersionID = pkg.CurrentVersionID
	config.PackageFilename = pkg.OriginalFilename
	config.PackageChanged = config.Content != config.PackageContent
	config.PackageUpdated = env.Installed && env.InstalledPackageSHA256 != "" && env.InstalledPackageSHA256 != pkg.SHA256
	return config, nil
}

func (s *ServiceConfigService) Update(
	ctx context.Context,
	environmentID string,
	content []byte,
	actor domain.User,
) (domain.ServiceConfig, error) {
	current, err := s.Get(ctx, environmentID)
	if err != nil {
		return domain.ServiceConfig{}, err
	}
	if !current.Inherited && current.Content == string(content) {
		return current, nil
	}
	revision, err := s.save(ctx, environmentID, current, content, actor, "manual", "")
	if err != nil {
		return domain.ServiceConfig{}, err
	}
	config, err := s.store.GetServiceConfig(ctx, environmentID)
	if err == nil {
		config.CurrentRevisionID = revision.ID
	}
	return config, err
}

func (s *ServiceConfigService) Preview(ctx context.Context, environmentID string, content []byte) (domain.ServiceConfigPreview, error) {
	current, err := s.Get(ctx, environmentID)
	if err != nil {
		return domain.ServiceConfigPreview{}, err
	}
	port, err := archive.ValidateConfig(content, current.Format)
	if err != nil {
		return domain.ServiceConfigPreview{}, err
	}
	return domain.ServiceConfigPreview{CurrentContent: current.Content, ProposedContent: string(content),
		Changed: current.Content != string(content), Format: current.Format, Path: current.Path, Port: port}, nil
}

func (s *ServiceConfigService) ListRevisions(ctx context.Context, environmentID string) ([]domain.ServiceConfigRevision, error) {
	if _, err := s.store.GetEnvironment(ctx, environmentID); err != nil {
		return nil, err
	}
	return s.store.ListServiceConfigRevisions(ctx, environmentID)
}

func (s *ServiceConfigService) GetRevision(ctx context.Context, environmentID, revisionID string) (domain.ServiceConfigRevision, error) {
	if _, err := s.store.GetEnvironment(ctx, environmentID); err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	return s.store.GetServiceConfigRevision(ctx, environmentID, revisionID)
}

func (s *ServiceConfigService) Rollback(ctx context.Context, environmentID, revisionID string, actor domain.User) (domain.ServiceConfigRevision, error) {
	current, err := s.Get(ctx, environmentID)
	if err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	target, err := s.GetRevision(ctx, environmentID, revisionID)
	if err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	if target.Current {
		return domain.ServiceConfigRevision{}, &domain.AppError{Code: "CONFIG_REVISION_CURRENT", Message: "该修订已经是当前配置，无需回滚"}
	}
	return s.save(ctx, environmentID, current, []byte(target.Content), actor, "rollback", target.ID)
}

func (s *ServiceConfigService) save(ctx context.Context, environmentID string, current domain.ServiceConfig, content []byte, actor domain.User, source, restoredFrom string) (domain.ServiceConfigRevision, error) {
	env, err := s.store.GetEnvironment(ctx, environmentID)
	if err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	_, _, inspection, err := s.packages.ReadConfigForOwner(ctx, env.OwnerID, env.ServiceType)
	if err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	port, err := archive.ValidateConfig(content, inspection.ConfigType)
	if err != nil {
		return domain.ServiceConfigRevision{}, err
	}
	config := domain.ServiceConfig{
		EnvironmentID: environmentID,
		Content:       string(content),
		Format:        inspection.ConfigType,
		Path:          archive.RelativeConfigPath(inspection),
		Port:          port,
	}
	var password []byte
	if env.Installed {
		var decryptErr error
		password, decryptErr = s.cipher.Decrypt(env.SSHPasswordEnc)
		if decryptErr != nil {
			return domain.ServiceConfigRevision{}, decryptErr
		}
		defer clear(password)
		fingerprint, writeErr := s.remote.WriteConfig(ctx, env, password, config.Path, content)
		if writeErr != nil {
			return domain.ServiceConfigRevision{}, writeErr
		}
		if fingerprint != "" {
			_ = s.store.RecordValidation(ctx, env.ID, fingerprint, env.Arch)
		}
	}
	revision := domain.ServiceConfigRevision{ID: domain.NewID(), EnvironmentID: environmentID,
		Content: config.Content, Format: config.Format, Path: config.Path, Port: config.Port,
		Source: source, RestoredFromID: restoredFrom, CreatedBy: actor.ID,
		CreatedByName: actor.Username, CreatedAt: time.Now().UTC()}
	saved, err := s.store.SaveServiceConfigRevision(ctx, config, revision, env.Installed)
	if err != nil {
		if env.Installed {
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			_, restoreErr := s.remote.WriteConfig(restoreCtx, env, password, current.Path, []byte(current.Content))
			cancel()
			if restoreErr != nil {
				return domain.ServiceConfigRevision{}, errors.Join(err, fmt.Errorf("restore remote config: %w", restoreErr))
			}
		}
		return domain.ServiceConfigRevision{}, err
	}
	return saved, nil
}
