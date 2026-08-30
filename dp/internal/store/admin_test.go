package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/domain"
)

func TestTransferResourcesPreservesOperationSnapshot(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, _ := db.CreateUser(ctx, domain.User{Username: "source", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	target, _ := db.CreateUser(ctx, domain.User{Username: "target", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: source.ID, Name: "prod", IP: "192.0.2.10", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/app", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.UpsertPackage(ctx, domain.Package{OwnerID: source.ID, ServiceType: "demo", StoragePath: "packages/source/demo/current.tar.gz", OriginalFilename: "demo.tar.gz", SHA256: "sha", SizeBytes: 1, ConfigPort: 8080, UploadedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	op := domain.Operation{ID: NewID(), EnvironmentID: env.ID, ActorUserID: source.ID, ActorUsername: source.Username, OwnerID: source.ID, OwnerUsername: source.Username, EnvironmentName: env.Name, EnvironmentIP: env.IP, ServiceType: env.ServiceType, Action: domain.ActionInstall, Status: domain.OperationSucceeded, Stage: "completed", LogPath: "operations/test.jsonl", CreatedAt: now}
	if err := db.CreateOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	packages, environments, err := db.TransferPreview(ctx, source.ID, target.ID)
	if err != nil || len(packages) != 1 || environments != 1 {
		t.Fatalf("preview packages=%+v environments=%d err=%v", packages, environments, err)
	}
	result, err := db.TransferResources(ctx, source.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Packages != 1 || result.Environments != 1 {
		t.Fatalf("result=%+v", result)
	}
	transferred, _ := db.GetEnvironment(ctx, env.ID)
	if transferred.OwnerID != target.ID {
		t.Fatalf("owner=%s", transferred.OwnerID)
	}
	history, _ := db.GetOperation(ctx, op.ID)
	if history.OwnerID != source.ID || history.OwnerUsername != source.Username {
		t.Fatalf("operation snapshot changed: %+v", history)
	}
	stale := op
	stale.ID = NewID()
	stale.Status = domain.OperationQueued
	if err := db.CreateOperation(ctx, stale); appErrorCode(err) != "TRANSFER_CONFLICT" {
		t.Fatalf("stale owner operation error=%v", err)
	}
	transferredPackage, err := db.GetPackageByOwner(ctx, target.ID, "demo")
	if err != nil || transferredPackage.StoragePath != "packages/source/demo/current.tar.gz" {
		t.Fatalf("transferred package=%+v err=%v", transferredPackage, err)
	}
}

func TestTransferPreviewRejectsConflictsAndActiveOperations(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, _ := db.CreateUser(ctx, domain.User{Username: "source", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	target, _ := db.CreateUser(ctx, domain.User{Username: "target", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: source.ID, Name: "active", IP: "192.0.2.30", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/demo", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.CreateOperation(ctx, domain.Operation{ID: NewID(), OwnerID: source.ID, EnvironmentID: env.ID, Action: domain.ActionStart, Status: domain.OperationRunning, Stage: "script", LogPath: "operations/active.jsonl", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.TransferPreview(ctx, source.ID, target.ID); appErrorCode(err) != "TRANSFER_CONFLICT" {
		t.Fatalf("active operation error=%v", err)
	}
}

func TestNotificationLifecycleAndPagination(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	created, err := db.CreateNotification(ctx, domain.Notification{RiskLevel: "high", Category: "security", Title: "risk", Message: "message", Link: "/audit"})
	if err != nil {
		t.Fatal(err)
	}
	summary, _ := db.NotificationSummary(ctx)
	if summary.Unread != 1 || summary.Unresolved != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	read, err := db.MarkNotificationRead(ctx, created.ID, "admin", time.Now())
	if err != nil || !read.Read || read.Resolved {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	resolved, err := db.ResolveNotification(ctx, created.ID, "admin", time.Now())
	if err != nil || !resolved.Read || !resolved.Resolved {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	summary, _ = db.NotificationSummary(ctx)
	if summary.Unread != 0 || summary.Unresolved != 0 {
		t.Fatalf("summary after resolve=%+v", summary)
	}
}

func TestNotificationDedupeAndResolvedRetention(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	item := domain.Notification{DedupeKey: "login:admin:127.0.0.1", RiskLevel: "high", Category: "security", Title: "risk", Message: "message", Link: "/audit"}
	first, err := db.CreateNotification(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateNotification(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate notification created: %s != %s", first.ID, second.ID)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := db.ResolveNotification(ctx, first.ID, "admin", old); err != nil {
		t.Fatal(err)
	}
	third, err := db.CreateNotification(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID {
		t.Fatal("resolved notification should allow a new occurrence")
	}
	deleted, err := db.DeleteResolvedNotificationsBefore(ctx, time.Now().UTC().Add(-24*time.Hour), 100)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
}

func TestOperationRequestIDAndRetention(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	finished := time.Now().UTC().Add(-48 * time.Hour)
	op := domain.Operation{ID: NewID(), EnvironmentID: "deleted", RequestID: NewID(), Action: domain.ActionStart,
		Status: domain.OperationSucceeded, Stage: "completed", LogPath: "operations/old.jsonl", CreatedAt: finished, FinishedAt: &finished}
	if err := db.CreateOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.GetOperation(ctx, op.ID)
	if err != nil || loaded.RequestID != op.RequestID {
		t.Fatalf("operation=%+v err=%v", loaded, err)
	}
	paths, err := db.DeleteTerminalOperationsBefore(ctx, time.Now().UTC().Add(-24*time.Hour), 100)
	if err != nil || len(paths) != 1 || paths[0] != op.LogPath {
		t.Fatalf("paths=%v err=%v", paths, err)
	}
	if _, err := db.GetOperation(ctx, op.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected operation deleted, err=%v", err)
	}
}

func appErrorCode(err error) string {
	var app *domain.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}
