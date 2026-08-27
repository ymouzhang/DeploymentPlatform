package operation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"DP/internal/archive"
	"DP/internal/audit"
	"DP/internal/domain"
	"DP/internal/remote"
	"DP/internal/security"
	"DP/internal/store"
)

type Manager struct {
	ctx      context.Context
	dataDir  string
	store    *store.Store
	cipher   *security.PasswordCipher
	packages *archive.Manager
	remote   *remote.Executor
	audit    *audit.Service
	log      *slog.Logger

	mu     sync.RWMutex
	active map[string]string
	wg     sync.WaitGroup
}

func NewManager(
	ctx context.Context,
	dataDir string,
	store *store.Store,
	cipher *security.PasswordCipher,
	packages *archive.Manager,
	remoteExecutor *remote.Executor,
	auditService *audit.Service,
	log *slog.Logger,
) *Manager {
	return &Manager{
		ctx: ctx, dataDir: dataDir, store: store, cipher: cipher,
		packages: packages, remote: remoteExecutor, audit: auditService, log: log,
		active: make(map[string]string),
	}
}

func (m *Manager) Busy(environmentID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.active[environmentID]
	return ok
}

func (m *Manager) Start(
	ctx context.Context,
	environmentID string,
	action domain.OperationAction,
) (domain.Operation, error) {
	return m.start(ctx, environmentID, action, nil)
}

func (m *Manager) StartWithAudit(
	ctx context.Context,
	environmentID string,
	action domain.OperationAction,
	event domain.AuditEvent,
) (domain.Operation, error) {
	return m.start(ctx, environmentID, action, &event)
}

func (m *Manager) start(
	ctx context.Context,
	environmentID string,
	action domain.OperationAction,
	auditEvent *domain.AuditEvent,
) (domain.Operation, error) {
	env, err := m.store.GetEnvironment(ctx, environmentID)
	if err != nil {
		return domain.Operation{}, err
	}
	switch action {
	case domain.ActionInstall:
		if env.Installed {
			return domain.Operation{}, domain.ErrAlreadyInstalled
		}
		if _, err := m.packages.GetForOwner(ctx, env.OwnerID, env.ServiceType); err != nil {
			return domain.Operation{}, &domain.AppError{
				Code: "PACKAGE_NOT_FOUND", Message: "请先上传该服务类型的安装包", Err: err,
			}
		}
	case domain.ActionStart, domain.ActionStop:
		if !env.Installed {
			return domain.Operation{}, domain.ErrNotInstalled
		}
	case domain.ActionReset:
		// 重置不校验安装状态：安装失败（installed=false）时远端服务可能仍在
		// 运行，需要允许重置以强制停止远端服务并清理安装标记。
	default:
		return domain.Operation{}, domain.FieldError("action", "不支持该操作")
	}

	m.mu.Lock()
	if _, exists := m.active[environmentID]; exists {
		m.mu.Unlock()
		return domain.Operation{}, domain.ErrOperationInProgress
	}
	id := store.NewID()
	m.active[environmentID] = id
	m.mu.Unlock()

	logRelative := filepath.Join("operations", id+".jsonl")
	op := domain.Operation{
		ID: id, EnvironmentID: environmentID, Action: action,
		Status: domain.OperationQueued, Stage: "queued",
		LogPath: logRelative, CreatedAt: time.Now().UTC(),
	}
	op.OwnerID, op.EnvironmentName, op.EnvironmentIP, op.ServiceType = env.OwnerID, env.Name, env.IP, env.ServiceType
	if auditEvent != nil {
		op.RequestID = auditEvent.RequestID
		op.ActorUserID, op.ActorUsername = auditEvent.ActorUserID, auditEvent.ActorUsername
		op.OwnerUsername = auditEvent.OwnerUsername
	}
	if err := os.MkdirAll(filepath.Join(m.dataDir, "operations"), 0o750); err != nil {
		m.release(environmentID)
		return domain.Operation{}, err
	}
	if err := m.store.CreateOperation(ctx, op); err != nil {
		m.release(environmentID)
		return domain.Operation{}, err
	}
	if auditEvent != nil && m.audit != nil {
		auditEvent.OperationID = op.ID
		m.audit.Record(ctx, *auditEvent)
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.run(op)
	}()
	return op, nil
}

// Wait blocks until all accepted operations have persisted their terminal state.
func (m *Manager) Wait() {
	m.wg.Wait()
}

func (m *Manager) run(op domain.Operation) {
	defer m.release(op.EnvironmentID)
	logger, err := newEventLogger(filepath.Join(m.dataDir, op.LogPath))
	if err != nil {
		m.finish(&op, domain.OperationFailed, "prepare", "LOG_CREATE_FAILED", err.Error(), nil)
		return
	}
	defer logger.Close()
	emit := func(stream, message string) {
		_ = logger.Write(domain.OperationEvent{
			Type: "log", Stream: stream, Message: message, Stage: op.Stage,
		})
	}
	now := time.Now().UTC()
	op.StartedAt = &now
	op.Status = domain.OperationRunning
	op.Stage = "prepare"
	_ = m.store.UpdateOperation(m.ctx, op)
	_ = logger.Write(domain.OperationEvent{
		Type: "state", Status: string(op.Status), Stage: op.Stage, Message: "操作开始",
	})

	env, err := m.store.GetEnvironment(m.ctx, op.EnvironmentID)
	if err != nil {
		m.finishLogged(&op, logger, domain.OperationFailed, "prepare", "ENVIRONMENT_NOT_FOUND", err.Error(), nil)
		return
	}
	password, err := m.cipher.Decrypt(env.SSHPasswordEnc)
	if err != nil {
		m.finishLogged(&op, logger, domain.OperationFailed, "prepare", "PASSWORD_DECRYPT_FAILED", err.Error(), nil)
		return
	}
	defer clear(password)

	var fingerprint string
	var exitCode int
	switch op.Action {
	case domain.ActionInstall:
		m.stage(&op, logger, "package", "正在固定安装包版本")
		pkg, packagePath, inspection, cleanup, snapshotErr :=
			m.packages.SnapshotForOwner(m.ctx, env.OwnerID, env.ServiceType)
		if snapshotErr != nil {
			m.finishLogged(&op, logger, domain.OperationFailed, "package", "PACKAGE_INVALID", snapshotErr.Error(), nil)
			return
		}
		defer cleanup()
		config, configErr := m.store.GetServiceConfig(m.ctx, env.ID)
		if errors.Is(configErr, domain.ErrNotFound) {
			config = domain.ServiceConfig{
				EnvironmentID: env.ID,
				Content:       string(inspection.Config),
				Format:        inspection.ConfigType,
				Path:          archive.RelativeConfigPath(inspection),
				Port:          inspection.Port,
				Inherited:     true,
			}
		} else if configErr != nil {
			m.finishLogged(&op, logger, domain.OperationFailed, "package", "CONFIG_LOAD_FAILED", configErr.Error(), nil)
			return
		}
		m.stage(&op, logger, "remote", "正在连接目标服务器")
		fingerprint, exitCode, err = m.remote.Install(
			m.ctx, env, password, packagePath, pkg.SHA256, config.Port,
			config.Path, []byte(config.Content),
			inspection.HasInstall, inspection.RootPrefix != "", emit,
		)
		if err == nil {
			if config.Inherited {
				config.Inherited = false
				_, err = m.store.UpsertServiceConfig(m.ctx, config)
			}
			if err == nil {
				err = m.store.MarkInstalled(m.ctx, env.ID, pkg.SHA256, config.Port)
			}
			if err == nil {
				// 架构采集为尽力而为：失败仅记日志，不影响安装结果。
				if arch, detectErr := m.remote.DetectArch(m.ctx, env, password); detectErr != nil {
					m.log.Warn("detect environment arch", "environment_id", env.ID, "error", detectErr)
				} else if updateErr := m.store.UpdateEnvironmentArch(m.ctx, env.ID, arch); updateErr != nil {
					m.log.Warn("update environment arch", "environment_id", env.ID, "error", updateErr)
				} else {
					env.Arch = arch
				}
			}
		} else {
			var existing *domain.ExistingInstallationError
			if errors.As(err, &existing) {
				sha, port := existing.PackageSHA256, existing.HealthPort
				if sha == "" {
					sha = pkg.SHA256
				}
				if port == 0 {
					port = config.Port
				}
				_ = m.store.MarkInstalled(m.ctx, env.ID, sha, port)
				emit("system", "检测到远端已安装标记，已同步本地安装状态")
			}
		}
	case domain.ActionStart:
		m.stage(&op, logger, "script", "正在启动服务")
		fingerprint, exitCode, err = m.remote.RunScript(m.ctx, env, password, "start.sh", emit)
	case domain.ActionStop:
		m.stage(&op, logger, "script", "正在停止服务")
		fingerprint, exitCode, err = m.remote.RunScript(m.ctx, env, password, "stop.sh", emit)
	case domain.ActionReset:
		lastAction, actionErr := m.store.LastSuccessfulAction(m.ctx, env.ID)
		if actionErr != nil && !errors.Is(actionErr, domain.ErrNotFound) {
			m.finishLogged(&op, logger, domain.OperationFailed, "prepare", "OPERATION_HISTORY_FAILED", actionErr.Error(), nil)
			return
		}
		runStop := actionErr != nil || lastAction != domain.ActionStop
		if runStop {
			m.stage(&op, logger, "script", "重置前正在停止服务")
		} else {
			m.stage(&op, logger, "reset", "服务最近已成功停止，跳过 stop.sh")
		}
		fingerprint, exitCode, err = m.remote.ResetInstallation(
			m.ctx, env, password, runStop, emit,
		)
		if err == nil {
			err = m.store.MarkUninstalled(m.ctx, env.ID)
		}
	}
	if fingerprint != "" && (env.HostKeyFingerprint == "" || env.HostKeyFingerprint == fingerprint) {
		_ = m.store.RecordValidation(m.ctx, env.ID, fingerprint, env.Arch)
	}
	if err != nil {
		exit := exitCode
		status, code := classifyError(err)
		m.finishLogged(&op, logger, status, op.Stage, code, err.Error(), &exit)
		return
	}
	exit := exitCode
	m.finishLogged(&op, logger, domain.OperationSucceeded, "completed", "", "", &exit)
}

func (m *Manager) stage(op *domain.Operation, logger *eventLogger, stage, message string) {
	op.Stage = stage
	_ = m.store.UpdateOperation(m.ctx, *op)
	_ = logger.Write(domain.OperationEvent{
		Type: "state", Status: string(op.Status), Stage: stage, Message: message,
	})
}

func (m *Manager) finishLogged(
	op *domain.Operation,
	logger *eventLogger,
	status domain.OperationStatus,
	stage, code, message string,
	exitCode *int,
) {
	m.finish(op, status, stage, code, message, exitCode)
	_ = logger.Write(domain.OperationEvent{
		Type: "state", Status: string(status), Stage: stage, Message: message,
	})
}

func (m *Manager) finish(
	op *domain.Operation,
	status domain.OperationStatus,
	stage, code, message string,
	exitCode *int,
) {
	now := time.Now().UTC()
	op.Status = status
	op.Stage = stage
	op.ErrorCode = code
	op.ErrorMessage = message
	op.ExitCode = exitCode
	op.FinishedAt = &now
	if err := m.store.UpdateOperation(context.Background(), *op); err != nil {
		m.log.Error("update operation result", "operation_id", op.ID, "error", err)
	}
	if m.audit != nil {
		m.audit.CompleteOperation(context.Background(), *op)
	}
}

func (m *Manager) Get(ctx context.Context, id string) (domain.Operation, error) {
	return m.store.GetOperation(ctx, id)
}

func (m *Manager) LogPath(op domain.Operation) string {
	return filepath.Join(m.dataDir, filepath.Clean(op.LogPath))
}

func (m *Manager) ReadEvents(op domain.Operation, after int64) ([]domain.OperationEvent, error) {
	file, err := os.Open(m.LogPath(op))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var events []domain.OperationEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		var event domain.OperationEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Seq > after {
			events = append(events, event)
		}
	}
	return events, scanner.Err()
}

func (m *Manager) release(environmentID string) {
	m.mu.Lock()
	delete(m.active, environmentID)
	m.mu.Unlock()
}

func classifyError(err error) (domain.OperationStatus, string) {
	switch {
	case errors.Is(err, domain.ErrTimedOut), errors.Is(err, context.DeadlineExceeded):
		return domain.OperationTimedOut, "OPERATION_TIMEOUT"
	case errors.Is(err, context.Canceled):
		return domain.OperationInterrupted, "OPERATION_INTERRUPTED"
	case errors.Is(err, domain.ErrAlreadyInstalled):
		return domain.OperationFailed, "ALREADY_INSTALLED"
	default:
		var appErr *domain.AppError
		if errors.As(err, &appErr) {
			return domain.OperationFailed, appErr.Code
		}
		return domain.OperationFailed, "REMOTE_OPERATION_FAILED"
	}
}

type eventLogger struct {
	mu   sync.Mutex
	file *os.File
	seq  int64
}

func newEventLogger(path string) (*eventLogger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	return &eventLogger{file: file}, nil
}

func (l *eventLogger) Write(event domain.OperationEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	event.Seq = l.seq
	event.Time = time.Now().UTC()
	content, err := json.Marshal(event)
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if _, err := l.file.Write(content); err != nil {
		return fmt.Errorf("write operation event: %w", err)
	}
	return nil
}

func (l *eventLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
