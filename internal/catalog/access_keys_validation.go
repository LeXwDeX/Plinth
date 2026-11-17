package catalog

import (
	"errors"
	"fmt"
	"strings"
)

// The scopes a key can be issued for. The ids are the fixed vocabulary of the
// code that checks them; the Schema owns the labels.
const (
	accessKeyScopeRead     = "read"
	accessKeyScopeGenerate = "generate"
)

// validateAccessKeys validates application.access_keys (#332). A Catalog
// without the section stays valid: every check is skipped and the management
// routes are not registered.
func validateAccessKeys(schema ApplicationSchema, auditActions map[string]struct{}) error {
	keys := schema.AccessKeys
	if keys == nil {
		return nil
	}
	if keys.KeyPrefix == "" || len(keys.KeyPrefix) > 16 {
		return errors.New("access_keys.key_prefix 必须是 1 到 16 个小写字母或数字")
	}
	for _, r := range keys.KeyPrefix {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return errors.New("access_keys.key_prefix 只能含小写字母和数字（它后面紧跟下划线分隔符）")
		}
	}
	if keys.MaxKeysPerUser < 1 || keys.MaxKeysPerUser > 100 {
		return errors.New("access_keys.max_keys_per_user 必须在 1 到 100 之间")
	}
	if keys.DefaultTTLDays < 1 || keys.MaxTTLDays < keys.DefaultTTLDays || keys.MaxTTLDays > 3650 {
		return errors.New("access_keys 必须满足 1 <= default_ttl_days <= max_ttl_days <= 3650")
	}
	if keys.NameMaxLength < 1 || keys.NameMaxLength > 200 {
		return errors.New("access_keys.name_max_length 必须在 1 到 200 之间")
	}
	if err := validateValueLabels("access_keys.scopes", keys.Scopes); err != nil {
		return err
	}
	for _, scope := range keys.Scopes {
		switch scope.Value {
		case accessKeyScopeRead, accessKeyScopeGenerate:
		default:
			return fmt.Errorf("access_keys.scopes 含未知范围 %q", scope.Value)
		}
	}
	if keys.LastUsedUpdateSeconds < 1 {
		return errors.New("access_keys.last_used_update_seconds 必须为正数")
	}
	if message := keys.Messages.LimitReached; strings.TrimSpace(message) == "" || !strings.Contains(message, "{max}") {
		return errors.New("access_keys.messages.limit_reached 必须非空并包含 {max}")
	}
	audit := keys.Audit
	if strings.TrimSpace(audit.ResourceType) == "" {
		return errors.New("access_keys.audit.resource_type 不能为空")
	}
	if audit.CreateAction == audit.RevokeAction {
		return errors.New("access_keys.audit 的 create_action 与 revoke_action 必须不同")
	}
	for name, action := range map[string]string{"create_action": audit.CreateAction, "revoke_action": audit.RevokeAction} {
		if _, declared := auditActions[action]; !declared {
			return fmt.Errorf("access_keys.audit.%s 引用了未知 audit action %q", name, action)
		}
	}
	routes := schema.Runtime.HTTP.Routes
	if strings.TrimSpace(routes.AccountAccessKeys) == "" || strings.TrimSpace(routes.AccountAccessKey) == "" {
		return errors.New("application.access_keys 需要 runtime.http.routes.account_access_keys 与 account_access_key")
	}
	if !configuredRouteParametersMatch(routes.AccountAccessKeys, nil) {
		return errors.New("http access keys route account_access_keys 不能含路径参数")
	}
	if !configuredRouteParametersMatch(routes.AccountAccessKey, []string{"id"}) {
		return errors.New("http access keys route account_access_key 必须恰好含路径参数 {id}")
	}
	return nil
}
