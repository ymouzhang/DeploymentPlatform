package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/domain"
)

func TestModelUploadPersistenceAndTargetUniqueness(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: InitialAdminID, Name: "gpu",
		IP: "127.0.0.2", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc",
		InstallDir: "/opt/service", ServiceType: "vllm"})
	if err != nil {
		t.Fatal(err)
	}
	model := domain.Model{OwnerID: InitialAdminID, EnvironmentID: env.ID, EnvironmentName: env.Name,
		EnvironmentIP: env.IP, Name: "Qwen", Source: "offline", TargetDir: "/opt/models/qwen",
		OriginalFilename: "qwen.tar.gz", SizeBytes: 40 << 30, Status: domain.ModelUploading}
	upload := domain.ModelUpload{RemotePath: "/opt/models/.upload.tar.gz", TotalBytes: model.SizeBytes,
		Status: "uploading", ExpiresAt: time.Now().Add(time.Hour)}
	created, session, err := db.CreateModelUpload(ctx, model, upload)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetModelUploadOffset(ctx, session.ID, 64<<20); err != nil {
		t.Fatal(err)
	}
	persisted, err := db.GetModelUpload(ctx, session.ID)
	if err != nil || persisted.Offset != 64<<20 {
		t.Fatalf("upload=%+v err=%v", persisted, err)
	}
	items, err := db.ListModels(ctx, InitialAdminID)
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	model.ID = ""
	upload.ID = ""
	if _, _, err := db.CreateModelUpload(ctx, model, upload); err == nil {
		t.Fatal("expected duplicate target conflict")
	} else {
		var app *domain.AppError
		if !errors.As(err, &app) || app.Code != "MODEL_TARGET_EXISTS" {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestInterruptActiveModelTasks(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Foreign keys are intentionally disabled only for this focused state-transition fixture.
	if _, err := db.db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO model_tasks(id, model_id, owner_id, action, status, stage, progress, log_path, created_at) VALUES ('task', 'model', ?, 'deploy', ?, 'extract', 50, 'x', ?)`, InitialAdminID, domain.OperationRunning, formatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := db.InterruptActiveModelTasks(ctx); err != nil {
		t.Fatal(err)
	}
	task, err := db.GetModelTask(ctx, "task")
	if err != nil || task.Status != domain.OperationInterrupted {
		t.Fatalf("task=%+v err=%v", task, err)
	}
}
