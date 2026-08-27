package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/domain"
)

func TestResourceTagsAreOwnerScopedAndFilterByIntersection(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner, err := db.CreateUser(ctx, domain.User{Username: "tag-owner", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateUser(ctx, domain.User{Username: "tag-other", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := db.CreateResourceTag(ctx, owner.ID, domain.ResourceTagInput{GroupName: "环境阶段", Value: "生产"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateResourceTag(ctx, owner.ID, domain.ResourceTagInput{GroupName: "环境阶段", Value: "生产"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate error=%v", err)
	}
	region, err := db.CreateResourceTag(ctx, owner.ID, domain.ResourceTagInput{GroupName: "区域", Value: "华东"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := db.CreateResourceTag(ctx, other.ID, domain.ResourceTagInput{GroupName: "区域", Value: "华北"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: owner.ID, Name: "production", IP: "192.0.2.10", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/x", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceEnvironmentTags(ctx, env.ID, []string{stage.ID, foreign.ID}); err == nil {
		t.Fatal("expected cross-owner tag rejection")
	}
	if err := db.ReplaceEnvironmentTags(ctx, env.ID, []string{stage.ID, region.ID}); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListEnvironmentsByOwner(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(FilterEnvironmentsByTagIDs(items, []string{stage.ID, region.ID})); got != 1 {
		t.Fatalf("intersection count=%d", got)
	}
	if got := len(FilterEnvironmentsByTagIDs(items, []string{stage.ID, foreign.ID})); got != 0 {
		t.Fatalf("cross intersection count=%d", got)
	}
}

func TestOperationKeepsTagSnapshotAcrossRenameAndDelete(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner, err := db.CreateUser(ctx, domain.User{Username: "snapshot-owner", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := db.CreateResourceTag(ctx, owner.ID, domain.ResourceTagInput{GroupName: "环境阶段", Value: "生产"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: owner.ID, Name: "production", IP: "192.0.2.20", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/x", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceEnvironmentTags(ctx, env.ID, []string{tag.ID}); err != nil {
		t.Fatal(err)
	}
	op := domain.Operation{ID: NewID(), EnvironmentID: env.ID, OwnerID: owner.ID, EnvironmentName: env.Name, Action: domain.ActionStart, Status: domain.OperationSucceeded, Stage: "completed", LogPath: "operations/test.jsonl", CreatedAt: time.Now().UTC()}
	if err := db.CreateOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateResourceTag(ctx, tag.ID, domain.ResourceTagInput{GroupName: "环境阶段", Value: "核心生产"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteResourceTag(ctx, tag.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetOperation(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Tags) != 1 || stored.Tags[0].Value != "生产" {
		t.Fatalf("snapshot=%+v", stored.Tags)
	}
	filtered, err := db.ListOperations(ctx, domain.OperationFilter{OwnerID: owner.ID, TagIDs: []string{tag.ID}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != op.ID {
		t.Fatalf("filtered=%+v", filtered)
	}
	visible, err := db.ListResourceTags(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 0 {
		t.Fatalf("deleted tag still visible: %+v", visible)
	}
}

func TestResourceTransferRecreatesEquivalentTargetTags(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := db.CreateUser(ctx, domain.User{Username: "source-tags", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	target, err := db.CreateUser(ctx, domain.User{Username: "target-tags", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := db.CreateResourceTag(ctx, source.ID, domain.ResourceTagInput{GroupName: "项目", Value: "迁移项目"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: source.ID, Name: "transfer", IP: "192.0.2.30", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/x", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceEnvironmentTags(ctx, env.ID, []string{tag.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.TransferResources(ctx, source.ID, target.ID, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	transferred, err := db.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if transferred.OwnerID != target.ID || len(transferred.Tags) != 1 || transferred.Tags[0].ID == tag.ID || transferred.Tags[0].Value != tag.Value {
		t.Fatalf("transferred=%+v", transferred)
	}
	targetTags, err := db.ListResourceTags(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targetTags) != 1 || targetTags[0].EnvironmentCount != 1 {
		t.Fatalf("target tags=%+v", targetTags)
	}
}

func TestDeleteUserRemovesUnusedTagCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner, err := db.CreateUser(ctx, domain.User{Username: "unused-tags", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateResourceTag(ctx, owner.ID, domain.ResourceTagInput{GroupName: "项目", Value: "无资源"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetUser(ctx, owner.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted user error=%v", err)
	}
}
