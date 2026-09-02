package application

import (
	"context"

	"DP/internal/domain"
	"DP/internal/remote"
	"DP/internal/security"
)

type ServiceLogRepository interface {
	GetServiceInstance(context.Context, string) (domain.ServiceInstance, error)
	RecordHostValidation(context.Context, string, string, string) error
}

type ServiceLogService struct {
	store  ServiceLogRepository
	cipher *security.PasswordCipher
	remote *remote.Executor
}

func NewServiceLogService(
	store ServiceLogRepository,
	cipher *security.PasswordCipher,
	remoteExecutor *remote.Executor,
) *ServiceLogService {
	return &ServiceLogService{store: store, cipher: cipher, remote: remoteExecutor}
}

func (s *ServiceLogService) Stream(
	ctx context.Context,
	serviceInstanceID string,
	tail int,
	emit remote.EmitFunc,
) error {
	instance, err := s.store.GetServiceInstance(ctx, serviceInstanceID)
	if err != nil {
		return err
	}
	if !instance.Installed {
		return domain.ErrNotInstalled
	}
	password, err := s.cipher.Decrypt(instance.Host.SSHPasswordEnc)
	if err != nil {
		return err
	}
	defer clear(password)
	fingerprint, err := s.remote.FollowComposeLogs(ctx, instance, password, tail, emit)
	if fingerprint != "" && (instance.Host.HostKeyFingerprint == "" || instance.Host.HostKeyFingerprint == fingerprint) {
		_ = s.store.RecordHostValidation(context.Background(), instance.HostID, fingerprint, instance.Host.Arch)
	}
	return err
}
