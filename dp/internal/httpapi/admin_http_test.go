package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"DP/internal/access"
	"DP/internal/application"
	"DP/internal/domain"
	"DP/internal/testutil"
)

func TestPermissionMiddlewareRejectsMissingBusinessGrants(t *testing.T) {
	api := &API{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	user := domain.User{ID: "user-1", Permissions: access.Grants{}}
	tests := []struct {
		path       string
		permission access.Permission
	}{
		{"/api/v1/admin/dashboard", access.DashboardRead},
		{"/api/v1/operations", access.OperationRead},
		{"/api/v1/notifications", access.NotificationRead},
		{"/api/v1/users/user-2", access.AccountRead},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: user}))
			recorder := httptest.NewRecorder()
			handler := api.requirePermission(test.permission, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAdminDashboardIncludesCurrentAdministratorsUnreadCommunications(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenPostgres(t)
	admin, err := db.InitializeAdmin(ctx, domain.InitialAdminID, "dashboard-admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(ctx, testutil.User(t, "dashboard-user", access.RoleOperator, true))
	if err != nil {
		t.Fatal(err)
	}
	communications := application.NewCommunicationService(db, nil)
	thread, err := communications.Create(ctx, admin, domain.CommunicationCreateInput{TargetUserID: user.ID, Title: "请确认部署", Content: "请反馈服务状态"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := communications.Send(ctx, user, thread.ID, "服务已启动"); err != nil {
		t.Fatal(err)
	}

	api := &API{store: db, communications: communications, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard", nil)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: admin}))
	recorder := httptest.NewRecorder()
	api.adminDashboard(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data domain.AdminDashboard `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Metrics.UnreadCommunications != 1 {
		t.Fatalf("unread communications=%d", response.Data.Metrics.UnreadCommunications)
	}
	if len(response.Data.Communications) != 1 || response.Data.Communications[0].ID != thread.ID {
		t.Fatalf("communications=%+v", response.Data.Communications)
	}
}
