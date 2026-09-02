package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/application"
	"DP/internal/domain"
	"DP/internal/testutil"
)

func TestForcedPasswordChangeMiddlewareBlocksBusinessAPI(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenPostgres(t)
	auth := application.NewAuthService(db, time.Hour)
	if err := auth.InitializeAdmin(ctx, "admin", "initial-password"); err != nil {
		t.Fatal(err)
	}
	admin, token, _, err := auth.Login(ctx, "admin", "initial-password")
	if err != nil {
		t.Fatal(err)
	}
	if !admin.MustChangePassword {
		t.Fatal("initialized administrator must require password change")
	}
	api := &API{auth: auth, log: slog.Default()}
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	handler := api.authMiddleware(next)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/packages", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if called || response.Code != http.StatusForbidden {
		t.Fatalf("business API called=%v status=%d", called, response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !called {
		t.Fatal("auth/me should be allowed")
	}
}

func TestAuthenticationMiddlewareRejectsMissingSession(t *testing.T) {
	api := &API{log: slog.Default()}
	called := false
	handler := api.authMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/packages", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if called || response.Code != http.StatusUnauthorized {
		t.Fatalf("business API called=%v status=%d", called, response.Code)
	}
}

func TestServiceInstanceAuthorizationRejectsOtherOwners(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenPostgres(t)
	owner, err := db.CreateUser(ctx, testutil.User(t, "owner", access.RoleOperator, true))
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateUser(ctx, testutil.User(t, "other", access.RoleOperator, true))
	if err != nil {
		t.Fatal(err)
	}
	env, err := testutil.CreateServiceInstance(t, ctx, db, domain.ServiceInstance{OwnerID: owner.ID, Name: "env", Host: domain.Host{IP: "127.0.0.1", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc"}, InstallDir: "/opt/x", ServiceType: "service"})
	if err != nil {
		t.Fatal(err)
	}
	api := &API{store: db}
	other.Permissions = access.Grants{access.ServiceRead: access.ScopeOwn}
	request := httptest.NewRequest("GET", "/api/v1/services/"+env.ID+"/config", nil)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: other}))
	if _, err := api.authorizeServiceInstance(request, env.ID, access.ServiceRead); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-owner error=%v", err)
	} else if status, _, _, _ := classifyHTTPError(err); status != http.StatusForbidden {
		t.Fatalf("cross-owner status=%d", status)
	}
	if _, err := api.authorizeServiceInstance(request, domain.NewID(), access.ServiceRead); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing serviceInstance error=%v", err)
	} else if status, _, _, _ := classifyHTTPError(err); status != http.StatusNotFound {
		t.Fatalf("missing serviceInstance status=%d", status)
	}
	admin := other
	admin.Permissions = access.Grants{access.ServiceRead: access.ScopeAll}
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: admin}))
	if _, err := api.authorizeServiceInstance(request, env.ID, access.ServiceRead); err != nil {
		t.Fatalf("admin access: %v", err)
	}
}

func TestPackageOwnerScopeAllowsOrdinaryUserOwnID(t *testing.T) {
	user := domain.User{ID: "user-1", Permissions: access.Grants{access.PackageRead: access.ScopeOwn}}
	api := &API{}

	ownRequest := httptest.NewRequest("GET", "/api/v1/service-types/demo/package?owner_id=user-1", nil)
	ownRequest = ownRequest.WithContext(context.WithValue(ownRequest.Context(), authContextKey{}, authenticated{User: user}))
	ownerID, err := api.packageOwnerScope(ownRequest, access.PackageRead)
	if err != nil || ownerID != user.ID {
		t.Fatalf("own owner scope=%q err=%v", ownerID, err)
	}

	otherRequest := httptest.NewRequest("GET", "/api/v1/service-types/demo/package?owner_id=user-2", nil)
	otherRequest = otherRequest.WithContext(context.WithValue(otherRequest.Context(), authContextKey{}, authenticated{User: user}))
	if _, err := api.packageOwnerScope(otherRequest, access.PackageRead); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-owner error=%v", err)
	}
}

func TestVisibleTagIDsRejectsCrossOwnerForOrdinaryUser(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenPostgres(t)
	owner, err := db.CreateUser(ctx, testutil.User(t, "tag-a", access.RoleOperator, true))
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateUser(ctx, testutil.User(t, "tag-b", access.RoleOperator, true))
	if err != nil {
		t.Fatal(err)
	}
	tag, err := db.CreateResourceTag(ctx, owner.ID, domain.ResourceTagInput{GroupName: "项目", Value: "A"})
	if err != nil {
		t.Fatal(err)
	}
	api := &API{store: db}
	other.Permissions = access.Grants{access.HostRead: access.ScopeOwn}
	owner.Permissions = access.Grants{access.HostRead: access.ScopeOwn}
	request := httptest.NewRequest("GET", "/api/v1/services?tag_id="+tag.ID, nil)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: other}))
	if _, err := api.visibleTagIDs(request, other.ID, access.HostRead); err == nil {
		t.Fatal("expected cross-owner tag rejection")
	}
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authenticated{User: owner}))
	ids, err := api.visibleTagIDs(request, owner.ID, access.HostRead)
	if err != nil || len(ids) != 1 || ids[0] != tag.ID {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}
