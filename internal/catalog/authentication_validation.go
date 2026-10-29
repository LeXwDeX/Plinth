package catalog

// Authentication configuration contracts.
import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func validateAuthentication(schema AuthenticationSchema) error {
	if _, err := time.Parse(time.RFC3339, schema.Session.LegacyAcceptUntil); err != nil {
		return errors.New("authentication.session.legacy_accept_until must be RFC3339")
	}
	cookie := schema.Cookie
	if strings.TrimSpace(cookie.Name) == "" ||
		strings.TrimSpace(cookie.Path) == "" ||
		cookie.TTLSeconds <= 0 {
		return errors.New("authentication.cookie 的 name、path 和 ttl_seconds 必须有效")
	}
	if cookie.Format != "hmac-session-v2" {
		return fmt.Errorf("authentication.cookie.format 暂不支持 %q", cookie.Format)
	}
	switch cookie.SameSite {
	case "lax", "strict", "none":
	default:
		return fmt.Errorf("authentication.cookie.same_site 无效: %q", cookie.SameSite)
	}
	switch cookie.SecureMode {
	case "auto", "always", "never":
	default:
		return fmt.Errorf("authentication.cookie.secure_mode 无效: %q", cookie.SecureMode)
	}
	api := schema.API
	for field, value := range map[string]string{
		"id_field":                      api.IDField,
		"username_field":                api.UsernameField,
		"password_field":                api.PasswordField,
		"name_field":                    api.NameField,
		"email_field":                   api.EmailField,
		"error_field":                   api.ErrorField,
		"success_field":                 api.SuccessField,
		"user_field":                    api.UserField,
		"is_admin_field":                api.IsAdminField,
		"mock_id_prefix":                api.MockIDPrefix,
		"password_hash_algorithm":       api.PasswordHashAlgorithm,
		"session_secret_missing_policy": api.SessionSecretMissingPolicy,
		"login_audit_action":            api.LoginAuditAction,
		"logout_audit_action":           api.LogoutAuditAction,
		"audit_resource_type":           api.AuditResourceType,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("authentication.api.%s 不能为空", field)
		}
	}
	if api.PasswordHashAlgorithm != "bcrypt" {
		return fmt.Errorf(
			"authentication.api.password_hash_algorithm 暂不支持 %q",
			api.PasswordHashAlgorithm,
		)
	}
	if api.PasswordHashCost < 4 || api.PasswordHashCost > 31 {
		return errors.New("authentication.api.password_hash_cost 必须在 4 到 31 之间")
	}
	switch api.SessionSecretMissingPolicy {
	case "reject", "ephemeral-non-production":
	default:
		return fmt.Errorf(
			"authentication.api.session_secret_missing_policy 无效: %q",
			api.SessionSecretMissingPolicy,
		)
	}
	retry := schema.SessionRetry
	if retry.InitialDelayMilliseconds < 100 ||
		retry.MaxBackoffMilliseconds < retry.InitialDelayMilliseconds ||
		retry.MaxRetryAfterMilliseconds < retry.MaxBackoffMilliseconds {
		return errors.New("authentication.session_retry 须满足 100 <= initial_delay_ms <= max_backoff_ms <= max_retry_after_ms")
	}
	if len(schema.PublicPaths) == 0 {
		return errors.New("authentication.public_paths 不能为空")
	}
	publicPaths := map[string]struct{}{}
	for index, path := range schema.PublicPaths {
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("authentication.public_paths[%d] 必须以 / 开头", index)
		}
		if _, exists := publicPaths[path]; exists {
			return fmt.Errorf("authentication.public_paths 重复: %q", path)
		}
		publicPaths[path] = struct{}{}
	}
	if schema.DirectoryErrors != "allow" && schema.DirectoryErrors != "deny" {
		return fmt.Errorf(
			"authentication.directory_errors 仅支持 allow 或 deny，当前为 %q",
			schema.DirectoryErrors,
		)
	}

	permissionKeys := map[string]struct{}{}
	for index, permission := range schema.Permissions {
		if strings.TrimSpace(permission.Key) == "" || strings.TrimSpace(permission.Label) == "" {
			return fmt.Errorf("authentication.permissions[%d] 的 key 和 label 不能为空", index)
		}
		if _, exists := permissionKeys[permission.Key]; exists {
			return fmt.Errorf("authentication permission 重复: %q", permission.Key)
		}
		permissionKeys[permission.Key] = struct{}{}
	}
	if len(permissionKeys) == 0 {
		return errors.New("authentication.permissions 不能为空")
	}

	roleKeys := map[string]struct{}{}
	for index, role := range schema.Roles {
		if strings.TrimSpace(role.Key) == "" || strings.TrimSpace(role.Label) == "" {
			return fmt.Errorf("authentication.roles[%d] 的 key 和 label 不能为空", index)
		}
		if _, exists := roleKeys[role.Key]; exists {
			return fmt.Errorf("authentication role 重复: %q", role.Key)
		}
		roleKeys[role.Key] = struct{}{}
		rolePermissions := map[string]struct{}{}
		for _, permission := range role.Permissions {
			if _, exists := permissionKeys[permission]; !exists {
				return fmt.Errorf(
					"authentication role %q 引用了未知 permission %q",
					role.Key,
					permission,
				)
			}
			if _, exists := rolePermissions[permission]; exists {
				return fmt.Errorf(
					"authentication role %q 的 permission 重复: %q",
					role.Key,
					permission,
				)
			}
			rolePermissions[permission] = struct{}{}
		}
	}
	for field, value := range map[string]string{
		"default_role": schema.DefaultRole,
		"admin_role":   schema.AdminRole,
	} {
		if _, exists := roleKeys[value]; !exists {
			return fmt.Errorf("authentication.%s 引用了未知 role %q", field, value)
		}
	}

	for field, value := range map[string]string{
		"unauthenticated":        schema.Messages.Unauthenticated,
		"forbidden":              schema.Messages.Forbidden,
		"user_deactivated":       schema.Messages.UserDeactivated,
		"missing_credentials":    schema.Messages.MissingCredentials,
		"invalid_credentials":    schema.Messages.InvalidCredentials,
		"login_failed":           schema.Messages.LoginFailed,
		"rate_limited":           schema.Messages.RateLimited,
		"network_error":          schema.Messages.NetworkError,
		"internal_error":         schema.Messages.InternalError,
		"mock_disabled":          schema.Messages.MockDisabled,
		"mock_user_missing":      schema.Messages.MockUserMissing,
		"mock_user_invalid":      schema.Messages.MockUserInvalid,
		"login.title":            schema.Login.Title,
		"login.description":      schema.Login.Description,
		"login.username_label":   schema.Login.UsernameLabel,
		"login.password_label":   schema.Login.PasswordLabel,
		"login.submit_label":     schema.Login.SubmitLabel,
		"login.default_redirect": schema.Login.DefaultRedirect,
		"login.redirect_query":   schema.Login.RedirectQuery,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("authentication 展示文本 %s 不能为空", field)
		}
	}
	return nil
}
