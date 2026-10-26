package catalog

import (
	"slices"
	"testing"
)

func TestDefaultCatalogOwnsAdministrationAuditLogContract(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	auditLogs := application.Administration.AuditLogs

	if got := application.Runtime.HTTP.LegacyRoutes.AdminAuditLogs; got != "/api/admin/audit-logs" {
		t.Fatalf("admin audit logs route = %q", got)
	}
	if auditLogs.Permission != "audit.read" {
		t.Fatalf("admin audit permission = %q", auditLogs.Permission)
	}
	if auditLogs.Defaults.IDPrefix != "audit_" {
		t.Fatalf("admin audit id prefix = %q", auditLogs.Defaults.IDPrefix)
	}
	if auditLogs.RequestHeaders.ForwardedFor != "X-Forwarded-For" ||
		auditLogs.RequestHeaders.RealIP != "X-Real-IP" ||
		auditLogs.RequestHeaders.UserAgent != "User-Agent" {
		t.Fatalf(
			"admin audit request headers = %+v",
			auditLogs.RequestHeaders,
		)
	}
	for name, value := range map[string]string{
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
		"query_page":    auditLogs.API.Query.Page,
		"query_page_size": auditLogs.API.Query.
			PageSize,
		"query_user_id": auditLogs.API.Query.UserID,
		"query_action":  auditLogs.API.Query.Action,
		"query_from":    auditLogs.API.Query.From,
		"query_to":      auditLogs.API.Query.To,
	} {
		if value == "" {
			t.Fatalf("administration.audit_logs.api.%s is empty", name)
		}
	}
	if auditLogs.Limits.DefaultPage != 1 ||
		auditLogs.Limits.DefaultPageSize != 50 ||
		auditLogs.Limits.MaximumPageSize != 100 {
		t.Fatalf("unexpected audit log limits: %+v", auditLogs.Limits)
	}
	if auditLogs.WritePolicy.MaximumAttempts != 2 ||
		auditLogs.WritePolicy.RetryDelayMilliseconds != 1000 {
		t.Fatalf("unexpected audit write policy: %+v", auditLogs.WritePolicy)
	}
	if auditLogs.Presentation.DefaultRecentDays != 3 ||
		auditLogs.Presentation.LoadingRows != 5 ||
		auditLogs.Presentation.UserIDPreviewCharacters != 8 ||
		auditLogs.Presentation.ResourceIDPreviewCharacters != 12 {
		t.Fatalf(
			"unexpected audit log presentation limits: %+v",
			auditLogs.Presentation,
		)
	}
	for _, key := range requiredAdministrationAuditLogTextKeys {
		if auditLogs.Presentation.Text[key] == "" {
			t.Fatalf(
				"administration.audit_logs.presentation.text[%q] is empty",
				key,
			)
		}
	}
	for action := range auditLogs.Presentation.ActionBadgeClasses {
		if !slices.ContainsFunc(application.AuditActions, func(item ValueLabel) bool {
			return item.Value == action
		}) {
			t.Fatalf("badge action %q is not declared", action)
		}
	}
}

func TestApplicationReturnsDefensiveAdministrationAuditLogClone(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	first.Administration.AuditLogs.Presentation.Text["title"] = "mutated"
	first.Administration.AuditLogs.Presentation.DateTimeOptions["year"] = "short"
	first.Administration.AuditLogs.Presentation.ActionBadgeClasses["login"] = "mutated"
	first.Administration.AuditLogs.Presentation.ActionPrefixBadgeClasses["admin_"] = "mutated"
	second := taskCatalog.Application()

	presentation := second.Administration.AuditLogs.Presentation
	if presentation.Text["title"] == "mutated" ||
		presentation.DateTimeOptions["year"] == "short" ||
		presentation.ActionBadgeClasses["login"] == "mutated" ||
		presentation.ActionPrefixBadgeClasses["admin_"] == "mutated" {
		t.Fatal("administration audit presentation maps leaked through clone")
	}
}
