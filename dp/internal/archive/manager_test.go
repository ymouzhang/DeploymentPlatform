package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"DP/internal/domain"
	"DP/internal/store"
)

func TestInspectValidPackage(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"config/config.json": `{"port": 8080}`,
		"start.sh":           "#!/bin/sh\n",
		"stop.sh":            "#!/bin/sh\n",
	})
	result, err := inspect(file, 1<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Port != 8080 || !result.HasStart || !result.HasStop {
		t.Fatalf("unexpected inspection: %+v", result)
	}
}

func TestInspectRejectsTraversal(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"../bad":             "bad",
		"config/config.json": `{"port": 8080}`,
	})
	if _, err := inspect(file, 1<<20, false); err == nil {
		t.Fatal("expected path traversal error")
	}
}

func TestDeletePackage(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dataDir, "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(dataDir, 10<<20, db)

	file := buildArchive(t, map[string]string{
		"config/config.json": `{"port": 8080}`,
		"start.sh":           "#!/bin/sh\n",
		"stop.sh":            "#!/bin/sh\n",
	})
	src, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := manager.Upload(ctx, "dp-demo", "dp-demo.tar.gz", src, nil); err != nil {
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
	var appErr *domain.AppError
	if err := manager.Delete(ctx, "dp-demo"); !errors.As(err, &appErr) || appErr.Code != "PACKAGE_IN_USE" {
		t.Fatalf("expected PACKAGE_IN_USE, got %v", err)
	}
	if err := db.MarkUninstalled(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(ctx, "dp-demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetPackage(ctx, "dp-demo"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "packages", "dp-demo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected package directory removed, got %v", err)
	}
	if err := manager.Delete(ctx, "dp-demo"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestTransferOwnerPreservesImmutablePackageFile(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dataDir, "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := db.CreateUser(ctx, domain.User{Username: "source", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	target, err := db.CreateUser(ctx, domain.User{Username: "target", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(dataDir, 1<<20, db)
	archivePath := buildArchive(t, map[string]string{"config/config.json": `{"port":8080}`, "start.sh": "#!/bin/sh\n", "stop.sh": "#!/bin/sh\n"})
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UploadForOwner(ctx, source.ID, "demo", "demo.tar.gz", archiveFile, nil); err != nil {
		archiveFile.Close()
		t.Fatal(err)
	}
	archiveFile.Close()
	result, err := manager.TransferOwner(ctx, source.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Packages != 1 {
		t.Fatalf("result=%+v", result)
	}
	if _, err := db.GetPackageByOwner(ctx, source.ID, "demo"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("source package err=%v", err)
	}
	pkg, err := db.GetPackageByOwner(ctx, target.ID, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.AbsolutePath(pkg)); err != nil {
		t.Fatalf("target package file: %v", err)
	}
	if !strings.Contains(pkg.StoragePath, source.ID) {
		t.Fatalf("storage path was rewritten: %q", pkg.StoragePath)
	}
	versions, err := manager.ListVersions(ctx, target.ID, "demo")
	if err != nil || len(versions) != 1 || versions[0].OwnerID != target.ID {
		t.Fatalf("transferred versions=%+v err=%v", versions, err)
	}
	packagePath := manager.AbsolutePath(pkg)
	if err := manager.DeleteForOwner(ctx, target.ID, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(packagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transferred package file still exists: %v", err)
	}
}

func TestPackageNote(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dataDir, "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(dataDir, 10<<20, db)

	upload := func(note *string) {
		t.Helper()
		file := buildArchive(t, map[string]string{
			"config/config.json": `{"port": 8080}`,
			"start.sh":           "#!/bin/sh\n",
			"stop.sh":            "#!/bin/sh\n",
		})
		src, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		if _, err := manager.Upload(ctx, "dp-demo", "dp-demo.tar.gz", src, note); err != nil {
			t.Fatal(err)
		}
	}

	note := "首个版本"
	upload(&note)
	pkg, err := manager.Get(ctx, "dp-demo")
	if err != nil || pkg.Note != "首个版本" {
		t.Fatalf("expected note saved, got %+v, err=%v", pkg, err)
	}

	// 上传新版本时未传备注，保留当前版本备注。
	file := buildArchive(t, map[string]string{
		"config/config.json": `{"port": 8080}`,
		"start.sh":           "#!/bin/sh\n# version 2\n",
		"stop.sh":            "#!/bin/sh\n",
	})
	src, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Upload(ctx, "dp-demo", "dp-demo-v2.tar.gz", src, nil); err != nil {
		t.Fatal(err)
	}
	_ = src.Close()
	pkg, err = manager.Get(ctx, "dp-demo")
	if err != nil || pkg.Note != "首个版本" {
		t.Fatalf("expected note preserved, got %+v, err=%v", pkg, err)
	}

	pkg, err = manager.UpdateNote(ctx, "dp-demo", "")
	if err != nil || pkg.Note != "" {
		t.Fatalf("expected note cleared, got %+v, err=%v", pkg, err)
	}

	tooLong := strings.Repeat("备", domain.MaxNoteLength+1)
	if _, err := manager.UpdateNote(ctx, "dp-demo", tooLong); err == nil {
		t.Fatal("expected note length error")
	}
	if _, err := manager.UpdateNote(ctx, "missing", "note"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestPackageVersionLifecycle(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dataDir, "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(dataDir, 10<<20, db)
	upload := func(marker string) domain.Package {
		file := buildArchive(t, map[string]string{
			"config/config.json": `{"port": 8080}`,
			"start.sh":           "#!/bin/sh\n" + marker + "\n", "stop.sh": "#!/bin/sh\n",
		})
		src, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		pkg, err := manager.Upload(ctx, "versioned", marker+".tar.gz", src, nil)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}
	first := upload("v1")
	second := upload("v2")
	versions, err := manager.ListVersions(ctx, store.InitialAdminID, "versioned")
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if versions[0].ID != second.CurrentVersionID || !versions[0].Current {
		t.Fatalf("current=%+v", versions[0])
	}
	activated, err := manager.ActivateVersion(ctx, store.InitialAdminID, "versioned", first.CurrentVersionID)
	if err != nil || activated.CurrentVersionID != first.CurrentVersionID || activated.SHA256 != first.SHA256 {
		t.Fatalf("activated=%+v err=%v", activated, err)
	}
	if err := manager.DeleteVersion(ctx, store.InitialAdminID, "versioned", first.CurrentVersionID); appErrorCode(err) != "PACKAGE_VERSION_CURRENT" {
		t.Fatalf("delete current err=%v", err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: store.InitialAdminID, Name: "installed", IP: "192.0.2.30", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/versioned", ServiceType: "versioned", Installed: true, InstalledPackageSHA256: second.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.DeleteVersion(ctx, store.InitialAdminID, "versioned", second.CurrentVersionID); appErrorCode(err) != "PACKAGE_VERSION_IN_USE" {
		t.Fatalf("delete referenced err=%v", err)
	}
	if err := db.MarkUninstalled(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.DeleteVersion(ctx, store.InitialAdminID, "versioned", second.CurrentVersionID); err != nil {
		t.Fatal(err)
	}
	versions, _ = manager.ListVersions(ctx, store.InitialAdminID, "versioned")
	if len(versions) != 1 {
		t.Fatalf("versions after delete=%+v", versions)
	}
}

func TestPackageVersionRetentionKeepsCurrentAndReferenced(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dataDir, "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(dataDir, 10<<20, db)
	manager.ConfigureRetention(2)
	for _, marker := range []string{"v1", "v2", "v3"} {
		file := buildArchive(t, map[string]string{"config/config.json": `{"port":8080}`, "start.sh": "#!/bin/sh\n" + marker, "stop.sh": "#!/bin/sh\n"})
		src, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		_, uploadErr := manager.Upload(ctx, "retained", marker+".tar.gz", src, nil)
		_ = src.Close()
		if uploadErr != nil {
			t.Fatal(uploadErr)
		}
	}
	versions, err := manager.ListVersions(ctx, store.InitialAdminID, "retained")
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if !versions[0].Current {
		t.Fatalf("newest version should be current: %+v", versions)
	}
}

func appErrorCode(err error) string {
	var app *domain.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}

func buildArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "test.tar.gz")
	out, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "./", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = out.Close()
	return filename
}

func TestConfigPortString(t *testing.T) {
	port, err := configPort(bytes.TrimSpace([]byte(`{"port":"18080"}`)), "json")
	if err != nil || port != 18080 {
		t.Fatalf("port=%d err=%v", port, err)
	}
}

func TestInspectYAMLPackage(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"config/config.yaml": "port: 18081\nlog_level: info\n",
		"start.sh":           "#!/bin/sh\n",
		"stop.sh":            "#!/bin/sh\n",
	})
	result, err := inspect(file, 1<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Port != 18081 || result.ConfigType != "yaml" ||
		result.ConfigPath != "config/config.yaml" {
		t.Fatalf("unexpected inspection: %+v", result)
	}
}

func TestInspectWrappedYAMLPackageAndNestedPort(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"dist/config/config.yaml": "server:\n  port: 33182\n",
		"dist/start.sh":           "#!/bin/sh\n",
		"dist/stop.sh":            "#!/bin/sh\n",
		"dist/dp-demo":      "binary",
	})
	result, err := inspect(file, 1<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Port != 33182 || result.RootPrefix != "dist" ||
		result.ConfigPath != "dist/config/config.yaml" ||
		!result.HasStart || !result.HasStop {
		t.Fatalf("unexpected inspection: %+v", result)
	}
}

func TestInspectRejectsMixedWrappedAndRootFiles(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"dist/config/config.yaml": "port: 8080\n",
		"dist/start.sh":           "#!/bin/sh\n",
		"dist/stop.sh":            "#!/bin/sh\n",
		"unexpected.txt":          "outside wrapper",
	})
	if _, err := inspect(file, 1<<20, false); err == nil {
		t.Fatal("expected mixed root error")
	}
}

func TestInspectRejectsConfigAndStartScriptMismatch(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"dist/config/config.yaml": "port: 8080\n",
		"dist/start.sh":           "./dp-demo --config=config/config.json\n",
		"dist/stop.sh":            "#!/bin/sh\n",
	})
	if _, err := inspect(file, 1<<20, false); err == nil {
		t.Fatal("expected config path mismatch")
	}
}

func TestInspectRejectsBothConfigFormats(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"config/config.json": `{"port": 8080}`,
		"config/config.yaml": "port: 8080\n",
		"start.sh":           "#!/bin/sh\n",
		"stop.sh":            "#!/bin/sh\n",
	})
	if _, err := inspect(file, 1<<20, false); err == nil {
		t.Fatal("expected ambiguous config error")
	}
}

func TestInspectApplicationYmlPackage(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"config/application.yml": "server:\n  port: 18082\nspring:\n  application:\n    name: demo\n",
		"start.sh":               "#!/bin/sh\njava -jar app.jar\n",
		"stop.sh":                "#!/bin/sh\n",
	})
	result, err := inspect(file, 1<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Port != 18082 || result.ConfigType != "yaml" ||
		result.ConfigPath != "config/application.yml" {
		t.Fatalf("unexpected inspection: %+v", result)
	}
}

func TestInspectWrappedApplicationYamlPackage(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"app/config/application.yaml": "port: 18083\n",
		"app/start.sh":                "#!/bin/sh\n",
		"app/stop.sh":                 "#!/bin/sh\n",
		"app/app.jar":                 "binary",
	})
	result, err := inspect(file, 1<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Port != 18083 || result.RootPrefix != "app" ||
		result.ConfigPath != "app/config/application.yaml" {
		t.Fatalf("unexpected inspection: %+v", result)
	}
}

func TestInspectRejectsMixedConfigNames(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"config/config.yaml":     "port: 8080\n",
		"config/application.yml": "port: 8080\n",
		"start.sh":               "#!/bin/sh\n",
		"stop.sh":                "#!/bin/sh\n",
	})
	if _, err := inspect(file, 1<<20, false); err == nil {
		t.Fatal("expected ambiguous config error")
	}
}

func TestInspectRejectsStartScriptReferencingOtherConfig(t *testing.T) {
	file := buildArchive(t, map[string]string{
		"config/application.yml": "port: 8080\n",
		"start.sh":               "./app --config=config/config.yaml\n",
		"stop.sh":                "#!/bin/sh\n",
	})
	if _, err := inspect(file, 1<<20, false); err == nil {
		t.Fatal("expected config path mismatch")
	}
}
