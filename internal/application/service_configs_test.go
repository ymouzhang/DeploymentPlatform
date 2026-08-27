package application

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"path/filepath"
	"testing"

	"DP/internal/archive"
	"DP/internal/domain"
	"DP/internal/store"
)

func TestServiceConfigPreviewHistoryAndRollback(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dataDir, "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := archive.NewManager(dataDir, 10<<20, db)
	content := configArchive(t, `{"port":8080,"mode":"safe"}`)
	if _, err := manager.Upload(ctx, "demo", "demo.tar.gz", bytes.NewReader(content), nil); err != nil {
		t.Fatal(err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: store.InitialAdminID, Name: "test", IP: "192.0.2.20", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/demo", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceConfigService(db, manager, nil, nil)
	actor := domain.User{ID: store.InitialAdminID, Username: "admin", Role: domain.RoleAdmin}
	preview, err := service.Preview(ctx, env.ID, []byte(`{"port":8080,"mode":"safe"}`))
	if err != nil || preview.Changed {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	first, err := service.Update(ctx, env.ID, []byte(`{"port":8080,"mode":"safe"}`), actor)
	if err != nil || first.CurrentRevisionID == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	secondContent := `{"port":9090,"mode":"fast"}`
	second, err := service.Update(ctx, env.ID, []byte(secondContent), actor)
	if err != nil || second.Port != 9090 {
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
	if err != nil || current.Content != `{"port":8080,"mode":"safe"}` {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	revisions, _ = service.ListRevisions(ctx, env.ID)
	if len(revisions) != 3 || !revisions[0].Current {
		t.Fatalf("revisions after rollback=%+v", revisions)
	}
	if _, err := db.DeleteEnvironment(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	revisions, err = db.ListServiceConfigRevisions(ctx, env.ID)
	if err != nil || len(revisions) != 0 {
		t.Fatalf("revision cascade=%+v err=%v", revisions, err)
	}
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
