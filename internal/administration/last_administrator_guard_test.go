package administration

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// newGuardedUserService 模拟 Schema 声明
// users.guards.minimum_active_administrators = 1 的运行接线。
func newGuardedUserService(
	t *testing.T,
	repository UserRepository,
	audit AuditRecorder,
) *UserService {
	t.Helper()
	service, err := NewUserService(UserServiceConfig{
		Guards:                    UserGuards{MinimumActiveAdministrators: 1},
		Identity:                  UserIdentityRules{EmailRequired: true, EmailMustContainAt: true},
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
			Create: "user-created", Update: "user-updated",
			Deactivate: "user-disabled", Delete: "user-deleted",
			Resource: "account",
		},
		AuditFields: UserAuditFields{
			Username: "login", Admin: "privileged", Active: "enabled",
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

func guardedSeed(t *testing.T, service *UserService) (Actor, string) {
	t.Helper()
	actor := Actor{ID: "admin-1", Username: "root"}
	admin, err := service.CreateUser(context.Background(), actor, CreateUser{
		Username: "only-admin", Password: "secret-1",
		Email: "admin@example.com", Admin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return actor, admin.ID
}

// TestGuardedServiceKeepsLastActiveAdministrator：降权、停用、删除任何一个
// 管理员都会改变"可用管理员"数量。若这次变更之后一个都不剩，系统就永久
// 失去管理入口（没有管理员就没有管理界面），只能改库恢复——必须在服务层
// 硬拒，不能只靠操作者小心。
func TestGuardedServiceKeepsLastActiveAdministrator(t *testing.T) {
	cases := []struct {
		name    string
		command UpdateUser
	}{
		{name: "demote", command: UpdateUser{Admin: boolPointer(false)}},
		{name: "deactivate", command: UpdateUser{Active: boolPointer(false)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repository := newMemoryUserRepository()
			audit := &memoryAuditRecorder{}
			service := newGuardedUserService(t, repository, audit)
			actor, adminID := guardedSeed(t, service)

			_, err := service.UpdateUser(
				context.Background(), actor, adminID, tc.command,
			)
			if !errors.Is(err, ErrLastAdministrator) {
				t.Fatalf("期望 ErrLastAdministrator，实际 %v", err)
			}
			assertStillActiveAdmin(t, service, adminID)
			if len(audit.events) != 1 {
				t.Fatalf("被拒绝的变更仍写了审计：%+v", audit.events)
			}
		})
	}

	t.Run("delete", func(t *testing.T) {
		repository := newMemoryUserRepository()
		audit := &memoryAuditRecorder{}
		service := newGuardedUserService(t, repository, audit)
		actor, adminID := guardedSeed(t, service)
		// 另一个管理员才能删：自删早已被 ErrSelfDelete 拦住。
		other, err := service.CreateUser(context.Background(), actor, CreateUser{
			Username: "second-admin", Password: "secret-2",
			Email: "second@example.com", Admin: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		// 删掉其中一个后仍剩一个，必须放行。
		if err := service.DeleteUser(
			context.Background(), actor, other.ID,
		); err != nil {
			t.Fatalf("删除非最后一个管理员被拒：%v", err)
		}
		// 现在只剩一个，再删就必须被拒。
		if err := service.DeleteUser(
			context.Background(), actor, adminID,
		); !errors.Is(err, ErrLastAdministrator) {
			t.Fatalf("期望 ErrLastAdministrator，实际 %v", err)
		}
		assertStillActiveAdmin(t, service, adminID)
	})
}

// TestGuardedServiceAllowsRemovingRedundantAdministrators：保底不是"一个都
// 不能动"，而是"不能动到零"。有两个管理员时降权其中一个必须成功，否则
// 这个功能形同虚设。
func TestGuardedServiceAllowsRemovingRedundantAdministrators(t *testing.T) {
	repository := newMemoryUserRepository()
	service := newGuardedUserService(t, repository, &memoryAuditRecorder{})
	actor, adminID := guardedSeed(t, service)
	if _, err := service.CreateUser(
		context.Background(), actor, CreateUser{
			Username: "second-admin", Password: "secret-2",
			Email: "second@example.com", Admin: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateUser(
		context.Background(), actor, adminID,
		UpdateUser{Admin: boolPointer(false)},
	); err != nil {
		t.Fatalf("存在另一个管理员时降权被拒：%v", err)
	}
}

// TestGuardedServiceIsSchemaDriven：Schema 把保底设为 0 时保护随之关闭——
// 不变量来自接线，不能在服务里写死。
func TestGuardedServiceIsSchemaDriven(t *testing.T) {
	repository := newMemoryUserRepository()
	service := newUserService(t, repository, &memoryAuditRecorder{})
	actor := Actor{ID: "admin-1", Username: "root"}
	admin, err := service.CreateUser(context.Background(), actor, CreateUser{
		Username: "only-admin", Password: "secret-1",
		Email: "admin@example.com", Admin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateUser(
		context.Background(), actor, admin.ID,
		UpdateUser{Admin: boolPointer(false)},
	); err != nil {
		t.Fatalf("Schema 未设保底时降权被拒：%v", err)
	}
}

func assertStillActiveAdmin(
	t *testing.T,
	service *UserService,
	id string,
) {
	t.Helper()
	users, err := service.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.ID == id && (!user.Admin || !user.Active) {
			t.Fatalf("被拒绝的变更仍生效：%+v", user)
		}
	}
}

func boolPointer(value bool) *bool {
	return &value
}
