package administration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound      = errors.New("administration: user not found")
	ErrDuplicateUsername = errors.New("administration: duplicate username")
	ErrInvalidUsername   = errors.New("administration: invalid username")
	ErrInvalidPassword   = errors.New("administration: invalid password")
	// ErrInvalidEmail：登录会话签发要求 email 非空（authentication.validUser），
	// 因此建号/改号必须按同一契约校验，否则会造出无法登录的账号。
	ErrInvalidEmail   = errors.New("administration: invalid email")
	ErrSelfDeactivate = errors.New("administration: cannot deactivate self")
	ErrSelfDelete     = errors.New("administration: cannot delete self")
	// ErrLastAdministrator：变更会让系统失去最后一个可用的管理员账号。
	// 这种状态无法通过产品界面恢复（没有管理员就没有管理入口），只能改库，
	// 因此必须在服务层硬拒，而不是靠操作者小心。
	ErrLastAdministrator = errors.New(
		"administration: last active administrator",
	)
	// ErrInvalidProjectGroup：项目组 KEY 不符合 Schema 声明的格式（#599）。
	// 服务端只 trim，不改大小写，非法值直接拒绝而不是静默改写。
	ErrInvalidProjectGroup = errors.New(
		"administration: invalid project group key",
	)
)

type Actor struct {
	ID        string
	Username  string
	IPAddress string
	UserAgent string
}

type User struct {
	ID        string
	Username  string
	Nickname  string
	Name      string
	Email     string
	Admin     bool
	Active    bool
	CreatedAt time.Time
	LastLogin *time.Time
	OpenID    *string
	// ProjectGroupKey 是用户所属项目组的 KEY（#599），空串表示未分组。
	// 它只是统计归集用的标签，不是权限，不进入角色或 access.Principal。
	ProjectGroupKey string
}

type StoredUser struct {
	User
	PasswordHash string
	Roles        []string
	UpdatedAt    time.Time
	Deleted      bool
}

type CreateUser struct {
	Username        string
	Password        string
	Name            string
	Email           string
	Admin           bool
	ProjectGroupKey string
}

type UpdateUser struct {
	Name     *string
	Email    *string
	Admin    *bool
	Active   *bool
	Password *string
	// ProjectGroupKey 为 nil 时不变；指向空串（trim 后）时清空为未分组。
	ProjectGroupKey *string
}

type UserRepository interface {
	ListAdministrationUsers(context.Context) ([]User, error)
	FindAdministrationUser(
		context.Context,
		string,
	) (StoredUser, bool, error)
	FindAdministrationUserByUsername(
		context.Context,
		string,
	) (StoredUser, bool, error)
	CreateAdministrationUser(context.Context, StoredUser) error
	// Each mutation must enforce Guards against current durable state in the
	// same write transaction as the mutation.
	UpdateAdministrationUser(context.Context, StoredUser, UserGuards) error
	SoftDeleteAdministrationUser(context.Context, string, time.Time, UserGuards) error
}

type AuditEvent struct {
	ID           string
	Action       string
	ActorID      string
	ActorName    string
	ResourceType string
	ResourceID   string
	Detail       map[string]any
	IPAddress    string
	UserAgent    string
	CreatedAt    time.Time
}

type AuditRecorder interface {
	RecordAudit(context.Context, AuditEvent) error
}

type UserAuditActions struct {
	Create     string
	Update     string
	Deactivate string
	Delete     string
	Resource   string
}

type UserAuditFields struct {
	Username string
	Admin    string
	Active   string
	// ProjectGroupKey 与 PreviousProjectGroupKey 记录项目组变更后与变更前的
	// 值（未分组记为 null），以便日后按生效日期归属历史时有据可依（#599）。
	ProjectGroupKey         string
	PreviousProjectGroupKey string
}

// UserGuards 声明管理员数量的不变量（值来自 Schema）。
type UserGuards struct {
	MinimumActiveAdministrators int
}

type UserIdentityRules struct {
	EmailRequired      bool
	EmailMustContainAt bool
}

type UserServiceConfig struct {
	Guards                    UserGuards
	Identity                  UserIdentityRules
	Repository                UserRepository
	Audit                     AuditRecorder
	PasswordHashCost          int
	UsernameMinimumCharacters int
	PasswordMinimumCharacters int
	IDPrefix                  string
	DefaultRole               string
	AdminRole                 string
	DefaultActive             bool
	AuditActions              UserAuditActions
	AuditFields               UserAuditFields
	// ProjectGroupKeyPattern 是 Schema 声明的项目组 KEY 正则（#599）。
	ProjectGroupKeyPattern string
	Clock                  func() time.Time
}

type UserService struct {
	guards                    UserGuards
	identity                  UserIdentityRules
	repository                UserRepository
	audit                     AuditRecorder
	passwordHashCost          int
	usernameMinimumCharacters int
	passwordMinimumCharacters int
	idPrefix                  string
	defaultRole               string
	adminRole                 string
	defaultActive             bool
	auditActions              UserAuditActions
	auditFields               UserAuditFields
	projectGroupKeyPattern    *regexp.Regexp
	clock                     func() time.Time
}

func NewUserService(config UserServiceConfig) (*UserService, error) {
	if config.Repository == nil {
		return nil, errors.New("administration: 缺少 UserRepository")
	}
	if config.PasswordHashCost < bcrypt.MinCost ||
		config.PasswordHashCost > bcrypt.MaxCost {
		return nil, errors.New("administration: bcrypt cost 无效")
	}
	if config.UsernameMinimumCharacters <= 0 ||
		config.PasswordMinimumCharacters <= 0 {
		return nil, errors.New("administration: 用户名和密码下限必须大于 0")
	}
	for name, value := range map[string]string{
		"id prefix":        config.IDPrefix,
		"default role":     config.DefaultRole,
		"admin role":       config.AdminRole,
		"audit create":     config.AuditActions.Create,
		"audit update":     config.AuditActions.Update,
		"audit deactivate": config.AuditActions.Deactivate,
		"audit delete":     config.AuditActions.Delete,
		"audit resource":   config.AuditActions.Resource,
		"audit username field": config.AuditFields.
			Username,
		"audit admin field":  config.AuditFields.Admin,
		"audit active field": config.AuditFields.Active,
		"audit project group field": config.AuditFields.
			ProjectGroupKey,
		"audit previous project group field": config.AuditFields.
			PreviousProjectGroupKey,
		"project group key pattern": config.ProjectGroupKeyPattern,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("administration: %s 不能为空", name)
		}
	}
	projectGroupKeyPattern, err := regexp.Compile(
		config.ProjectGroupKeyPattern,
	)
	if err != nil {
		return nil, fmt.Errorf("administration: 项目组 KEY 正则无效: %w", err)
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &UserService{
		guards:                    config.Guards,
		identity:                  config.Identity,
		repository:                config.Repository,
		audit:                     config.Audit,
		passwordHashCost:          config.PasswordHashCost,
		usernameMinimumCharacters: config.UsernameMinimumCharacters,
		passwordMinimumCharacters: config.PasswordMinimumCharacters,
		idPrefix:                  config.IDPrefix,
		defaultRole:               config.DefaultRole,
		adminRole:                 config.AdminRole,
		defaultActive:             config.DefaultActive,
		auditActions:              config.AuditActions,
		auditFields:               config.AuditFields,
		projectGroupKeyPattern:    projectGroupKeyPattern,
		clock:                     config.Clock,
	}, nil
}

func (s *UserService) ListUsers(ctx context.Context) ([]User, error) {
	return s.repository.ListAdministrationUsers(ctx)
}

func (s *UserService) CreateUser(
	ctx context.Context,
	actor Actor,
	command CreateUser,
) (User, error) {
	username := strings.TrimSpace(command.Username)
	if utf8.RuneCountInString(username) < s.usernameMinimumCharacters {
		return User{}, ErrInvalidUsername
	}
	if utf8.RuneCountInString(command.Password) < s.passwordMinimumCharacters {
		return User{}, ErrInvalidPassword
	}
	if err := s.validateEmail(command.Email); err != nil {
		return User{}, err
	}
	projectGroupKey, err := s.normalizeProjectGroupKey(command.ProjectGroupKey)
	if err != nil {
		return User{}, err
	}
	if existing, exists, err := s.repository.
		FindAdministrationUserByUsername(ctx, username); err != nil {
		return User{}, err
	} else if exists && !existing.Deleted {
		return User{}, ErrDuplicateUsername
	} else if exists {
		// Soft-deleted usernames remain reserved, matching the unique DB invariant.
		return User{}, ErrDuplicateUsername
	}
	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(command.Password),
		s.passwordHashCost,
	)
	if err != nil {
		return User{}, fmt.Errorf("administration: hash password: %w", err)
	}
	id, err := randomUserID(s.idPrefix)
	if err != nil {
		return User{}, err
	}
	name := strings.TrimSpace(command.Name)
	if name == "" {
		name = username
	}
	now := s.clock().UTC()
	roles := []string{s.defaultRole}
	if command.Admin {
		roles = appendRole(roles, s.adminRole)
	}
	stored := StoredUser{
		User: User{
			ID:              id,
			Username:        username,
			Nickname:        name,
			Name:            name,
			Email:           strings.TrimSpace(command.Email),
			Admin:           command.Admin,
			Active:          s.defaultActive,
			CreatedAt:       now,
			ProjectGroupKey: projectGroupKey,
		},
		PasswordHash: string(passwordHash),
		Roles:        roles,
		UpdatedAt:    now,
	}
	if err := s.repository.CreateAdministrationUser(ctx, stored); err != nil {
		if errors.Is(err, ErrDuplicateUsername) {
			return User{}, ErrDuplicateUsername
		}
		return User{}, err
	}
	s.recordAudit(ctx, actor, s.auditActions.Create, stored.ID, map[string]any{
		s.auditFields.Username:        stored.Username,
		s.auditFields.Admin:           stored.Admin,
		s.auditFields.ProjectGroupKey: auditProjectGroupKey(stored.ProjectGroupKey),
	})
	return stored.User, nil
}

// normalizeProjectGroupKey 先 trim，再按 Schema 正则严格校验；空串表示未分组。
// 不改写大小写：管理员输入的 KEY 要么原样合法，要么被拒。
func (s *UserService) normalizeProjectGroupKey(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if !s.projectGroupKeyPattern.MatchString(trimmed) {
		return "", ErrInvalidProjectGroup
	}
	return trimmed, nil
}

// auditProjectGroupKey 把未分组写成 null，与 wire 上的表示一致。
func auditProjectGroupKey(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// validateEmail 按 Schema 声明的身份契约校验邮箱。
func (s *UserService) validateEmail(email string) error {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		if s.identity.EmailRequired {
			return ErrInvalidEmail
		}
		return nil
	}
	if s.identity.EmailMustContainAt && !strings.Contains(trimmed, "@") {
		return ErrInvalidEmail
	}
	return nil
}

func (s *UserService) UpdateUser(
	ctx context.Context,
	actor Actor,
	id string,
	command UpdateUser,
) (User, error) {
	stored, exists, err := s.repository.FindAdministrationUser(ctx, id)
	if err != nil {
		return User{}, err
	}
	if !exists || stored.Deleted {
		return User{}, ErrUserNotFound
	}
	wasActive := stored.Active
	previousProjectGroupKey := stored.ProjectGroupKey
	if command.ProjectGroupKey != nil {
		projectGroupKey, err := s.normalizeProjectGroupKey(
			*command.ProjectGroupKey,
		)
		if err != nil {
			return User{}, err
		}
		stored.ProjectGroupKey = projectGroupKey
	}
	if command.Name != nil {
		name := strings.TrimSpace(*command.Name)
		stored.Name = name
		stored.Nickname = name
	}
	if command.Email != nil {
		if err := s.validateEmail(*command.Email); err != nil {
			return User{}, err
		}
		stored.Email = strings.TrimSpace(*command.Email)
	}
	// 停用自己先给具体提示：操作者需要知道是"不能对自己动手"，而不是
	// 一句"必须保留至少一个管理员"——后者对自停用场景是误导。
	if command.Active != nil && !*command.Active && actor.ID == id {
		return User{}, ErrSelfDeactivate
	}
	if command.Admin != nil {
		stored.Admin = *command.Admin
		stored.Roles = roleMembership(
			stored.Roles,
			s.adminRole,
			stored.Admin,
		)
	}
	if command.Active != nil {
		stored.Active = *command.Active
	}
	if command.Password != nil && *command.Password != "" {
		if utf8.RuneCountInString(*command.Password) <
			s.passwordMinimumCharacters {
			return User{}, ErrInvalidPassword
		}
		hash, hashErr := bcrypt.GenerateFromPassword(
			[]byte(*command.Password),
			s.passwordHashCost,
		)
		if hashErr != nil {
			return User{}, fmt.Errorf(
				"administration: hash password: %w",
				hashErr,
			)
		}
		stored.PasswordHash = string(hash)
	}
	stored.UpdatedAt = s.clock().UTC()
	if err := s.repository.UpdateAdministrationUser(ctx, stored, s.guards); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	s.recordAudit(ctx, actor, s.auditActions.Update, id, map[string]any{
		s.auditFields.Username:        stored.Username,
		s.auditFields.Admin:           stored.Admin,
		s.auditFields.Active:          stored.Active,
		s.auditFields.ProjectGroupKey: auditProjectGroupKey(stored.ProjectGroupKey),
		s.auditFields.PreviousProjectGroupKey: auditProjectGroupKey(
			previousProjectGroupKey,
		),
	})
	if wasActive && !stored.Active {
		s.recordAudit(
			ctx,
			actor,
			s.auditActions.Deactivate,
			id,
			map[string]any{s.auditFields.Username: stored.Username},
		)
	}
	return stored.User, nil
}

func (s *UserService) DeleteUser(
	ctx context.Context,
	actor Actor,
	id string,
) error {
	if actor.ID == id {
		return ErrSelfDelete
	}
	stored, exists, err := s.repository.FindAdministrationUser(ctx, id)
	if err != nil {
		return err
	}
	if !exists || stored.Deleted {
		return ErrUserNotFound
	}
	if err := s.repository.SoftDeleteAdministrationUser(
		ctx,
		id,
		s.clock().UTC(),
		s.guards,
	); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrUserNotFound
		}
		return err
	}
	s.recordAudit(ctx, actor, s.auditActions.Delete, id, map[string]any{
		s.auditFields.Username: stored.Username,
	})
	return nil
}

func (s *UserService) recordAudit(
	ctx context.Context,
	actor Actor,
	action string,
	resourceID string,
	detail map[string]any,
) {
	if s.audit == nil {
		return
	}
	// Audit is intentionally best effort, matching the existing product contract:
	// a logging outage must not roll back an already committed user mutation.
	_ = s.audit.RecordAudit(ctx, AuditEvent{
		Action:       action,
		ActorID:      actor.ID,
		ActorName:    actor.Username,
		ResourceType: s.auditActions.Resource,
		ResourceID:   resourceID,
		Detail:       detail,
		IPAddress:    actor.IPAddress,
		UserAgent:    actor.UserAgent,
		CreatedAt:    s.clock().UTC(),
	})
}

func randomUserID(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("administration: generate user ID: %w", err)
	}
	return prefix + hex.EncodeToString(raw), nil
}

func appendRole(roles []string, role string) []string {
	if slices.Contains(roles, role) {
		return roles
	}
	return append(roles, role)
}

func roleMembership(roles []string, role string, member bool) []string {
	result := slices.DeleteFunc(
		append([]string(nil), roles...),
		func(candidate string) bool { return candidate == role },
	)
	if member {
		result = append(result, role)
	}
	return result
}
