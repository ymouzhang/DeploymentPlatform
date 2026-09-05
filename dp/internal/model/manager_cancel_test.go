package model

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"DP/internal/domain"
)

type cancelTaskRepository struct {
	mu   sync.Mutex
	task domain.ModelTask
}

func (r *cancelTaskRepository) GetModelTask(_ context.Context, _ string) (domain.ModelTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.task, nil
}
func (r *cancelTaskRepository) setTask(task domain.ModelTask) {
	r.mu.Lock()
	r.task = task
	r.mu.Unlock()
}

func (*cancelTaskRepository) CancelModelUpload(context.Context, string) error   { return nil }
func (*cancelTaskRepository) CompleteModelUpload(context.Context, string) error { return nil }
func (*cancelTaskRepository) CreateModelTask(context.Context, domain.ModelTask) (domain.ModelTask, error) {
	return domain.ModelTask{}, errors.New("not implemented")
}
func (*cancelTaskRepository) CreateModelUpload(context.Context, domain.Model, domain.ModelUpload) (domain.Model, domain.ModelUpload, error) {
	return domain.Model{}, domain.ModelUpload{}, errors.New("not implemented")
}
func (*cancelTaskRepository) GetHost(context.Context, string) (domain.Host, error) {
	return domain.Host{}, errors.New("not implemented")
}
func (*cancelTaskRepository) GetModel(context.Context, string) (domain.Model, error) {
	return domain.Model{}, errors.New("not implemented")
}
func (*cancelTaskRepository) GetModelUpload(context.Context, string) (domain.ModelUpload, error) {
	return domain.ModelUpload{}, errors.New("not implemented")
}
func (*cancelTaskRepository) GetModelUploadByModel(context.Context, string) (domain.ModelUpload, error) {
	return domain.ModelUpload{}, errors.New("not implemented")
}
func (*cancelTaskRepository) ListExpiredModelUploads(context.Context, time.Time, int) ([]domain.ModelUpload, error) {
	return nil, errors.New("not implemented")
}
func (*cancelTaskRepository) ListModels(context.Context, string) ([]domain.Model, error) {
	return nil, nil
}
func (*cancelTaskRepository) MarkModelDeleted(context.Context, string) error { return nil }
func (*cancelTaskRepository) MarkModelReady(context.Context, string, string, int64, int64) error {
	return nil
}
func (*cancelTaskRepository) SetModelState(context.Context, string, domain.ModelStatus, string) error {
	return nil
}
func (*cancelTaskRepository) SetModelUploadOffset(context.Context, string, int64) error { return nil }
func (*cancelTaskRepository) UpdateModelTask(context.Context, domain.ModelTask) error   { return nil }

func TestCancelTaskWaitsForVerifiedCleanup(t *testing.T) {
	repository := &cancelTaskRepository{task: domain.ModelTask{ID: "task", Action: domain.ModelTaskDeploy, Status: domain.OperationRunning}}
	taskCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	manager := &Manager{store: repository, tasks: map[string]*activeTask{"task": {cancel: cancel, done: done, requested: make(chan struct{})}}}
	go func() {
		<-taskCtx.Done()
		time.Sleep(20 * time.Millisecond)
		task := repository.task
		task.Status = domain.OperationInterrupted
		task.ErrorCode = "MODEL_DEPLOY_CANCELLED"
		repository.setTask(task)
		close(done)
	}()
	started := time.Now()
	if err := manager.CancelTask(context.Background(), "task"); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 20*time.Millisecond {
		t.Fatal("cancel returned before cleanup completion")
	}
}

func TestCancelTaskRejectsAtomicCommit(t *testing.T) {
	repository := &cancelTaskRepository{task: domain.ModelTask{ID: "task", Action: domain.ModelTaskDeploy, Status: domain.OperationRunning}}
	taskCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	manager := &Manager{store: repository, tasks: map[string]*activeTask{"task": {cancel: cancel, done: done, requested: make(chan struct{})}}}
	go func() {
		<-taskCtx.Done()
		task := repository.task
		task.Status = domain.OperationSucceeded
		repository.setTask(task)
		close(done)
	}()
	err := manager.CancelTask(context.Background(), "task")
	var appErr *domain.AppError
	if !errors.As(err, &appErr) || appErr.Code != "MODEL_TASK_ALREADY_COMMITTED" {
		t.Fatalf("expected MODEL_TASK_ALREADY_COMMITTED, got %v", err)
	}
}
