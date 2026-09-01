package application

import (
	"context"

	"DP/internal/domain"
	"DP/internal/remote"
	"DP/internal/security"
)

type ServiceLogRepository interface {
	GetEnvironment(context.Context, string) (domain.Environment, error)
	RecordValidation(context.Context, string, string, string) error
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
	environmentID string,
	tail int,
	emit remote.EmitFunc,
) error {
	env, err := s.store.GetEnvironment(ctx, environmentID)
	if err != nil {
		return err
	}
	if !env.Installed {
		return domain.ErrNotInstalled
	}
	password, err := s.cipher.Decrypt(env.SSHPasswordEnc)
	if err != nil {
		return err
	}
	defer clear(password)
	fingerprint, err := s.remote.FollowComposeLogs(ctx, env, password, tail, emit)
	if fingerprint != "" && (env.HostKeyFingerprint == "" || env.HostKeyFingerprint == fingerprint) {
		_ = s.store.RecordValidation(context.Background(), env.ID, fingerprint, env.Arch)
	}
	return err
}
