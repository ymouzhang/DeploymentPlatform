package application

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"DP/internal/domain"
	"DP/internal/store"
)

func TestAuthLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := NewAuthService(db, time.Hour)
	if err := auth.InitializeAdmin(ctx, "Admin", "initial-password"); err != nil {
		t.Fatal(err)
	}
	admin, token, _, err := auth.Login(ctx, "admin", "initial-password")
	if err != nil || admin.Role != domain.RoleAdmin || !admin.MustChangePassword {
		t.Fatalf("login: user=%+v err=%v", admin, err)
	}
	if got, err := auth.Authenticate(ctx, token); err != nil || got.ID != admin.ID {
		t.Fatalf("authenticate: user=%+v err=%v", got, err)
	}
	user, err := auth.CreateUser(ctx, "operator", "operator-password", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if !user.MustChangePassword {
		t.Fatal("new ordinary account must require a first-login password change")
	}
	createdAdmin, err := auth.CreateUser(ctx, "second-admin", "second-admin-password", domain.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if !createdAdmin.MustChangePassword {
		t.Fatal("new administrator account must require a first-login password change")
	}
	_, userToken, _, err := auth.Login(ctx, user.Username, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.UpdateEnabled(ctx, admin, user.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := auth.Login(ctx, user.Username, "operator-password"); err == nil || err.Error() != "账号已被禁用" {
		t.Fatalf("disabled login error=%v", err)
	}
	if _, _, _, err := auth.Login(ctx, user.Username, "wrong-password"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("wrong password must not reveal disabled state, got %v", err)
	}
	if _, err := auth.Authenticate(ctx, userToken); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("disabled session should fail, got %v", err)
	}
	if _, err := auth.UpdateEnabled(ctx, admin, admin.ID, false); err == nil {
		t.Fatal("current admin should be protected")
	}
}

func TestPasswordChangeRevokesSessions(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := NewAuthService(db, time.Hour)
	if err := auth.InitializeAdmin(ctx, "admin", "initial-password"); err != nil {
		t.Fatal(err)
	}
	admin, first, _, _ := auth.Login(ctx, "admin", "initial-password")
	_, second, _, _ := auth.Login(ctx, "admin", "initial-password")
	if err := auth.ChangePassword(ctx, admin, "initial-password", "changed-password"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		if _, err := auth.Authenticate(ctx, token); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("session not revoked: %v", err)
		}
	}
	if _, _, _, err := auth.Login(ctx, "admin", "changed-password"); err != nil {
		t.Fatal(err)
	}
	if err := auth.InitializeAdmin(ctx, "replacement", "replacement-password"); err != nil {
		t.Fatal(err)
	}
	unchanged, _, _, err := auth.Login(ctx, "admin", "changed-password")
	if err != nil || unchanged.MustChangePassword {
		t.Fatalf("restart must not reset initialized admin: user=%+v err=%v", unchanged, err)
	}
}

func TestLoginThrottleUsesUsernameAndSourceIP(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := NewAuthService(db, time.Hour)
	if err := auth.InitializeAdmin(ctx, "admin", "initial-password"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, _, _, err := auth.LoginWithContext(ctx, "admin", "wrong-password", "192.0.2.10", "test"); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("failure %d: %v", i+1, err)
		}
	}
	_, _, _, err = auth.LoginWithContext(ctx, "unknown", "wrong-password", "192.0.2.10", "test")
	var appErr *domain.AppError
	if !errors.As(err, &appErr) || appErr.Code != "LOGIN_THROTTLED" {
		t.Fatalf("shared IP should be throttled, got %v", err)
	}
}

func TestForcedPasswordChangeAndPreciseSessionRevocation(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "dp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := NewAuthService(db, time.Hour)
	if err := auth.InitializeAdmin(ctx, "admin", "initial-password"); err != nil {
		t.Fatal(err)
	}
	admin, _, _, err := auth.LoginWithContext(ctx, "admin", "initial-password", "192.0.2.1", "admin-browser")
	if err != nil {
		t.Fatal(err)
	}
	user, err := auth.CreateUser(ctx, "operator", "operator-password", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.ResetPasswordWithPolicy(ctx, user.ID, "temporary-password", true); err != nil {
		t.Fatal(err)
	}
	forced, firstToken, _, err := auth.LoginWithContext(ctx, user.Username, "temporary-password", "192.0.2.2", "first-browser")
	if err != nil || !forced.MustChangePassword {
		t.Fatalf("forced login user=%+v err=%v", forced, err)
	}
	_, secondToken, _, err := auth.LoginWithContext(ctx, user.Username, "temporary-password", "192.0.2.3", "second-browser")
	if err != nil {
		t.Fatal(err)
	}
	_, firstSession, err := auth.AuthenticateSession(ctx, firstToken)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := auth.ListSessions(ctx, user.ID, firstSession.ID)
	if err != nil || len(sessions) != 2 || !sessions[0].Current && !sessions[1].Current {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	if err := auth.RevokeSession(ctx, user.ID, firstSession.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, firstToken); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked token remains valid: %v", err)
	}
	if _, err := auth.Authenticate(ctx, secondToken); err != nil {
		t.Fatalf("other session should remain valid: %v", err)
	}
	if err := auth.ChangePassword(ctx, forced, "temporary-password", "final-password"); err != nil {
		t.Fatal(err)
	}
	updated, _, _, err := auth.Login(ctx, user.Username, "final-password")
	if err != nil || updated.MustChangePassword {
		t.Fatalf("password flag was not cleared: user=%+v err=%v admin=%s", updated, err, admin.Username)
	}
}
