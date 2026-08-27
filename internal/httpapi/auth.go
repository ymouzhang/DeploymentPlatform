package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"DP/internal/domain"
)

const sessionCookieName = "dp_session"

type authContextKey struct{}

type authenticated struct {
	User    domain.User
	Session domain.Session
}

func currentUser(r *http.Request) domain.User {
	switch value := r.Context().Value(authContextKey{}).(type) {
	case authenticated:
		return value.User
	case domain.User: // 兼容直接构造认证上下文的单元测试。
		return value
	default:
		return domain.User{}
	}
}

func currentSession(r *http.Request) domain.Session {
	value, _ := r.Context().Value(authContextKey{}).(authenticated)
	return value.Session
}

func (a *API) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/api/v1/auth/login" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			a.writeError(w, r, domain.ErrUnauthorized)
			return
		}
		user, session, err := a.auth.AuthenticateSession(r.Context(), cookie.Value)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		if user.MustChangePassword && r.URL.Path != "/api/v1/auth/me" && r.URL.Path != "/api/v1/auth/password" && r.URL.Path != "/api/v1/auth/logout" {
			a.writeError(w, r, &domain.AppError{Code: "PASSWORD_CHANGE_REQUIRED", Message: "请先修改临时密码"})
			return
		}
		value := authenticated{User: user, Session: session}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, value)))
	})
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	attemptUsername := []rune(strings.ToLower(strings.TrimSpace(input.Username)))
	if len(attemptUsername) > 32 {
		attemptUsername = attemptUsername[:32]
	}
	setAuditActor(r, domain.User{}, string(attemptUsername))
	user, token, expires, err := a.auth.LoginWithContext(r.Context(), input.Username, input.Password, a.sourceIP(r), r.UserAgent())
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			err = &domain.AppError{Code: "INVALID_CREDENTIALS", Message: "用户名或密码错误"}
		}
		a.writeError(w, r, err)
		return
	}
	setAuditActor(r, user, user.Username)
	setAuditTarget(r, user, "session", user.ID, user.Username, nil)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: requestIsHTTPS(r)})
	writeData(w, http.StatusOK, user)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = a.auth.Logout(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: requestIsHTTPS(r)})
	writeData(w, http.StatusOK, map[string]bool{"logged_out": true})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) { writeData(w, http.StatusOK, currentUser(r)) }

func (a *API) listOwnSessions(w http.ResponseWriter, r *http.Request) {
	items, err := a.auth.ListSessions(r.Context(), currentUser(r).ID, currentSession(r).ID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) revokeOwnSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	if err := a.auth.RevokeSession(r.Context(), currentUser(r).ID, sessionID); err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "session", sessionID, currentUser(r).Username, map[string]any{"current": sessionID == currentSession(r).ID})
	current := sessionID == currentSession(r).ID
	if current {
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: requestIsHTTPS(r)})
	}
	writeData(w, http.StatusOK, map[string]bool{"session_revoked": true, "current": current})
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	if err := a.auth.ChangePassword(r.Context(), currentUser(r), input.Current, input.Next); err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "user", currentUser(r).ID, currentUser(r).Username, map[string]any{"password_changed": true})
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: requestIsHTTPS(r)})
	writeData(w, http.StatusOK, map[string]bool{"password_changed": true})
}

func (a *API) requireAdmin(r *http.Request) error {
	if currentUser(r).Role != domain.RoleAdmin {
		return domain.ErrForbidden
	}
	return nil
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	if err := a.requireAdmin(r); err != nil {
		a.writeError(w, r, err)
		return
	}
	users, err := a.auth.ListUsers(r.Context())
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, users)
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	if err := a.requireAdmin(r); err != nil {
		a.writeError(w, r, err)
		return
	}
	var input struct {
		Username string          `json:"username"`
		Password string          `json:"password"`
		Role     domain.UserRole `json:"role"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, currentUser(r), "user", "", strings.ToLower(strings.TrimSpace(input.Username)), map[string]any{"role": input.Role, "must_change_password": true})
	user, err := a.auth.CreateUserBy(r.Context(), currentUser(r).ID, input.Username, input.Password, input.Role)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, user, "user", user.ID, user.Username, map[string]any{"role": user.Role, "enabled": user.Enabled, "must_change_password": user.MustChangePassword})
	writeData(w, http.StatusCreated, user)
}

func (a *API) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	if err := a.requireAdmin(r); err != nil {
		a.writeError(w, r, err)
		return
	}
	var input struct {
		Password      string `json:"new_password"`
		RequireChange *bool  `json:"require_password_change"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	requireChange := true
	if input.RequireChange != nil {
		requireChange = *input.RequireChange
	}
	if target, err := a.store.GetUser(r.Context(), r.PathValue("id")); err == nil {
		setAuditTarget(r, target, "user", target.ID, target.Username, map[string]any{"password_reset": true, "require_password_change": requireChange})
	}
	if err := a.auth.ResetPasswordWithPolicy(r.Context(), r.PathValue("id"), input.Password, requireChange); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"password_reset": true, "require_password_change": requireChange})
}

func (a *API) updateUserStatus(w http.ResponseWriter, r *http.Request) {
	if err := a.requireAdmin(r); err != nil {
		a.writeError(w, r, err)
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &input); err != nil {
		a.writeError(w, r, err)
		return
	}
	if input.Enabled == nil {
		a.writeError(w, r, domain.FieldError("enabled", "必须提供 enabled"))
		return
	}
	if *input.Enabled {
		setAuditAction(r, "account.enable")
	} else {
		setAuditAction(r, "account.disable")
	}
	if target, err := a.store.GetUser(r.Context(), r.PathValue("id")); err == nil {
		setAuditTarget(r, target, "user", target.ID, target.Username, map[string]any{"enabled": *input.Enabled})
	}
	user, err := a.auth.UpdateEnabled(r.Context(), currentUser(r), r.PathValue("id"), *input.Enabled)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	setAuditTarget(r, user, "user", user.ID, user.Username, map[string]any{"enabled": user.Enabled})
	writeData(w, http.StatusOK, user)
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := a.requireAdmin(r); err != nil {
		a.writeError(w, r, err)
		return
	}
	target, _ := a.store.GetUser(r.Context(), r.PathValue("id"))
	if target.ID != "" {
		setAuditTarget(r, target, "user", target.ID, target.Username, nil)
	}
	if err := a.auth.DeleteUser(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}

func (a *API) listOwnerScope(r *http.Request) (string, error) {
	user := currentUser(r)
	requested := strings.TrimSpace(r.URL.Query().Get("owner_id"))
	if user.Role != domain.RoleAdmin {
		return user.ID, nil
	}
	if requested != "" {
		if _, err := a.store.GetUser(r.Context(), requested); err != nil {
			return "", err
		}
	}
	return requested, nil
}

func (a *API) packageOwnerScope(r *http.Request) (string, error) {
	user := currentUser(r)
	requested := strings.TrimSpace(r.URL.Query().Get("owner_id"))
	if requested == "" || requested == user.ID {
		return user.ID, nil
	}
	if user.Role != domain.RoleAdmin {
		return "", domain.ErrNotFound
	}
	if _, err := a.store.GetUser(r.Context(), requested); err != nil {
		return "", err
	}
	return requested, nil
}

func (a *API) createOwnerScope(r *http.Request) (string, error) {
	user := currentUser(r)
	requested := strings.TrimSpace(r.URL.Query().Get("owner_id"))
	if requested == "" || requested == user.ID {
		return user.ID, nil
	}
	if user.Role != domain.RoleAdmin {
		return "", domain.ErrNotFound
	}
	target, err := a.store.GetUser(r.Context(), requested)
	if err != nil {
		return "", err
	}
	if !target.Enabled {
		return "", &domain.AppError{Code: "ACCOUNT_DISABLED", Message: "不能为已禁用账号新增资源"}
	}
	return target.ID, nil
}

func (a *API) authorizeEnvironment(r *http.Request, id string) (domain.Environment, error) {
	env, err := a.store.GetEnvironment(r.Context(), id)
	if err != nil {
		return domain.Environment{}, err
	}
	user := currentUser(r)
	if user.Role != domain.RoleAdmin && env.OwnerID != user.ID {
		return domain.Environment{}, domain.ErrNotFound
	}
	return env, nil
}

func (a *API) authorizeOperation(r *http.Request, id string) (domain.Operation, error) {
	op, err := a.operations.Get(r.Context(), id)
	if err != nil {
		return domain.Operation{}, err
	}
	user := currentUser(r)
	if user.Role == domain.RoleAdmin || (op.OwnerID != "" && op.OwnerID == user.ID) {
		return op, nil
	}
	if op.OwnerID == "" {
		_, err = a.authorizeEnvironment(r, op.EnvironmentID)
		return op, err
	}
	return domain.Operation{}, domain.ErrNotFound
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}
