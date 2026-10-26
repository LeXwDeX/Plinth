package catalog

// Administration pages, audit views and their text contracts.
import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var requiredAdministrationUserTextKeys = []string{
	"forbidden_title",
	"forbidden_description",
	"count_summary",
	"add_user",
	"list_title",
	"empty",
	"column_username",
	"column_name",
	"column_email",
	"column_role",
	"column_status",
	"column_created_at",
	"column_last_login",
	"column_actions",
	"role_admin",
	"role_user",
	"status_active",
	"status_inactive",
	"edit",
	"activate",
	"deactivate",
	"self_deactivate",
	"delete_user",
	"self_delete",
	"create_title",
	"edit_title",
	"create_description",
	"edit_description",
	"username_label",
	"username_placeholder",
	"password_label",
	"new_password_label",
	"password_placeholder",
	"new_password_placeholder",
	"name_label",
	"name_placeholder",
	"email_label",
	"email_placeholder",
	"admin_label",
	"admin_description",
	"cancel",
	"save",
	"saving",
	"delete_title",
	"delete_description",
	"delete_confirm",
	"deleting",
	"password_too_short",
	"update_failed",
	"create_failed",
	"operation_failed",
	"delete_failed",
	"updated",
	"created",
	"active_changed",
	"deleted",
}

var requiredAdministrationAuditLogTextKeys = []string{
	"forbidden_title",
	"forbidden_description",
	"filter_action",
	"all_actions",
	"filter_user",
	"all_users",
	"unnamed_user",
	"user_option_label",
	"filter_from",
	"filter_to",
	"filter_submit",
	"title",
	"total_summary",
	"empty",
	"column_time",
	"column_user",
	"column_action",
	"column_resource_type",
	"column_resource_id",
	"column_ip_address",
	"column_actions",
	"detail_button",
	"pagination_previous",
	"pagination_summary",
	"pagination_next",
	"detail_title",
	"detail_id",
	"detail_created_at",
	"detail_user_name",
	"detail_user_id",
	"detail_action",
	"detail_resource_type",
	"detail_resource_id",
	"detail_ip_address",
	"detail_user_agent",
	"detail_payload",
}

var requiredAdministrationGenerationTextKeys = []string{
	"forbidden_title",
	"forbidden_description",
	"filter_user",
	"all_users",
	"unnamed_user",
	"user_option_label",
	"filter_task_type",
	"all_task_types",
	"legacy_animation",
	"filter_status",
	"all_statuses",
	"filter_from",
	"filter_to",
	"filter_submit",
	"title",
	"total_summary",
	"empty",
	"column_preview",
	"column_time",
	"column_user",
	"column_task_type",
	"column_model_params",
	"column_task_id",
	"column_status",
	"column_estimated_credits",
	"column_actions",
	"estimated_credits",
	"detail_button",
	"open_task",
	"download",
	"downloading",
	"download_failed",
	"package_download_tooltip",
	"package",
	"download_all_package",
	"download_all",
	"result_count",
	"detail_cache_full",
	"pagination_previous",
	"pagination_summary",
	"pagination_next",
	"detail_title",
	"detail_loading",
	"detail_empty",
	"detail_basic",
	"detail_user",
	"detail_task_type",
	"detail_task_id",
	"detail_status",
	"detail_created_at",
	"detail_finished_at",
	"detail_estimated_credits",
	"detail_params",
	"detail_result_urls",
	"detail_no_result_urls",
	"detail_cache",
	"detail_cache_status",
	"detail_cache_provider",
	"detail_cache_data",
}

var requiredAdministrationUserAssetsTextKeys = []string{
	"select_user_label",
	"unnamed_user",
	"user_option_label",
	"select_user_placeholder",
	"search_user_placeholder",
	"no_matching_users",
	"start_date",
	"end_date",
	"filter_action",
	"filter",
	"unselected_title",
	"unselected_description",
	"summary_upload",
	"summary_generation",
	"summary_download",
	"summary_storage",
	"tab_uploads",
	"tab_generations",
	"tab_downloads",
	"uploads_title",
	"uploads_empty",
	"generations_title",
	"generations_empty",
	"downloads_title",
	"downloads_empty",
	"column_preview",
	"column_time",
	"column_filename",
	"column_type",
	"column_size",
	"column_task_type",
	"column_task_id",
	"column_status",
	"column_credits",
	"column_result",
	"column_operation",
	"column_action",
	"column_resource_type",
	"column_resource_id",
	"view",
	"result_files",
	"download",
	"package",
	"downloading",
	"package_download_tooltip",
	"pagination_previous",
	"pagination_summary",
	"pagination_next",
}

var requiredAdministrationUsageTextKeys = []string{
	"forbidden_title",
	"forbidden_description",
	"balance_label",
	"usage_label",
	"task_count_label",
	"disclaimer_prefix",
	"disclaimer_emphasis",
	"disclaimer_suffix",
	"time_range",
	"custom_range",
	"query",
	"ranking_title",
	"ranking_description",
	"empty",
	"column_user",
	"column_tasks",
	"column_succeeded",
	"column_failed",
	"column_credits",
	"column_last_active",
}

func validateAdministration(
	schema AdministrationSchema,
	runtime RuntimeSchema,
	authentication AuthenticationSchema,
) error {
	if len(schema.Navigation) == 0 {
		return errors.New("administration.navigation 不能为空")
	}
	seenNavigation := make(map[string]struct{}, len(schema.Navigation))
	seenNavigationKeys := make(map[string]struct{}, len(schema.Navigation))
	for index, item := range schema.Navigation {
		if strings.TrimSpace(item.Key) == "" ||
			strings.TrimSpace(item.Href) == "" ||
			!strings.HasPrefix(item.Href, "/admin") ||
			strings.TrimSpace(item.Label) == "" ||
			strings.TrimSpace(item.Icon) == "" {
			return fmt.Errorf(
				"administration.navigation[%d] 配置无效",
				index,
			)
		}
		switch item.Match {
		case "exact", "prefix":
		default:
			return fmt.Errorf(
				"administration.navigation[%d].match 无效: %q",
				index,
				item.Match,
			)
		}
		if _, exists := seenNavigation[item.Href]; exists {
			return fmt.Errorf(
				"administration.navigation href 重复: %q",
				item.Href,
			)
		}
		seenNavigation[item.Href] = struct{}{}
		if _, exists := seenNavigationKeys[item.Key]; exists {
			return fmt.Errorf(
				"administration.navigation key 重复: %q",
				item.Key,
			)
		}
		seenNavigationKeys[item.Key] = struct{}{}
	}

	users := schema.Users
	fields := map[string]string{
		"data":       users.API.Fields.Data,
		"success":    users.API.Fields.Success,
		"error":      users.API.Fields.Error,
		"id":         users.API.Fields.ID,
		"username":   users.API.Fields.Username,
		"password":   users.API.Fields.Password,
		"nickname":   users.API.Fields.Nickname,
		"name":       users.API.Fields.Name,
		"email":      users.API.Fields.Email,
		"is_admin":   users.API.Fields.IsAdmin,
		"is_active":  users.API.Fields.IsActive,
		"created_at": users.API.Fields.CreatedAt,
		"last_login": users.API.Fields.LastLogin,
		"open_id":    users.API.Fields.OpenID,
	}
	seenFields := make(map[string]string, len(fields))
	for name, field := range fields {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("users.api.fields.%s 不能为空", name)
		}
		if previous, exists := seenFields[field]; exists {
			return fmt.Errorf(
				"users.api.fields.%s 与 %s 不能同为 %q",
				previous,
				name,
				field,
			)
		}
		seenFields[field] = name
	}

	pathParameter := strings.TrimSpace(users.API.PathParameters.UserID)
	if pathParameter == "" || strings.ContainsAny(pathParameter, "{} /?#") {
		return errors.New("users.api.path_parameters.user_id 非法")
	}
	if parameters := configuredRouteParameters(
		runtime.HTTP.LegacyRoutes.AdminUser,
	); len(parameters) != 1 || parameters[0] != pathParameter {
		return errors.New(
			"runtime.http.legacy_routes.admin_user 必须只声明 users.api.path_parameters.user_id",
		)
	}
	if len(configuredRouteParameters(
		runtime.HTTP.LegacyRoutes.AdminUsers,
	)) != 0 {
		return errors.New(
			"runtime.http.legacy_routes.admin_users 不得声明路径参数",
		)
	}

	permissionFound := false
	for _, permission := range authentication.Permissions {
		if permission.Key == users.Permission {
			permissionFound = true
			break
		}
	}
	if !permissionFound {
		return fmt.Errorf(
			"users.permission 引用了未知 permission %q",
			users.Permission,
		)
	}
	// 允许 0 表示不设保底；一旦声明就必须为正数，否则 Schema 会静默放弃
	// "最后一个管理员"保护，而调用方以为它在生效。
	if users.Guards.MinimumActiveAdministrators < 0 {
		return errors.New(
			"users.guards.minimum_active_administrators 不能为负数",
		)
	}
	if users.Limits.UsernameMinimumCharacters <= 0 ||
		users.Limits.PasswordMinimumCharacters <= 0 {
		return errors.New("users.limits 的字符下限必须大于 0")
	}
	if strings.TrimSpace(users.Defaults.IDPrefix) == "" ||
		strings.ContainsAny(users.Defaults.IDPrefix, " /?#") {
		return errors.New("users.defaults.id_prefix 非法")
	}
	// 登录会话签发要求 email 非空（authentication.validUser）。若 Schema 允许
	// 建号不带邮箱，管理员就能造出永远无法登录的账号，因此这是硬性契约。
	if !users.API.Identity.EmailRequired {
		return errors.New("users.api.identity.email_required 必须为 true")
	}
	if !users.API.Identity.EmailMustContainAt {
		return errors.New(
			"users.api.identity.email_must_contain_at 必须为 true",
		)
	}
	for name, value := range map[string]string{
		"create_action":     users.Audit.CreateAction,
		"update_action":     users.Audit.UpdateAction,
		"deactivate_action": users.Audit.DeactivateAction,
		"delete_action":     users.Audit.DeleteAction,
		"resource_type":     users.Audit.ResourceType,
		"detail_username":   users.Audit.DetailFields.Username,
		"detail_is_admin":   users.Audit.DetailFields.IsAdmin,
		"detail_is_active":  users.Audit.DetailFields.IsActive,
		"user_not_found":    users.Messages.UserNotFound,
		"self_deactivate":   users.Messages.SelfDeactivate,
		"self_delete":       users.Messages.SelfDelete,
		"duplicate_username": users.Messages.
			DuplicateUsername,
		"invalid_username":   users.Messages.InvalidUsername,
		"invalid_password":   users.Messages.InvalidPassword,
		"invalid_email":      users.Messages.InvalidEmail,
		"last_administrator": users.Messages.LastAdministrator,
		"invalid_request":    users.Messages.InvalidRequest,
		"internal_error":     users.Messages.InternalError,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("users 配置 %s 不能为空", name)
		}
	}
	presentation := users.Presentation
	if strings.TrimSpace(presentation.DateLocale) == "" ||
		presentation.LoadingRows <= 0 ||
		strings.TrimSpace(presentation.EmptyPlaceholder) == "" {
		return errors.New("users.presentation 的日期、加载行和空值占位必须有效")
	}
	if len(presentation.DateTimeOptions) == 0 {
		return errors.New("users.presentation.date_time_options 不能为空")
	}
	allowedDateParts := map[string]struct{}{
		"weekday": {}, "year": {}, "month": {}, "day": {},
		"hour": {}, "minute": {}, "second": {},
	}
	allowedDateValues := map[string]struct{}{
		"numeric": {}, "2-digit": {}, "long": {}, "short": {}, "narrow": {},
	}
	for part, value := range presentation.DateTimeOptions {
		if _, exists := allowedDateParts[part]; !exists {
			return fmt.Errorf(
				"users.presentation.date_time_options 的字段非法: %q",
				part,
			)
		}
		if _, exists := allowedDateValues[value]; !exists {
			return fmt.Errorf(
				"users.presentation.date_time_options[%q] 非法: %q",
				part,
				value,
			)
		}
	}
	for _, key := range requiredAdministrationUserTextKeys {
		if strings.TrimSpace(presentation.Text[key]) == "" {
			return fmt.Errorf(
				"users.presentation.text[%q] 不能为空",
				key,
			)
		}
	}
	// 逐请求级审计：每个有读路径的管理模块都必须声明读动作与资源类型，
	// 否则"管理员读了什么"没有留痕，且动作名会在各 handler 里长成第二真源。
	if err := validateAdministrationReadAudits(schema); err != nil {
		return err
	}
	if err := validateAdministrationAuditLogs(
		schema.AuditLogs,
		runtime,
		authentication,
	); err != nil {
		return err
	}
	if err := validateAdministrationGenerations(
		schema.Generations,
		runtime,
		authentication,
	); err != nil {
		return err
	}
	if err := validateAdministrationUsage(
		schema.Usage,
		runtime,
		authentication,
	); err != nil {
		return err
	}
	if err := validateAdministrationUserAssets(
		schema.UserAssets,
		runtime,
		authentication,
	); err != nil {
		return err
	}
	return validateAdministrationBackfillStatus(
		schema.BackfillStatus,
		runtime,
		authentication,
	)
}

// validateAdministrationReadAudits 集中校验各管理模块的读审计声明。
func validateAdministrationReadAudits(
	schema AdministrationSchema,
) error {
	for module, audit := range map[string]AdministrationReadAuditSchema{
		"users":       {ReadAction: schema.Users.Audit.ReadAction, ResourceType: schema.Users.Audit.ResourceType},
		"audit_logs":  schema.AuditLogs.Audit,
		"generations": {ReadAction: schema.Generations.Audit.ReadAction, ResourceType: schema.Generations.Audit.ResourceType},
		"usage":       schema.Usage.Audit,
		"user_assets": schema.UserAssets.Audit,
	} {
		if strings.TrimSpace(audit.ReadAction) == "" {
			return fmt.Errorf(
				"administration.%s.audit.read_action 不能为空",
				module,
			)
		}
		if strings.TrimSpace(audit.ResourceType) == "" {
			return fmt.Errorf(
				"administration.%s.audit.resource_type 不能为空",
				module,
			)
		}
	}
	if strings.TrimSpace(schema.BackfillStatus.Audit.TriggerAction) == "" ||
		strings.TrimSpace(schema.BackfillStatus.Audit.ResourceType) == "" {
		return errors.New(
			"administration.backfill_status.audit 的触发动作与资源类型不能为空",
		)
	}
	return nil
}

func validateAdministrationBackfillStatus(
	schema AdministrationBackfillStatusSchema,
	runtime RuntimeSchema,
	authentication AuthenticationSchema,
) error {
	fields := map[string]string{
		"error":       schema.API.Fields.Error,
		"message":     schema.API.Fields.Message,
		"fixed":       schema.API.Fields.Fixed,
		"total":       schema.API.Fields.Total,
		"sync_fixed":  schema.API.Fields.SyncFixed,
		"async_fixed": schema.API.Fields.AsyncFixed,
		"unchanged":   schema.API.Fields.Unchanged,
	}
	seenFields := make(map[string]string, len(fields))
	for name, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return fmt.Errorf("backfill_status.api.fields.%s 不能为空", name)
		}
		if previous, exists := seenFields[field]; exists {
			return fmt.Errorf(
				"backfill_status.api.fields.%s 与 %s 不能同为 %q",
				previous,
				name,
				field,
			)
		}
		seenFields[field] = name
	}
	if len(configuredRouteParameters(
		runtime.HTTP.LegacyRoutes.AdminBackfillStatus,
	)) != 0 {
		return errors.New(
			"runtime.http.legacy_routes.admin_backfill_status 不得声明路径参数",
		)
	}
	permissionFound := false
	for _, permission := range authentication.Permissions {
		if permission.Key == schema.Permission {
			permissionFound = true
			break
		}
	}
	if !permissionFound {
		return fmt.Errorf(
			"backfill_status.permission 引用了未知 permission %q",
			schema.Permission,
		)
	}
	pending := strings.TrimSpace(schema.Statuses.Pending)
	succeeded := strings.TrimSpace(schema.Statuses.Succeeded)
	if pending == "" || succeeded == "" || pending == succeeded {
		return errors.New("backfill_status.statuses 配置无效")
	}
	if len(schema.SyncProviderIDs) == 0 {
		return errors.New("backfill_status.sync_provider_ids 不能为空")
	}
	seenProviders := make(map[string]struct{}, len(schema.SyncProviderIDs))
	for _, providerID := range schema.SyncProviderIDs {
		providerID = strings.TrimSpace(providerID)
		if providerID == "" {
			return errors.New("backfill_status.sync_provider_ids 包含空值")
		}
		if _, exists := seenProviders[providerID]; exists {
			return fmt.Errorf(
				"backfill_status.sync_provider_ids 重复声明 %q",
				providerID,
			)
		}
		seenProviders[providerID] = struct{}{}
	}
	cacheFields := map[string]string{
		"model_urls":    schema.CacheResultFields.ModelURLs,
		"thumbnail_url": schema.CacheResultFields.ThumbnailURL,
		"image_urls":    schema.CacheResultFields.ImageURLs,
		"texture_urls":  schema.CacheResultFields.TextureURLs,
		"base_color":    schema.CacheResultFields.BaseColor,
		"video_url":     schema.CacheResultFields.VideoURL,
	}
	seenCacheFields := make(map[string]string, len(cacheFields))
	for name, field := range cacheFields {
		field = strings.TrimSpace(field)
		if field == "" {
			return fmt.Errorf(
				"backfill_status.cache_result_fields.%s 不能为空",
				name,
			)
		}
		if previous, exists := seenCacheFields[field]; exists {
			return fmt.Errorf(
				"backfill_status.cache_result_fields.%s 与 %s 不能同为 %q",
				previous,
				name,
				field,
			)
		}
		seenCacheFields[field] = name
	}
	for name, message := range map[string]string{
		"no_pending":     schema.Messages.NoPending,
		"complete":       schema.Messages.Complete,
		"internal_error": schema.Messages.InternalError,
	} {
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("backfill_status.messages.%s 不能为空", name)
		}
	}
	return nil
}

func validateAdministrationUsage(
	usage AdministrationUsageSchema,
	runtime RuntimeSchema,
	authentication AuthenticationSchema,
) error {
	fields := map[string]string{
		"data":               usage.API.Fields.Data,
		"error":              usage.API.Fields.Error,
		"user_id":            usage.API.Fields.UserID,
		"user_name":          usage.API.Fields.UserName,
		"user_email":         usage.API.Fields.UserEmail,
		"total_tasks":        usage.API.Fields.TotalTasks,
		"succeeded_tasks":    usage.API.Fields.SucceededTasks,
		"failed_tasks":       usage.API.Fields.FailedTasks,
		"estimated_credits":  usage.API.Fields.EstimatedCredits,
		"last_active_at":     usage.API.Fields.LastActiveAt,
		"by_type":            usage.API.Fields.ByType,
		"total":              usage.API.Fields.Total,
		"query_days":         usage.API.Query.Days,
		"query_start_date":   usage.API.Query.StartDate,
		"query_end_date":     usage.API.Query.EndDate,
		"current_balance":    usage.API.BalanceFields.CurrentBalance,
		"balance_history":    usage.API.BalanceFields.History,
		"history_balance":    usage.API.BalanceFields.Balance,
		"captured_at":        usage.API.BalanceFields.CapturedAt,
		"permission":         usage.Permission,
		"date_pattern":       usage.Validation.DatePattern,
		"date_layout":        usage.Validation.DateLayout,
		"timezone":           usage.Validation.Timezone,
		"balance_account_id": usage.Balance.AccountID,
		"snapshot_id_prefix": usage.Balance.SnapshotIDPrefix,
		"snapshot_write_policy": usage.Balance.
			SnapshotWritePolicy,
		"invalid_range":        usage.Messages.InvalidRange,
		"internal_error":       usage.Messages.InternalError,
		"empty_placeholder":    usage.Presentation.EmptyPlaceholder,
		"approximation_prefix": usage.Presentation.ApproximationPrefix,
		"range_separator":      usage.Presentation.RangeSeparator,
		"date_separator":       usage.Presentation.DateSeparator,
		"date_locale":          usage.Presentation.DateLocale,
	}
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("usage 配置 %s 不能为空", name)
		}
	}
	if len(configuredRouteParameters(
		runtime.HTTP.LegacyRoutes.AdminUsage,
	)) != 0 {
		return errors.New(
			"runtime.http.legacy_routes.admin_usage 不得声明路径参数",
		)
	}
	permissionFound := false
	for _, permission := range authentication.Permissions {
		if permission.Key == usage.Permission {
			permissionFound = true
			break
		}
	}
	if !permissionFound {
		return fmt.Errorf(
			"usage.permission 引用了未知 permission %q",
			usage.Permission,
		)
	}
	if usage.Validation.MaximumDays <= 0 ||
		usage.Defaults.Days <= 0 ||
		usage.Defaults.CustomRangeDays <= 0 ||
		usage.Balance.HistoryDays <= 0 ||
		usage.Presentation.LoadingRows <= 0 ||
		len(usage.Presentation.QuickRanges) == 0 ||
		len(usage.Presentation.DateTimeOptions) == 0 {
		return errors.New("usage 默认值或展示限制无效")
	}
	if _, exists := runtime.ProviderAccounts[usage.Balance.AccountID]; !exists {
		return fmt.Errorf(
			"usage.balance.account_id 引用了未知 Provider 账户 %q",
			usage.Balance.AccountID,
		)
	}
	switch usage.Balance.SnapshotWritePolicy {
	case "best-effort", "required":
	default:
		return fmt.Errorf(
			"usage.balance.snapshot_write_policy 无效: %q",
			usage.Balance.SnapshotWritePolicy,
		)
	}
	if _, err := time.LoadLocation(usage.Validation.Timezone); err != nil {
		return fmt.Errorf("usage.validation.timezone 无效: %w", err)
	}
	if _, err := regexp.Compile(usage.Validation.DatePattern); err != nil {
		return fmt.Errorf("usage.validation.date_pattern 无效: %w", err)
	}
	seenRanges := map[int]struct{}{}
	for _, quickRange := range usage.Presentation.QuickRanges {
		if quickRange.Days <= 0 ||
			strings.TrimSpace(quickRange.Label) == "" {
			return errors.New("usage.presentation.quick_ranges 配置无效")
		}
		if _, exists := seenRanges[quickRange.Days]; exists {
			return errors.New(
				"usage.presentation.quick_ranges 包含重复天数",
			)
		}
		seenRanges[quickRange.Days] = struct{}{}
	}
	for _, key := range requiredAdministrationUsageTextKeys {
		if strings.TrimSpace(usage.Presentation.Text[key]) == "" {
			return fmt.Errorf(
				"usage.presentation.text[%q] 不能为空",
				key,
			)
		}
	}
	return nil
}

func validateAdministrationUserAssets(
	schema AdministrationUserAssetsSchema,
	runtime RuntimeSchema,
	authentication AuthenticationSchema,
) error {
	validateFields := func(group string, fields map[string]string) error {
		seen := make(map[string]string, len(fields))
		for name, value := range fields {
			value = strings.TrimSpace(value)
			if value == "" {
				return fmt.Errorf(
					"user_assets.api.%s.%s 不能为空",
					group,
					name,
				)
			}
			if previous, exists := seen[value]; exists {
				return fmt.Errorf(
					"user_assets.api.%s.%s 与 %s 不能同为 %q",
					group,
					previous,
					name,
					value,
				)
			}
			seen[value] = name
		}
		return nil
	}
	for group, fields := range map[string]map[string]string{
		"envelope_fields": {
			"error":       schema.API.EnvelopeFields.Error,
			"user":        schema.API.EnvelopeFields.User,
			"summary":     schema.API.EnvelopeFields.Summary,
			"uploads":     schema.API.EnvelopeFields.Uploads,
			"generations": schema.API.EnvelopeFields.Generations,
			"downloads":   schema.API.EnvelopeFields.Downloads,
		},
		"page_fields": {
			"data":      schema.API.PageFields.Data,
			"total":     schema.API.PageFields.Total,
			"page":      schema.API.PageFields.Page,
			"page_size": schema.API.PageFields.PageSize,
		},
		"user_fields": {
			"id":         schema.API.UserFields.ID,
			"username":   schema.API.UserFields.Username,
			"nickname":   schema.API.UserFields.Nickname,
			"email":      schema.API.UserFields.Email,
			"name":       schema.API.UserFields.Name,
			"is_admin":   schema.API.UserFields.IsAdmin,
			"is_active":  schema.API.UserFields.IsActive,
			"created_at": schema.API.UserFields.CreatedAt,
			"last_login": schema.API.UserFields.LastLogin,
		},
		"summary_fields": {
			"total_uploads":       schema.API.SummaryFields.TotalUploads,
			"total_generations":   schema.API.SummaryFields.TotalGenerations,
			"total_downloads":     schema.API.SummaryFields.TotalDownloads,
			"total_storage_bytes": schema.API.SummaryFields.TotalStorageBytes,
		},
		"upload_fields": {
			"id":                schema.API.UploadFields.ID,
			"asset_type":        schema.API.UploadFields.AssetType,
			"original_filename": schema.API.UploadFields.OriginalFilename,
			"stored_filename":   schema.API.UploadFields.StoredFilename,
			"mime_type":         schema.API.UploadFields.MimeType,
			"file_size":         schema.API.UploadFields.FileSize,
			"storage_path":      schema.API.UploadFields.StoragePath,
			"created_at":        schema.API.UploadFields.CreatedAt,
		},
		"generation_fields": {
			"id":                schema.API.GenerationFields.ID,
			"task_type":         schema.API.GenerationFields.TaskType,
			"task_id":           schema.API.GenerationFields.TaskID,
			"task_status":       schema.API.GenerationFields.TaskStatus,
			"result_urls":       schema.API.GenerationFields.ResultURLs,
			"params":            schema.API.GenerationFields.Params,
			"estimated_credits": schema.API.GenerationFields.EstimatedCredits,
			"thumbnail_url":     schema.API.GenerationFields.ThumbnailURL,
			"created_at":        schema.API.GenerationFields.CreatedAt,
			"finished_at":       schema.API.GenerationFields.FinishedAt,
		},
		"download_fields": {
			"id":            schema.API.DownloadFields.ID,
			"action":        schema.API.DownloadFields.Action,
			"resource_type": schema.API.DownloadFields.ResourceType,
			"resource_id":   schema.API.DownloadFields.ResourceID,
			"detail":        schema.API.DownloadFields.Detail,
			"created_at":    schema.API.DownloadFields.CreatedAt,
		},
	} {
		if err := validateFields(group, fields); err != nil {
			return err
		}
	}
	query := map[string]string{
		"page_size":        schema.API.Query.PageSize,
		"uploads_page":     schema.API.Query.UploadsPage,
		"generations_page": schema.API.Query.GenerationsPage,
		"downloads_page":   schema.API.Query.DownloadsPage,
		"from":             schema.API.Query.From,
		"to":               schema.API.Query.To,
	}
	if err := validateFields("query", query); err != nil {
		return err
	}
	for name, value := range query {
		if strings.ContainsAny(value, " ?&=/#") {
			return fmt.Errorf("user_assets.api.query.%s 非法", name)
		}
	}
	pathParameter := strings.TrimSpace(schema.API.PathParameters.UserID)
	if pathParameter == "" || strings.ContainsAny(pathParameter, "{} /?#") {
		return errors.New("user_assets.api.path_parameters.user_id 非法")
	}
	if parameters := configuredRouteParameters(
		runtime.HTTP.LegacyRoutes.AdminUserAssets,
	); len(parameters) != 1 || parameters[0] != pathParameter {
		return errors.New(
			"runtime.http.legacy_routes.admin_user_assets 必须只声明 user_assets.api.path_parameters.user_id",
		)
	}
	permissionFound := false
	for _, permission := range authentication.Permissions {
		if permission.Key == schema.Permission {
			permissionFound = true
			break
		}
	}
	if !permissionFound {
		return fmt.Errorf(
			"user_assets.permission 引用了未知 permission %q",
			schema.Permission,
		)
	}
	if schema.Limits.DefaultPage <= 0 ||
		schema.Limits.DefaultPageSize <= 0 ||
		schema.Limits.MaximumPageSize < schema.Limits.DefaultPageSize ||
		schema.Limits.FrontendPageSize <= 0 ||
		schema.Limits.FrontendPageSize > schema.Limits.MaximumPageSize ||
		schema.Limits.DefaultRecentDays <= 0 {
		return errors.New("user_assets.limits 配置无效")
	}
	if len(schema.Filters.DownloadActions) == 0 {
		return errors.New("user_assets.filters.download_actions 不能为空")
	}
	for name, value := range map[string]string{
		"image_asset_type": schema.Filters.ImageAssetType,
		"model_asset_type": schema.Filters.ModelAssetType,
		"image_media_type_prefix": schema.Filters.
			ImageMediaTypePrefix,
		"generated_image_resource_type": schema.Filters.
			GeneratedImageResourceType,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("user_assets.filters.%s 不能为空", name)
		}
	}
	seenActions := make(map[string]struct{}, len(schema.Filters.DownloadActions))
	for _, action := range schema.Filters.DownloadActions {
		if strings.TrimSpace(action.Action) == "" ||
			strings.TrimSpace(action.Label) == "" ||
			strings.TrimSpace(action.TextClass) == "" ||
			strings.TrimSpace(action.DotClass) == "" {
			return errors.New(
				"user_assets.filters.download_actions 配置无效",
			)
		}
		if _, exists := seenActions[action.Action]; exists {
			return fmt.Errorf(
				"user_assets.filters.download_actions 重复声明 %q",
				action.Action,
			)
		}
		seenActions[action.Action] = struct{}{}
	}
	if strings.TrimSpace(schema.Messages.UserNotFound) == "" ||
		strings.TrimSpace(schema.Messages.InternalError) == "" {
		return errors.New("user_assets.messages 配置无效")
	}
	presentation := schema.Presentation
	if presentation.LoadingRows <= 0 ||
		presentation.FilenamePreviewCharacters <= 0 ||
		presentation.TaskIDPreviewCharacters <= 0 ||
		presentation.ResourceIDPreviewCharacters <= 0 ||
		strings.TrimSpace(presentation.PreviewSuffix) == "" ||
		strings.TrimSpace(presentation.EmptyPlaceholder) == "" ||
		strings.TrimSpace(presentation.DateLocale) == "" ||
		len(presentation.DateTimeOptions) == 0 ||
		presentation.BytesPerUnit <= 1 ||
		presentation.SizeDecimalPlaces < 0 ||
		len(presentation.SizeUnits) == 0 {
		return errors.New("user_assets.presentation 配置无效")
	}
	for _, unit := range presentation.SizeUnits {
		if strings.TrimSpace(unit) == "" {
			return errors.New(
				"user_assets.presentation.size_units 包含空值",
			)
		}
	}
	for _, assetType := range []string{"image", "model"} {
		if strings.TrimSpace(presentation.AssetTypeLabels[assetType]) == "" {
			return fmt.Errorf(
				"user_assets.presentation.asset_type_labels 缺少 %q",
				assetType,
			)
		}
	}
	allowedDateParts := map[string]struct{}{
		"weekday": {}, "year": {}, "month": {}, "day": {},
		"hour": {}, "minute": {}, "second": {},
	}
	allowedDateValues := map[string]struct{}{
		"numeric": {}, "2-digit": {}, "long": {}, "short": {}, "narrow": {},
	}
	for part, value := range presentation.DateTimeOptions {
		if _, exists := allowedDateParts[part]; !exists {
			return fmt.Errorf(
				"user_assets.presentation.date_time_options 字段非法: %q",
				part,
			)
		}
		if _, exists := allowedDateValues[value]; !exists {
			return fmt.Errorf(
				"user_assets.presentation.date_time_options[%q] 非法: %q",
				part,
				value,
			)
		}
	}
	for _, key := range requiredAdministrationUserAssetsTextKeys {
		if strings.TrimSpace(presentation.Text[key]) == "" {
			return fmt.Errorf(
				"user_assets.presentation.text[%q] 不能为空",
				key,
			)
		}
	}
	return nil
}

func validateAdministrationAuditLogs(
	auditLogs AdministrationAuditLogsSchema,
	runtime RuntimeSchema,
	authentication AuthenticationSchema,
) error {
	fields := map[string]string{
		"data":          auditLogs.API.Fields.Data,
		"error":         auditLogs.API.Fields.Error,
		"total":         auditLogs.API.Fields.Total,
		"page":          auditLogs.API.Fields.Page,
		"page_size":     auditLogs.API.Fields.PageSize,
		"id":            auditLogs.API.Fields.ID,
		"action":        auditLogs.API.Fields.Action,
		"user_id":       auditLogs.API.Fields.UserID,
		"user_name":     auditLogs.API.Fields.UserName,
		"resource_type": auditLogs.API.Fields.ResourceType,
		"resource_id":   auditLogs.API.Fields.ResourceID,
		"detail":        auditLogs.API.Fields.Detail,
		"ip_address":    auditLogs.API.Fields.IPAddress,
		"user_agent":    auditLogs.API.Fields.UserAgent,
		"created_at":    auditLogs.API.Fields.CreatedAt,
	}
	seenFields := make(map[string]string, len(fields))
	for name, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return fmt.Errorf("audit_logs.api.fields.%s 不能为空", name)
		}
		if previous, exists := seenFields[field]; exists {
			return fmt.Errorf(
				"audit_logs.api.fields.%s 与 %s 不能同为 %q",
				previous,
				name,
				field,
			)
		}
		seenFields[field] = name
	}
	query := map[string]string{
		"page":      auditLogs.API.Query.Page,
		"page_size": auditLogs.API.Query.PageSize,
		"user_id":   auditLogs.API.Query.UserID,
		"action":    auditLogs.API.Query.Action,
		"from":      auditLogs.API.Query.From,
		"to":        auditLogs.API.Query.To,
	}
	seenQuery := make(map[string]string, len(query))
	for name, field := range query {
		field = strings.TrimSpace(field)
		if field == "" || strings.ContainsAny(field, " ?&=/#") {
			return fmt.Errorf("audit_logs.api.query.%s 非法", name)
		}
		if previous, exists := seenQuery[field]; exists {
			return fmt.Errorf(
				"audit_logs.api.query.%s 与 %s 不能同为 %q",
				previous,
				name,
				field,
			)
		}
		seenQuery[field] = name
	}
	if len(configuredRouteParameters(
		runtime.HTTP.LegacyRoutes.AdminAuditLogs,
	)) != 0 {
		return errors.New(
			"runtime.http.legacy_routes.admin_audit_logs 不得声明路径参数",
		)
	}
	permissionFound := false
	for _, permission := range authentication.Permissions {
		if permission.Key == auditLogs.Permission {
			permissionFound = true
			break
		}
	}
	if !permissionFound {
		return fmt.Errorf(
			"audit_logs.permission 引用了未知 permission %q",
			auditLogs.Permission,
		)
	}
	limits := auditLogs.Limits
	if limits.DefaultPage <= 0 ||
		limits.DefaultPageSize <= 0 ||
		limits.MaximumPageSize < limits.DefaultPageSize {
		return errors.New("audit_logs.limits 的分页配置无效")
	}
	if auditLogs.WritePolicy.MaximumAttempts <= 0 ||
		auditLogs.WritePolicy.RetryDelayMilliseconds < 0 {
		return errors.New("audit_logs.write_policy 配置无效")
	}
	presentation := auditLogs.Presentation
	if presentation.DefaultRecentDays <= 0 ||
		presentation.LoadingRows <= 0 ||
		presentation.UserIDPreviewCharacters <= 0 ||
		presentation.ResourceIDPreviewCharacters <= 0 {
		return errors.New("audit_logs.presentation 的数量配置必须大于 0")
	}
	for name, value := range map[string]string{
		"messages.internal_error": auditLogs.Messages.InternalError,
		"defaults.id_prefix":      auditLogs.Defaults.IDPrefix,
		"request_headers.forwarded_for": auditLogs.RequestHeaders.
			ForwardedFor,
		"request_headers.real_ip": auditLogs.RequestHeaders.RealIP,
		"request_headers.user_agent": auditLogs.RequestHeaders.
			UserAgent,
		"preview_suffix":      presentation.PreviewSuffix,
		"empty_placeholder":   presentation.EmptyPlaceholder,
		"date_locale":         presentation.DateLocale,
		"default_badge_class": presentation.DefaultBadgeClass,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("audit_logs.%s 不能为空", name)
		}
	}
	if len(presentation.DateTimeOptions) == 0 {
		return errors.New(
			"audit_logs.presentation.date_time_options 不能为空",
		)
	}
	allowedDateParts := map[string]struct{}{
		"weekday": {}, "year": {}, "month": {}, "day": {},
		"hour": {}, "minute": {}, "second": {},
	}
	allowedDateValues := map[string]struct{}{
		"numeric": {}, "2-digit": {}, "long": {}, "short": {}, "narrow": {},
	}
	for part, value := range presentation.DateTimeOptions {
		if _, exists := allowedDateParts[part]; !exists {
			return fmt.Errorf(
				"audit_logs.presentation.date_time_options 的字段非法: %q",
				part,
			)
		}
		if _, exists := allowedDateValues[value]; !exists {
			return fmt.Errorf(
				"audit_logs.presentation.date_time_options[%q] 非法: %q",
				part,
				value,
			)
		}
	}
	if len(presentation.ActionBadgeClasses) == 0 {
		return errors.New(
			"audit_logs.presentation.action_badge_classes 不能为空",
		)
	}
	for action, className := range presentation.ActionBadgeClasses {
		if strings.TrimSpace(action) == "" ||
			strings.TrimSpace(className) == "" {
			return errors.New(
				"audit_logs.presentation.action_badge_classes 非法",
			)
		}
	}
	for prefix, className := range presentation.ActionPrefixBadgeClasses {
		if strings.TrimSpace(prefix) == "" ||
			strings.TrimSpace(className) == "" {
			return errors.New(
				"audit_logs.presentation.action_prefix_badge_classes 非法",
			)
		}
	}
	for _, key := range requiredAdministrationAuditLogTextKeys {
		if strings.TrimSpace(presentation.Text[key]) == "" {
			return fmt.Errorf(
				"audit_logs.presentation.text[%q] 不能为空",
				key,
			)
		}
	}
	return nil
}

func validateAdministrationGenerations(
	generations AdministrationGenerationsSchema,
	runtime RuntimeSchema,
	authentication AuthenticationSchema,
) error {
	fields := map[string]string{
		"data":              generations.API.Fields.Data,
		"error":             generations.API.Fields.Error,
		"total":             generations.API.Fields.Total,
		"page":              generations.API.Fields.Page,
		"page_size":         generations.API.Fields.PageSize,
		"id":                generations.API.Fields.ID,
		"user_id":           generations.API.Fields.UserID,
		"user_name":         generations.API.Fields.UserName,
		"user_email":        generations.API.Fields.UserEmail,
		"task_type":         generations.API.Fields.TaskType,
		"task_id":           generations.API.Fields.TaskID,
		"estimated_credits": generations.API.Fields.EstimatedCredits,
		"task_status":       generations.API.Fields.TaskStatus,
		"params":            generations.API.Fields.Params,
		"result_urls":       generations.API.Fields.ResultURLs,
		"thumbnail_url":     generations.API.Fields.ThumbnailURL,
		"created_at":        generations.API.Fields.CreatedAt,
		"finished_at":       generations.API.Fields.FinishedAt,
		"ledger":            generations.API.Fields.Ledger,
		"task_cache":        generations.API.Fields.TaskCache,
		"status":            generations.API.Fields.Status,
		"provider_id":       generations.API.Fields.ProviderID,
		"cache_data":        generations.API.Fields.CacheData,
		"cached_at":         generations.API.Fields.CachedAt,
		"updated_at":        generations.API.Fields.UpdatedAt,
		"redirect":          generations.API.Fields.Redirect,
	}
	for name, field := range fields {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("generations.api.fields.%s 不能为空", name)
		}
	}
	queryFields := map[string]string{
		"page":      generations.API.Query.Page,
		"page_size": generations.API.Query.PageSize,
		"user_id":   generations.API.Query.UserID,
		"task_type": generations.API.Query.TaskType,
		"status":    generations.API.Query.Status,
		"from":      generations.API.Query.From,
		"to":        generations.API.Query.To,
	}
	seenQueryFields := make(map[string]string, len(queryFields))
	for name, field := range queryFields {
		field = strings.TrimSpace(field)
		if field == "" {
			return fmt.Errorf("generations.api.query.%s 不能为空", name)
		}
		if previous, exists := seenQueryFields[field]; exists {
			return fmt.Errorf(
				"generations.api.query.%s 与 %s 不能同为 %q",
				previous,
				name,
				field,
			)
		}
		seenQueryFields[field] = name
	}

	pathParameter := strings.TrimSpace(
		generations.API.PathParameters.TaskID,
	)
	if pathParameter == "" ||
		strings.ContainsAny(pathParameter, "{} /?#") {
		return errors.New(
			"generations.api.path_parameters.task_id 非法",
		)
	}
	if len(configuredRouteParameters(
		runtime.HTTP.LegacyRoutes.AdminGenerations,
	)) != 0 {
		return errors.New(
			"runtime.http.legacy_routes.admin_generations 不得声明路径参数",
		)
	}
	for name, route := range map[string]string{
		"admin_generation": runtime.HTTP.LegacyRoutes.AdminGeneration,
		"admin_generation_download": runtime.HTTP.LegacyRoutes.
			AdminGenerationDownload,
	} {
		parameters := configuredRouteParameters(route)
		if len(parameters) != 1 || parameters[0] != pathParameter {
			return fmt.Errorf(
				"runtime.http.legacy_routes.%s 必须只声明 generations.api.path_parameters.task_id",
				name,
			)
		}
	}

	permissionFound := false
	for _, permission := range authentication.Permissions {
		if permission.Key == generations.Permission {
			permissionFound = true
			break
		}
	}
	if !permissionFound {
		return fmt.Errorf(
			"generations.permission 引用了未知 permission %q",
			generations.Permission,
		)
	}
	if generations.Limits.DefaultPage <= 0 ||
		generations.Limits.DefaultPageSize <= 0 ||
		generations.Limits.MaximumPageSize <
			generations.Limits.DefaultPageSize ||
		generations.Limits.DefaultRecentDays <= 0 {
		return errors.New("generations.limits 配置无效")
	}
	for name, value := range map[string]string{
		"audit_download_action": generations.Audit.DownloadAction,
		"audit_resource_type":   generations.Audit.ResourceType,
		"thumbnail_role":        generations.Assets.ThumbnailRole,
		"download_filename_template": generations.Assets.
			DownloadFilenameTemplate,
		"asset_filename_template": generations.Assets.
			AssetFilenameTemplate,
		"variant_separator":   generations.Assets.VariantSeparator,
		"duplicate_separator": generations.Assets.DuplicateSeparator,
		"archive_media_type":  generations.Assets.ArchiveMediaType,
		"default_media_type":  generations.Assets.DefaultMediaType,
		"unknown_extension":   generations.Assets.UnknownExtension,
		"legacy_cache_root_setting": generations.Assets.
			LegacyCacheRootSetting,
		"legacy_cache_metadata_suffix": generations.Assets.
			LegacyCacheMetadataSuffix,
		"legacy_cache_content_type_field": generations.Assets.
			LegacyCacheContentTypeField,
		"remote_thumbnail_url_query": generations.Assets.
			RemoteThumbnailURLQuery,
		"remote_thumbnail_task_id_query": generations.Assets.
			RemoteThumbnailTaskIDQuery,
		"task_not_found":  generations.Messages.TaskNotFound,
		"cache_not_found": generations.Messages.CacheNotFound,
		"no_downloadable_assets": generations.Messages.
			NoDownloadableAssets,
		"all_downloads_failed": generations.Messages.
			AllDownloadsFailed,
		"invalid_request": generations.Messages.InvalidRequest,
		"internal_error":  generations.Messages.InternalError,
		"download_failed": generations.Messages.DownloadFailed,
		"unknown_download_response": generations.Messages.
			UnknownDownloadResponse,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("generations 配置 %s 不能为空", name)
		}
	}
	if !strings.Contains(
		generations.Assets.DownloadFilenameTemplate,
		"{task_id}",
	) {
		return errors.New(
			"generations.assets.download_filename_template 必须包含 {task_id}",
		)
	}
	for _, token := range []string{
		"{role}",
		"{variant_segment}",
		"{ordinal}",
		"{extension}",
	} {
		if !strings.Contains(
			generations.Assets.AssetFilenameTemplate,
			token,
		) {
			return fmt.Errorf(
				"generations.assets.asset_filename_template 缺少 %s",
				token,
			)
		}
	}
	if generations.Assets.OrdinalBase <= 0 ||
		generations.Assets.MaximumPackageBytes <= 0 ||
		generations.Assets.LegacyCachePrefixCharacters <= 0 ||
		generations.Assets.RemoteDownloadTimeoutMilliseconds <= 0 ||
		generations.Assets.MaximumRemoteRedirects <= 0 ||
		len(generations.Assets.MediaTypeExtensions) == 0 ||
		len(generations.Assets.DownloadExcludedRoles) == 0 {
		return errors.New("generations.assets 下载配置无效")
	}
	legacyCacheRootSetting := generations.Assets.LegacyCacheRootSetting
	if _, exists := runtime.Configuration.
		Settings[legacyCacheRootSetting]; !exists {
		return errors.New(
			"generations.assets.legacy_cache_root_setting 引用了未知运行配置",
		)
	}
	for mediaType, extension := range generations.Assets.
		MediaTypeExtensions {
		if strings.TrimSpace(mediaType) == "" ||
			!strings.HasPrefix(strings.TrimSpace(extension), ".") {
			return errors.New(
				"generations.assets.media_type_extensions 配置无效",
			)
		}
	}
	seenExcludedRoles := map[string]struct{}{}
	for _, role := range generations.Assets.DownloadExcludedRoles {
		role = strings.TrimSpace(role)
		if role == "" {
			return errors.New(
				"generations.assets.download_excluded_roles 包含空角色",
			)
		}
		if _, exists := seenExcludedRoles[role]; exists {
			return errors.New(
				"generations.assets.download_excluded_roles 包含重复角色",
			)
		}
		seenExcludedRoles[role] = struct{}{}
	}
	if generations.Assets.RemoteThumbnailURLQuery ==
		generations.Assets.RemoteThumbnailTaskIDQuery ||
		len(generations.Assets.PreviewImageExtensions) == 0 {
		return errors.New("generations.assets 远程缩略图配置无效")
	}
	for _, extension := range generations.Assets.PreviewImageExtensions {
		if strings.Trim(strings.TrimSpace(extension), ".") == "" {
			return errors.New(
				"generations.assets.preview_image_extensions 包含空扩展名",
			)
		}
	}
	presentation := generations.Presentation
	if presentation.LoadingRows <= 0 ||
		presentation.UserIDPreviewCharacters <= 0 ||
		presentation.TaskIDPreviewCharacters <= 0 ||
		strings.TrimSpace(presentation.PreviewSuffix) == "" ||
		strings.TrimSpace(presentation.EmptyPlaceholder) == "" ||
		strings.TrimSpace(presentation.DateLocale) == "" ||
		len(presentation.DateTimeOptions) == 0 {
		return errors.New("generations.presentation 配置无效")
	}
	allowedDateParts := map[string]struct{}{
		"weekday": {}, "year": {}, "month": {}, "day": {},
		"hour": {}, "minute": {}, "second": {},
	}
	allowedDateValues := map[string]struct{}{
		"numeric": {}, "2-digit": {}, "long": {}, "short": {}, "narrow": {},
	}
	for part, value := range presentation.DateTimeOptions {
		if _, exists := allowedDateParts[part]; !exists {
			return fmt.Errorf(
				"generations.presentation.date_time_options 字段非法: %q",
				part,
			)
		}
		if _, exists := allowedDateValues[value]; !exists {
			return fmt.Errorf(
				"generations.presentation.date_time_options[%q] 非法: %q",
				part,
				value,
			)
		}
	}
	for _, key := range requiredAdministrationGenerationTextKeys {
		if strings.TrimSpace(presentation.Text[key]) == "" {
			return fmt.Errorf(
				"generations.presentation.text[%q] 不能为空",
				key,
			)
		}
	}
	return nil
}
