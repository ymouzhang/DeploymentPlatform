package application

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"testing"

	"DP/internal/archive"
	"DP/internal/domain"
	"DP/internal/security"
	"DP/internal/testutil"
)

func TestServiceConfigPreviewHistoryAndRollback(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db := testutil.OpenPostgres(t)
	manager := archive.NewManager(dataDir, 10<<20, db)
	content := configArchive(t, `{"port":8080,"api_port":8000,"mode":"safe"}`)
	if _, err := manager.Upload(ctx, "demo", "demo.tar.gz", bytes.NewReader(content), nil); err != nil {
		t.Fatal(err)
	}
	env, err := testutil.CreateServiceInstance(t, ctx, db, domain.ServiceInstance{OwnerID: domain.InitialAdminID, Name: "test", Host: domain.Host{IP: "192.0.2.20", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc"}, InstallDir: "/opt/demo", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceConfigService(db, manager, nil, nil)
	actor := domain.User{ID: domain.InitialAdminID, Username: "admin"}
	preview, err := service.Preview(ctx, env.ID, []byte(`{"port":8080,"api_port":8000,"mode":"safe"}`))
	if err != nil || preview.Changed {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	first, err := service.Update(ctx, env.ID, []byte(`{"port":8080,"api_port":8000,"mode":"safe"}`), actor)
	if err != nil || first.CurrentRevisionID == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	secondContent := `{"port":9090,"api_port":9000,"mode":"fast"}`
	second, err := service.Update(ctx, env.ID, []byte(secondContent), actor)
	if err != nil || second.Port != 9090 || second.APIPort == nil || *second.APIPort != 9000 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	revisions, err := service.ListRevisions(ctx, env.ID)
	if err != nil || len(revisions) != 2 || !revisions[0].Current {
		t.Fatalf("revisions=%+v err=%v", revisions, err)
	}
	rolled, err := service.Rollback(ctx, env.ID, revisions[1].ID, actor)
	if err != nil || rolled.Source != "rollback" || rolled.RestoredFromID != revisions[1].ID {
		t.Fatalf("rolled=%+v err=%v", rolled, err)
	}
	current, err := service.Get(ctx, env.ID)
	if err != nil || current.Content != `{"port":8080,"api_port":8000,"mode":"safe"}` || current.APIPort == nil || *current.APIPort != 8000 {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	revisions, _ = service.ListRevisions(ctx, env.ID)
	if len(revisions) != 3 || !revisions[0].Current {
		t.Fatalf("revisions after rollback=%+v", revisions)
	}
	if _, err := db.DeleteServiceInstance(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	revisions, err = db.ListServiceConfigRevisions(ctx, env.ID)
	if err != nil || len(revisions) != 0 {
		t.Fatalf("revision cascade=%+v err=%v", revisions, err)
	}
}

func TestPackageUpdateOnlyChangesInheritedServiceConfigs(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db := testutil.OpenPostgres(t)
	manager := archive.NewManager(dataDir, 10<<20, db)
	firstPackage, err := manager.Upload(ctx, "demo", "v1.tar.gz", bytes.NewReader(configArchive(t, `{"port":8080,"version":1}`)), nil)
	if err != nil {
		t.Fatal(err)
	}
	createServiceInstance := func(name, ip string) domain.ServiceInstance {
		t.Helper()
		env, err := testutil.CreateServiceInstance(t, ctx, db, domain.ServiceInstance{OwnerID: domain.InitialAdminID, Name: name,
			Host: domain.Host{IP: ip, SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc"}, InstallDir: "/opt/demo", ServiceType: "demo"})
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	independent := createServiceInstance("independent", "192.0.2.30")
	inherited := createServiceInstance("inherited", "192.0.2.31")
	installedWithoutSavedConfig := createServiceInstance("installed-without-saved-config", "192.0.2.32")
	service := NewServiceConfigService(db, manager, nil, nil)
	actor := domain.User{ID: domain.InitialAdminID, Username: "admin"}
	custom := `{"port":9090,"custom":true}`
	if _, err := service.Update(ctx, independent.ID, []byte(custom), actor); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkInstalled(ctx, independent.ID, firstPackage.SHA256, 9090); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkInstalled(ctx, installedWithoutSavedConfig.ID, firstPackage.SHA256, 8080); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Upload(ctx, "demo", "v2.tar.gz", bytes.NewReader(configArchive(t, `{"port":8081,"version":2}`)), nil); err != nil {
		t.Fatal(err)
	}
	gotIndependent, err := service.Get(ctx, independent.ID)
	if err != nil || gotIndependent.Inherited || gotIndependent.Content != custom ||
		!gotIndependent.PackageChanged || !gotIndependent.PackageUpdated ||
		gotIndependent.PackageContent != `{"port":8081,"version":2}` {
		t.Fatalf("independent config=%+v err=%v", gotIndependent, err)
	}
	gotInherited, err := service.Get(ctx, inherited.ID)
	if err != nil || !gotInherited.Inherited || gotInherited.Content != `{"port":8081,"version":2}` ||
		gotInherited.PackageChanged || gotInherited.PackageUpdated {
		t.Fatalf("inherited config=%+v err=%v", gotInherited, err)
	}
	gotInstalled, err := service.Get(ctx, installedWithoutSavedConfig.ID)
	if err != nil || !gotInstalled.Inherited || gotInstalled.Content != `{"port":8080,"version":1}` ||
		gotInstalled.PackageContent != `{"port":8081,"version":2}` || !gotInstalled.PackageChanged || !gotInstalled.PackageUpdated {
		t.Fatalf("installed config=%+v err=%v", gotInstalled, err)
	}
}

func TestServiceConfigRestoresRemoteWhenLocalCommitFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dataDir := t.TempDir()
	db := testutil.OpenPostgres(t)
	manager := archive.NewManager(dataDir, 10<<20, db)
	if _, err := manager.Upload(ctx, "demo", "demo.tar.gz", bytes.NewReader(configArchive(t, `{"port":8080}`)), nil); err != nil {
		t.Fatal(err)
	}
	cipher, err := security.NewPasswordCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	port := 8080
	env, err := testutil.CreateServiceInstance(t, ctx, db, domain.ServiceInstance{
		OwnerID: domain.InitialAdminID, Name: "test", Host: domain.Host{IP: "192.0.2.21", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: encrypted}, InstallDir: "/opt/demo", ServiceType: "demo", Installed: true, HealthPort: &port,
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := &cancelingConfigWriter{cancel: cancel}
	service := NewServiceConfigService(db, manager, cipher, writer)
	actor := domain.User{ID: domain.InitialAdminID, Username: "admin"}
	_, err = service.Update(ctx, env.ID, []byte(`{"port":9090}`), actor)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Update() error = %v, want context cancellation", err)
	}
	if len(writer.contents) != 2 || string(writer.contents[0]) != `{"port":9090}` || string(writer.contents[1]) != `{"port":8080}` {
		t.Fatalf("remote writes = %q", writer.contents)
	}
}

type cancelingConfigWriter struct {
	cancel   context.CancelFunc
	contents [][]byte
}

func (w *cancelingConfigWriter) WriteConfig(_ context.Context, _ domain.ServiceInstance, _ []byte, _ string, content []byte) (string, error) {
	w.contents = append(w.contents, bytes.Clone(content))
	if len(w.contents) == 1 {
		w.cancel()
	}
	return "", nil
}

func configArchive(t *testing.T, config string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	files := map[string]string{"config/config.json": config, "start.sh": "#!/bin/sh\n", "stop.sh": "#!/bin/sh\n"}
	for name, body := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
