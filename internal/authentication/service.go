package authentication

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var (
	ErrUnauthenticated = errors.New("未认证")
	// ErrWeakPassword：新密码低于 Schema 声明的字符下限。
	ErrWeakPassword       = errors.New("authentication: weak password")
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrMockDisabled       = errors.New("mock auth 未启用")
	ErrMockUserMissing    = errors.New("缺少 mock 用户")
	ErrMockUserInvalid    = errors.New("mock 用户 JSON 无效")
)

// User is the signed-in identity carried in the session claims. Claims from
// older releases may also hold an "open_id" member; decoding ignores it.
type User struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Email string   `json:"email"`
	Roles []string `json:"roles,omitempty"`
}

type UserRecord struct {
	ID           string
	OpenID       string
	Username     string
	PasswordHash string
	Nickname     string
	Name         string
	Email        string
	Roles        []string
	Admin        bool
	Active       bool
	Deleted      bool
}

type Directory interface {
	FindActiveByUsername(
		context.Context,
		string,
	) (UserRecord, bool, error)
	FindByID(context.Context, string) (UserRecord, bool, error)
	UpdateLastLogin(context.Context, string, time.Time) error
}

type BootstrapDirectory interface {
	FindByUsername(
		context.Context,
		string,
	) (UserRecord, bool, error)
	CreateBootstrapAdmin(context.Context, UserRecord, time.Time) error
	UpdatePassword(context.Context, string, string, time.Time) error
}

// PasswordDirectory 是自助改密所需的最小目录能力。
type PasswordDirectory interface {
	FindByID(context.Context, string) (UserRecord, bool, error)
	UpdatePassword(context.Context, string, string, time.Time) error
}

// ChangeOwnPassword 让已认证用户改自己的密码。改密必须先验当前密码：
// 拿到别人会话的人不能借此永久接管账号。改密成功后吊销该用户的全部
// 既有会话（SEC-06），否则被盗会话会活得比密码还久。
func (s *Service) ChangeOwnPassword(
	ctx context.Context,
	userID string,
	current string,
	next string,
) error {
	directory, supported := s.directory.(PasswordDirectory)
	if !supported {
		return errors.New(
			"authentication: user directory 不支持自助改密",
		)
	}
	minimum := s.schema.API.PasswordMinimumCharacters
	if utf8.RuneCountInString(next) < minimum {
		return ErrWeakPassword
	}
	record, exists, err := directory.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if !exists || record.Deleted || !record.Active {
		return ErrUnauthenticated
	}
	if bcrypt.CompareHashAndPassword(
		[]byte(record.PasswordHash),
		[]byte(current),
	) != nil {
		return ErrInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword(
		[]byte(next),
		s.schema.API.PasswordHashCost,
	)
	if err != nil {
		return fmt.Errorf("authentication: 生成新密码哈希: %w", err)
	}
	if err := directory.UpdatePassword(
		ctx,
		userID,
		string(hash),
		s.clock().UTC(),
	); err != nil {
		return err
	}
	return s.sessions.RevokeUserSessions(ctx, userID, s.clock().UTC())
}

type BootstrapAdmin struct {
	Username string
	Password string
	Name     string
	Email    string
	Role     string
	// Reset rewrites an existing administrator's password from Password.
	// Without it the bootstrap only creates a missing administrator, so a
	// password changed in the product survives restarts (#256).
	Reset bool
}

type FallbackIdentity interface {
	Identify(*http.Request) (access.Principal, error)
}

type Config struct {
	Sessions       SessionStore
	Schema         catalog.AuthenticationSchema
	SessionSecret  string
	PublicAppURL   string
	Environment    string
	MockEnabled    bool
	MockUserJSON   string
	AdminAllowlist []string
	Policy         *access.Policy
	Directory      Directory
	Fallback       FallbackIdentity
	Clock          func() time.Time
}

type Service struct {
	sessions SessionStore
	// dummyHashOnce/dummyPasswordHash 支撑"用户不存在"路径的一次固定
	// bcrypt 比较（SEC-01）：抹平与真实用户校验的响应时间差，防用户名
	// 枚举。惰性生成，避免拖慢启动。
	dummyHashOnce     sync.Once
	dummyPasswordHash string
	legacyAcceptUntil time.Time
	schema            catalog.AuthenticationSchema
	sessionSecret     []byte
	publicAppURL      string
	mockEnabled       bool
	mockUserJSON      string
	adminAllowlist    map[string]struct{}
	policy            *access.Policy
	directory         Directory
	fallback          FallbackIdentity
	clock             func() time.Time
}

func New(config Config) (*Service, error) {
	if config.Policy == nil {
		return nil, errors.New("authentication: 缺少 access policy")
	}
	if config.Directory == nil {
		return nil, errors.New("authentication: 缺少 user directory")
	}
	secret := strings.TrimSpace(config.SessionSecret)
	if secret == "" {
		if config.Schema.API.SessionSecretMissingPolicy !=
			"ephemeral-non-production" ||
			config.Environment == "production" {
			return nil, errors.New("authentication: SESSION_SECRET 未配置")
		}
		generated := make([]byte, 32)
		if _, err := rand.Read(generated); err != nil {
			return nil, fmt.Errorf("authentication: 生成临时 session secret: %w", err)
		}
		secret = hex.EncodeToString(generated)
	}
	if len(secret) < 32 {
		return nil, errors.New("authentication: SESSION_SECRET 至少需要 32 个字符")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	legacyAcceptUntil, err := time.Parse(time.RFC3339, config.Schema.Session.LegacyAcceptUntil)
	if err != nil {
		return nil, fmt.Errorf("authentication: invalid legacy session cutoff: %w", err)
	}
	sessions := config.Sessions
	if sessions == nil {
		sessions, _ = config.Directory.(SessionStore)
	}
	if sessions == nil {
		if config.Environment == "production" {
			return nil, errors.New("authentication: durable session store is required")
		}
		sessions = &memorySessionStore{records: map[string]SessionRecord{}}
	}
	return &Service{
		sessions:          sessions,
		legacyAcceptUntil: legacyAcceptUntil,
		schema:            config.Schema,
		sessionSecret:     []byte(secret),
		publicAppURL:      strings.TrimSpace(config.PublicAppURL),
		mockEnabled:       config.MockEnabled,
		mockUserJSON:      strings.TrimSpace(config.MockUserJSON),
		adminAllowlist:    stringSet(config.AdminAllowlist),
		policy:            config.Policy,
		directory:         config.Directory,
		fallback:          config.Fallback,
		clock:             config.Clock,
	}, nil
}

func (s *Service) Identify(request *http.Request) (access.Principal, error) {
	_, cookieErr := request.Cookie(s.schema.Cookie.Name)
	hasSessionCookie := cookieErr == nil
	user, exists, err := s.CurrentUser(request.Context(), request)
	if err != nil {
		return access.Principal{}, err
	}
	if exists {
		admin, adminErr := s.IsAdmin(request.Context(), user)
		if adminErr != nil {
			return access.Principal{}, adminErr
		}
		// Historical roles are data, not a second source of administrator
		// authority. Rebuild this role from the current directory/allowlist
		// decision without changing persisted roles or signed session claims.
		roles := make([]string, 0, len(user.Roles)+1)
		for _, role := range user.Roles {
			if strings.TrimSpace(role) != s.schema.AdminRole {
				roles = append(roles, role)
			}
		}
		if admin {
			roles = appendRoleIfMissing(
				roles,
				s.schema.AdminRole,
			)
		}
		return s.policy.Principal(access.Claims{
			ID:    user.ID,
			Name:  user.Name,
			Email: user.Email,
			Roles: roles,
		}), nil
	}
	// A present session cookie is an explicit authentication attempt. If it is
	// expired, revoked, deleted or otherwise invalid, never silently replace it
	// with a development/fallback identity.
	if hasSessionCookie {
		return access.Principal{}, ErrUnauthenticated
	}
	if s.fallback != nil {
		return s.fallback.Identify(request)
	}
	return access.Principal{}, ErrUnauthenticated
}

func (s *Service) CurrentUser(
	ctx context.Context,
	request *http.Request,
) (User, bool, error) {
	cookie, err := request.Cookie(s.schema.Cookie.Name)
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			return s.configuredMockUser()
		}
		return User{}, false, err
	}
	claims, valid, err := s.sessionClaims(ctx, cookie)
	if err != nil || !valid {
		return User{}, false, err
	}
	user := claims.User
	record, recordExists, directoryErr := s.directory.FindByID(ctx, user.ID)
	if directoryErr != nil {
		return User{}, false, directoryErr
	}
	if recordExists {
		if record.Deleted || !record.Active {
			return User{}, false, nil
		}
		user = s.sessionUser(record)
	} else if !claims.Mock || !s.mockEnabled {
		return User{}, false, nil
	}
	return user, true, nil
}

func (s *Service) configuredMockUser() (User, bool, error) {
	if !s.mockEnabled {
		return User{}, false, nil
	}
	var user User
	if s.mockUserJSON == "" ||
		json.Unmarshal([]byte(s.mockUserJSON), &user) != nil ||
		!validUser(user) {
		return User{}, false, nil
	}
	return user, true, nil
}

func (s *Service) AuthenticatePassword(
	ctx context.Context,
	username string,
	password string,
) (User, error) {
	record, exists, err := s.directory.FindActiveByUsername(ctx, username)
	if err != nil {
		return User{}, err
	}
	if !exists || strings.TrimSpace(record.PasswordHash) == "" {
		// SEC-01：用户不存在时也执行一次等价 bcrypt 比较，防时间差枚举。
		s.burnDummyPasswordCost(password)
		return User{}, ErrInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword(
		[]byte(record.PasswordHash),
		[]byte(password),
	) != nil {
		return User{}, ErrInvalidCredentials
	}
	if err := s.directory.UpdateLastLogin(ctx, record.ID, s.clock().UTC()); err != nil {
		return User{}, err
	}
	return s.sessionUser(record), nil
}

// burnDummyPasswordCost 对固定 dummy 哈希执行一次真实 bcrypt 比较。
func (s *Service) burnDummyPasswordCost(password string) {
	s.dummyHashOnce.Do(func() {
		hash, err := bcrypt.GenerateFromPassword(
			[]byte("art-ai-dummy-password-placeholder"),
			s.schema.API.PasswordHashCost,
		)
		if err != nil {
			return
		}
		s.dummyPasswordHash = string(hash)
	})
	if s.dummyPasswordHash == "" {
		return
	}
	_ = bcrypt.CompareHashAndPassword(
		[]byte(s.dummyPasswordHash),
		[]byte(password),
	)
}

// RevokeUserSessions 吊销指定用户的全部活跃会话（SEC-06）：管理员改密、
// 停用、降权后，攻击者手里的既有会话立即失效。
func (s *Service) RevokeUserSessions(ctx context.Context, userID string) error {
	return s.sessions.RevokeUserSessions(ctx, userID, s.clock().UTC())
}

// BootstrapAdminAction says what the startup bootstrap did (#319).
type BootstrapAdminAction string

const (
	// BootstrapAdminSkipped: no ADMIN_PASSWORD, nothing was looked at.
	BootstrapAdminSkipped BootstrapAdminAction = ""
	// BootstrapAdminCreated: the administrator did not exist and was created.
	BootstrapAdminCreated BootstrapAdminAction = "created"
	// BootstrapAdminPasswordReset: ADMIN_PASSWORD_RESET replaced the password
	// and revoked the administrator's sessions.
	BootstrapAdminPasswordReset BootstrapAdminAction = "password_reset"
	// BootstrapAdminUnchanged: the administrator exists and was left as is.
	BootstrapAdminUnchanged BootstrapAdminAction = "unchanged"
)

// BootstrapAdminResult is the outcome of the startup bootstrap. It never
// carries the password or its hash.
type BootstrapAdminResult struct {
	Action          BootstrapAdminAction
	UserID          string
	SessionsRevoked bool
}

func (s *Service) BootstrapAdmin(
	ctx context.Context,
	bootstrap BootstrapAdmin,
) error {
	_, err := s.BootstrapAdminOutcome(ctx, bootstrap)
	return err
}

// BootstrapAdminOutcome creates a missing administrator or, with Reset,
// replaces an existing administrator's password, and reports what it did.
func (s *Service) BootstrapAdminOutcome(
	ctx context.Context,
	bootstrap BootstrapAdmin,
) (BootstrapAdminResult, error) {
	if strings.TrimSpace(bootstrap.Password) == "" {
		return BootstrapAdminResult{Action: BootstrapAdminSkipped}, nil
	}
	directory, supported := s.directory.(BootstrapDirectory)
	if !supported {
		return BootstrapAdminResult{}, errors.New("authentication: user directory 不支持管理员初始化")
	}
	record, exists, err := directory.FindByUsername(ctx, bootstrap.Username)
	if err != nil {
		return BootstrapAdminResult{}, err
	}
	if exists && (!bootstrap.Reset || bcrypt.CompareHashAndPassword(
		[]byte(record.PasswordHash),
		[]byte(bootstrap.Password),
	) == nil) {
		return BootstrapAdminResult{Action: BootstrapAdminUnchanged, UserID: record.ID}, nil
	}
	// Creating or resetting the admin applies the same length rule as a
	// self-service change (#256); an existing admin is left untouched above.
	if utf8.RuneCountInString(bootstrap.Password) < s.schema.API.PasswordMinimumCharacters {
		return BootstrapAdminResult{}, ErrWeakPassword
	}
	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(bootstrap.Password),
		s.schema.API.PasswordHashCost,
	)
	if err != nil {
		return BootstrapAdminResult{}, fmt.Errorf("authentication: 生成管理员密码哈希: %w", err)
	}
	now := s.clock().UTC()
	if exists {
		if err := directory.UpdatePassword(
			ctx,
			record.ID,
			string(passwordHash),
			now,
		); err != nil {
			return BootstrapAdminResult{}, err
		}
		// ADMIN_PASSWORD_RESET is a password change like any other: sessions
		// issued under the old password are revoked (SEC-06).
		if err := s.sessions.RevokeUserSessions(ctx, record.ID, now); err != nil {
			return BootstrapAdminResult{}, err
		}
		return BootstrapAdminResult{Action: BootstrapAdminPasswordReset, UserID: record.ID, SessionsRevoked: true}, nil
	}
	id, err := randomID()
	if err != nil {
		return BootstrapAdminResult{}, err
	}
	if err := directory.CreateBootstrapAdmin(ctx, UserRecord{
		ID:           id,
		Username:     bootstrap.Username,
		PasswordHash: string(passwordHash),
		Nickname:     bootstrap.Name,
		Name:         bootstrap.Name,
		Email:        bootstrap.Email,
		Roles:        []string{bootstrap.Role},
		Admin:        true,
		Active:       true,
	}, now); err != nil {
		return BootstrapAdminResult{}, err
	}
	return BootstrapAdminResult{Action: BootstrapAdminCreated, UserID: id}, nil
}

func (s *Service) MockUser(name string, email string) (User, error) {
	if !s.mockEnabled {
		return User{}, ErrMockDisabled
	}
	if strings.TrimSpace(name) != "" && strings.TrimSpace(email) != "" {
		return User{
			ID: fmt.Sprintf(
				"%s%d",
				s.schema.API.MockIDPrefix,
				s.clock().UnixMilli(),
			),
			Name:  name,
			Email: email,
		}, nil
	}
	if s.mockUserJSON == "" {
		return User{}, ErrMockUserMissing
	}
	var user User
	if err := json.Unmarshal([]byte(s.mockUserJSON), &user); err != nil ||
		!validUser(user) {
		return User{}, ErrMockUserInvalid
	}
	return user, nil
}

func (s *Service) IsAdmin(
	ctx context.Context,
	user User,
) (bool, error) {
	record, exists, err := s.directory.FindByID(ctx, user.ID)
	if err != nil {
		return false, err
	}
	if exists {
		if record.Deleted || !record.Active {
			return false, nil
		}
		if record.Admin {
			return true, nil
		}
		user = s.sessionUser(record)
	} else if !s.mockEnabled {
		return false, nil
	}

	// Administrators are the directory's is_admin users plus the configured
	// e-mail allowlist; external role keys no longer grant it (#249).
	_, allowlisted := s.adminAllowlist[user.Email]
	return allowlisted, nil
}

func (s *Service) IssueSession(
	writer http.ResponseWriter,
	request *http.Request,
	user User,
) error {
	if !validUser(user) {
		return ErrUnauthenticated
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	now := s.clock().UTC()
	expires := now.Add(time.Duration(s.schema.Cookie.TTLSeconds) * time.Second)
	record, exists, err := s.directory.FindByID(request.Context(), user.ID)
	if err != nil {
		return err
	}
	if exists && (record.Deleted || !record.Active) {
		return ErrUnauthenticated
	}
	if !exists && !s.mockEnabled {
		return ErrUnauthenticated
	}
	claims := sessionClaims{Version: 2, SessionID: id, IssuedAt: now.Unix(), ExpiresAt: expires.Unix(), User: user, Mock: !exists && s.mockEnabled}
	payload, err := json.Marshal(claims)
	if err != nil {
		return fmt.Errorf("authentication: 编码 session user: %w", err)
	}
	if err := s.sessions.CreateSessionRecord(request.Context(), SessionRecord{ID: id, UserID: user.ID, IssuedAt: now, ExpiresAt: expires}); err != nil {
		return err
	}
	http.SetCookie(writer, &http.Cookie{
		Expires:  expires,
		Name:     s.schema.Cookie.Name,
		Value:    encodeCookieValue(s.sign(string(payload))),
		Path:     s.schema.Cookie.Path,
		MaxAge:   s.schema.Cookie.TTLSeconds,
		HttpOnly: s.schema.Cookie.HTTPOnly,
		Secure:   s.secureCookie(),
		SameSite: sameSiteMode(s.schema.Cookie.SameSite),
	})
	return nil
}

func (s *Service) ClearSession(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{
		Name:     s.schema.Cookie.Name,
		Value:    "",
		Path:     s.schema.Cookie.Path,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: s.schema.Cookie.HTTPOnly,
		Secure:   s.secureCookie(),
		SameSite: sameSiteMode(s.schema.Cookie.SameSite),
	})
}

func (s *Service) sign(payload string) string {
	mac := hmac.New(sha256.New, s.sessionSecret)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) verify(signedValue string) (string, bool) {
	lastDot := strings.LastIndexByte(signedValue, '.')
	if lastDot < 0 {
		return "", false
	}
	payload := signedValue[:lastDot]
	provided, err := hex.DecodeString(signedValue[lastDot+1:])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, s.sessionSecret)
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return "", false
	}
	return payload, true
}

func (s *Service) secureCookie() bool {
	switch s.schema.Cookie.SecureMode {
	case "always":
		return true
	case "never":
		return false
	default:
		return strings.HasPrefix(s.publicAppURL, "https")
	}
}

func sameSiteMode(value string) http.SameSite {
	switch value {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

func encodeCookieValue(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func decodeCookieValue(value string) string {
	decoded, err := url.QueryUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

func validUser(user User) bool {
	return strings.TrimSpace(user.ID) != "" &&
		strings.TrimSpace(user.Name) != "" &&
		strings.TrimSpace(user.Email) != ""
}

func (s *Service) sessionUser(record UserRecord) User {
	name := record.Nickname
	if name == "" {
		name = record.Name
	}
	if name == "" {
		name = record.Username
	}
	roles := append([]string(nil), record.Roles...)
	if record.Admin {
		roles = appendRoleIfMissing(roles, s.schema.AdminRole)
	}
	return User{
		ID:    record.ID,
		Name:  name,
		Email: record.Email,
		Roles: roles,
	}
}

func appendRoleIfMissing(roles []string, role string) []string {
	for _, current := range roles {
		if current == role {
			return roles
		}
	}
	return append(roles, role)
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("authentication: 生成用户 ID: %w", err)
	}
	return hex.EncodeToString(value), nil
}
