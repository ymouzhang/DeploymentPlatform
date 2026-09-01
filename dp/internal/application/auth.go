package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"DP/internal/access"
	"DP/internal/domain"

	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

type AuthService struct {
	store      AuthRepository
	sessionTTL time.Duration
}

type AuthRepository interface {
	PendingInitialAdmin(context.Context) (domain.User, bool, error)
	InitializeAdmin(context.Context, string, string, string) (domain.User, error)
	GetUserByUsername(context.Context, string) (domain.User, error)
	GetUser(context.Context, string) (domain.User, error)
	ListUsers(context.Context) ([]domain.User, error)
	CreateUser(context.Context, domain.User) (domain.User, error)
	UpdateUserPasswordAndRevokeSessions(context.Context, string, string, bool) error
	UpdateUserEnabled(context.Context, string, bool) (domain.User, error)
	DeleteUser(context.Context, string) error
	UserBusinessCounts(context.Context, string) (int, int, error)
	UserDetail(context.Context, string, time.Time) (domain.UserDetail, error)
	CreateSession(context.Context, string, string, string, string, time.Time) (domain.Session, error)
	UserForSession(context.Context, string, time.Time) (domain.User, domain.Session, error)
	DeleteSession(context.Context, string) error
	DeleteUserSessions(context.Context, string) error
	DeleteExpiredSessions(context.Context, time.Time) error
	ListUserSessions(context.Context, string, time.Time) ([]domain.Session, error)
	DeleteUserSessionByID(context.Context, string, string) error
	LoginThrottleUntil(context.Context, []string, time.Time) (time.Time, error)
	RecordLoginFailure(context.Context, []string, time.Time) (time.Time, error)
	ClearLoginThrottle(context.Context, []string) error
}

func NewAuthService(store AuthRepository, sessionTTL time.Duration) *AuthService {
	return &AuthService{store: store, sessionTTL: sessionTTL}
}

func (s *AuthService) InitializeAdmin(ctx context.Context, username, password string) error {
	pending, exists, err := s.store.PendingInitialAdmin(ctx)
	if err != nil || !exists {
		return err
	}
	username, err = normalizeUsername(username)
	if err != nil {
		return fmt.Errorf("DP_ADMIN_USERNAME: %w", err)
	}
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("DP_ADMIN_PASSWORD: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.store.InitializeAdmin(ctx, pending.ID, username, string(hash))
	if errors.Is(err, domain.ErrConflict) {
		return errors.New("DP_ADMIN_USERNAME already exists")
	}
	return err
}

func (s *AuthService) Login(ctx context.Context, username, password string) (domain.User, string, time.Time, error) {
	return s.LoginWithContext(ctx, username, password, "", "")
}

func (s *AuthService) LoginWithContext(ctx context.Context, username, password, sourceIP, userAgent string) (domain.User, string, time.Time, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	keys := loginThrottleKeys(username, sourceIP)
	now := time.Now().UTC()
	if until, err := s.store.LoginThrottleUntil(ctx, keys, now); err != nil {
		return domain.User{}, "", time.Time{}, err
	} else if until.After(now) {
		return domain.User{}, "", time.Time{}, &domain.AppError{Code: "LOGIN_THROTTLED", Message: "登录尝试过于频繁，请稍后再试"}
	}
	if utf8.RuneCountInString(username) > 32 || utf8.RuneCountInString(password) > 128 {
		if _, throttleErr := s.store.RecordLoginFailure(ctx, keys, now); throttleErr != nil {
			return domain.User{}, "", time.Time{}, throttleErr
		}
		return domain.User{}, "", time.Time{}, domain.ErrUnauthorized
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		if _, throttleErr := s.store.RecordLoginFailure(ctx, keys, now); throttleErr != nil {
			return domain.User{}, "", time.Time{}, throttleErr
		}
		return domain.User{}, "", time.Time{}, domain.ErrUnauthorized
	}
	if !user.Enabled {
		if _, throttleErr := s.store.RecordLoginFailure(ctx, keys, now); throttleErr != nil {
			return domain.User{}, "", time.Time{}, throttleErr
		}
		return domain.User{}, "", time.Time{}, &domain.AppError{
			Code: "ACCOUNT_DISABLED", Message: "账号已被禁用",
		}
	}
	if err := s.store.ClearLoginThrottle(ctx, keys); err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	if err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	expires := now.Add(s.sessionTTL)
	if utf8.RuneCountInString(userAgent) > 512 {
		userAgent = string([]rune(userAgent)[:512])
	}
	if _, err := s.store.CreateSession(ctx, hashSessionToken(token), user.ID, sourceIP, userAgent, expires); err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	return user, token, expires, nil
}

func (s *AuthService) Authenticate(ctx context.Context, token string) (domain.User, error) {
	user, _, err := s.AuthenticateSession(ctx, token)
	return user, err
}

func (s *AuthService) AuthenticateSession(ctx context.Context, token string) (domain.User, domain.Session, error) {
	if token == "" {
		return domain.User{}, domain.Session{}, domain.ErrUnauthorized
	}
	user, session, err := s.store.UserForSession(ctx, hashSessionToken(token), time.Now().UTC())
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, domain.Session{}, domain.ErrUnauthorized
	}
	return user, session, err
}

func (s *AuthService) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, hashSessionToken(token))
}

func (s *AuthService) ChangePassword(ctx context.Context, user domain.User, current, next string) error {
	stored, err := s.store.GetUser(ctx, user.ID)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(current)) != nil {
		return domain.FieldError("current_password", "当前密码不正确")
	}
	if err := validatePassword(next); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.store.UpdateUserPasswordAndRevokeSessions(ctx, user.ID, string(hash), false)
}

func (s *AuthService) ListUsers(ctx context.Context) ([]domain.User, error) {
	return s.store.ListUsers(ctx)
}

func (s *AuthService) UserDetail(ctx context.Context, id string) (domain.UserDetail, error) {
	return s.store.UserDetail(ctx, id, time.Now().UTC())
}

func (s *AuthService) RevokeSessions(ctx context.Context, actor domain.User, id string) error {
	target, err := s.store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if target.ID == actor.ID {
		return &domain.AppError{Code: "USER_PROTECTED", Message: "不能强制下线当前登录账号"}
	}
	if err := authorizePrivilegedAccountMutation(actor, target); err != nil {
		return err
	}
	return s.store.DeleteUserSessions(ctx, id)
}

func (s *AuthService) ListSessions(ctx context.Context, userID, currentSessionID string) ([]domain.Session, error) {
	if _, err := s.store.GetUser(ctx, userID); err != nil {
		return nil, err
	}
	items, err := s.store.ListUserSessions(ctx, userID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Current = items[i].ID == currentSessionID
	}
	return items, nil
}

func (s *AuthService) RevokeSession(ctx context.Context, userID, sessionID string) error {
	return s.store.DeleteUserSessionByID(ctx, userID, sessionID)
}

func (s *AuthService) RevokeUserSession(ctx context.Context, actor domain.User, currentSessionID, userID, sessionID string) error {
	if actor.ID == userID && currentSessionID == sessionID {
		return &domain.AppError{Code: "USER_PROTECTED", Message: "不能从账号管理撤销当前会话，请使用个人会话入口"}
	}
	target, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := authorizePrivilegedAccountMutation(actor, target); err != nil {
		return err
	}
	return s.store.DeleteUserSessionByID(ctx, userID, sessionID)
}

func (s *AuthService) CreateUser(
	ctx context.Context,
	username string,
	password string,
	roles []access.RoleRef,
) (domain.User, error) {
	return s.CreateUserBy(ctx, "", username, password, roles)
}

func (s *AuthService) CreateUserBy(
	ctx context.Context,
	creatorID string,
	username string,
	password string,
	roles []access.RoleRef,
) (domain.User, error) {
	var err error
	username, err = normalizeUsername(username)
	if err != nil {
		return domain.User{}, err
	}
	if err := validatePassword(password); err != nil {
		return domain.User{}, err
	}
	if len(roles) == 0 {
		return domain.User{}, domain.FieldError("role_ids", "至少选择一个角色")
	}
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if strings.TrimSpace(role.ID) == "" {
			return domain.User{}, domain.FieldError("role_ids", "角色 ID 不能为空")
		}
		if _, ok := seen[role.ID]; ok {
			return domain.User{}, domain.FieldError("role_ids", "角色不能重复")
		}
		seen[role.ID] = struct{}{}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, err
	}
	user, err := s.store.CreateUser(ctx, domain.User{
		Username: username, PasswordHash: string(hash), Roles: roles,
		Enabled: true, MustChangePassword: true, CreatedBy: creatorID,
	})
	if errors.Is(err, domain.ErrConflict) {
		return domain.User{}, &domain.AppError{Code: "USERNAME_CONFLICT", Message: "用户名已存在"}
	}
	return user, err
}

func (s *AuthService) ResetPassword(
	ctx context.Context,
	actor domain.User,
	id string,
	password string,
	mustChange bool,
) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	target, err := s.store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if err := authorizePrivilegedAccountMutation(actor, target); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.store.UpdateUserPasswordAndRevokeSessions(ctx, id, string(hash), mustChange)
}

func (s *AuthService) UpdateEnabled(ctx context.Context, actor domain.User, id string, enabled bool) (domain.User, error) {
	target, err := s.store.GetUser(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	if !enabled && (target.IsInitialAdmin || target.ID == actor.ID) {
		return domain.User{}, &domain.AppError{Code: "USER_PROTECTED", Message: "初始管理员和当前登录账号不能被禁用"}
	}
	if err := authorizePrivilegedAccountMutation(actor, target); err != nil {
		return domain.User{}, err
	}
	updated, err := s.store.UpdateUserEnabled(ctx, id, enabled)
	if errors.Is(err, access.ErrProtected) {
		return domain.User{}, &domain.AppError{Code: "USER_PROTECTED", Message: "系统必须保留至少一个启用的超级管理员"}
	}
	if errors.Is(err, access.ErrInvalidInput) {
		return domain.User{}, domain.FieldError("enabled", "启用账号前必须至少分配一个角色")
	}
	if err != nil {
		return domain.User{}, err
	}
	if !enabled {
		if err := s.store.DeleteUserSessions(ctx, id); err != nil {
			return domain.User{}, err
		}
	}
	return updated, nil
}

func (s *AuthService) DeleteUser(ctx context.Context, actor domain.User, id string) error {
	target, err := s.store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if target.IsInitialAdmin || target.ID == actor.ID {
		return &domain.AppError{Code: "USER_PROTECTED", Message: "初始管理员和当前登录账号不能被删除"}
	}
	if err := authorizePrivilegedAccountMutation(actor, target); err != nil {
		return err
	}
	packages, environments, err := s.store.UserBusinessCounts(ctx, id)
	if err != nil {
		return err
	}
	if packages > 0 || environments > 0 {
		return &domain.AppError{Code: "USER_IN_USE", Message: "该账号仍有安装包或环境，请先清理业务数据"}
	}
	err = s.store.DeleteUser(ctx, id)
	if errors.Is(err, access.ErrProtected) {
		return &domain.AppError{Code: "USER_PROTECTED", Message: "系统必须保留至少一个启用的超级管理员"}
	}
	return err
}

func authorizePrivilegedAccountMutation(actor, target domain.User) error {
	if hasRole(target, access.RoleSuperAdmin) && !hasRole(actor, access.RoleSuperAdmin) {
		return &domain.AppError{Code: "USER_PROTECTED", Message: "只有超级管理员可以修改超级管理员账号"}
	}
	return nil
}

func hasRole(user domain.User, roleKey string) bool {
	for _, role := range user.Roles {
		if role.Key == roleKey {
			return true
		}
	}
	return false
}

func normalizeUsername(username string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) {
		return "", domain.FieldError("username", "用户名须为 3–32 位，以字母或数字开头，只能包含小写字母、数字、点、下划线和连字符")
	}
	return username, nil
}

func validatePassword(password string) error {
	length := utf8.RuneCountInString(password)
	if length < 8 || length > 128 {
		return domain.FieldError("password", "密码长度必须为 8–128 个字符")
	}
	return nil
}

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func loginThrottleKeys(username, sourceIP string) []string {
	if len(username) > 64 {
		sum := sha256.Sum256([]byte(username))
		username = "invalid:" + hex.EncodeToString(sum[:])
	}
	keys := []string{"username:" + username}
	if sourceIP != "" {
		keys = append(keys, "ip:"+sourceIP)
	}
	return keys
}
