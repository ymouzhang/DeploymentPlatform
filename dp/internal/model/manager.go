package model

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"DP/internal/audit"
	"DP/internal/domain"
	"DP/internal/remote"
	"DP/internal/security"
)

type Repository interface {
	CancelModelUpload(context.Context, string) error
	CompleteModelUpload(context.Context, string) error
	CreateModelTask(context.Context, domain.ModelTask) (domain.ModelTask, error)
	CreateModelUpload(context.Context, domain.Model, domain.ModelUpload) (domain.Model, domain.ModelUpload, error)
	GetHost(context.Context, string) (domain.Host, error)
	GetModel(context.Context, string) (domain.Model, error)
	GetModelTask(context.Context, string) (domain.ModelTask, error)
	GetModelUpload(context.Context, string) (domain.ModelUpload, error)
	GetModelUploadByModel(context.Context, string) (domain.ModelUpload, error)
	ListExpiredModelUploads(context.Context, time.Time, int) ([]domain.ModelUpload, error)
	ListModels(context.Context, string) ([]domain.Model, error)
	MarkModelDeleted(context.Context, string) error
	MarkModelReady(context.Context, string, string, int64, int64) error
	SetModelState(context.Context, string, domain.ModelStatus, string) error
	SetModelUploadOffset(context.Context, string, int64) error
	UpdateModelTask(context.Context, domain.ModelTask) error
}

type Manager struct {
	ctx             context.Context
	dataDir         string
	store           Repository
	cipher          *security.PasswordCipher
	remote          *remote.Executor
	audit           *audit.Service
	maxBytes        int64
	chunkBytes      int64
	retention       time.Duration
	transferTimeout time.Duration
	sem             chan struct{}
	log             *slog.Logger
	mu              sync.Mutex
	active          map[string]struct{}
	wg              sync.WaitGroup
}

type UploadCreated struct {
	Model      domain.Model `json:"model"`
	UploadID   string       `json:"upload_id"`
	Offset     int64        `json:"offset"`
	ChunkBytes int64        `json:"chunk_bytes"`
	ExpiresAt  time.Time    `json:"expires_at"`
}

func NewManager(ctx context.Context, dataDir string, db Repository, cipher *security.PasswordCipher, executor *remote.Executor,
	auditService *audit.Service, maxBytes, chunkBytes int64, retention, transferTimeout time.Duration, concurrency int, log *slog.Logger) *Manager {
	return &Manager{ctx: ctx, dataDir: dataDir, store: db, cipher: cipher, remote: executor,
		audit:    auditService,
		maxBytes: maxBytes, chunkBytes: chunkBytes, retention: retention, transferTimeout: transferTimeout,
		sem: make(chan struct{}, concurrency), active: make(map[string]struct{}), log: log}
}

func (m *Manager) CreateUpload(ctx context.Context, owner domain.User, actor domain.User, input domain.ModelUploadCreateInput) (UploadCreated, error) {
	input.Normalize()
	if err := input.Validate(m.maxBytes); err != nil {
		return UploadCreated{}, err
	}
	env, err := m.store.GetHost(ctx, input.HostID)
	if err != nil {
		return UploadCreated{}, err
	}
	if env.OwnerID != owner.ID {
		return UploadCreated{}, domain.ErrNotFound
	}
	if env.SSHPasswordEnc == "" {
		return UploadCreated{}, &domain.AppError{Code: "HOST_CREDENTIALS_MISSING", Message: "目标主机没有可用的 SSH 凭据"}
	}
	if env.HostKeyFingerprint == "" {
		return UploadCreated{}, &domain.AppError{Code: "HOST_NOT_VALIDATED", Message: "请先在主机管理中完成 SSH 校验，再上传模型"}
	}
	modelID, uploadID := domain.NewID(), domain.NewID()
	remotePath := remote.ModelUploadRemotePath(input.TargetDir, uploadID)
	password, err := m.cipher.Decrypt(env.SSHPasswordEnc)
	if err != nil {
		return UploadCreated{}, err
	}
	defer clearBytes(password)
	if err := m.remote.PrepareModelUpload(ctx, env, password, input.TargetDir, remotePath, input.TotalBytes); err != nil {
		return UploadCreated{}, err
	}
	model := domain.Model{ID: modelID, OwnerID: owner.ID, HostID: env.ID,
		HostName: env.Name, HostIP: env.IP, Name: input.Name, Source: "offline",
		TargetDir: input.TargetDir, OriginalFilename: input.OriginalFilename, SizeBytes: input.TotalBytes,
		Status: domain.ModelUploading, CreatedBy: actor.ID, CreatedByUsername: actor.Username}
	upload := domain.ModelUpload{ID: uploadID, RemotePath: remotePath, TotalBytes: input.TotalBytes,
		Status: "uploading", ExpiresAt: time.Now().UTC().Add(m.retention)}
	model, upload, err = m.store.CreateModelUpload(ctx, model, upload)
	if err != nil {
		_ = m.remote.RemoveModelUpload(context.Background(), env, password, remotePath)
		return UploadCreated{}, err
	}
	return UploadCreated{Model: model, UploadID: upload.ID, Offset: 0, ChunkBytes: m.chunkBytes, ExpiresAt: upload.ExpiresAt}, nil
}

func (m *Manager) List(ctx context.Context, ownerID string) ([]domain.Model, error) {
	return m.store.ListModels(ctx, ownerID)
}
func (m *Manager) Get(ctx context.Context, id string) (domain.Model, error) {
	return m.store.GetModel(ctx, id)
}
func (m *Manager) GetTask(ctx context.Context, id string) (domain.ModelTask, error) {
	return m.store.GetModelTask(ctx, id)
}

func (m *Manager) UploadOffset(ctx context.Context, id string) (domain.ModelUpload, error) {
	m.lock(id)
	defer m.unlock(id)
	upload, model, env, password, err := m.uploadContext(ctx, id)
	if err != nil {
		return upload, err
	}
	defer clearBytes(password)
	remoteOffset, err := m.remote.ModelUploadOffset(ctx, env, password, upload.RemotePath)
	if err != nil {
		return upload, err
	}
	if remoteOffset < 0 || remoteOffset > upload.TotalBytes {
		return upload, &domain.AppError{Code: "MODEL_UPLOAD_CORRUPT", Message: "远端暂存文件大小异常"}
	}
	if remoteOffset != upload.Offset {
		if err := m.store.SetModelUploadOffset(ctx, upload.ID, remoteOffset); err != nil {
			return upload, err
		}
		upload.Offset = remoteOffset
	}
	_ = model
	return upload, nil
}

func (m *Manager) AppendChunk(ctx context.Context, id string, expectedOffset, contentLength int64, reader io.Reader) (int64, error) {
	if contentLength <= 0 || contentLength > m.chunkBytes {
		return 0, domain.FieldError("chunk", fmt.Sprintf("分片大小必须为 1 到 %d 字节", m.chunkBytes))
	}
	m.lock(id)
	defer m.unlock(id)
	upload, _, env, password, err := m.uploadContext(ctx, id)
	if err != nil {
		return 0, err
	}
	defer clearBytes(password)
	if time.Now().After(upload.ExpiresAt) {
		return upload.Offset, &domain.AppError{Code: "MODEL_UPLOAD_EXPIRED", Message: "上传会话已过期"}
	}
	remoteOffset, err := m.remote.ModelUploadOffset(ctx, env, password, upload.RemotePath)
	if err != nil {
		return upload.Offset, err
	}
	if remoteOffset != upload.Offset {
		if err := m.store.SetModelUploadOffset(ctx, upload.ID, remoteOffset); err != nil {
			return remoteOffset, err
		}
		upload.Offset = remoteOffset
	}
	if expectedOffset != upload.Offset {
		return upload.Offset, &domain.AppError{Code: "UPLOAD_OFFSET_MISMATCH", Message: "上传偏移不匹配，请从服务端返回位置继续"}
	}
	if contentLength > upload.TotalBytes-upload.Offset {
		return upload.Offset, domain.FieldError("chunk", "分片超过模型文件声明大小")
	}
	next, err := m.remote.AppendModelChunk(ctx, env, password, upload.RemotePath, upload.Offset, contentLength, reader)
	if err == nil && next >= 0 && next <= upload.TotalBytes && next != upload.Offset {
		_ = m.store.SetModelUploadOffset(context.Background(), upload.ID, next)
	}
	return next, err
}

func (m *Manager) CompleteUpload(ctx context.Context, id string, actor domain.User) (domain.ModelTask, error) {
	m.lock(id)
	defer m.unlock(id)
	upload, model, env, password, err := m.uploadContext(ctx, id)
	if err != nil {
		return domain.ModelTask{}, err
	}
	defer clearBytes(password)
	remoteOffset, err := m.remote.ModelUploadOffset(ctx, env, password, upload.RemotePath)
	if err != nil {
		return domain.ModelTask{}, err
	}
	if remoteOffset != upload.TotalBytes {
		return domain.ModelTask{}, &domain.AppError{Code: "MODEL_UPLOAD_INCOMPLETE", Message: "模型文件尚未完整上传"}
	}
	if upload.Offset != remoteOffset {
		if err := m.store.SetModelUploadOffset(ctx, upload.ID, remoteOffset); err != nil {
			return domain.ModelTask{}, err
		}
	}
	if err := m.store.CompleteModelUpload(ctx, upload.ID); err != nil {
		return domain.ModelTask{}, err
	}
	if err := m.store.SetModelState(ctx, model.ID, domain.ModelDeploying, ""); err != nil {
		return domain.ModelTask{}, err
	}
	task, err := m.startTask(ctx, model, domain.ModelTaskDeploy, actor)
	if err != nil {
		_ = m.store.SetModelState(context.Background(), model.ID, domain.ModelFailed, err.Error())
	}
	return task, err
}

func (m *Manager) Retry(ctx context.Context, modelID string, actor domain.User) (domain.ModelTask, error) {
	model, err := m.store.GetModel(ctx, modelID)
	if err != nil {
		return domain.ModelTask{}, err
	}
	if model.Status != domain.ModelFailed {
		return domain.ModelTask{}, &domain.AppError{Code: "MODEL_NOT_RETRYABLE", Message: "只有部署失败或中断的模型可以重试"}
	}
	if model.LatestTask == nil || model.LatestTask.Action != domain.ModelTaskDeploy {
		return domain.ModelTask{}, &domain.AppError{Code: "MODEL_NOT_RETRYABLE", Message: "当前失败不是模型部署失败"}
	}
	upload, err := m.uploadByModel(ctx, model.ID)
	if err != nil || upload.Status != "completed" {
		return domain.ModelTask{}, &domain.AppError{Code: "MODEL_UPLOAD_NOT_AVAILABLE", Message: "远端模型压缩包不可用于重试", Err: err}
	}
	if err := m.store.SetModelState(ctx, model.ID, domain.ModelDeploying, ""); err != nil {
		return domain.ModelTask{}, err
	}
	task, err := m.startTask(ctx, model, domain.ModelTaskDeploy, actor)
	if err != nil {
		_ = m.store.SetModelState(context.Background(), model.ID, domain.ModelFailed, err.Error())
	}
	return task, err
}

func (m *Manager) Delete(ctx context.Context, modelID string, actor domain.User) (domain.ModelTask, error) {
	model, err := m.store.GetModel(ctx, modelID)
	if err != nil {
		return domain.ModelTask{}, err
	}
	if model.Status == domain.ModelUploading {
		upload, err := m.uploadByModel(ctx, model.ID)
		if err != nil {
			return domain.ModelTask{}, err
		}
		if err := m.cancelUpload(ctx, upload, model); err != nil {
			return domain.ModelTask{}, err
		}
		return domain.ModelTask{}, nil
	}
	if model.Status == domain.ModelFailed {
		if model.LatestTask != nil && model.LatestTask.Action == domain.ModelTaskDelete {
			if err := m.store.SetModelState(ctx, model.ID, domain.ModelDeleting, ""); err != nil {
				return domain.ModelTask{}, err
			}
			task, err := m.startTask(ctx, model, domain.ModelTaskDelete, actor)
			if err != nil {
				_ = m.store.SetModelState(context.Background(), model.ID, domain.ModelFailed, err.Error())
			}
			return task, err
		}
		upload, err := m.uploadByModel(ctx, model.ID)
		if err != nil {
			return domain.ModelTask{}, err
		}
		env, err := m.store.GetHost(ctx, model.HostID)
		if err != nil {
			return domain.ModelTask{}, err
		}
		password, err := m.cipher.Decrypt(env.SSHPasswordEnc)
		if err != nil {
			return domain.ModelTask{}, err
		}
		owned, ownedErr := m.remote.ModelTargetOwned(ctx, env, password, model)
		if ownedErr != nil {
			clearBytes(password)
			return domain.ModelTask{}, ownedErr
		}
		if owned {
			clearBytes(password)
			if err := m.store.SetModelState(ctx, model.ID, domain.ModelDeleting, ""); err != nil {
				return domain.ModelTask{}, err
			}
			task, err := m.startTask(ctx, model, domain.ModelTaskDelete, actor)
			if err != nil {
				_ = m.store.SetModelState(context.Background(), model.ID, domain.ModelFailed, err.Error())
			}
			return task, err
		}
		removeErr := m.remote.RemoveModelUpload(ctx, env, password, upload.RemotePath)
		clearBytes(password)
		if removeErr != nil {
			return domain.ModelTask{}, removeErr
		}
		return domain.ModelTask{}, m.store.MarkModelDeleted(ctx, model.ID)
	}
	if model.Status != domain.ModelReady {
		return domain.ModelTask{}, &domain.AppError{Code: "MODEL_OPERATION_IN_PROGRESS", Message: "模型正在执行其他任务"}
	}
	if err := m.store.SetModelState(ctx, model.ID, domain.ModelDeleting, ""); err != nil {
		return domain.ModelTask{}, err
	}
	task, err := m.startTask(ctx, model, domain.ModelTaskDelete, actor)
	if err != nil {
		_ = m.store.SetModelState(context.Background(), model.ID, domain.ModelReady, "")
	}
	return task, err
}

func (m *Manager) CancelUpload(ctx context.Context, id string) error {
	m.lock(id)
	defer m.unlock(id)
	upload, err := m.store.GetModelUpload(ctx, id)
	if err != nil {
		return err
	}
	if upload.Status != "uploading" {
		return &domain.AppError{Code: "MODEL_UPLOAD_CLOSED", Message: "上传会话已结束"}
	}
	model, err := m.store.GetModel(ctx, upload.ModelID)
	if err != nil {
		return err
	}
	return m.cancelUpload(ctx, upload, model)
}

func (m *Manager) cancelUpload(ctx context.Context, upload domain.ModelUpload, model domain.Model) error {
	env, err := m.store.GetHost(ctx, model.HostID)
	if err != nil {
		return err
	}
	password, err := m.cipher.Decrypt(env.SSHPasswordEnc)
	if err != nil {
		return err
	}
	defer clearBytes(password)
	if err := m.remote.RemoveModelUpload(ctx, env, password, upload.RemotePath); err != nil {
		return err
	}
	if upload.Status == "uploading" {
		if err := m.store.CancelModelUpload(ctx, upload.ID); err != nil {
			return err
		}
	}
	return m.store.MarkModelDeleted(ctx, model.ID)
}

func (m *Manager) startTask(ctx context.Context, model domain.Model, action domain.ModelTaskAction, actor domain.User) (domain.ModelTask, error) {
	m.mu.Lock()
	if _, exists := m.active[model.ID]; exists {
		m.mu.Unlock()
		return domain.ModelTask{}, &domain.AppError{Code: "MODEL_OPERATION_IN_PROGRESS", Message: "模型已有任务正在执行"}
	}
	m.active[model.ID] = struct{}{}
	m.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(m.dataDir, "model-tasks"), 0o750); err != nil {
		m.release(model.ID)
		return domain.ModelTask{}, err
	}
	task := domain.ModelTask{ModelID: model.ID, OwnerID: model.OwnerID, ActorUserID: actor.ID, ActorUsername: actor.Username, Action: action,
		Status: domain.OperationQueued, Stage: "queued", LogPath: filepath.Join("model-tasks", domain.NewID()+".jsonl")}
	var err error
	task, err = m.store.CreateModelTask(ctx, task)
	if err != nil {
		m.release(model.ID)
		return task, err
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.release(model.ID)
		m.sem <- struct{}{}
		defer func() { <-m.sem }()
		m.run(task)
	}()
	return task, nil
}

func (m *Manager) run(task domain.ModelTask) {
	logger, err := newLogger(filepath.Join(m.dataDir, task.LogPath))
	if err != nil {
		m.finish(&task, domain.OperationFailed, "prepare", "LOG_CREATE_FAILED", err.Error(), nil)
		return
	}
	defer logger.Close()
	emit := func(stream, message string) {
		_ = logger.write(domain.OperationEvent{Type: "log", Stream: stream, Message: message, Stage: task.Stage})
	}
	now := time.Now().UTC()
	task.StartedAt = &now
	task.Status = domain.OperationRunning
	task.Stage = "prepare"
	task.Progress = 5
	_ = m.store.UpdateModelTask(m.ctx, task)
	_ = logger.write(domain.OperationEvent{Type: "state", Status: string(task.Status), Stage: task.Stage, Message: "模型任务开始"})
	model, err := m.store.GetModel(m.ctx, task.ModelID)
	if err != nil {
		m.finishLogged(&task, logger, domain.OperationFailed, "prepare", "MODEL_NOT_FOUND", err.Error())
		return
	}
	env, err := m.store.GetHost(m.ctx, model.HostID)
	if err != nil {
		m.finishLogged(&task, logger, domain.OperationFailed, "prepare", "HOST_NOT_FOUND", err.Error())
		return
	}
	password, err := m.cipher.Decrypt(env.SSHPasswordEnc)
	if err != nil {
		m.finishLogged(&task, logger, domain.OperationFailed, "prepare", "PASSWORD_DECRYPT_FAILED", err.Error())
		return
	}
	defer clearBytes(password)
	ctx, cancel := context.WithTimeout(m.ctx, m.transferTimeout)
	defer cancel()
	if task.Action == domain.ModelTaskDeploy {
		upload, err := m.uploadByModel(ctx, model.ID)
		if err != nil {
			m.finishLogged(&task, logger, domain.OperationFailed, "prepare", "MODEL_UPLOAD_NOT_FOUND", err.Error())
			return
		}
		m.stage(&task, logger, "validate", 20, "正在流式校验远端模型包")
		maxExpanded := m.maxBytes
		if model.SizeBytes <= m.maxBytes/20 {
			maxExpanded = model.SizeBytes * 20
		}
		if maxExpanded < 1<<30 && m.maxBytes >= 1<<30 {
			maxExpanded = 1 << 30
		}
		lastValidationPercent := 0
		validationProgress := func(done, total int64) {
			if total <= 0 {
				return
			}
			percent := int(done * 100 / total)
			if percent > 100 {
				percent = 100
			}
			if percent < lastValidationPercent+5 && percent < 100 {
				return
			}
			lastValidationPercent = percent
			m.stage(&task, logger, "validate", 20+percent*30/100,
				fmt.Sprintf("模型包校验进度 %d%%（%d / %d MiB）", percent, done>>20, total>>20))
		}
		inspection, err := m.remote.InspectModelArchive(ctx, env, password, upload.RemotePath, maxExpanded, validationProgress, emit)
		if err == nil {
			m.stage(&task, logger, "extract", 55, fmt.Sprintf("校验完成：%d 个文件，展开后 %d 字节", inspection.FileCount, inspection.ExpandedSize))
			err = m.remote.DeployModelArchive(ctx, env, password, model, upload.ID, inspection, emit)
		}
		if err != nil {
			m.finishLogged(&task, logger, classify(err), task.Stage, errorCode(err), err.Error())
			_ = m.store.SetModelState(context.Background(), model.ID, domain.ModelFailed, err.Error())
			return
		}
		if err := m.store.MarkModelReady(context.Background(), model.ID, inspection.SHA256, inspection.ExpandedSize, inspection.FileCount); err != nil {
			m.finishLogged(&task, logger, domain.OperationFailed, "commit", "MODEL_COMMIT_FAILED", err.Error())
			return
		}
	} else {
		m.stage(&task, logger, "delete", 30, "正在校验模型目录归属并删除")
		if err := m.remote.DeleteModel(ctx, env, password, model, task.ID, emit); err != nil {
			m.finishLogged(&task, logger, classify(err), "delete", errorCode(err), err.Error())
			_ = m.store.SetModelState(context.Background(), model.ID, domain.ModelFailed, err.Error())
			return
		}
		if err := m.store.MarkModelDeleted(context.Background(), model.ID); err != nil {
			m.finishLogged(&task, logger, domain.OperationFailed, "commit", "MODEL_COMMIT_FAILED", err.Error())
			return
		}
	}
	m.finishLogged(&task, logger, domain.OperationSucceeded, "completed", "", "模型任务完成")
}

func (m *Manager) stage(task *domain.ModelTask, logger *eventLogger, stage string, progress int, message string) {
	task.Stage, task.Progress = stage, progress
	_ = m.store.UpdateModelTask(m.ctx, *task)
	_ = logger.write(domain.OperationEvent{Type: "state", Status: string(task.Status), Stage: stage, Message: message})
}
func (m *Manager) finishLogged(task *domain.ModelTask, logger *eventLogger, status domain.OperationStatus, stage, code, message string) {
	m.finish(task, status, stage, code, message, nil)
	_ = logger.write(domain.OperationEvent{Type: "state", Status: string(status), Stage: stage, Message: message})
}
func (m *Manager) finish(task *domain.ModelTask, status domain.OperationStatus, stage, code, message string, _ *int) {
	now := time.Now().UTC()
	task.Status, task.Stage, task.ErrorCode, task.ErrorMessage, task.Progress, task.FinishedAt = status, stage, code, message, 100, &now
	if err := m.store.UpdateModelTask(context.Background(), *task); err != nil {
		m.log.Error("update model task", "task_id", task.ID, "error", err)
	}
	if status != domain.OperationSucceeded {
		_ = m.store.SetModelState(context.Background(), task.ModelID, domain.ModelFailed, message)
	}
	if m.audit != nil {
		model, err := m.store.GetModel(context.Background(), task.ModelID)
		if err == nil {
			outcome := "success"
			if status != domain.OperationSucceeded {
				outcome = "failure"
			}
			m.audit.Record(context.Background(), domain.AuditEvent{
				Category: "model", Action: "model." + string(task.Action) + ".completed", Outcome: outcome,
				ActorUserID: task.ActorUserID, ActorUsername: task.ActorUsername,
				OwnerID: task.OwnerID, OwnerUsername: model.OwnerUsername,
				TargetType: "model", TargetID: model.ID, TargetLabel: model.Name, ErrorCode: code,
				Changes: map[string]any{"task_id": task.ID, "target_dir": model.TargetDir, "host_id": model.HostID, "status": status},
			})
		}
	}
}

func (m *Manager) uploadContext(ctx context.Context, id string) (domain.ModelUpload, domain.Model, domain.Host, []byte, error) {
	upload, err := m.store.GetModelUpload(ctx, id)
	if err != nil {
		return upload, domain.Model{}, domain.Host{}, nil, err
	}
	if upload.Status != "uploading" {
		return upload, domain.Model{}, domain.Host{}, nil, &domain.AppError{Code: "MODEL_UPLOAD_CLOSED", Message: "上传会话已结束"}
	}
	model, err := m.store.GetModel(ctx, upload.ModelID)
	if err != nil {
		return upload, model, domain.Host{}, nil, err
	}
	env, err := m.store.GetHost(ctx, model.HostID)
	if err != nil {
		return upload, model, env, nil, err
	}
	password, err := m.cipher.Decrypt(env.SSHPasswordEnc)
	return upload, model, env, password, err
}
func (m *Manager) uploadByModel(ctx context.Context, modelID string) (domain.ModelUpload, error) {
	return m.store.GetModelUploadByModel(ctx, modelID)
}
func (m *Manager) lock(id string) {
	m.mu.Lock()
	for {
		if _, ok := m.active["upload:"+id]; !ok {
			m.active["upload:"+id] = struct{}{}
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		m.mu.Lock()
	}
}
func (m *Manager) unlock(id string)  { m.mu.Lock(); delete(m.active, "upload:"+id); m.mu.Unlock() }
func (m *Manager) release(id string) { m.mu.Lock(); delete(m.active, id); m.mu.Unlock() }
func (m *Manager) Wait()             { m.wg.Wait() }

func (m *Manager) Run(ctx context.Context) {
	m.cleanupExpired(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.cleanupExpired(ctx)
		}
	}
}

func (m *Manager) cleanupExpired(ctx context.Context) {
	items, err := m.store.ListExpiredModelUploads(ctx, time.Now().UTC(), 100)
	if err != nil {
		m.log.Error("list expired model uploads", "error", err)
		return
	}
	for _, upload := range items {
		model, err := m.store.GetModel(ctx, upload.ModelID)
		if err != nil {
			continue
		}
		env, err := m.store.GetHost(ctx, model.HostID)
		if err != nil {
			continue
		}
		password, err := m.cipher.Decrypt(env.SSHPasswordEnc)
		if err != nil {
			continue
		}
		err = m.remote.RemoveModelUpload(ctx, env, password, upload.RemotePath)
		clearBytes(password)
		if err != nil {
			m.log.Warn("remove expired remote model upload", "upload_id", upload.ID, "error", err)
			continue
		}
		if err := m.store.CancelModelUpload(ctx, upload.ID); err != nil {
			continue
		}
		_ = m.store.MarkModelDeleted(ctx, model.ID)
	}
}
func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func classify(err error) domain.OperationStatus {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.OperationTimedOut
	}
	if errors.Is(err, context.Canceled) {
		return domain.OperationInterrupted
	}
	return domain.OperationFailed
}
func errorCode(err error) string {
	var app *domain.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "MODEL_TASK_TIMEOUT"
	}
	return "MODEL_REMOTE_FAILED"
}

func (m *Manager) ReadEvents(task domain.ModelTask, after int64) ([]domain.OperationEvent, error) {
	file, err := os.Open(filepath.Join(m.dataDir, filepath.Clean(task.LogPath)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var result []domain.OperationEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		var event domain.OperationEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Seq > after {
			result = append(result, event)
		}
	}
	return result, scanner.Err()
}

type eventLogger struct {
	mu   sync.Mutex
	file *os.File
	seq  int64
}

func newLogger(name string) (*eventLogger, error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	return &eventLogger{file: file}, nil
}
func (l *eventLogger) write(event domain.OperationEvent) error {
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
	_, err = l.file.Write(content)
	return err
}
func (l *eventLogger) Close() error { return l.file.Close() }
