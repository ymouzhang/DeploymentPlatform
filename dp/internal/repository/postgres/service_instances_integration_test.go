package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"DP/internal/domain"
	"DP/internal/testdb"
)

func TestServiceInstanceRepositoryIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, testdb.PostgresURL(t))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	tag, err := db.CreateResourceTag(ctx, domain.InitialAdminID, domain.ResourceTagInput{
		GroupName: "region", Value: "cn-north",
	})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	host, err := db.CreateHost(ctx, domain.Host{OwnerID: domain.InitialAdminID, Name: "integration-host", IP: "192.0.2.50", SSHUser: "deploy", SSHPort: 22, SSHPasswordEnc: "encrypted"})
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	instance, err := db.CreateServiceInstanceWithTags(ctx, domain.ServiceInstance{
		OwnerID: domain.InitialAdminID, HostID: host.ID, Name: "integration-instance", InstallDir: "/opt/dp",
		ServiceType: "integration", Note: "test",
	}, []string{tag.ID})
	if err != nil {
		t.Fatalf("create serviceInstance: %v", err)
	}
	if instance.Host.IP != "192.0.2.50" || len(instance.Tags) != 1 || instance.Tags[0].ID != tag.ID {
		t.Fatalf("unexpected serviceInstance: %+v", instance)
	}
	_, err = db.CreateServiceInstance(ctx, domain.ServiceInstance{
		OwnerID: domain.InitialAdminID, HostID: instance.HostID, Name: "duplicate", InstallDir: instance.InstallDir,
		ServiceType: instance.ServiceType,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected serviceInstance conflict, got %v", err)
	}
	if err := db.RecordHostValidation(ctx, instance.HostID, "SHA256:fingerprint", "amd64"); err != nil {
		t.Fatalf("record validation: %v", err)
	}
	if err := db.MarkInstalled(ctx, instance.ID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 18080); err != nil {
		t.Fatalf("mark installed: %v", err)
	}
	loaded, err := db.GetServiceInstance(ctx, instance.ID)
	if err != nil || !loaded.Installed || loaded.HealthPort == nil || *loaded.HealthPort != 18080 || loaded.Host.Arch != "amd64" {
		t.Fatalf("unexpected installed serviceInstance: %+v err=%v", loaded, err)
	}

	config, err := db.UpsertServiceConfig(ctx, domain.ServiceConfig{
		ServiceInstanceID: instance.ID, Content: "port: 18080\n", Format: "yaml", Path: "config.yaml", Port: 18080,
	})
	if err != nil {
		t.Fatalf("upsert service config: %v", err)
	}
	if config.Port != 18080 || config.Content != "port: 18080\n" {
		t.Fatalf("unexpected service config: %+v", config)
	}
	revision := domain.ServiceConfigRevision{
		ID: domain.NewID(), ServiceInstanceID: instance.ID, Content: "port: 18081\n", Format: "yaml",
		Path: "config.yaml", Port: 18081, Source: "manual", CreatedBy: domain.InitialAdminID,
		CreatedByName: "integration-admin", CreatedAt: time.Now().UTC(),
	}
	_, err = db.SaveServiceConfigRevision(ctx, domain.ServiceConfig{
		ServiceInstanceID: instance.ID, Content: revision.Content, Format: revision.Format,
		Path: revision.Path, Port: revision.Port,
	}, revision, true)
	if err != nil {
		t.Fatalf("save service config revision: %v", err)
	}
	revisions, err := db.ListServiceConfigRevisions(ctx, instance.ID)
	if err != nil || len(revisions) != 1 || revisions[0].Content != "" || !revisions[0].Current {
		t.Fatalf("unexpected config revisions: %+v err=%v", revisions, err)
	}
	loadedRevision, err := db.GetServiceConfigRevision(ctx, instance.ID, revision.ID)
	if err != nil || loadedRevision.Content != revision.Content {
		t.Fatalf("unexpected config revision: %+v err=%v", loadedRevision, err)
	}
	updatedInstance, err := db.GetServiceInstance(ctx, instance.ID)
	if err != nil || updatedInstance.HealthPort == nil || *updatedInstance.HealthPort != 18081 {
		t.Fatalf("unexpected health port: instance=%+v err=%v", updatedInstance, err)
	}
	operation := domain.Operation{
		ID: domain.NewID(), ServiceInstanceID: instance.ID, RequestID: domain.NewID(),
		ActorUserID: domain.InitialAdminID, ActorUsername: "integration-admin",
		OwnerID: domain.InitialAdminID, OwnerUsername: "integration-admin",
		ServiceInstanceName: instance.Name, HostIP: instance.Host.IP, ServiceType: instance.ServiceType,
		Action: domain.ActionStart, Status: domain.OperationQueued, Stage: "queued",
		LogPath: "operations/integration.jsonl", CreatedAt: time.Now().UTC(),
	}
	if err := db.CreateOperation(ctx, operation); err != nil {
		t.Fatalf("create operation: %v", err)
	}
	startedAt, finishedAt := time.Now().UTC(), time.Now().UTC()
	operation.Status = domain.OperationSucceeded
	operation.Stage = "done"
	operation.StartedAt = &startedAt
	operation.FinishedAt = &finishedAt
	if err := db.UpdateOperation(ctx, operation); err != nil {
		t.Fatalf("update operation: %v", err)
	}
	loadedOperation, err := db.GetOperation(ctx, operation.ID)
	if err != nil || loadedOperation.Status != domain.OperationSucceeded || len(loadedOperation.Tags) != 1 {
		t.Fatalf("unexpected operation: %+v err=%v", loadedOperation, err)
	}
	lastAction, err := db.LastSuccessfulAction(ctx, instance.ID)
	if err != nil || lastAction != domain.ActionStart {
		t.Fatalf("unexpected last action: %s err=%v", lastAction, err)
	}
	operations, err := db.ListOperations(ctx, domain.OperationFilter{Keyword: "integration-instance", Limit: 10})
	if err != nil || len(operations) != 1 {
		t.Fatalf("unexpected operation list: %+v err=%v", operations, err)
	}
	paths, err := db.DeleteTerminalOperationsBefore(ctx, time.Now().UTC().Add(time.Minute), 10)
	if err != nil || len(paths) != 1 || paths[0] != operation.LogPath {
		t.Fatalf("unexpected deleted operation paths: %+v err=%v", paths, err)
	}
	model, upload, err := db.CreateModelUpload(ctx, domain.Model{
		OwnerID: domain.InitialAdminID, HostID: instance.HostID, HostName: instance.Host.Name,
		HostIP: instance.Host.IP, Name: "integration-model", Source: "offline",
		TargetDir: "/opt/models/integration", OriginalFilename: "model.tar.gz", SizeBytes: 4096,
		Status: domain.ModelUploading, CreatedBy: domain.InitialAdminID,
		CreatedByUsername: "integration-admin",
	}, domain.ModelUpload{
		RemotePath: "/tmp/model.tar.gz", TotalBytes: 4096, Status: "uploading",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create model upload: %v", err)
	}
	if err := db.SetModelUploadOffset(ctx, upload.ID, 4096); err != nil {
		t.Fatalf("set model upload offset: %v", err)
	}
	loadedUpload, err := db.GetModelUploadByModel(ctx, model.ID)
	if err != nil || loadedUpload.Offset != 4096 {
		t.Fatalf("unexpected model upload: %+v err=%v", loadedUpload, err)
	}
	if err := db.CompleteModelUpload(ctx, upload.ID); err != nil {
		t.Fatalf("complete model upload: %v", err)
	}
	modelTask, err := db.CreateModelTask(ctx, domain.ModelTask{
		ModelID: model.ID, OwnerID: domain.InitialAdminID, ActorUserID: domain.InitialAdminID,
		ActorUsername: "integration-admin", Action: domain.ModelTaskDeploy,
		Status: domain.OperationQueued, Stage: "queued", LogPath: "model-tasks/integration.jsonl",
	})
	if err != nil {
		t.Fatalf("create model task: %v", err)
	}
	modelTask.Status = domain.OperationSucceeded
	modelTask.Progress = 100
	modelTask.StartedAt = &startedAt
	modelTask.FinishedAt = &finishedAt
	if err := db.UpdateModelTask(ctx, modelTask); err != nil {
		t.Fatalf("update model task: %v", err)
	}
	if err := db.MarkModelReady(ctx, model.ID,
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", 8192, 12); err != nil {
		t.Fatalf("mark model ready: %v", err)
	}
	models, err := db.ListModels(ctx, domain.InitialAdminID)
	if err != nil || len(models) != 1 || models[0].LatestTask == nil || models[0].Status != domain.ModelReady {
		t.Fatalf("unexpected models: %+v err=%v", models, err)
	}
	hasModels, err := db.HostHasModels(ctx, instance.HostID)
	if err != nil || !hasModels {
		t.Fatalf("serviceInstance model check failed: has=%v err=%v", hasModels, err)
	}
	if err := db.MarkModelDeleted(ctx, model.ID); err != nil {
		t.Fatalf("mark model deleted: %v", err)
	}
	if _, err := db.DeleteServiceInstance(ctx, instance.ID); err != nil {
		t.Fatalf("delete serviceInstance: %v", err)
	}
	if err := db.DeleteResourceTag(ctx, tag.ID); err != nil {
		t.Fatalf("delete resource tag: %v", err)
	}
}
