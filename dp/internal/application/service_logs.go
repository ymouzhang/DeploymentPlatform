package application

import (
	"context"

	"DP/internal/domain"
	"DP/internal/remote"
	"DP/internal/security"
	"DP/internal/store"
)

type ServiceLogService struct {
	store  *store.Store
	cipher *security.PasswordCipher
	remote *remote.Executor
}

func NewServiceLogService(
	store *store.Store,
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
