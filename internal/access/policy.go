package access

import (
	"errors"
	"fmt"
	"strings"
)

type Permission string

const (
	PermissionTasksReadOwn Permission = "tasks.read.own"
	PermissionTasksReadAny Permission = "tasks.read.any"
)

type Claims struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Email  string   `json:"email"`
	Roles  []string `json:"roles,omitempty"`
	OpenID string   `json:"open_id,omitempty"`
}

type Principal struct {
	Claims      Claims
	permissions map[Permission]struct{}
	admin       bool
}

func (p Principal) ID() string {
	return p.Claims.ID
}

func (p Principal) Can(permission Permission) bool {
	_, allowed := p.permissions[permission]
	return allowed
}

func (p Principal) IsAdmin() bool {
	return p.admin
}

type RoleDefinition struct {
	Key         string
	Permissions []Permission
}

type PolicyConfig struct {
	DefaultRole string
	AdminRole   string
	Roles       []RoleDefinition
}

type Policy struct {
	defaultRole string
	adminRole   string
	roles       map[string][]Permission
}

func NewPolicy(config PolicyConfig) (*Policy, error) {
	if strings.TrimSpace(config.DefaultRole) == "" ||
		strings.TrimSpace(config.AdminRole) == "" {
		return nil, errors.New("default role 和 admin role 不能为空")
	}
	policy := &Policy{
		defaultRole: config.DefaultRole,
		adminRole:   config.AdminRole,
		roles:       make(map[string][]Permission, len(config.Roles)),
	}
	for index, role := range config.Roles {
		role.Key = strings.TrimSpace(role.Key)
		if role.Key == "" {
			return nil, fmt.Errorf("roles[%d].key 不能为空", index)
		}
		if _, exists := policy.roles[role.Key]; exists {
			return nil, fmt.Errorf("role 重复: %q", role.Key)
		}
		permissions := make([]Permission, 0, len(role.Permissions))
		seen := map[Permission]struct{}{}
		for _, permission := range role.Permissions {
			permission = Permission(strings.TrimSpace(string(permission)))
			if permission == "" {
				return nil, fmt.Errorf("role %q 包含空 permission", role.Key)
			}
			if _, exists := seen[permission]; exists {
				return nil, fmt.Errorf("role %q 的 permission 重复: %q", role.Key, permission)
			}
			seen[permission] = struct{}{}
			permissions = append(permissions, permission)
		}
		policy.roles[role.Key] = permissions
	}
	for field, role := range map[string]string{
		"default role": config.DefaultRole,
		"admin role":   config.AdminRole,
	} {
		if _, exists := policy.roles[role]; !exists {
			return nil, fmt.Errorf("%s 引用了未知 role %q", field, role)
		}
	}
	return policy, nil
}

func (p *Policy) Principal(claims Claims) Principal {
	claims.ID = strings.TrimSpace(claims.ID)
	claims.Name = strings.TrimSpace(claims.Name)
	claims.Email = strings.TrimSpace(claims.Email)

	roleKeys := make([]string, 0, len(claims.Roles)+1)
	seenRoles := map[string]struct{}{}
	appendRole := func(role string) {
		role = strings.TrimSpace(role)
		if role == "" {
			return
		}
		if _, exists := seenRoles[role]; exists {
			return
		}
		seenRoles[role] = struct{}{}
		roleKeys = append(roleKeys, role)
	}
	appendRole(p.defaultRole)
	for _, role := range claims.Roles {
		appendRole(role)
	}
	claims.Roles = roleKeys

	principal := Principal{
		Claims:      claims,
		permissions: map[Permission]struct{}{},
	}
	for _, role := range roleKeys {
		if role == p.adminRole {
			principal.admin = true
		}
		for _, permission := range p.roles[role] {
			principal.permissions[permission] = struct{}{}
		}
	}
	return principal
}
