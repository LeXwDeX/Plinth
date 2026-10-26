package access_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
)

func TestPolicyProjectsRolesToPermissionsWithoutHandlerChecks(t *testing.T) {
	policy, err := access.NewPolicy(access.PolicyConfig{
		DefaultRole: "user",
		AdminRole:   "admin",
		Roles: []access.RoleDefinition{
			{Key: "user", Permissions: []access.Permission{"tasks.read.own"}},
			{
				Key: "admin",
				Permissions: []access.Permission{
					"tasks.read.own",
					"tasks.read.any",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	regular := policy.Principal(access.Claims{ID: "user-a"})
	if !regular.Can("tasks.read.own") || regular.Can("tasks.read.any") || regular.IsAdmin() {
		t.Fatalf("regular principal = %#v", regular)
	}

	admin := policy.Principal(access.Claims{ID: "admin-a", Roles: []string{"admin"}})
	if !admin.Can("tasks.read.any") || !admin.IsAdmin() {
		t.Fatalf("admin principal = %#v", admin)
	}
}

func TestPolicyRejectsUnknownOrDuplicateConfiguration(t *testing.T) {
	for name, config := range map[string]access.PolicyConfig{
		"unknown default": {
			DefaultRole: "missing",
			AdminRole:   "admin",
			Roles:       []access.RoleDefinition{{Key: "admin"}},
		},
		"duplicate role": {
			DefaultRole: "user",
			AdminRole:   "user",
			Roles: []access.RoleDefinition{
				{Key: "user"},
				{Key: "user"},
			},
		},
		"duplicate permission": {
			DefaultRole: "user",
			AdminRole:   "user",
			Roles: []access.RoleDefinition{{
				Key:         "user",
				Permissions: []access.Permission{"tasks.read.own", "tasks.read.own"},
			}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := access.NewPolicy(config); err == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}
