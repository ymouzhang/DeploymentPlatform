package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/domain"
	"DP/internal/realtime"
	"DP/internal/testutil"
)

func TestCommunicationLifecycleAndIsolation(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenPostgres(t)
	admin, err := db.InitializeAdmin(ctx, domain.InitialAdminID, "admin-one", "hash")
	if err != nil {
		t.Fatal(err)
	}
	secondAdmin, err := db.CreateUser(ctx, testutil.User(t, "admin-two", access.RolePlatformAdmin, true))
	if err != nil {
		t.Fatal(err)
	}
	lateAdmin, err := db.CreateUser(ctx, testutil.User(t, "admin-late", access.RolePlatformAdmin, false))
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(ctx, testutil.User(t, "user-one", access.RoleOperator, true))
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateUser(ctx, testutil.User(t, "user-two", access.RoleOperator, true))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.UpsertPackage(ctx, domain.Package{OwnerID: user.ID, ServiceType: "demo", OriginalFilename: "demo.tar.gz", StoragePath: "demo", SHA256: "sha", SizeBytes: 1, ConfigPort: 8080, UploadedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	environment, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: user.ID, Name: "production", IP: "192.0.2.10", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/demo", ServiceType: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewCommunicationService(db, nil)
	thread, err := service.Create(ctx, admin, domain.CommunicationCreateInput{TargetUserID: user.ID, Title: "检查部署",
		Content: "请确认部署状态", Resources: []domain.CommunicationResourceInput{{ResourceType: "package", ResourceKey: "demo"}, {ResourceType: "environment", ResourceID: environment.ID}, {ResourceType: "service", ResourceID: environment.ID}}})
	if err != nil || len(thread.Resources) != 3 || len(thread.Messages) != 1 {
		t.Fatalf("thread=%+v err=%v", thread, err)
	}
	if _, err := service.Get(ctx, other, thread.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("other user must not see communication: %v", err)
	}
	summary, _ := service.Summary(ctx, user)
	if summary.Unread != 1 {
		t.Fatalf("user unread=%d", summary.Unread)
	}
	if _, err := service.MarkRead(ctx, user, thread.ID); err != nil {
		t.Fatal(err)
	}
	summary, _ = service.Summary(ctx, user)
	if summary.Unread != 0 {
		t.Fatalf("user unread after read=%d", summary.Unread)
	}
	receipt, err := service.Send(ctx, user, thread.ID, "已经确认")
	if err != nil || receipt.Type != domain.CommunicationUserReceipt {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	adminView, err := service.Get(ctx, secondAdmin, thread.ID)
	if err != nil || len(adminView.Messages[len(adminView.Messages)-1].Recipients) != 2 {
		t.Fatalf("admin view=%+v err=%v", adminView, err)
	}
	for _, actor := range []domain.User{admin, secondAdmin} {
		summary, _ = service.Summary(ctx, actor)
		if summary.Unread != 1 {
			t.Fatalf("admin %s unread=%d", actor.Username, summary.Unread)
		}
	}
	lateAdmin, err = db.UpdateUserEnabled(ctx, lateAdmin.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	summary, _ = service.Summary(ctx, lateAdmin)
	if summary.Unread != 0 {
		t.Fatalf("newly enabled admin inherited historical unread=%d", summary.Unread)
	}
	closed, err := service.ChangeState(ctx, secondAdmin, thread.ID, false, "处理完毕")
	if err != nil || closed.Status != domain.CommunicationClosed {
		t.Fatalf("closed=%+v err=%v", closed, err)
	}
	if _, err := service.Send(ctx, user, thread.ID, "关闭后回复"); communicationErrorCode(err) != "COMMUNICATION_CLOSED" {
		t.Fatalf("closed reply error=%v", err)
	}
	reopened, err := service.ChangeState(ctx, admin, thread.ID, true, "需要补充信息")
	if err != nil || reopened.Status != domain.CommunicationOpen || reopened.ReopenCount != 1 {
		t.Fatalf("reopened=%+v err=%v", reopened, err)
	}
	if last := reopened.Messages[len(reopened.Messages)-1]; last.Type != domain.CommunicationSystemReopen {
		t.Fatalf("last message=%+v", last)
	}
	if _, err := service.Send(ctx, user, thread.ID, "补充完成"); err != nil {
		t.Fatal(err)
	}
	summary, _ = service.Summary(ctx, lateAdmin)
	if summary.Unread != 1 {
		t.Fatalf("enabled admin did not receive new receipt, unread=%d", summary.Unread)
	}
	if _, err := db.DeleteEnvironment(ctx, environment.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeletePackageByOwner(ctx, user.ID, "demo"); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := service.Get(ctx, admin, thread.ID)
	if err != nil || len(afterDelete.Resources) != 3 {
		t.Fatalf("resource snapshots missing after deletion: thread=%+v err=%v", afterDelete, err)
	}
	for _, resource := range afterDelete.Resources {
		if resource.Available {
			t.Fatalf("deleted resource still available: %+v", resource)
		}
	}
}

func TestCommunicationRejectsOrdinaryCreationAndCrossOwnerResource(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenPostgres(t)
	admin, _ := db.InitializeAdmin(ctx, domain.InitialAdminID, "admin-test", "hash")
	first, _ := db.CreateUser(ctx, testutil.User(t, "first-user", access.RoleOperator, true))
	second, _ := db.CreateUser(ctx, testutil.User(t, "second-user", access.RoleOperator, true))
	disabled, _ := db.CreateUser(ctx, testutil.User(t, "disabled-user", access.RoleOperator, false))
	environment, _ := db.CreateEnvironment(ctx, domain.Environment{OwnerID: second.ID, Name: "other", IP: "192.0.2.20", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/x", ServiceType: "demo"})
	service := NewCommunicationService(db, nil)
	input := domain.CommunicationCreateInput{TargetUserID: first.ID, Title: "标题", Content: "内容"}
	if _, err := service.Create(ctx, first, input); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ordinary creation error=%v", err)
	}
	input.Resources = []domain.CommunicationResourceInput{{ResourceType: "environment", ResourceID: environment.ID}}
	if _, err := service.Create(ctx, admin, input); communicationErrorCode(err) != "COMMUNICATION_RESOURCE_INVALID" {
		t.Fatalf("cross-owner resource error=%v", err)
	}
	input.TargetUserID, input.Resources = disabled.ID, nil
	if _, err := service.Create(ctx, admin, input); communicationErrorCode(err) != "COMMUNICATION_TARGET_DISABLED" {
		t.Fatalf("disabled target error=%v", err)
	}
	input.TargetUserID = domain.NewID()
	if _, err := service.Create(ctx, admin, input); communicationErrorCode(err) != "COMMUNICATION_TARGET_DISABLED" {
		t.Fatalf("missing target error=%v", err)
	}
}

func TestCommunicationRealtimeEventsUseRecipientScope(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenPostgres(t)
	admin, _ := db.InitializeAdmin(ctx, domain.InitialAdminID, "event-admin", "hash")
	secondAdmin, _ := db.CreateUser(ctx, testutil.User(t, "event-admin-two", access.RolePlatformAdmin, true))
	disabledAdmin, _ := db.CreateUser(ctx, testutil.User(t, "event-admin-disabled", access.RolePlatformAdmin, false))
	target, _ := db.CreateUser(ctx, testutil.User(t, "event-target", access.RoleOperator, true))
	other, _ := db.CreateUser(ctx, testutil.User(t, "event-other", access.RoleOperator, true))
	hub := realtime.NewHub(16)
	adminEvents := hub.Subscribe(admin.ID)
	defer adminEvents.Close()
	secondAdminEvents := hub.Subscribe(secondAdmin.ID)
	defer secondAdminEvents.Close()
	disabledAdminEvents := hub.Subscribe(disabledAdmin.ID)
	defer disabledAdminEvents.Close()
	targetEvents := hub.Subscribe(target.ID)
	defer targetEvents.Close()
	otherEvents := hub.Subscribe(other.ID)
	defer otherEvents.Close()
	service := NewCommunicationService(db, hub)

	thread, err := service.Create(ctx, admin, domain.CommunicationCreateInput{TargetUserID: target.ID, Title: "实时事项", Content: "请确认"})
	if err != nil {
		t.Fatal(err)
	}
	for name, subscription := range map[string]*realtime.Subscription{"admin": adminEvents, "second admin": secondAdminEvents, "target": targetEvents} {
		event := receiveRealtimeEvent(t, subscription)
		if event.Change != realtime.ChangeCreated || event.ResourceID != thread.ID {
			t.Fatalf("%s create event=%+v", name, event)
		}
	}
	assertNoRealtimeEvent(t, otherEvents)
	assertNoRealtimeEvent(t, disabledAdminEvents)

	if _, err := service.Send(ctx, target, thread.ID, "已经收到"); err != nil {
		t.Fatal(err)
	}
	for name, subscription := range map[string]*realtime.Subscription{"admin": adminEvents, "second admin": secondAdminEvents, "target": targetEvents} {
		if event := receiveRealtimeEvent(t, subscription); event.Change != realtime.ChangeMessage {
			t.Fatalf("%s receipt event=%+v", name, event)
		}
	}
	assertNoRealtimeEvent(t, otherEvents)
	assertNoRealtimeEvent(t, disabledAdminEvents)

	if _, err := service.Send(ctx, secondAdmin, thread.ID, "管理员补充"); err != nil {
		t.Fatal(err)
	}
	for name, subscription := range map[string]*realtime.Subscription{"admin": adminEvents, "second admin": secondAdminEvents, "target": targetEvents} {
		if event := receiveRealtimeEvent(t, subscription); event.Change != realtime.ChangeMessage {
			t.Fatalf("%s admin message event=%+v", name, event)
		}
	}
	assertNoRealtimeEvent(t, otherEvents)
	assertNoRealtimeEvent(t, disabledAdminEvents)

	if _, err := service.MarkRead(ctx, target, thread.ID); err != nil {
		t.Fatal(err)
	}
	for name, subscription := range map[string]*realtime.Subscription{"admin": adminEvents, "second admin": secondAdminEvents, "target": targetEvents} {
		if event := receiveRealtimeEvent(t, subscription); event.Change != realtime.ChangeRead {
			t.Fatalf("%s read event=%+v", name, event)
		}
	}
	assertNoRealtimeEvent(t, otherEvents)
	assertNoRealtimeEvent(t, disabledAdminEvents)

	if _, err := service.ChangeState(ctx, admin, thread.ID, false, "已处理"); err != nil {
		t.Fatal(err)
	}
	for name, subscription := range map[string]*realtime.Subscription{"admin": adminEvents, "second admin": secondAdminEvents, "target": targetEvents} {
		if event := receiveRealtimeEvent(t, subscription); event.Change != realtime.ChangeClosed {
			t.Fatalf("%s close event=%+v", name, event)
		}
	}
	assertNoRealtimeEvent(t, otherEvents)
	assertNoRealtimeEvent(t, disabledAdminEvents)

	if _, err := service.ChangeState(ctx, secondAdmin, thread.ID, true, "继续处理"); err != nil {
		t.Fatal(err)
	}
	for name, subscription := range map[string]*realtime.Subscription{"admin": adminEvents, "second admin": secondAdminEvents, "target": targetEvents} {
		if event := receiveRealtimeEvent(t, subscription); event.Change != realtime.ChangeReopened {
			t.Fatalf("%s reopen event=%+v", name, event)
		}
	}
	assertNoRealtimeEvent(t, otherEvents)
	assertNoRealtimeEvent(t, disabledAdminEvents)
}

func receiveRealtimeEvent(t *testing.T, subscription *realtime.Subscription) realtime.Event {
	t.Helper()
	select {
	case event := <-subscription.Events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime event")
		return realtime.Event{}
	}
}

func assertNoRealtimeEvent(t *testing.T, subscription *realtime.Subscription) {
	t.Helper()
	select {
	case event := <-subscription.Events:
		t.Fatalf("unexpected realtime event: %+v", event)
	default:
	}
}

func communicationErrorCode(err error) string {
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return ""
}
