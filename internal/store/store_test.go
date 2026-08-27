package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/domain"
)

func TestOwnershipMigrationAssignsExistingDataToInitialAdmin(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 5; version++ {
		matches, err := migrationFS.ReadDir("migrations")
		if err != nil {
			t.Fatal(err)
		}
		var name string
		for _, entry := range matches {
			if len(entry.Name()) >= 4 && entry.Name()[:4] == fmt.Sprintf("%03d_", version) {
				name = entry.Name()
				break
			}
		}
		content, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.ExecContext(ctx, string(content)); err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, version, formatTime(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	now := formatTime(time.Now())
	if _, err := legacy.ExecContext(ctx, `INSERT INTO packages(service_type, original_filename, storage_path, sha256, size_bytes, config_port, uploaded_at, updated_at, note) VALUES ('legacy', 'x.tar.gz', 'packages/legacy/current.tar.gz', 'sha', 1, 80, ?, ?, '')`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO environments(id, name, ip, ssh_user, ssh_port, ssh_password_enc, install_dir, service_type, created_at, updated_at, arch, note) VALUES ('env-1', 'legacy', '127.0.0.1', 'u', 22, 'enc', '/opt/x', 'legacy', ?, ?, '', '')`, now, now); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	env, err := upgraded.GetEnvironment(ctx, "env-1")
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := upgraded.GetPackageByOwner(ctx, InitialAdminID, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if env.OwnerID != InitialAdminID || pkg.OwnerID != InitialAdminID {
		t.Fatalf("owners env=%q pkg=%q", env.OwnerID, pkg.OwnerID)
	}
}

func TestEnvironmentUniqueIPAndServiceType(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := domain.Environment{
		Name: "first", IP: "127.0.0.1", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/service",
		ServiceType: "dp-demo",
	}
	if _, err := db.CreateEnvironment(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Name = "duplicate"
	if _, err := db.CreateEnvironment(ctx, base); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestOwnershipAllowsSameEnvironmentAndPackageKeys(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	second, err := db.CreateUser(ctx, domain.User{Username: "second", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	base := domain.Environment{Name: "env", IP: "127.0.0.1", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/x", ServiceType: "same"}
	if _, err := db.CreateEnvironment(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.OwnerID = second.ID
	if _, err := db.CreateEnvironment(ctx, base); err != nil {
		t.Fatalf("second owner environment: %v", err)
	}
	for _, owner := range []string{InitialAdminID, second.ID} {
		if err := db.UpsertPackage(ctx, domain.Package{OwnerID: owner, ServiceType: "same", OriginalFilename: "x.tar.gz", StoragePath: "x", SHA256: owner, SizeBytes: 1, ConfigPort: 80, UploadedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	packages, err := db.ListPackagesByOwner(ctx, second.ID)
	if err != nil || len(packages) != 1 || packages[0].OwnerID != second.ID {
		t.Fatalf("scoped packages=%+v err=%v", packages, err)
	}
}

func TestListPackagesReturnsEveryServiceType(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, serviceType := range []string{"dp-demo", "video-forward"} {
		now := time.Now().UTC()
		if err := db.UpsertPackage(ctx, domain.Package{
			ServiceType: serviceType, OriginalFilename: serviceType + ".tar.gz",
			StoragePath: "packages/" + serviceType + "/current.tar.gz",
			SHA256:      "sha", SizeBytes: 10, ConfigPort: 8080,
			UploadedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	packages, err := db.ListPackages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 ||
		packages[0].ServiceType != "dp-demo" ||
		packages[1].ServiceType != "video-forward" {
		t.Fatalf("unexpected packages: %+v", packages)
	}
}

func TestEnvironmentNoteRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	env, err := db.CreateEnvironment(ctx, domain.Environment{
		Name: "noted", IP: "127.0.0.10", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/noted",
		ServiceType: "dp-demo", Note: "机房 A 机架 3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.Note != "机房 A 机架 3" {
		t.Fatalf("expected note saved, got %+v", env)
	}
	env.Note = ""
	updated, err := db.UpdateEnvironment(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Note != "" {
		t.Fatalf("expected note cleared, got %+v", updated)
	}
	environments, err := db.ListEnvironments(ctx)
	if err != nil || len(environments) != 1 {
		t.Fatalf("unexpected environments: %+v, err=%v", environments, err)
	}
}

func TestResetStateAndLastSuccessfulAction(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	env, err := db.CreateEnvironment(ctx, domain.Environment{
		Name: "reset", IP: "127.0.0.9", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/reset",
		ServiceType: "dp-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MarkInstalled(ctx, env.ID, "package-sha", 8080); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index, action := range []domain.OperationAction{domain.ActionStart, domain.ActionStop} {
		finished := now.Add(time.Duration(index+1) * time.Second)
		err := db.CreateOperation(ctx, domain.Operation{
			ID: storeTestID(index), EnvironmentID: env.ID, Action: action,
			Status: domain.OperationSucceeded, Stage: "completed",
			LogPath: "test.log", CreatedAt: finished, StartedAt: &finished, FinishedAt: &finished,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	action, err := db.LastSuccessfulAction(ctx, env.ID)
	if err != nil || action != domain.ActionStop {
		t.Fatalf("action=%q err=%v", action, err)
	}
	if err := db.MarkUninstalled(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	reset, err := db.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Installed || reset.InstalledAt != nil ||
		reset.InstalledPackageSHA256 != "" || reset.HealthPort != nil {
		t.Fatalf("installation state was not cleared: %+v", reset)
	}
}

func TestDeletePackage(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if err := db.UpsertPackage(ctx, domain.Package{
		ServiceType: "dp-demo", OriginalFilename: "dp-demo.tar.gz",
		StoragePath: "packages/dp-demo/current.tar.gz",
		SHA256:      "sha", SizeBytes: 10, ConfigPort: 8080,
		UploadedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{
		Name: "installed", IP: "127.0.0.1", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/service",
		ServiceType: "dp-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MarkInstalled(ctx, env.ID, "sha", 8080); err != nil {
		t.Fatal(err)
	}
	count, err := db.CountInstalledEnvironments(ctx, "dp-demo")
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := db.MarkUninstalled(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	count, err = db.CountInstalledEnvironments(ctx, "dp-demo")
	if err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := db.DeletePackage(ctx, "dp-demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetPackage(ctx, "dp-demo"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := db.DeletePackage(ctx, "dp-demo"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestDeleteEnvironmentCascades(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	env, err := db.CreateEnvironment(ctx, domain.Environment{
		Name: "delete", IP: "127.0.0.1", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/service",
		ServiceType: "dp-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertServiceConfig(ctx, domain.ServiceConfig{
		EnvironmentID: env.ID, Content: `{"port":8080}`,
		Format: "json", Path: "config/config.json", Port: 8080,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.CreateOperation(ctx, domain.Operation{
		ID: storeTestID(0), EnvironmentID: env.ID, Action: domain.ActionInstall,
		Status: domain.OperationSucceeded, Stage: "completed",
		LogPath: "operations/" + storeTestID(0) + ".jsonl", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	logPaths, err := db.DeleteEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logPaths) != 0 {
		t.Fatalf("unexpected log paths: %+v", logPaths)
	}
	if _, err := db.GetEnvironment(ctx, env.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if op, err := db.GetOperation(ctx, storeTestID(0)); err != nil || op.EnvironmentID != env.ID {
		t.Fatalf("expected operation history retained, op=%+v err=%v", op, err)
	}
	if _, err := db.GetServiceConfig(ctx, env.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if _, err := db.DeleteEnvironment(ctx, env.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func storeTestID(index int) string {
	if index == 0 {
		return "00000000-0000-4000-8000-000000000001"
	}
	return "00000000-0000-4000-8000-000000000002"
}

func TestServiceConfigIsIndependentPerEnvironment(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	create := func(name, ip string) domain.Environment {
		env, createErr := db.CreateEnvironment(ctx, domain.Environment{
			Name: name, IP: ip, SSHUser: "user", SSHPort: 22,
			SSHPasswordEnc: "encrypted", InstallDir: "/opt/service",
			ServiceType: "dp-demo",
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return env
	}
	first := create("first", "127.0.0.1")
	second := create("second", "127.0.0.2")
	for _, item := range []struct {
		env     domain.Environment
		content string
		port    int
	}{
		{env: first, content: `{"port":8081}`, port: 8081},
		{env: second, content: `{"port":8082}`, port: 8082},
	} {
		_, err = db.UpsertServiceConfig(ctx, domain.ServiceConfig{
			EnvironmentID: item.env.ID,
			Content:       item.content,
			Format:        "json",
			Path:          "config/config.json",
			Port:          item.port,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	firstConfig, err := db.GetServiceConfig(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondConfig, err := db.GetServiceConfig(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstConfig.Port != 8081 || secondConfig.Port != 8082 ||
		firstConfig.Content == secondConfig.Content {
		t.Fatalf("configs are not independent: first=%+v second=%+v", firstConfig, secondConfig)
	}
}

func TestListServicePortsPrefersInstanceConfig(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	if err := db.UpsertPackage(ctx, domain.Package{
		ServiceType: "dp-demo", OriginalFilename: "dp-demo.tar.gz",
		StoragePath: "packages/dp-demo/current.tar.gz",
		SHA256:      "sha", SizeBytes: 10, ConfigPort: 8080,
		UploadedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	inherited, err := db.CreateEnvironment(ctx, domain.Environment{
		Name: "inherited", IP: "127.0.0.1", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/inherited",
		ServiceType: "dp-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	customized, err := db.CreateEnvironment(ctx, domain.Environment{
		Name: "customized", IP: "127.0.0.2", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/customized",
		ServiceType: "dp-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertServiceConfig(ctx, domain.ServiceConfig{
		EnvironmentID: customized.ID, Content: `{"port":9090}`,
		Format: "json", Path: "config/config.json", Port: 9090,
	}); err != nil {
		t.Fatal(err)
	}

	ports, err := db.ListServicePorts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ports[inherited.ID] != 8080 || ports[customized.ID] != 9090 {
		t.Fatalf("unexpected service ports: %+v", ports)
	}
}

func TestEnvironmentArchLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	env, err := db.CreateEnvironment(ctx, domain.Environment{
		Name: "arch", IP: "127.0.0.10", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/arch",
		ServiceType: "dp-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.Arch != "" {
		t.Fatalf("expected empty arch on create, got %q", env.Arch)
	}
	if err := db.RecordValidation(ctx, env.ID, "SHA256:fingerprint", "x86_64"); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Arch != "x86_64" || saved.HostKeyFingerprint != "SHA256:fingerprint" {
		t.Fatalf("validation state not persisted: %+v", saved)
	}
	if err := db.UpdateEnvironmentArch(ctx, env.ID, "aarch64"); err != nil {
		t.Fatal(err)
	}
	saved, err = db.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Arch != "aarch64" {
		t.Fatalf("arch not updated: %+v", saved)
	}
	// 编辑环境不应覆盖已采集的架构。
	saved.Name = "renamed"
	updated, err := db.UpdateEnvironment(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Arch != "aarch64" {
		t.Fatalf("arch overwritten by update: %+v", updated)
	}
}

func TestLatestOperationsReturnsNewestPerEnvironment(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := db.CreateEnvironment(ctx, domain.Environment{
		Name: "first", IP: "127.0.0.10", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/first",
		ServiceType: "dp-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateEnvironment(ctx, domain.Environment{
		Name: "second", IP: "127.0.0.11", SSHUser: "user", SSHPort: 22,
		SSHPasswordEnc: "encrypted", InstallDir: "/opt/second",
		ServiceType: "video-forward",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	failed := now.Add(time.Second)
	operations := []domain.Operation{
		{ID: "00000000-0000-4000-8000-000000000010", EnvironmentID: first.ID, Action: domain.ActionInstall,
			Status: domain.OperationFailed, Stage: "script", ErrorMessage: "exit 1",
			LogPath: "a.log", CreatedAt: now, FinishedAt: &failed},
		{ID: "00000000-0000-4000-8000-000000000011", EnvironmentID: first.ID, Action: domain.ActionReset,
			Status: domain.OperationSucceeded, Stage: "completed",
			LogPath: "b.log", CreatedAt: failed, FinishedAt: &failed},
		{ID: "00000000-0000-4000-8000-000000000012", EnvironmentID: second.ID, Action: domain.ActionInstall,
			Status: domain.OperationTimedOut, Stage: "script",
			LogPath: "c.log", CreatedAt: now, FinishedAt: &failed},
	}
	for _, op := range operations {
		if err := db.CreateOperation(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := db.LatestOperations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(latest))
	}
	if op := latest[first.ID]; op.Action != domain.ActionReset || op.Status != domain.OperationSucceeded {
		t.Fatalf("first env latest = %+v", op)
	}
	if op := latest[second.ID]; op.Action != domain.ActionInstall || op.Status != domain.OperationTimedOut {
		t.Fatalf("second env latest = %+v", op)
	}
}
