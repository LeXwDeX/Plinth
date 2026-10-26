package catalog

// Public content and presentation configuration validation.
import (
	"errors"
	"fmt"
	"strings"
)

// validateCountForms enforces the project copy contract for localized count
// messages: every base message containing {count} declares `<key>_one` with
// the same placeholders, and every bundle keeps that pair. This is stricter
// than English grammar: a text whose wording does not change with the count
// repeats the same text in `_one`. It is not a general grammar classifier.
func validateCountForms(schema ApplicationSchema) error {
	countKeys := make([]string, 0)
	for key, value := range schema.Content.Messages {
		if strings.HasSuffix(key, "_one") || !strings.Contains(value, "{count}") {
			continue
		}
		one, exists := schema.Content.Messages[key+"_one"]
		if !exists || strings.TrimSpace(one) == "" {
			return fmt.Errorf("计数文案 %q 缺少单数形式 %q", key, key+"_one")
		}
		if !strings.Contains(one, "{count}") {
			return fmt.Errorf("单数文案 %q 缺少 {count}", key+"_one")
		}
		if !samePlaceholders(value, one) {
			return fmt.Errorf("计数文案 %q 与 %q 占位符不一致", key, key+"_one")
		}
		countKeys = append(countKeys, key)
	}
	for key, value := range schema.Content.Messages {
		if !strings.HasSuffix(key, "_one") || !strings.Contains(value, "{count}") {
			continue
		}
		base := strings.TrimSuffix(key, "_one")
		general, exists := schema.Content.Messages[base]
		if !exists {
			return fmt.Errorf("单数文案 %q 缺少一般形式 %q", key, base)
		}
		if !strings.Contains(general, "{count}") {
			return fmt.Errorf("单数文案 %q 的一般形式 %q 缺少 {count}", key, base)
		}
	}
	if schema.Localization == nil {
		return nil
	}
	for locale, bundle := range schema.Localization.Bundles {
		for _, key := range countKeys {
			translated, hasBase := bundle.Messages[key]
			translatedOne, hasOne := bundle.Messages[key+"_one"]
			if !hasBase && !hasOne {
				continue
			}
			if !hasBase || !hasOne {
				return fmt.Errorf("bundles.%s.messages 的计数文案 %q 必须同时声明 %q 与 %q", locale, key, key, key+"_one")
			}
			if !samePlaceholders(translated, translatedOne) {
				return fmt.Errorf("bundles.%s.messages 的 %q 与 %q 占位符不一致", locale, key, key+"_one")
			}
		}
	}
	return nil
}

func validateContent(schema ContentSchema) error {
	required := []string{
		"error.internal",
		"error.invalid_capability",
		"error.invalid_input",
		"error.unknown_offering",
		"error.no_offering",
		"error.idempotency_conflict",
		"error.not_found",
		"error.conflict",
		"error.unavailable",
		"error.invalid_json",
		"error.trailing_json",
		"error.required_parameter",
		"error.prompt_enhance_required",
		"error.prompt_enhance_failed",
		"error.provider_balance_failed",
		"error.task_parameters_encode",
		"error.task_not_found",
		"error.task_parameters_corrupt",
		"error.input_asset_service_unavailable",
		"error.multipart_required",
		"error.invalid_upload",
		"error.forbidden",
		"error.legacy_asset_missing_upload",
		"error.legacy_asset_upload_too_large",
		"error.legacy_asset_upload_failed",
		"error.legacy_asset_not_found",
		"error.legacy_asset_forbidden",
		"error.legacy_asset_get_failed",
		"error.legacy_task_delete_not_found",
		"error.legacy_task_delete_failed",
		"error.limit_positive",
		"error.after_nonnegative",
		"ui.header.menu_toggle",
		"ui.header.failures_show_title",
		"ui.header.failures_hide_title",
		"ui.header.failures_hidden",
		"ui.header.failures_visible",
		"ui.header.cache_count",
		"ui.header.cache_tooltip",
		"ui.header.cache_tooltip_details",
		"ui.header.cache_item",
		"ui.header.cache_task_id",
		"ui.header.unknown_task",
		"ui.header.unknown_task_id",
		"ui.header.unknown_asset",
		"ui.header.logout",
		"ui.header.logout_success",
		"ui.header.logout_failed",
		"ui.header.default_user_initial",
		"ui.asset.model",
		"ui.asset.texture",
		"ui.asset.animation",
		"ui.asset.result",
		"ui.workbench.prompt_section",
		"ui.workbench.parameters_section",
		"ui.workbench.enhance",
		"ui.workbench.regenerate",
		"ui.workbench.progress",
		"ui.workbench.task_id_prefix",
		"ui.workbench.not_found",
		"ui.workbench.result",
		"ui.workbench.prompt_required",
		"ui.workbench.enhance_prompt_required",
		"ui.workbench.enhance_failed",
		"ui.workbench.enhance_no_change",
		"ui.workbench.enhance_success",
		"ui.task_progress.retry_badge",
		"ui.task_progress.retry_imminent",
		"ui.task_progress.retry_seconds",
		"ui.task_progress.retry_minutes",
		"ui.task_progress.retry_hours",
		"ui.task_progress.percentage",
		"ui.text_to_image.description",
		"ui.text_to_image.positive_prompt_placeholder",
		"ui.text_to_image.positive_prompt_label",
		"ui.text_to_image.negative_prompt_placeholder",
		"ui.text_to_image.negative_prompt_label",
		"ui.text_to_image.generate",
		"ui.text_to_image.task_created",
		"ui.text_to_image.create_failed",
		"ui.text_to_image.empty",
		"behavior.image_to_image.multi_reference_prompt",
		"ui.image_to_image.description",
		"ui.image_to_image.reference_section",
		"ui.image_to_image.reference_description",
		"ui.image_to_image.reference_target",
		"ui.image_to_image.reference_style_suffix",
		"ui.image_to_image.reference_required",
		"ui.image_to_image.positive_prompt_placeholder",
		"ui.image_to_image.positive_prompt_label",
		"ui.image_to_image.negative_prompt_placeholder",
		"ui.image_to_image.negative_prompt_label",
		"ui.image_to_image.convert",
		"ui.image_to_image.reconvert",
		"ui.image_to_image.task_created",
		"ui.image_to_image.create_failed",
		"ui.image_to_image.aspect_ratio_warning",
		"ui.image_to_image.result",
		"ui.image_to_image.empty",
		"ui.providers.load_failed",
		"ui.tasks.title",
		"ui.tasks.description",
		"ui.tasks.all",
		"ui.tasks.total_prefix",
		"ui.tasks.record_suffix",
		"ui.tasks.current_filter_prefix",
		"ui.tasks.search_placeholder",
		"ui.tasks.empty",
		"ui.pagination.previous",
		"ui.pagination.next",
		"ui.pagination.page_prefix",
		"ui.pagination.page_separator",
		"ui.pagination.page_suffix",
		"failure.invalid_input",
		"failure.authentication",
		"failure.rate_limited",
		"failure.transport",
		"failure.timed_out",
		"failure.rejected",
		"failure.insufficient_balance",
		"failure.upstream_unavailable",
		"failure.submission_unknown",
		"failure.integrity_failure",
		"failure.protocol",
		"failure.internal",
		"failure.source_expired",
	}
	if len(schema.Messages) == 0 {
		return errors.New("messages 不能为空")
	}
	if strings.TrimSpace(schema.Brand.Title) == "" ||
		strings.TrimSpace(schema.Brand.MetadataTitle) == "" ||
		strings.TrimSpace(schema.Brand.MetadataDescription) == "" ||
		strings.TrimSpace(schema.Brand.VersionPrefix) == "" {
		return errors.New("brand 的 title 和 metadata 文本不能为空")
	}
	if strings.TrimSpace(schema.Service.ID) == "" ||
		strings.TrimSpace(schema.Service.Name) == "" ||
		strings.TrimSpace(schema.Service.Version) == "" {
		return errors.New("service 的 id、name 和 version 不能为空")
	}
	if strings.TrimSpace(schema.Home.Title) == "" ||
		strings.TrimSpace(schema.Home.Description) == "" ||
		len(schema.Home.Links) == 0 {
		return errors.New("home 的 title、description 和 links 不能为空")
	}
	homeLinks := map[string]struct{}{}
	for index, link := range schema.Home.Links {
		if !strings.HasPrefix(link.Href, "/") ||
			strings.TrimSpace(link.Label) == "" {
			return fmt.Errorf("home.links[%d] 的 href 和 label 无效", index)
		}
		if _, exists := homeLinks[link.Href]; exists {
			return fmt.Errorf("home link href 重复: %q", link.Href)
		}
		homeLinks[link.Href] = struct{}{}
	}
	if len(schema.Navigation) == 0 {
		return errors.New("navigation 不能为空")
	}
	navigationKeys := map[string]struct{}{}
	navigationHrefs := map[string]struct{}{}
	navigationOrders := map[int]struct{}{}
	for index, item := range schema.Navigation {
		if strings.TrimSpace(item.Key) == "" ||
			strings.TrimSpace(item.Label) == "" ||
			strings.TrimSpace(item.Icon) == "" ||
			!strings.HasPrefix(item.Href, "/") ||
			item.Order < 0 {
			return fmt.Errorf("navigation[%d] 的 key、label、href、icon 或 order 无效", index)
		}
		if item.Placement != "main" && item.Placement != "bottom" {
			return fmt.Errorf("navigation[%d].placement 无效: %q", index, item.Placement)
		}
		if _, exists := navigationKeys[item.Key]; exists {
			return fmt.Errorf("navigation key 重复: %q", item.Key)
		}
		if _, exists := navigationHrefs[item.Href]; exists {
			return fmt.Errorf("navigation href 重复: %q", item.Href)
		}
		if _, exists := navigationOrders[item.Order]; exists {
			return fmt.Errorf("navigation order 重复: %d", item.Order)
		}
		navigationKeys[item.Key] = struct{}{}
		navigationHrefs[item.Href] = struct{}{}
		navigationOrders[item.Order] = struct{}{}
	}
	if len(schema.AssetKinds) == 0 {
		return errors.New("asset_kinds 不能为空")
	}
	assetKinds := map[string]struct{}{}
	for index, item := range schema.AssetKinds {
		if strings.TrimSpace(item.Value) == "" ||
			strings.TrimSpace(item.Label) == "" {
			return fmt.Errorf("asset_kinds[%d] 的 value 和 label 不能为空", index)
		}
		if _, exists := assetKinds[item.Value]; exists {
			return fmt.Errorf("asset kind 重复: %q", item.Value)
		}
		assetKinds[item.Value] = struct{}{}
	}
	if len(schema.AssetKindPatterns) == 0 {
		return errors.New("asset_kind_patterns 不能为空")
	}
	assetKindPrefixes := map[string]struct{}{}
	for index, item := range schema.AssetKindPatterns {
		if strings.TrimSpace(item.Prefix) == "" ||
			strings.Contains(item.Prefix, ":") ||
			strings.TrimSpace(item.MessageKey) == "" {
			return fmt.Errorf(
				"asset_kind_patterns[%d] 的 prefix 或 message_key 无效",
				index,
			)
		}
		if _, exists := assetKindPrefixes[item.Prefix]; exists {
			return fmt.Errorf("asset kind pattern prefix 重复: %q", item.Prefix)
		}
		if strings.TrimSpace(schema.Messages[item.MessageKey]) == "" {
			return fmt.Errorf(
				"asset kind pattern %q 引用了不存在的 message %q",
				item.Prefix,
				item.MessageKey,
			)
		}
		assetKindPrefixes[item.Prefix] = struct{}{}
	}
	for key, value := range schema.Messages {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return errors.New("messages 的 key 和文本不能为空")
		}
	}
	for _, key := range required {
		if strings.TrimSpace(schema.Messages[key]) == "" {
			return fmt.Errorf("messages 缺少必需文本 %q", key)
		}
	}
	return nil
}
