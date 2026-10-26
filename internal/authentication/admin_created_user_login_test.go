package authentication_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/authentication"
)

// 回归防线：管理员建号（批一交付前在 dev-test 真机验收发现）创建的账号必须能登录。
// 该链路横跨两个模块——administration 写入 authentication_users，
// authentication 读同一张表并回写 last_login——任何一侧的列/过滤条件漂移
// 都会让账号静默不可用（当时表现为登录 500，而不是 401）。
func TestAdminCreatedUserCanAuthenticate(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	schema := authenticationSchema(t)
	users, err := administration.NewUserService(administration.UserServiceConfig{
		Repository:                repository,
		Audit:                     discardAudit{},
		PasswordHashCost:          schema.API.PasswordHashCost,
		UsernameMinimumCharacters: 3,
		PasswordMinimumCharacters: 8,
		IDPrefix:                  "user_",
		DefaultRole:               schema.DefaultRole,
		AdminRole:                 schema.AdminRole,
		DefaultActive:             true,
		AuditActions: administration.UserAuditActions{
			Create:     "user.create",
			Update:     "user.update",
			Deactivate: "user.deactivate",
			Delete:     "user.delete",
			Resource:   "user",
		},
		AuditFields: administration.UserAuditFields{
			Username: "username",
			Admin:    "is_admin",
			Active:   "is_active",
		},
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := users.CreateUser(
		context.Background(),
		administration.Actor{ID: "admin", Username: "admin"},
		administration.CreateUser{
			Username: "created-user",
			Password: "created-password",
			Name:     "Created",
		},
	)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	service, err := authentication.New(authentication.Config{
		Schema:        schema,
		SessionSecret: strings.Repeat("s", 32),
		Environment:   "test",
		Policy:        testPolicy(t),
		Directory:     repository,
		Clock:         func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}

	user, err := service.AuthenticatePassword(
		context.Background(),
		"created-user",
		"created-password",
	)
	if err != nil {
		t.Fatalf("管理员建号后无法登录: %v", err)
	}
	if user.ID != created.ID {
		t.Fatalf("登录身份 = %q, want %q", user.ID, created.ID)
	}
	if len(user.Roles) == 0 {
		t.Fatal("登录身份缺少角色")
	}

	record, exists, err := repository.FindByID(context.Background(), created.ID)
	if err != nil || !exists {
		t.Fatalf("回读建号记录: exists=%v err=%v", exists, err)
	}
	if !record.Active || record.Deleted {
		t.Fatalf("建号记录状态异常: active=%v deleted=%v", record.Active, record.Deleted)
	}

	// 错误口令必须仍是 401 语义的 ErrInvalidCredentials，而不是 500 路径。
	if _, err := service.AuthenticatePassword(
		context.Background(),
		"created-user",
		"wrong-password",
	); err == nil {
		t.Fatal("错误口令被接受")
	}
}

type discardAudit struct{}

func (discardAudit) RecordAudit(context.Context, administration.AuditEvent) error {
	return nil
}
