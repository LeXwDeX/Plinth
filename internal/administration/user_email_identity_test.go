package administration

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// newEmailRequiredUserService 模拟 Schema 声明
// administration.users.api.identity.email_required = true 的运行接线。
func newEmailRequiredUserService(
	t *testing.T,
	repository UserRepository,
	audit AuditRecorder,
) *UserService {
	t.Helper()
	service, err := NewUserService(UserServiceConfig{
		Identity: UserIdentityRules{
			EmailRequired:      true,
			EmailMustContainAt: true,
		},
		Repository:                repository,
		Audit:                     audit,
		PasswordHashCost:          bcrypt.MinCost,
		UsernameMinimumCharacters: 2,
		PasswordMinimumCharacters: 6,
		IDPrefix:                  "acct_",
		DefaultRole:               "member",
		AdminRole:                 "superuser",
		DefaultActive:             true,
		AuditActions: UserAuditActions{
			Create:     "user-created",
			Update:     "user-updated",
			Deactivate: "user-disabled",
			Delete:     "user-deleted",
			Resource:   "account",
		},
		AuditFields: UserAuditFields{
			Username: "login",
			Admin:    "privileged",
			Active:   "enabled",
		},
		Clock: func() time.Time {
			return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// TestUserServiceRejectsAccountsThatCouldNeverSignIn：会话签发要求 email 非空
// （authentication.validUser），因此建号必须在同一条契约上校验邮箱，否则会
// 造出管理员能建、但该账号永远无法登录的"死号"。
func TestUserServiceRejectsAccountsThatCouldNeverSignIn(t *testing.T) {
	cases := []struct {
		name  string
		email string
	}{
		{name: "empty", email: ""},
		{name: "blank", email: "   "},
		{name: "missing at", email: "not-an-email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repository := newMemoryUserRepository()
			audit := &memoryAuditRecorder{}
			service := newEmailRequiredUserService(t, repository, audit)

			_, err := service.CreateUser(context.Background(), Actor{
				ID:       "admin-1",
				Username: "root",
			}, CreateUser{
				Username: "member",
				Password: "secret-1",
				Email:    tc.email,
			})
			if !errors.Is(err, ErrInvalidEmail) {
				t.Fatalf("email=%q 期望 ErrInvalidEmail，实际 %v", tc.email, err)
			}
			if _, exists, lookupErr := repository.
				FindAdministrationUserByUsername(
					context.Background(), "member",
				); lookupErr != nil {
				t.Fatal(lookupErr)
			} else if exists {
				t.Fatal("被拒绝的建号仍写入了仓储")
			}
			if len(audit.events) != 0 {
				t.Fatalf("被拒绝的建号仍写了审计：%+v", audit.events)
			}
		})
	}
}

// TestUserServiceUpdateRejectsClearingEmail：改号同样不得把已有账号改成
// 不可登录状态（邮箱清空/格式非法都要拒绝并保持原值）。
func TestUserServiceUpdateRejectsClearingEmail(t *testing.T) {
	repository := newMemoryUserRepository()
	audit := &memoryAuditRecorder{}
	service := newEmailRequiredUserService(t, repository, audit)
	actor := Actor{ID: "admin-1", Username: "root"}

	created, err := service.CreateUser(context.Background(), actor, CreateUser{
		Username: "member",
		Password: "secret-1",
		Email:    "member@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, email := range []string{"", "  ", "missing-at"} {
		_, err = service.UpdateUser(
			context.Background(), actor, created.ID, UpdateUser{
				Email: &email,
			},
		)
		if !errors.Is(err, ErrInvalidEmail) {
			t.Fatalf("email=%q 期望 ErrInvalidEmail，实际 %v", email, err)
		}
	}
	stored, exists, err := repository.FindAdministrationUser(
		context.Background(), created.ID,
	)
	if err != nil || !exists {
		t.Fatalf("账号丢失: exists=%v err=%v", exists, err)
	}
	if stored.Email != "member@example.com" {
		t.Fatalf("被拒绝的改号仍改写了邮箱：%q", stored.Email)
	}

	updated := "new@example.com"
	if _, err = service.UpdateUser(
		context.Background(), actor, created.ID, UpdateUser{Email: &updated},
	); err != nil {
		t.Fatalf("合法邮箱被拒：%v", err)
	}
}

// TestUserServiceIdentityRulesAreSchemaDriven：Schema 关闭 email 必填时
// 校验随之关闭——规则来自接线，不得在服务里硬编码。
func TestUserServiceIdentityRulesAreSchemaDriven(t *testing.T) {
	repository := newMemoryUserRepository()
	service := newUserService(t, repository, &memoryAuditRecorder{})

	if _, err := service.CreateUser(
		context.Background(),
		Actor{ID: "admin-1", Username: "root"},
		CreateUser{Username: "member", Password: "secret-1"},
	); err != nil {
		t.Fatalf("Schema 未要求邮箱时建号被拒：%v", err)
	}
}
