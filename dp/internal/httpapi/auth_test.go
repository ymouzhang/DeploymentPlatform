package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/access"
	"DP/internal/application"
	"DP/internal/domain"
	"DP/internal/store"
)

func TestForcedPasswordChangeMiddlewareBlocksBusinessAPI(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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

func TestEnvironmentAuthorizationHidesOtherOwners(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner, err := db.CreateUser(ctx, domain.User{Username: "owner", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateUser(ctx, domain.User{Username: "other", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	env, err := db.CreateEnvironment(ctx, domain.Environment{OwnerID: owner.ID, Name: "env", IP: "127.0.0.1", SSHUser: "u", SSHPort: 22, SSHPasswordEnc: "enc", InstallDir: "/opt/x", ServiceType: "service"})
	if err != nil {
		t.Fatal(err)
	}
	api := &API{store: db}
	other.Permissions = access.Grants{access.ServiceRead: access.ScopeOwn}
	request := httptest.NewRequest("GET", "/api/v1/services/"+env.ID+"/config", nil)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, other))
	if _, err := api.authorizeEnvironment(request, env.ID, access.ServiceRead); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-owner error=%v", err)
	}
	admin := other
	admin.Permissions = access.Grants{access.ServiceRead: access.ScopeAll}
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, admin))
	if _, err := api.authorizeEnvironment(request, env.ID, access.ServiceRead); err != nil {
		t.Fatalf("admin access: %v", err)
	}
}

func TestPackageOwnerScopeAllowsOrdinaryUserOwnID(t *testing.T) {
	user := domain.User{ID: "user-1", Permissions: access.Grants{access.PackageRead: access.ScopeOwn}}
	api := &API{}

	ownRequest := httptest.NewRequest("GET", "/api/v1/service-types/demo/package?owner_id=user-1", nil)
	ownRequest = ownRequest.WithContext(context.WithValue(ownRequest.Context(), authContextKey{}, user))
	ownerID, err := api.packageOwnerScope(ownRequest, access.PackageRead)
	if err != nil || ownerID != user.ID {
		t.Fatalf("own owner scope=%q err=%v", ownerID, err)
	}

	otherRequest := httptest.NewRequest("GET", "/api/v1/service-types/demo/package?owner_id=user-2", nil)
	otherRequest = otherRequest.WithContext(context.WithValue(otherRequest.Context(), authContextKey{}, user))
	if _, err := api.packageOwnerScope(otherRequest, access.PackageRead); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-owner error=%v", err)
	}
}

func TestVisibleTagIDsRejectsCrossOwnerForOrdinaryUser(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner, err := db.CreateUser(ctx, domain.User{Username: "tag-a", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateUser(ctx, domain.User{Username: "tag-b", PasswordHash: "hash", Role: domain.RoleUser, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := db.CreateResourceTag(ctx, owner.ID, domain.ResourceTagInput{GroupName: "项目", Value: "A"})
	if err != nil {
		t.Fatal(err)
	}
	api := &API{store: db}
	other.Permissions = access.Grants{access.EnvironmentRead: access.ScopeOwn}
	owner.Permissions = access.Grants{access.EnvironmentRead: access.ScopeOwn}
	request := httptest.NewRequest("GET", "/api/v1/environments?tag_id="+tag.ID, nil)
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, other))
	if _, err := api.visibleTagIDs(request, other.ID, access.EnvironmentRead); err == nil {
		t.Fatal("expected cross-owner tag rejection")
	}
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, owner))
	ids, err := api.visibleTagIDs(request, owner.ID, access.EnvironmentRead)
	if err != nil || len(ids) != 1 || ids[0] != tag.ID {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}
