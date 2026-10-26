package httpapi_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestAdministrationAuditLogsRequireConfiguredPermission(t *testing.T) {
	t.Parallel()
	_, regular, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	forbidden := administrationRequest(
		regular,
		http.MethodGet,
		"/api/admin/audit-logs",
		"",
		nil,
	)
	assertAdministrationError(
		t,
		forbidden,
		http.StatusForbidden,
		"error",
		"无权执行此操作",
	)

	_, unauthenticated, _ := newHTTPHarnessWithAuthentication(
		t,
		nil,
		nil,
		func(config *authentication.Config) {
			config.Fallback = deniedFallbackIdentity{}
		},
	)
	denied := administrationRequest(
		unauthenticated,
		http.MethodGet,
		"/api/admin/audit-logs",
		"",
		nil,
	)
	assertAdministrationError(
		t,
		denied,
		http.StatusUnauthorized,
		"error",
		"未登录或登录已过期",
	)
}

func TestAdministrationAuditLogsMatchLegacyFilterAndPagingContract(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	for _, body := range []string{
		`{"username":"audit-first","password":"secret12","email":"audit-first@example.test"}`,
		`{"username":"audit-second","password":"secret12","email":"audit-second@example.test"}`,
	} {
		created := administrationRequest(
			handler,
			http.MethodPost,
			"/api/admin/users",
			body,
			nil,
		)
		if created.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", created.Code, created.Body)
		}
	}

	response := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?page=1&pageSize=1&userId=local-user&action=admin_create_user",
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	if body["total"] != float64(2) ||
		body["page"] != float64(1) ||
		body["pageSize"] != float64(1) {
		t.Fatalf("audit page body=%#v", body)
	}
	events, ok := body["data"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("audit events=%#v", body["data"])
	}
	event, ok := events[0].(map[string]any)
	if !ok ||
		event["id"] == "" ||
		event["action"] != "admin_create_user" ||
		event["userId"] != "local-user" ||
		event["resourceType"] != "user" ||
		event["resourceId"] == "" ||
		event["detail"] == "" ||
		event["createdAt"] == "" {
		t.Fatalf("audit event=%#v", event)
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache-control = %q", got)
	}

	clamped := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?page=0&pageSize=999",
		"",
		nil,
	)
	if clamped.Code != http.StatusOK {
		t.Fatalf("clamped status=%d body=%s", clamped.Code, clamped.Body)
	}
	clampedBody := decodeAdministrationBody(t, clamped)
	if clampedBody["page"] != float64(1) ||
		clampedBody["pageSize"] != float64(100) {
		t.Fatalf("clamped page body=%#v", clampedBody)
	}
	parseIntCompatible := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?page=2tail&pageSize=-7",
		"",
		nil,
	)
	if parseIntCompatible.Code != http.StatusOK {
		t.Fatalf(
			"parseInt status=%d body=%s",
			parseIntCompatible.Code,
			parseIntCompatible.Body,
		)
	}
	parseIntBody := decodeAdministrationBody(t, parseIntCompatible)
	if parseIntBody["page"] != float64(2) ||
		parseIntBody["pageSize"] != float64(1) {
		t.Fatalf("parseInt-compatible page body=%#v", parseIntBody)
	}

	empty := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?action=login&from=not-a-date&to=also-bad",
		"",
		nil,
	)
	if empty.Code != http.StatusOK {
		t.Fatalf("invalid dates status=%d body=%s", empty.Code, empty.Body)
	}
	if body := decodeAdministrationBody(t, empty); body["total"] != float64(0) {
		t.Fatalf("action filter body=%#v", body)
	}
}

func TestAdministrationAuditLogWireAndQueryChangeWithProductSchema(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.AdminAuditLogs =
				"/custom/audits"
			auditLogs := &application.Administration.AuditLogs
			fields := &auditLogs.API.Fields
			fields.Data = "records"
			fields.Error = "problem"
			fields.Total = "matches"
			fields.Page = "current"
			fields.PageSize = "limit"
			fields.ID = "event_id"
			fields.Action = "operation"
			fields.UserID = "actor_id"
			fields.UserName = "actor_name"
			fields.ResourceType = "subject_type"
			fields.ResourceID = "subject_id"
			fields.Detail = "payload"
			fields.IPAddress = "client_ip"
			fields.UserAgent = "client_agent"
			fields.CreatedAt = "occurred_at"
			query := &auditLogs.API.Query
			query.Page = "cursor"
			query.PageSize = "take"
			query.UserID = "actor"
			query.Action = "operation"
			query.From = "after"
			query.To = "before"
		},
	)
	created := administrationRequest(
		handler,
		http.MethodPost,
		"/api/admin/users",
		`{"username":"custom-audit","password":"secret12","email":"custom-audit@example.test"}`,
		nil,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body)
	}

	response := administrationRequest(
		handler,
		http.MethodGet,
		"/custom/audits?cursor=1&take=5&actor=local-user&operation=admin_create_user",
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("custom audit status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	if body["matches"] != float64(1) ||
		body["current"] != float64(1) ||
		body["limit"] != float64(5) {
		t.Fatalf("custom page body=%#v", body)
	}
	records, ok := body["records"].([]any)
	if !ok || len(records) != 1 {
		t.Fatalf("custom records=%#v", body)
	}
	record := records[0].(map[string]any)
	if record["operation"] != "admin_create_user" ||
		record["actor_id"] != "local-user" ||
		record["event_id"] == "" ||
		record["payload"] == "" {
		t.Fatalf("custom record=%#v", record)
	}
	for _, leaked := range []string{
		"data", "total", "page", "pageSize", "id", "action", "userId",
	} {
		if _, exists := body[leaked]; exists {
			t.Fatalf("default field %q leaked in body=%#v", leaked, body)
		}
		if _, exists := record[leaked]; exists {
			t.Fatalf("default field %q leaked in record=%#v", leaked, record)
		}
	}
}

func TestAdministrationAuditRequestMetadataHasOneSchemaSource(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			headers := &application.Administration.AuditLogs.RequestHeaders
			headers.ForwardedFor = "X-Schema-Forwarded"
			headers.RealIP = "X-Schema-Real-IP"
			headers.UserAgent = "X-Schema-Agent"
		},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/users",
		bytes.NewBufferString(
			`{"username":"metadata-audit","password":"secret12","email":"metadata-audit@example.test"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-ID", "local-user")
	request.Header.Set("X-Schema-Forwarded", "203.0.113.3, 10.0.0.7")
	request.Header.Set("X-Schema-Agent", "schema-agent")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body)
	}

	response := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?action=admin_create_user",
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	event := body["data"].([]any)[0].(map[string]any)
	if event["ipAddress"] != "203.0.113.3, 10.0.0.7" ||
		event["userAgent"] != "schema-agent" {
		t.Fatalf("audit request metadata=%#v", event)
	}
}
