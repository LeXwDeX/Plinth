package catalog

// Task history presentation and label validation.
import (
	"errors"
	"fmt"
	"strings"
)

func validateTaskHistory(schema TaskHistorySchema) error {
	query := map[string]string{
		"page":          schema.API.Query.Page,
		"page_size":     schema.API.Query.PageSize,
		"recent":        schema.API.Query.Recent,
		"type":          schema.API.Query.Type,
		"search":        schema.API.Query.Search,
		"status":        schema.API.Query.Status,
		"hide_failures": schema.API.Query.HideFailures,
	}
	if err := validateUniqueWireNames("api.query", query); err != nil {
		return err
	}
	fields := map[string]string{
		"data":          schema.API.Fields.Data,
		"total":         schema.API.Fields.Total,
		"page":          schema.API.Fields.Page,
		"page_size":     schema.API.Fields.PageSize,
		"id":            schema.API.Fields.ID,
		"task_type":     schema.API.Fields.TaskType,
		"status":        schema.API.Fields.Status,
		"progress":      schema.API.Fields.Progress,
		"thumbnail_url": schema.API.Fields.ThumbnailURL,
		"created_at":    schema.API.Fields.CreatedAt,
		"prompt":        schema.API.Fields.Prompt,
	}
	if err := validateUniqueWireNames("api.fields", fields); err != nil {
		return err
	}
	if len(schema.Assets.PreviewRoles) == 0 {
		return errors.New("assets.preview_roles 不能为空")
	}
	seenPreviewRoles := make(
		map[string]struct{},
		len(schema.Assets.PreviewRoles),
	)
	for index, role := range schema.Assets.PreviewRoles {
		role = strings.TrimSpace(role)
		if role == "" {
			return fmt.Errorf(
				"assets.preview_roles[%d] 不能为空",
				index,
			)
		}
		if _, exists := seenPreviewRoles[role]; exists {
			return fmt.Errorf(
				"assets.preview_roles[%d] 重复声明 %q",
				index,
				role,
			)
		}
		seenPreviewRoles[role] = struct{}{}
	}
	if err := validateTaskAssetPresentation(schema.Assets); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"all_capability": schema.Values.AllCapability,
		"enabled":        schema.Values.Enabled,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("values.%s 不能为空", name)
		}
	}
	if err := validateTaskHistoryLabels(schema.Labels); err != nil {
		return err
	}
	if r := schema.AssetRecovery; r != nil {
		for key, value := range map[string]string{
			"action":              r.Action,
			"eligibility_loading": r.EligibilityLoading,
			"eligibility_failed":  r.EligibilityFailed,
			"preparing":           r.Preparing,
			"prepare_failed":      r.PrepareFailed,
			"awaiting_approval":   r.AwaitingApproval,
			"approve":             r.Approve,
			"deny":                r.Deny,
			"submitting":          r.Submitting,
			"submit_failed":       r.SubmitFailed,
			"submitted":           r.Submitted,
			"open_recovery_task":  r.OpenRecoveryTask,
			"open_source_task":    r.OpenSourceTask,
			"source_label":        r.SourceLabel,
			"description":         r.Description,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("asset_recovery.%s 不能为空", key)
			}
		}
	}
	for capability, fields := range schema.HiddenFields {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			return errors.New("hidden_fields 键不能为空")
		}
		if len(fields) == 0 {
			return fmt.Errorf(
				"hidden_fields.%s 不能为空",
				capability,
			)
		}
		seenFields := make(map[string]struct{}, len(fields))
		for index, field := range fields {
			field = strings.TrimSpace(field)
			if field == "" {
				return fmt.Errorf(
					"hidden_fields.%s[%d] 不能为空",
					capability,
					index,
				)
			}
			if _, exists := seenFields[field]; exists {
				return fmt.Errorf(
					"hidden_fields.%s[%d] 重复声明 %q",
					capability,
					index,
					field,
				)
			}
			seenFields[field] = struct{}{}
		}
	}
	return nil
}

// validateTaskHistoryLabels 校验任务历史展示标签：models/parameters 的键与
// 值都不能为空；models 仅作展示，不要求（也不得暗示）激活任何 Offering。
func validateTaskHistoryLabels(schema TaskHistoryLabelSchema) error {
	for modelID, label := range schema.Models {
		if strings.TrimSpace(modelID) == "" {
			return errors.New("labels.models 键不能为空")
		}
		if strings.TrimSpace(label) == "" {
			return fmt.Errorf("labels.models.%s 不能为空", modelID)
		}
	}
	for capability, parameters := range schema.Parameters {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			return errors.New("labels.parameters 键不能为空")
		}
		if len(parameters) == 0 {
			return fmt.Errorf(
				"labels.parameters.%s 不能为空",
				capability,
			)
		}
		for name, label := range parameters {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf(
					"labels.parameters.%s 键不能为空",
					capability,
				)
			}
			if strings.TrimSpace(label) == "" {
				return fmt.Errorf(
					"labels.parameters.%s.%s 不能为空",
					capability,
					name,
				)
			}
		}
	}
	return nil
}

// Normalized report values are a versioned public protocol. Its labels remain
// Catalog data, while the browser consumes only these bounded DTO fields.
func validateTaskAssetPresentation(schema TaskHistoryAssetSchema) error {
	if err := validateRigCheckReportPresentation(schema.RigCheckReport); err != nil {
		return err
	}
	a, r := schema.Attachments, schema.PrintReport
	for name, value := range map[string]string{
		"attachments.title": a.Title, "attachments.fallback_role": a.FallbackRole,
		"attachments.format": a.Format, "attachments.media_type": a.MediaType,
		"attachments.size": a.Size, "attachments.unknown_size": a.UnknownSize,
		"attachments.download": a.Download, "attachments.no_preview": a.NoPreview,
		"print_report.format": r.Format, "print_report.title": r.Title,
		"print_report.loading": r.Loading, "print_report.load_failed": r.LoadFailed,
		"print_report.retry": r.Retry, "print_report.unsupported": r.Unsupported,
		"print_report.diagnostic_notice": r.DiagnosticNotice,
		"print_report.unknown_value":     r.UnknownValue, "print_report.not_reported": r.NotReported,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("assets.%s is required", name)
		}
	}
	if !strings.HasPrefix(r.MediaType, "application/vnd.") || !strings.HasSuffix(r.MediaType, "+json") {
		return errors.New("assets.print_report.media_type must name a vendor JSON protocol")
	}
	if r.MaxBytes <= 0 || r.MaxBytes > 64<<10 {
		return errors.New("assets.print_report.max_bytes exceeds the protocol budget")
	}
	validateLabels := func(name string, labels map[string]string, required []string) error {
		if len(labels) == 0 {
			return fmt.Errorf("assets.%s is required", name)
		}
		for key, value := range labels {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				return fmt.Errorf("assets.%s contains an empty label", name)
			}
		}
		for _, key := range required {
			if strings.TrimSpace(labels[key]) == "" {
				return fmt.Errorf("assets.%s.%s is required", name, key)
			}
		}
		return nil
	}
	if err := validateLabels("attachments.role_labels", a.RoleLabels, nil); err != nil {
		return err
	}
	for _, item := range []struct {
		name     string
		labels   map[string]string
		required []string
	}{
		{"print_report.kind_labels", r.KindLabels, []string{"analysis", "repair"}},
		{"print_report.status_labels", r.StatusLabels, []string{"healthy", "warning", "error", "unknown"}},
		{"print_report.details_status_labels", r.DetailsStatusLabels, []string{"reported", "not_reported"}},
		{"print_report.field_labels", r.FieldLabels, []string{"status", "issue_count", "error_count", "warning_count", "is_watertight", "volume_m3", "non_manifold_edges", "degenerate_faces", "holes", "printer_type", "height_mm", "longest_side_mm", "wall_thickness_mm", "dimensions_mm", "thin_wall_status", "thin_area_ratio", "has_thin_walls", "evaluated_at_ms", "details_status", "route", "applied", "faces_before", "faces_after", "thin_area_ratio_before", "thin_area_ratio_after"}},
		{"print_report.value_labels", r.ValueLabels, []string{"true", "false", "fdm", "sla", "full_color", "thin_wall", "topology", "topology_unmeasured"}},
	} {
		if err := validateLabels(item.name, item.labels, item.required); err != nil {
			return err
		}
	}
	return nil
}

func validateRigCheckReportPresentation(r RigCheckReportPresentationSchema) error {
	if r.Format != "art-ai.rig-check-report.v1" || r.MediaType != "application/vnd.art-ai.rig-check-report+json" || r.MaxBytes <= 0 || r.MaxBytes > 32<<10 {
		return errors.New("assets.rig_check_report requires the bounded v1 normalized protocol")
	}
	for _, value := range []string{r.Title, r.Loading, r.LoadFailed, r.Retry, r.Unsupported, r.Limitation} {
		if strings.TrimSpace(value) == "" {
			return errors.New("assets.rig_check_report presentation is required")
		}
	}
	for _, labels := range []struct {
		values map[string]string
		keys   []string
	}{
		{r.FieldLabels, []string{"riggable", "rig_type"}},
		{r.BooleanLabels, []string{"true", "false"}},
		{r.SpeciesLabels, []string{"biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic"}},
	} {
		if len(labels.values) != len(labels.keys) {
			return errors.New("assets.rig_check_report label set differs from v1")
		}
		for _, key := range labels.keys {
			if strings.TrimSpace(labels.values[key]) == "" {
				return errors.New("assets.rig_check_report label is required")
			}
		}
	}
	return nil
}
