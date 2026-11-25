package administration

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// #599：项目组 KEY 是用户上可空的一个标签，格式由 Schema 声明。服务端先 trim
// 再按 Schema 正则严格校验，不改大小写；空串表示未分组。

func TestUserServiceCreatesUserWithTrimmedProjectGroupAndAuditsIt599(t *testing.T) {
	repository := newMemoryUserRepository()
	audit := &memoryAuditRecorder{}
	service := newUserService(t, repository, audit)

	user, err := service.CreateUser(context.Background(), Actor{ID: "admin-1"}, CreateUser{
		Username:        "alice",
		Password:        "secret12",
		ProjectGroupKey: "  studio-a  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.ProjectGroupKey != "studio-a" ||
		repository.users[user.ID].ProjectGroupKey != "studio-a" {
		t.Fatalf("project group not stored trimmed: %+v", repository.users[user.ID])
	}
	if len(audit.events) != 1 || audit.events[0].Detail["group"] != "studio-a" {
		t.Fatalf("create audit must carry the new project group: %#v", audit.events)
	}
	if _, present := audit.events[0].Detail["previous_group"]; present {
		t.Fatalf("create audit has no previous project group: %#v", audit.events[0].Detail)
	}
}

func TestUserServiceCreatesUngroupedUserWhenProjectGroupBlank599(t *testing.T) {
	repository := newMemoryUserRepository()
	audit := &memoryAuditRecorder{}
	service := newUserService(t, repository, audit)

	user, err := service.CreateUser(context.Background(), Actor{ID: "admin-1"}, CreateUser{
		Username:        "alice",
		Password:        "secret12",
		ProjectGroupKey: "   ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.ProjectGroupKey != "" {
		t.Fatalf("blank project group must mean ungrouped: %+v", user)
	}
	if got, present := audit.events[0].Detail["group"]; !present || got != nil {
		t.Fatalf("ungrouped create audit records null group: %#v", audit.events[0].Detail)
	}
}

func TestUserServiceRejectsInvalidProjectGroupKeys599(t *testing.T) {
	cases := map[string]string{
		"uppercase":         "Studio-A",
		"inner space":       "studio a",
		"too long":          strings.Repeat("a", 33),
		"leading hyphen":    "-studio",
		"leading underline": "_studio",
		"other punctuation": "studio.a",
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			repository := newMemoryUserRepository()
			audit := &memoryAuditRecorder{}
			service := newUserService(t, repository, audit)
			_, err := service.CreateUser(context.Background(), Actor{ID: "admin-1"}, CreateUser{
				Username:        "alice",
				Password:        "secret12",
				ProjectGroupKey: key,
			})
			if !errors.Is(err, ErrInvalidProjectGroup) {
				t.Fatalf("create with %q error = %v", key, err)
			}
			if len(repository.users) != 0 || len(audit.events) != 0 {
				t.Fatalf("rejected key must not create or audit: %#v %#v", repository.users, audit.events)
			}

			seeded, err := service.CreateUser(context.Background(), Actor{ID: "admin-1"}, CreateUser{
				Username:        "bob",
				Password:        "secret12",
				ProjectGroupKey: "studio-a",
			})
			if err != nil {
				t.Fatal(err)
			}
			audit.events = nil
			value := key
			_, err = service.UpdateUser(context.Background(), Actor{ID: "admin-1"}, seeded.ID, UpdateUser{
				ProjectGroupKey: &value,
			})
			if !errors.Is(err, ErrInvalidProjectGroup) {
				t.Fatalf("update with %q error = %v", key, err)
			}
			if repository.users[seeded.ID].ProjectGroupKey != "studio-a" || len(audit.events) != 0 {
				t.Fatalf("rejected update must leave the group and audit untouched: %+v %#v",
					repository.users[seeded.ID], audit.events)
			}
		})
	}
}

func TestUserServiceAcceptsBoundaryProjectGroupKeys599(t *testing.T) {
	for _, key := range []string{"a", "0", "a_b-c9", strings.Repeat("z", 32)} {
		repository := newMemoryUserRepository()
		service := newUserService(t, repository, &memoryAuditRecorder{})
		user, err := service.CreateUser(context.Background(), Actor{ID: "admin-1"}, CreateUser{
			Username:        "alice",
			Password:        "secret12",
			ProjectGroupKey: key,
		})
		if err != nil || user.ProjectGroupKey != key {
			t.Fatalf("key %q: %+v %v", key, user, err)
		}
	}
}

func TestUserServiceUpdatesSetsClearsAndKeepsProjectGroup599(t *testing.T) {
	repository := newMemoryUserRepository()
	audit := &memoryAuditRecorder{}
	service := newUserService(t, repository, audit)
	actor := Actor{ID: "admin-1", Username: "root"}
	user, err := service.CreateUser(context.Background(), actor, CreateUser{
		Username: "alice",
		Password: "secret12",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 设置：前值为未分组（null），新值为 trim 后的 KEY。
	audit.events = nil
	set := " studio-a "
	updated, err := service.UpdateUser(context.Background(), actor, user.ID, UpdateUser{ProjectGroupKey: &set})
	if err != nil || updated.ProjectGroupKey != "studio-a" {
		t.Fatalf("set project group: %+v %v", updated, err)
	}
	if detail := audit.events[0].Detail; detail["group"] != "studio-a" || detail["previous_group"] != nil {
		t.Fatalf("set audit must carry before and after: %#v", detail)
	} else if _, present := detail["previous_group"]; !present {
		t.Fatalf("set audit must record the previous (null) group: %#v", detail)
	}

	// 改组：前后值都写进审计。
	audit.events = nil
	change := "studio-b"
	updated, err = service.UpdateUser(context.Background(), actor, user.ID, UpdateUser{ProjectGroupKey: &change})
	if err != nil || updated.ProjectGroupKey != "studio-b" {
		t.Fatalf("change project group: %+v %v", updated, err)
	}
	if detail := audit.events[0].Detail; detail["group"] != "studio-b" || detail["previous_group"] != "studio-a" {
		t.Fatalf("change audit must carry before and after: %#v", detail)
	}

	// 不变：nil 指针不动组，其他字段照常更新。
	audit.events = nil
	name := "Alice"
	updated, err = service.UpdateUser(context.Background(), actor, user.ID, UpdateUser{Name: &name})
	if err != nil || updated.ProjectGroupKey != "studio-b" || repository.users[user.ID].ProjectGroupKey != "studio-b" {
		t.Fatalf("nil project group must keep the group: %+v %v", updated, err)
	}
	if detail := audit.events[0].Detail; detail["group"] != "studio-b" || detail["previous_group"] != "studio-b" {
		t.Fatalf("unchanged group audit: %#v", detail)
	}

	// 清空：空串（含只有空白）表示未分组。
	audit.events = nil
	clear := "  "
	updated, err = service.UpdateUser(context.Background(), actor, user.ID, UpdateUser{ProjectGroupKey: &clear})
	if err != nil || updated.ProjectGroupKey != "" || repository.users[user.ID].ProjectGroupKey != "" {
		t.Fatalf("clear project group: %+v %v", updated, err)
	}
	if detail := audit.events[0].Detail; detail["group"] != nil || detail["previous_group"] != "studio-b" {
		t.Fatalf("clear audit must carry before and after: %#v", detail)
	}
}

func TestUserServiceSoftDeleteKeepsProjectGroup599(t *testing.T) {
	repository := newMemoryUserRepository()
	service := newUserService(t, repository, &memoryAuditRecorder{})
	actor := Actor{ID: "admin-1", Username: "root"}
	user, err := service.CreateUser(context.Background(), actor, CreateUser{
		Username:        "alice",
		Password:        "secret12",
		ProjectGroupKey: "studio-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteUser(context.Background(), actor, user.ID); err != nil {
		t.Fatal(err)
	}
	stored := repository.users[user.ID]
	if !stored.Deleted || stored.ProjectGroupKey != "studio-a" {
		t.Fatalf("soft delete must keep the project group: %+v", stored)
	}
}

func TestUserServiceRequiresSchemaProjectGroupContract599(t *testing.T) {
	base := func() UserServiceConfig {
		return UserServiceConfig{
			Repository:                newMemoryUserRepository(),
			PasswordHashCost:          4,
			UsernameMinimumCharacters: 2,
			PasswordMinimumCharacters: 6,
			IDPrefix:                  "acct_",
			DefaultRole:               "member",
			AdminRole:                 "superuser",
			AuditActions: UserAuditActions{
				Create: "c", Update: "u", Deactivate: "d", Delete: "x", Resource: "r",
			},
			AuditFields: UserAuditFields{
				Username: "login", Admin: "privileged", Active: "enabled",
				ProjectGroupKey: "group", PreviousProjectGroupKey: "previous_group",
			},
			ProjectGroupKeyPattern: `^[a-z]+$`,
		}
	}
	if _, err := NewUserService(base()); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, mutate := range map[string]func(*UserServiceConfig){
		"missing pattern":        func(c *UserServiceConfig) { c.ProjectGroupKeyPattern = " " },
		"invalid pattern":        func(c *UserServiceConfig) { c.ProjectGroupKeyPattern = `^[a-z` },
		"missing audit field":    func(c *UserServiceConfig) { c.AuditFields.ProjectGroupKey = "" },
		"missing previous field": func(c *UserServiceConfig) { c.AuditFields.PreviousProjectGroupKey = "" },
	} {
		config := base()
		mutate(&config)
		if _, err := NewUserService(config); err == nil {
			t.Fatalf("%s: config accepted", name)
		}
	}
}
