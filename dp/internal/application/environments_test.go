package application

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/domain"
	"DP/internal/security"
	"DP/internal/store"
)

func TestEnvironmentExportV2AndImportTags(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := db.CreateUser(ctx, domain.User{Username: "export-source", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	target, err := db.CreateUser(ctx, domain.User{Username: "import-target", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, ownerID := range []string{source.ID, target.ID} {
		if err := db.UpsertPackage(ctx, domain.Package{OwnerID: ownerID, ServiceType: "demo", OriginalFilename: "demo.tar.gz", StoragePath: "packages/" + ownerID + "/demo/current.tar.gz", SHA256: "sha-" + ownerID, SizeBytes: 1, ConfigPort: 8080, UploadedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	cipher, err := security.NewPasswordCipher([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	tag, err := db.CreateResourceTag(ctx, source.ID, domain.ResourceTagInput{GroupName: "环境阶段", Value: "生产"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: source.ID, Name: "exported", IP: "192.0.2.40", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: encrypted, InstallDir: "/opt/demo", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceEnvironmentTags(ctx, env.ID, []string{tag.ID}); err != nil {
		t.Fatal(err)
	}
	service := NewEnvironmentService(db, cipher, nil)
	document, err := service.Export(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 2 || len(document.Environments) != 1 || len(document.Environments[0].Tags) != 1 {
		t.Fatalf("document=%+v", document)
	}
	result, err := service.Import(ctx, target.ID, document)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 {
		t.Fatalf("result=%+v", result)
	}
	imported, err := db.ListEnvironmentsByOwner(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported) != 1 || len(imported[0].Tags) != 1 || imported[0].Tags[0].Value != "生产" {
		t.Fatalf("imported=%+v", imported)
	}
}

func TestEnvironmentImportV1ClearsTagsOnOverwrite(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner, err := db.CreateUser(ctx, domain.User{Username: "v1-owner", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.UpsertPackage(ctx, domain.Package{OwnerID: owner.ID, ServiceType: "demo", OriginalFilename: "demo.tar.gz", StoragePath: "packages/demo", SHA256: "sha", SizeBytes: 1, ConfigPort: 8080, UploadedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	cipher, err := security.NewPasswordCipher([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	tag, err := db.CreateResourceTag(ctx, owner.ID, domain.ResourceTagInput{GroupName: "项目", Value: "旧标签"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: owner.ID, Name: "existing", IP: "192.0.2.50", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: encrypted, InstallDir: "/opt/demo", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceEnvironmentTags(ctx, env.ID, []string{tag.ID}); err != nil {
		t.Fatal(err)
	}
	service := NewEnvironmentService(db, cipher, nil)
	_, err = service.Import(ctx, owner.ID, ExportDocument{SchemaVersion: 1, Environments: []ExportEnvironment{{Name: "overwritten", IP: env.IP, SSHUser: env.SSHUser, SSHPort: env.SSHPort, SSHPasswordEncrypted: encrypted, InstallDir: env.InstallDir, ServiceType: env.ServiceType}}})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := db.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Tags) != 0 {
		t.Fatalf("v1 import retained tags: %+v", updated.Tags)
	}
}
