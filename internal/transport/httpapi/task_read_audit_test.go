package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// auditEntries 读取某个审计动作下全部记录（以调用者身份走管理端审计日志接口）。
func auditEntries(t *testing.T, handler http.Handler, user, action string) []map[string]any {
	t.Helper()
	response := serveScopedRequest(handler, "/api/admin/audit-logs?action="+url.QueryEscape(action), user)
	if response.Code != http.StatusOK {
		t.Fatalf("audit-logs status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

// 管理员按任务 ID 读取他人任务：详情、事件、旧兼容详情和流各写 admin_read_task；
// 读本人任务不写；流只在建立时记一次。
func TestAdminReadingAnotherUsersTaskWritesAudit(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	other := submitHistoryFixture(t, module, "owner-b", generation.TextToImage, "theirs")
	reconcileFixtureTask(t, module, "owner-b", other.TaskID)
	mine := submitHistoryFixture(t, module, "caller", generation.TextToImage, "mine")
	reconcileFixtureTask(t, module, "caller", mine.TaskID)
	const action = "admin_read_task"
	otherID := string(other.TaskID)

	if got := auditEntries(t, handler, "caller", action); len(got) != 0 {
		t.Fatalf("audit before any cross-owner read = %#v", got)
	}

	// 读自己的任务（详情 + 事件）：不写。
	for _, path := range []string{
		"/api/v1/tasks/" + string(mine.TaskID),
		"/api/v1/tasks/" + string(mine.TaskID) + "/events",
	} {
		if response := serveScopedRequest(handler, path, "caller"); response.Code != http.StatusOK {
			t.Fatalf("own read %s = %d", path, response.Code)
		}
	}
	if got := auditEntries(t, handler, "caller", action); len(got) != 0 {
		t.Fatalf("own-task reads wrote audit: %#v", got)
	}

	// 读他人任务详情与事件：各一条。
	for _, path := range []string{
		"/api/v1/tasks/" + otherID,
		"/api/v1/tasks/" + otherID + "/events",
		"/api/meshy/text-to-image/" + otherID,
	} {
		if response := serveScopedRequest(handler, path, "caller"); response.Code != http.StatusOK {
			t.Fatalf("cross-owner read %s = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
	entries := auditEntries(t, handler, "caller", action)
	if len(entries) != 3 {
		t.Fatalf("audit entries = %d, want 3: %#v", len(entries), entries)
	}
	for _, entry := range entries {
		if entry["userId"] != "caller" || entry["resourceType"] != "task" || entry["resourceId"] != otherID {
			t.Fatalf("audit entry = %#v", entry)
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(entry["detail"].(string)), &detail); err != nil {
			t.Fatal(err)
		}
		if detail["taskId"] != otherID || detail["ownerId"] != "owner-b" {
			t.Fatalf("audit detail = %#v", detail)
		}
	}

	// 旧兼容流：建立时一次；终态任务立即断流，不再追加。
	before := len(entries)
	if response := serveScopedRequest(handler, "/api/meshy/stream/text-to-image/"+otherID, "caller"); response.Code != http.StatusOK {
		t.Fatalf("cross-owner stream = %d, body = %s", response.Code, response.Body.String())
	}
	if got := auditEntries(t, handler, "caller", action); len(got) != before+1 {
		t.Fatalf("stream wrote %d audit entries, want exactly 1", len(got)-before)
	}
}

// 普通用户读他人任务仍是 404，也不产生 admin_read_task。
func TestRegularUserCrossOwnerTaskReadIsHiddenAndUnaudited(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	other := submitHistoryFixture(t, module, "owner-b", generation.TextToImage, "theirs")
	reconcileFixtureTask(t, module, "owner-b", other.TaskID)
	for _, path := range []string{
		"/api/v1/tasks/" + string(other.TaskID),
		"/api/v1/tasks/" + string(other.TaskID) + "/events",
	} {
		if response := serveScopedRequest(handler, path, "caller"); response.Code != http.StatusNotFound {
			t.Fatalf("regular cross-owner %s = %d, want 404", path, response.Code)
		}
	}
}
