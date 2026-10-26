package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/etl"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

// TestETLMigratedAdministrationHistoryMatchesHTTPContracts 验证切换前的余额
// 快照与审计记录经 ETL 后，仍由现有管理后台 HTTP 契约完整返回。
func TestETLMigratedAdministrationHistoryMatchesHTTPContracts(
	t *testing.T,
) {
	t.Parallel()
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO balance_snapshots (id, balance, capturedAt)
		 VALUES (
			'migrated-balance', 321.25,
			'2025-02-03T04:05:06.789+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO audit_logs (
			id, action, userId, userName, resourceType, resourceId,
			detail, ipAddress, userAgent, createdAt
		 ) VALUES (
			'migrated-audit', 'upload_file', 'legacy-user', '旧用户',
			'asset', 'legacy-asset', '{"filename":"reference.png"}',
			'192.0.2.10', 'legacy-browser',
			'2025-02-03T04:05:06.789+00:00'
		 )`,
	)

	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Administration.Usage.Balance.HistoryDays = 5000
		},
		nil,
		nil,
		nil,
		func(t *testing.T, databasePath string) {
			t.Helper()
			var stdout, stderr bytes.Buffer
			if code := etl.Main([]string{
				"migrate",
				"--legacy-root", fixture.Root,
				"--target-db", databasePath,
			}, &stdout, &stderr); code != 0 {
				t.Fatalf(
					"ETL 迁移失败，退出码 %d:\n%s%s",
					code, stdout.String(), stderr.String(),
				)
			}
		},
	)

	balanceResponse := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/balance-summary",
		"",
		nil,
	)
	if balanceResponse.Code != http.StatusOK {
		t.Fatalf(
			"余额历史 status=%d body=%s",
			balanceResponse.Code,
			balanceResponse.Body.String(),
		)
	}
	var balance map[string]any
	if err := json.Unmarshal(balanceResponse.Body.Bytes(), &balance); err != nil {
		t.Fatal(err)
	}
	history, ok := balance["history"].([]any)
	if !ok {
		t.Fatalf("余额历史 = %#v", balance)
	}
	foundMigratedBalance := false
	for _, raw := range history {
		snapshot, _ := raw.(map[string]any)
		if snapshot["balance"] == float64(321.25) &&
			snapshot["captured_at"] ==
				"2025-02-03T04:05:06.789Z" {
			foundMigratedBalance = true
		}
	}
	if !foundMigratedBalance {
		t.Fatalf("余额历史未返回迁移快照: %#v", history)
	}

	auditResponse := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?userId=legacy-user&action=upload_file"+
			"&from=2025-02-03&to=2025-02-04",
		"",
		nil,
	)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf(
			"审计查询 status=%d body=%s",
			auditResponse.Code,
			auditResponse.Body.String(),
		)
	}
	var audit map[string]any
	if err := json.Unmarshal(auditResponse.Body.Bytes(), &audit); err != nil {
		t.Fatal(err)
	}
	events, ok := audit["data"].([]any)
	if !ok ||
		audit["total"] != float64(1) ||
		len(events) != 1 {
		t.Fatalf("审计查询 = %#v", audit)
	}
	event, _ := events[0].(map[string]any)
	if event["id"] != "migrated-audit" ||
		event["userId"] != "legacy-user" ||
		event["userName"] != "旧用户" ||
		event["action"] != "upload_file" ||
		event["resourceType"] != "asset" ||
		event["resourceId"] != "legacy-asset" ||
		event["ipAddress"] != "192.0.2.10" ||
		event["userAgent"] != "legacy-browser" ||
		event["detail"] != `{"filename":"reference.png"}` {
		t.Fatalf("迁移审计投影 = %#v", event)
	}
}
