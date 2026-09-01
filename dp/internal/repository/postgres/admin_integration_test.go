package postgres

import (
	"context"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
	"DP/internal/testdb"
)

func TestAdminRepositoryIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, testdb.PostgresURL(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	admin, err := db.InitializeAdmin(ctx, initialAdminID, "admin-repository", "hash")
	if err != nil {
		t.Fatalf("initialize administrator: %v", err)
	}
	source, err := db.CreateUser(ctx, domain.User{
		Username: "transfer-source", PasswordHash: "hash", Enabled: true, CreatedBy: admin.ID,
		Roles: []access.RoleRef{{ID: "00000000-0000-4000-8000-000000000103", Key: access.RoleOperator}},
	})
	if err != nil {
		t.Fatalf("create source user: %v", err)
	}
	target, err := db.CreateUser(ctx, domain.User{
		Username: "transfer-target", PasswordHash: "hash", Enabled: true, CreatedBy: admin.ID,
		Roles: []access.RoleRef{{ID: "00000000-0000-4000-8000-000000000103", Key: access.RoleOperator}},
	})
	if err != nil {
		t.Fatalf("create target user: %v", err)
	}

	tag, err := db.CreateResourceTag(ctx, source.ID, domain.ResourceTagInput{
		GroupName: "region", Value: "north",
	})
	if err != nil {
		t.Fatalf("create source tag: %v", err)
	}
	environment, err := db.CreateEnvironmentWithTags(ctx, domain.Environment{
		OwnerID: source.ID, Name: "source-environment", IP: "192.0.2.21", SSHUser: "root",
		SSHPort: 22, SSHPasswordEnc: "ciphertext", InstallDir: "/opt/service", ServiceType: "demo",
	}, []string{tag.ID})
	if err != nil {
		t.Fatalf("create source environment: %v", err)
	}
	model, _, err := db.CreateModelUpload(ctx, domain.Model{
		OwnerID: source.ID, EnvironmentID: environment.ID, EnvironmentName: environment.Name,
		EnvironmentIP: environment.IP, Name: "source-model", Source: "offline",
		TargetDir: "/models/source", OriginalFilename: "model.tar.gz", SizeBytes: 100,
		Status: domain.ModelUploading, CreatedBy: source.ID, CreatedByUsername: source.Username,
	}, domain.ModelUpload{RemotePath: "/models/.upload", TotalBytes: 100, Status: "uploading",
		ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatalf("create source model: %v", err)
	}
	const sessionHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := db.CreateSession(ctx, sessionHash, source.ID, "192.0.2.1", "integration", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create source session: %v", err)
	}

	result, err := db.TransferResources(ctx, source.ID, target.ID)
	if err != nil {
		t.Fatalf("transfer resources: %v", err)
	}
	if result.Environments != 1 || result.Models != 1 {
		t.Fatalf("unexpected transfer result: %+v", result)
	}
	transferredEnvironment, err := db.GetEnvironment(ctx, environment.ID)
	if err != nil || transferredEnvironment.OwnerID != target.ID || len(transferredEnvironment.Tags) != 1 {
		t.Fatalf("environment was not transferred with tags: environment=%+v err=%v", transferredEnvironment, err)
	}
	targetTags, err := db.ListResourceTags(ctx, target.ID)
	if err != nil || len(targetTags) != 1 || targetTags[0].ID != transferredEnvironment.Tags[0].ID {
		t.Fatalf("environment tag ownership was not transferred: tags=%+v err=%v", targetTags, err)
	}
	transferredModel, err := db.GetModel(ctx, model.ID)
	if err != nil || transferredModel.OwnerID != target.ID {
		t.Fatalf("model was not transferred: model=%+v err=%v", transferredModel, err)
	}
	if _, _, err := db.UserForSession(ctx, sessionHash, time.Now().UTC()); err == nil {
		t.Fatal("source sessions were not revoked")
	}

	metrics, err := db.DashboardMetrics(ctx, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC().Add(-30*24*time.Hour))
	if err != nil || metrics.Users != 3 || metrics.Environments != 1 {
		t.Fatalf("unexpected dashboard metrics: metrics=%+v err=%v", metrics, err)
	}
	staleUsers, err := db.ListStaleUsers(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || len(staleUsers) != 2 {
		t.Fatalf("unexpected stale users: users=%+v err=%v", staleUsers, err)
	}
}
