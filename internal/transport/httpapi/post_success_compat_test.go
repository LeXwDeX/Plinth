package httpapi_test

// post_success_compat_test.go 落实 §6.6 的 HTTP 侧用例（W6）：SSE 默认
// 终态断流契约不变 + 声明式 opt-in 续流；轮询完成度（resources[] 与
// resources_pending 随版本更新）；删除后迟到 webhook 不可复活；真实
// 非空转换任务的隐私哨兵（detail/list/SSE 不泄漏私有 Opaque/cost 内部）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	_ "modernc.org/sqlite"
)

// historicalPostSuccessCatalog explicitly represents persisted tasks created
// before the Tripo full-format Offering migration. Only this isolated test
// snapshot enables the retired Offering; the default Catalog remains disabled.
func historicalPostSuccessCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "catalog", "default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, item := range doc["offerings"].([]any) {
		offering := item.(map[string]any)
		// The retired text-to-3d Offering and the conversion Offering (disabled in
		// the default Schema since 2026-10-09) are live in this snapshot.
		if offering["id"] == "tripo/tripo-v3.1/text-to-3d" || offering["id"] == "tripo/tripo-v3.1/format-conversion" {
			offering["enabled"] = true
		}
		if offering["id"] == "tripo/tripo-v3.1/text-to-3d" {
			// Reconstruct the archived Cost revision for persisted Task tests.
			offering["revision"] = "2026-10-16.213.1"
			offering["estimated_credits"] = 20
			offering["cost"] = map[string]any{
				"extra_credits": 5, "selection_parameter": "output_formats",
				"consent_parameter": "authorize_extra_credits", "extra_label": "附加导出格式",
			}
			offering["cost_policy"] = map[string]any{"max_authorized_extra_credits": 30}
			delete(offering, "native_pricing")
		}
	}
	application := doc["application"].(map[string]any)
	discovery := application["provider_discovery"].(map[string]any)
	legacyModels := discovery["legacy_models"].([]any)
	discovery["legacy_models"] = append(legacyModels, map[string]any{
		"model_id": "tripo-v3.1", "provider_key": "tripo-3d", "recommended": false,
	})
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newPostSuccessHTTPHarness(t *testing.T, databaseSetups ...func(*testing.T, string)) (*generation.Module, http.Handler, *inputassets.Service) {
	t.Helper()
	module, handler, inputs, _ := newHTTPHarnessWithCatalogOverride(
		t, nil, nil, nil, nil, nil, historicalPostSuccessCatalog(t), databaseSetups...,
	)
	return module, handler, inputs
}

// acceleratePostSuccessSchedule 把任务的 next_action_at 拨到过去，绕开
// 目录声明的 60s 间隔（测试加速；不改变任何生命周期语义——只是让行
// 变为到期可领取）。
func acceleratePostSuccessSchedule(t *testing.T, dbPath string, taskID generation.TaskID) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(
		`UPDATE generation_tasks SET next_action_at = '2000-01-01T00:00:00Z' WHERE id = ?`,
		taskID,
	); err != nil {
		t.Fatal(err)
	}
}

// submitTripoPostSuccessTask 提交带格式选择的 tripo 文生 3D（mock 适配
// 器承载）并推进到 SUCCEEDED；返回任务与 pending 资源断言。
func submitTripoPostSuccessTask(
	t *testing.T,
	module *generation.Module,
	handler http.Handler,
) generation.TaskID {
	t.Helper()
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1/text-to-3d",
		Input:      json.RawMessage(`{"prompt":"post success compat","output_formats":["GLTF"],"authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileLegacyTask(t, module, accepted.TaskID, "post-success-compat")
	task, err := module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task = %s", task.State)
	}
	if !generation.ResourcesPending(task.Resources) {
		t.Fatalf("expected pending resources: %#v", task.Resources)
	}
	return accepted.TaskID
}

// drivePostSuccessRounds 推进成功后观察直到资源全部终态。
func drivePostSuccessRounds(
	t *testing.T,
	module *generation.Module,
	taskID generation.TaskID,
	rounds int,
) generation.Task {
	t.Helper()
	for range rounds {
		if _, err := module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
			WorkerID:  "post-success-driver",
			LeaseFor:  time.Second,
			BatchSize: 10,
		}); err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(context.Background(), "owner-a", taskID)
		if err != nil {
			t.Fatal(err)
		}
		if !generation.ResourcesPending(task.Resources) && task.NextActionAt.IsZero() {
			return task
		}
		time.Sleep(5 * time.Millisecond)
	}
	task, err := module.Get(context.Background(), "owner-a", taskID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// ── 轮询完成度：resources[]/resources_pending 随版本更新（触点 9） ──

func TestTaskDetailResourcesProjectionTracksPostSuccess(t *testing.T) {
	t.Parallel()
	var dbPath string
	module, handler, _ := newPostSuccessHTTPHarness(
		t, func(_ *testing.T, path string) { dbPath = path },
	)
	taskID := submitTripoPostSuccessTask(t, module, handler)
	acceleratePostSuccessSchedule(t, dbPath, taskID)

	pending := serveLegacyRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(taskID), nil)
	if pending.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", pending.Code, pending.Body)
	}
	var pendingBody struct {
		Version          int64 `json:"version"`
		ResourcesPending bool  `json:"resources_pending"`
		Resources        []struct {
			Role          string `json:"role"`
			VariantPrefix string `json:"variant_prefix"`
			State         string `json:"state"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(pending.Body).Decode(&pendingBody); err != nil {
		t.Fatal(err)
	}
	if !pendingBody.ResourcesPending || len(pendingBody.Resources) == 0 {
		t.Fatalf("pending projection = %#v body=%s", pendingBody, pending.Body)
	}
	convertedPending := false
	for _, resource := range pendingBody.Resources {
		if resource.Role == "converted_model" && resource.State == "pending" {
			convertedPending = true
		}
	}
	if !convertedPending {
		t.Fatalf("converted slot missing: %#v", pendingBody.Resources)
	}

	final := drivePostSuccessRounds(t, module, taskID, 10)
	if generation.ResourcesPending(final.Resources) {
		t.Fatalf("resources still pending: %#v", final.Resources)
	}
	completed := serveLegacyRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(taskID), nil)
	if completed.Code != http.StatusOK {
		t.Fatalf("status=%d", completed.Code)
	}
	var completedBody struct {
		Version          int64 `json:"version"`
		ResourcesPending bool  `json:"resources_pending"`
		Resources        []struct {
			Role          string `json:"role"`
			VariantPrefix string `json:"variant_prefix"`
			State         string `json:"state"`
		} `json:"resources"`
		Assets []struct {
			Role    string `json:"role"`
			Variant string `json:"variant"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(completed.Body).Decode(&completedBody); err != nil {
		t.Fatal(err)
	}
	if completedBody.ResourcesPending {
		t.Fatalf("resources_pending still true: %s", completed.Body)
	}
	if completedBody.Version <= pendingBody.Version {
		t.Fatalf("version did not advance: %d → %d", pendingBody.Version, completedBody.Version)
	}
	convertedAssets := 0
	for _, asset := range completedBody.Assets {
		if asset.Role == "converted_model" {
			convertedAssets++
		}
	}
	if convertedAssets == 0 {
		t.Fatalf("converted assets missing: %s", completed.Body)
	}
	for _, resource := range completedBody.Resources {
		if resource.Role == "converted_model" && resource.State != "ready" {
			t.Fatalf("converted slot = %#v", completedBody.Resources)
		}
	}
}

// ── SSE 默认终态断流不变 + 声明式 opt-in 续流（触点 6） ──

func TestTaskStreamDefaultTerminalCloseUnchangedForPendingResources(t *testing.T) {
	t.Parallel()
	module, handler, _ := newPostSuccessHTTPHarness(t)
	taskID := submitTripoPostSuccessTask(t, module, handler)
	// 默认（无 opt-in 查询参数）：终态 + resources_pending 仍立即断流
	//（旧契约逐字节保持——恰好一个 message 事件后 EOF）。
	streamed := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/meshy/stream/text-to-3d/"+string(taskID),
		nil,
	)
	if streamed.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", streamed.Code, streamed.Body)
	}
	events := decodeTaskSSEEvents(t, streamed.Body.String())
	if len(events) != 1 {
		t.Fatalf("default stream must close after terminal event; events=%d", len(events))
	}
}

func TestTaskStreamPostSuccessOptInContinuesUntilResourcesComplete(t *testing.T) {
	t.Parallel()
	var dbPath string
	module, handler, _ := newPostSuccessHTTPHarness(
		t, func(_ *testing.T, path string) { dbPath = path },
	)
	taskID := submitTripoPostSuccessTask(t, module, handler)
	streamed := make(chan string, 1)
	go func() {
		response := serveLegacyRequest(
			handler,
			http.MethodGet,
			"/api/meshy/stream/text-to-3d/"+string(taskID)+"?completion=post_success",
			nil,
		)
		streamed <- response.Body.String()
	}()
	// pending 期间续流：短时间内不得关闭。
	select {
	case <-streamed:
		t.Fatal("opt-in stream closed while resources pending")
	case <-time.After(150 * time.Millisecond):
	}
	// 资源落地 → 流关闭（先加速调度再驱动成功后观察）。
	acceleratePostSuccessSchedule(t, dbPath, taskID)
	drivePostSuccessRounds(t, module, taskID, 12)
	select {
	case body := <-streamed:
		events := decodeTaskSSEEvents(t, body)
		if len(events) < 1 {
			t.Fatalf("no events: %s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("opt-in stream did not close after resources completed")
	}
}

// ── 删除后迟到 webhook 不可复活（HTTP 侧：删除 + 模块入口） ──

func TestLegacyDeleteBlocksLateWebhookResurrection(t *testing.T) {
	t.Parallel()
	module, handler, _ := newPostSuccessHTTPHarness(t)
	taskID := submitTripoPostSuccessTask(t, module, handler)
	task, err := module.Get(context.Background(), "owner-a", taskID)
	if err != nil {
		t.Fatal(err)
	}
	var opaque []struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(task.ProviderData, &opaque); err != nil ||
		len(opaque) == 0 || opaque[0].Identifier == "" {
		t.Fatalf("provider data = %s (%v)", task.ProviderData, err)
	}
	identifier := opaque[0].Identifier
	deleted := serveLegacyRequest(
		handler,
		http.MethodDelete,
		"/api/meshy/text-to-3d/"+string(taskID),
		nil,
	)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body)
	}
	result, err := module.IngestProviderWebhook(context.Background(), generation.ProviderWebhookCommand{
		ProviderID: "tripo",
		RemoteID:   identifier,
		EventType:  "task.completed",
		Payload:    []byte(`{"status":"success"}`),
	})
	if !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("late webhook err = %v result=%#v; want not_found", err, result)
	}
	item := serveLegacyRequest(handler, http.MethodGet, "/api/meshy/text-to-3d/"+string(taskID), nil)
	if item.Code != http.StatusNotFound {
		t.Fatalf("item resurrected: %d", item.Code)
	}
}

// ── 真实非空转换任务的隐私哨兵（W5 咨询项 W6 加固）：detail/list/SSE
// 不泄漏私有 Opaque（identifier/source_name 等）、cost_policy 数值或
// 检查点字段（§8.1）。 ──

func TestConversionTaskPrivacyAcrossPublicSurfaces(t *testing.T) {
	t.Parallel()
	module, handler, _ := newPostSuccessHTTPHarness(t)
	// 源任务（成功后可直接发起转换）。
	sourceID := submitTripoPostSuccessTask(t, module, handler)
	input := `{"source_task":"` + string(sourceID) + `","format":"FBX","authorize_extra_credits":true}`
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.FormatConversion,
		OfferingID: "tripo/tripo-v3.1/format-conversion",
		Input:      json.RawMessage(input),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileLegacyTask(t, module, accepted.TaskID, "conversion-privacy")
	converted, err := module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if converted.State != generation.TaskSucceeded || len(converted.Assets) == 0 {
		t.Fatalf("conversion task = %s assets=%d（须为真实非空任务）", converted.State, len(converted.Assets))
	}
	if len(converted.ProviderData) == 0 {
		t.Fatal("fixture 需要非空私有 Opaque 才构成隐私哨兵")
	}
	sentinels := []string{
		"identifier",       // mock 私有 Opaque 字段
		"source_name",      // mock 私有 Opaque 字段
		"downloadable",     // mock 私有 Opaque 字段
		"cost_policy",      // 成本策略原始值
		"extra_credits",    // 成本原语原始值
		"max_authorized",   // 同意上限原始值
		"post_success",     // 调度声明原始值
		"interval_seconds", // 调度字段
		"deadline_seconds", // 调度字段
		"resource_state_json",
	}
	detail := serveLegacyRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(accepted.TaskID), nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d", detail.Code)
	}
	list := serveLegacyRequest(handler, http.MethodGet, "/api/v1/tasks?capability=format-conversion", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	stream := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/meshy/stream/text-to-image/"+string(accepted.TaskID),
		nil,
	)
	for name, surface := range map[string]string{
		"detail": detail.Body.String(),
		"list":   list.Body.String(),
		"stream": stream.Body.String(),
	} {
		for _, sentinel := range sentinels {
			if strings.Contains(surface, sentinel) {
				t.Fatalf("%s surface leaked %q", name, sentinel)
			}
		}
	}
	if !strings.Contains(detail.Body.String(), `"variant":"converted"`) {
		t.Fatalf("detail missing converted asset: %s", detail.Body)
	}
}
