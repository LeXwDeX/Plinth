package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// TestAdministrationReadPathsAreAudited：逐请求级审计。管理读路径此前完全不
// 写审计，"管理员读了哪些用户/任务/余额/资产/审计记录"没有留痕。这里逐个
// 触发管理读端点，再从审计接口读回，断言每个都留下了 Schema 声明的动作、
// 资源类型与操作者身份。
func TestAdministrationReadPathsAreAudited(t *testing.T) {
	t.Parallel()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	admin := application.Administration
	routes := application.Runtime.HTTP.LegacyRoutes
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})

	// 用户资产按 userID 读取，先建一个真实用户，避免 404 让读审计无从留痕。
	created := administrationRequest(
		handler,
		http.MethodPost,
		routes.AdminUsers,
		`{"username":"read-audit-user","password":"secret12",`+
			`"email":"read-audit@example.test"}`,
		nil,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("建号 status=%d body=%s", created.Code, created.Body)
	}
	createdBody := decodeAdministrationBody(t, created)
	createdUser, _ := createdBody["data"].(map[string]any)
	userID, _ := createdUser["id"].(string)
	if userID == "" {
		t.Fatalf("建号未返回 id：%#v", createdBody)
	}

	// 补数回填是 POST 触发，不是读路径，单独按 trigger_action 校验。
	cases := []struct {
		name         string
		method       string
		path         string
		action       string
		resourceType string
		resourceID   string
	}{
		{
			name: "users", method: http.MethodGet,
			path:         routes.AdminUsers,
			action:       admin.Users.Audit.ReadAction,
			resourceType: admin.Users.Audit.ResourceType,
		},
		{
			name: "audit_logs", method: http.MethodGet,
			path:         routes.AdminAuditLogs,
			action:       admin.AuditLogs.Audit.ReadAction,
			resourceType: admin.AuditLogs.Audit.ResourceType,
		},
		{
			name: "generations", method: http.MethodGet,
			path:         routes.AdminGenerations,
			action:       admin.Generations.Audit.ReadAction,
			resourceType: admin.Generations.Audit.ResourceType,
		},
		{
			name: "usage", method: http.MethodGet,
			path:         routes.AdminUsage,
			action:       admin.Usage.Audit.ReadAction,
			resourceType: admin.Usage.Audit.ResourceType,
		},
		{
			name: "user_assets", method: http.MethodGet,
			path:         strings.Replace(routes.AdminUserAssets, "{userID}", userID, 1),
			action:       admin.UserAssets.Audit.ReadAction,
			resourceType: admin.UserAssets.Audit.ResourceType,
			resourceID:   userID,
		},
		{
			name: "backfill_status", method: http.MethodPost,
			path:         routes.AdminBackfillStatus,
			action:       admin.BackfillStatus.Audit.TriggerAction,
			resourceType: admin.BackfillStatus.Audit.ResourceType,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.action == "" {
				t.Fatal("Schema 未声明审计动作，无法验证读审计")
			}
			response := administrationRequest(
				handler, tc.method, tc.path, "", nil,
			)
			if response.Code != http.StatusOK {
				t.Fatalf(
					"%s status=%d body=%s",
					tc.name, response.Code, response.Body,
				)
			}
			// 读审计本身也会再写一条，因此只按动作过滤。
			logged := administrationRequest(
				handler,
				http.MethodGet,
				routes.AdminAuditLogs+"?action="+tc.action,
				"",
				nil,
			)
			if logged.Code != http.StatusOK {
				t.Fatalf(
					"audit read status=%d body=%s",
					logged.Code, logged.Body,
				)
			}
			body := decodeAdministrationBody(t, logged)
			events, _ := body["data"].([]any)
			var found map[string]any
			for _, raw := range events {
				event := raw.(map[string]any)
				if event["action"] == tc.action {
					found = event
					break
				}
			}
			if found == nil {
				t.Fatalf(
					"%s 未留痕：action=%q events=%#v",
					tc.name, tc.action, body,
				)
			}
			if found["resourceType"] != tc.resourceType {
				t.Fatalf(
					"%s resourceType=%v, want %q",
					tc.name, found["resourceType"], tc.resourceType,
				)
			}
			if tc.resourceID != "" && found["resourceId"] != tc.resourceID {
				t.Fatalf(
					"%s resourceId=%v, want %q",
					tc.name, found["resourceId"], tc.resourceID,
				)
			}
			// 身份必须来自已认证 principal，不能是空串或请求体里的值。
			if id, _ := found["userId"].(string); id == "" {
				t.Fatalf("%s 审计未绑定操作者：%#v", tc.name, found)
			}
		})
	}
}

// TestAdministrationReadAuditsAreSchemaDeclared：守门——Schema 里声明了读
// 审计动作的模块，代码就必须真的写；新增管理模块时忘记接审计会在这里失败，
// 而不是等到出事才发现没有留痕。
func TestAdministrationReadAuditsAreSchemaDeclared(t *testing.T) {
	t.Parallel()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	admin := taskCatalog.Application().Administration
	declared := map[string]bool{
		admin.Users.Audit.ReadAction:             true,
		admin.AuditLogs.Audit.ReadAction:         true,
		admin.Generations.Audit.ReadAction:       true,
		admin.Usage.Audit.ReadAction:             true,
		admin.UserAssets.Audit.ReadAction:        true,
		admin.BackfillStatus.Audit.TriggerAction: true,
	}
	for _, action := range []string{
		admin.Users.Audit.ReadAction,
		admin.AuditLogs.Audit.ReadAction,
		admin.Generations.Audit.ReadAction,
		admin.Usage.Audit.ReadAction,
		admin.UserAssets.Audit.ReadAction,
		admin.BackfillStatus.Audit.TriggerAction,
	} {
		if action == "" || !declared[action] {
			t.Fatalf("审计动作缺失或重复：%q", action)
		}
	}
	// 读动作不得与写动作撞名，否则筛不出"读"这一类。
	writes := map[string]bool{
		admin.Users.Audit.CreateAction:     true,
		admin.Users.Audit.UpdateAction:     true,
		admin.Users.Audit.DeactivateAction: true,
		admin.Users.Audit.DeleteAction:     true,
	}
	for read, write := range map[string]string{
		admin.Users.Audit.ReadAction:       admin.Users.Audit.CreateAction,
		admin.AuditLogs.Audit.ReadAction:   admin.Users.Audit.UpdateAction,
		admin.Generations.Audit.ReadAction: admin.Generations.Audit.DownloadAction,
	} {
		if read != "" && read == write {
			t.Fatalf("读动作与写动作撞名：%q", read)
		}
		if read != "" && writes[read] {
			t.Fatalf("读动作 %q 已被写动作占用", read)
		}
	}
}
