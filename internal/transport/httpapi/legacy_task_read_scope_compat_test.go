package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 回归：按任务 ID 的读路径（旧详情、旧下载、原生详情/资产）对 tasks.read.any
// 持有者跨 owner 放行；历史列表只返回本人任务（#154、#158），两者口径不同是有意的，
// 管理员看他人任务的列表入口是 /api/admin/generations。
func TestAdminTaskReadPathsShareHistoryScope(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	accepted := submitHistoryFixture(
		t,
		module,
		"owner-b",
		generation.TextToImage,
		"cross owner detail",
	)
	task := reconcileFixtureTask(t, module, "owner-b", accepted.TaskID)
	if task.State != generation.TaskSucceeded || len(task.Assets) == 0 {
		t.Fatalf("fixture task = %#v", task)
	}

	// 历史列表对管理员也只返回本人任务（#158）；跨 owner 的缩略图取自 owner 自己的列表，
	// 再用管理员身份按 ID 读取，确认按 ID 的读路径仍放行。
	adminHistory := serveTaskHistoryRequest(handler, "/api/tasks?page=1&pageSize=32", "admin-user")
	if adminHistory.Code != http.StatusOK {
		t.Fatalf("admin history status = %d, body = %s", adminHistory.Code, adminHistory.Body.String())
	}
	var adminListed struct {
		Data []struct {
			ID generation.TaskID `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(adminHistory.Body).Decode(&adminListed); err != nil {
		t.Fatal(err)
	}
	if len(adminListed.Data) != 0 {
		t.Fatalf("admin history leaked another owner's tasks: %#v", adminListed.Data)
	}

	history := serveTaskHistoryRequest(handler, "/api/tasks?page=1&pageSize=32", "owner-b")
	if history.Code != http.StatusOK {
		t.Fatalf("history status = %d, body = %s", history.Code, history.Body.String())
	}
	var listed struct {
		Data []struct {
			ID           generation.TaskID `json:"id"`
			ThumbnailURL string            `json:"thumbnailUrl"`
		} `json:"data"`
	}
	if err := json.NewDecoder(history.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != accepted.TaskID {
		t.Fatalf("history data = %#v", listed.Data)
	}

	detail := serveScopedRequest(
		handler,
		"/api/meshy/text-to-image/"+string(accepted.TaskID),
		"admin-user",
	)
	if detail.Code != http.StatusOK {
		t.Fatalf(
			"admin cross-owner detail status = %d, body = %s",
			detail.Code,
			detail.Body.String(),
		)
	}
	var legacyTask struct {
		ID         generation.TaskID `json:"id"`
		OutputURLs []string          `json:"output_urls"`
	}
	if err := json.NewDecoder(detail.Body).Decode(&legacyTask); err != nil {
		t.Fatal(err)
	}
	if legacyTask.ID != accepted.TaskID || len(legacyTask.OutputURLs) != 1 {
		t.Fatalf("legacy detail = %#v", legacyTask)
	}

	download := serveScopedRequest(handler, legacyTask.OutputURLs[0], "admin-user")
	if download.Code != http.StatusOK || download.Body.Len() == 0 {
		t.Fatalf("admin cross-owner download status = %d", download.Code)
	}

	if listed.Data[0].ThumbnailURL == "" {
		t.Fatalf("history thumbnailUrl missing: %#v", listed.Data[0])
	}
	thumbnail := serveScopedRequest(
		handler,
		listed.Data[0].ThumbnailURL,
		"admin-user",
	)
	if thumbnail.Code != http.StatusOK || thumbnail.Body.Len() == 0 {
		t.Fatalf("admin cross-owner thumbnail status = %d", thumbnail.Code)
	}
}

// 回归：普通用户跨 owner 读取保持 404 隔离，read-any 扩展不放松边界。
func TestRegularUserCrossOwnerTaskReadsRemainHidden(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	accepted := submitHistoryFixture(
		t,
		module,
		"owner-b",
		generation.TextToImage,
		"isolated detail",
	)
	task := reconcileFixtureTask(t, module, "owner-b", accepted.TaskID)
	if task.State != generation.TaskSucceeded || len(task.Assets) == 0 {
		t.Fatalf("fixture task = %#v", task)
	}

	detail := serveScopedRequest(
		handler,
		"/api/meshy/text-to-image/"+string(accepted.TaskID),
		"owner-a",
	)
	if detail.Code != http.StatusNotFound {
		t.Fatalf(
			"cross-owner detail status = %d, body = %s",
			detail.Code,
			detail.Body.String(),
		)
	}

	ownHistory := serveTaskHistoryRequest(
		handler,
		"/api/tasks?page=1&pageSize=32",
		"owner-b",
	)
	if ownHistory.Code != http.StatusOK {
		t.Fatalf("owner history status = %d", ownHistory.Code)
	}
	var owned struct {
		Data []struct {
			ThumbnailURL string `json:"thumbnailUrl"`
		} `json:"data"`
	}
	if err := json.NewDecoder(ownHistory.Body).Decode(&owned); err != nil {
		t.Fatal(err)
	}
	if len(owned.Data) != 1 || owned.Data[0].ThumbnailURL == "" {
		t.Fatalf("owner history data = %#v", owned.Data)
	}
	asset := serveScopedRequest(
		handler,
		owned.Data[0].ThumbnailURL,
		"owner-a",
	)
	if asset.Code != http.StatusNotFound {
		t.Fatalf("cross-owner asset status = %d", asset.Code)
	}
}

func reconcileFixtureTask(
	t *testing.T,
	module *generation.Module,
	owner generation.OwnerID,
	taskID generation.TaskID,
) generation.Task {
	t.Helper()
	var task generation.Task
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := module.ReconcileOnce(
			context.Background(),
			generation.CoordinatorConfig{
				WorkerID:  "read-scope-test",
				LeaseFor:  time.Minute,
				BatchSize: 10,
			},
		); err != nil {
			t.Fatal(err)
		}
		var err error
		task, err = module.Get(context.Background(), owner, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() || time.Now().After(deadline) {
			break
		}
		time.Sleep(12 * time.Millisecond)
	}
	return task
}

func serveScopedRequest(
	handler http.Handler,
	target string,
	user string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("X-User-ID", user)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestTaskDetailShowsPromptToOwnerOnly(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	accepted := submitHistoryFixture(t, module, "owner-a", generation.TextToImage, "银色机械猫")
	reconcileFixtureTask(t, module, "owner-a", accepted.TaskID)

	own := serveScopedRequest(handler, "/api/v1/tasks/"+string(accepted.TaskID), "owner-a")
	if own.Code != http.StatusOK {
		t.Fatalf("owner status = %d body = %s", own.Code, own.Body.String())
	}
	var detail struct {
		Input *struct {
			Prompt string `json:"prompt"`
		} `json:"input"`
	}
	if err := json.NewDecoder(own.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Input == nil || detail.Input.Prompt != "银色机械猫" {
		t.Fatalf("owner input = %#v", detail.Input)
	}

	other := serveScopedRequest(handler, "/api/v1/tasks/"+string(accepted.TaskID), "owner-b")
	if other.Code != http.StatusNotFound {
		t.Fatalf("other owner status = %d body = %s", other.Code, other.Body.String())
	}
}

// #154：用户端原生任务列表 GET /api/v1/tasks 对所有角色只返回本人任务；
// 管理员按任务 ID 的详情仍沿 tasks.read.any，管理端生成记录走自己的接口。
func TestNativeTaskListIsOwnTasksOnlyForEveryRole(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		roles []string
	}{
		{name: "admin", roles: []string{"admin"}},
		{name: "user", roles: []string{"user"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			module, handler, _ := newHTTPHarnessWithRoles(t, test.roles)
			mine := submitHistoryFixture(t, module, "caller", generation.TextToImage, "mine")
			other := submitHistoryFixture(t, module, "owner-b", generation.TextToImage, "theirs")
			reconcileFixtureTask(t, module, "caller", mine.TaskID)
			reconcileFixtureTask(t, module, "owner-b", other.TaskID)

			response := serveScopedRequest(handler, "/api/v1/tasks", "caller")
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var body struct {
				Tasks []struct {
					ID generation.TaskID `json:"id"`
				} `json:"tasks"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Tasks) != 1 || body.Tasks[0].ID != mine.TaskID {
				t.Fatalf("native list = %#v, want only %s", body.Tasks, mine.TaskID)
			}

			detail := serveScopedRequest(handler, "/api/v1/tasks/"+string(other.TaskID), "caller")
			wantDetail := http.StatusNotFound
			if test.name == "admin" {
				wantDetail = http.StatusOK
			}
			if detail.Code != wantDetail {
				t.Fatalf("cross-owner detail status = %d, want %d", detail.Code, wantDetail)
			}
		})
	}
}
