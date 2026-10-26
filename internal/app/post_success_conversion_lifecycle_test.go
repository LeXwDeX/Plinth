package app

// post_success_conversion_lifecycle_test.go 是回顾性整改 P0/P1/P2 的真实链路
// 验收：**真实 Tripo 适配器**（默认目录声明接线）+ httptest 假上游 + 真实
// SQLite + 真实本地资产存储 + 内核读模型（公开 resources[] 投影的同一数据
// 源）。全部本地 fixture、零付费调用、零真实上游。
//
// 覆盖矩阵（对应整改要求的每一项）：
//   - 首个格式失败后其余已授权格式继续（P0）；
//   - 中间格式失败 → 混合结果按逐项槽位公开（P0）；
//   - 选中 3 项只处理 1 项时不得收尾（P0/P2）；
//   - 上游完成但下载 503 → 后续轮次下载成功，convert 计数不变（P1）；
//   - 多格式同时未兑现 → 重发按 DeliveryAttempts 最少者轮换，字典序靠前的
//     永久下载失败不得独占重发轮次；轮换进度跨重启不丢（P1 公平性，第三波）；
//   - 上游完成与下载之间重启 → 证据不丢、零重复付费（P1）；
//   - 提交 CAS 冲突 → 不丢下载证据、不假 READY（P1）；
//   - 零本地资产永不 READY；deadline 本地收尾（P1/P2）；
//   - 缺同意 → 零付费请求（P2）；
//   - 历史聚合快照不追溯改写、不产生新付费义务（P2 兼容）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

const (
	lifecycleOwnerID   = generation.OwnerID("owner-lifecycle")
	lifecycleOffering  = "tripo/tripo-v3.1/text-to-3d"
	lifecycleConverted = "converted_model"
	lifecycleSessionID = "lifecycle-session"
)

var lifecycleSubmissionSequence atomic.Uint64

const lifecycleGLTFBytes = `{"asset":{"version":"2.0"},"extras":{"fixture":"converted-gltf"}}`

// ── 假上游：可配置每个格式的子作业结局与下载失败次数 ──

type lifecycleUpstream struct {
	server *httptest.Server

	mu              sync.Mutex
	converts        []string          // 已发送的 convert 格式（顺序）
	childStatus     map[string]string // lower(format) → success/failed
	childPolls      map[string]int
	runningRounds   int            // 子作业终态前的 running 轮数
	downloadFails   map[string]int // lower(format) → 剩余 503 次数
	downloads       map[string]int // lower(format) → 下载命中次数
	parentDownloads int
}

func newLifecycleUpstream(t *testing.T) *lifecycleUpstream {
	t.Helper()
	upstream := &lifecycleUpstream{
		childStatus:   map[string]string{},
		childPolls:    map[string]int{},
		downloadFails: map[string]int{},
		downloads:     map[string]int{},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPost && path == "/generation/text-to-model":
			writeLifecycleJSON(w, map[string]any{
				"code": 0, "message": "success",
				"data": map[string]any{"task_id": "parent-1"},
			})
		case r.Method == http.MethodPost && path == "/models/convert":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			format, _ := body["format"].(string)
			upstream.mu.Lock()
			upstream.converts = append(upstream.converts, format)
			upstream.mu.Unlock()
			writeLifecycleJSON(w, map[string]any{
				"code": 0, "message": "success",
				"data": map[string]any{"task_id": "child-" + strings.ToLower(format)},
			})
		case r.Method == http.MethodGet && path == "/tasks/parent-1":
			writeLifecycleJSON(w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"task_id": "parent-1", "status": "success", "progress": 100,
					"output": map[string]any{
						"model_url":          upstream.assetURL("parent", "glb"),
						"rendered_image_url": upstream.assetURL("parent", "png"),
					},
				},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/tasks/child-"):
			format := strings.TrimPrefix(path, "/tasks/child-")
			upstream.mu.Lock()
			upstream.childPolls[format]++
			polls := upstream.childPolls[format]
			status := upstream.childStatus[format]
			running := polls <= upstream.runningRounds
			upstream.mu.Unlock()
			if status == "" {
				status = "success"
			}
			data := map[string]any{
				"task_id": "child-" + format, "status": status, "progress": 100,
			}
			if running {
				data["status"] = "running"
				data["progress"] = 50
			} else if status == "success" {
				data["credits_consumed"] = json.RawMessage(`5`)
				data["output"] = map[string]any{
					"model_url": upstream.assetURL("converted-"+format, formatExtension(format)),
				}
			}
			writeLifecycleJSON(w, map[string]any{"code": 0, "data": data})
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/assets/"):
			name := strings.TrimPrefix(path, "/assets/")
			if strings.HasPrefix(name, "converted-") {
				format := strings.TrimPrefix(name[:strings.Index(name, ".")], "converted-")
				upstream.mu.Lock()
				remaining := upstream.downloadFails[format]
				if remaining > 0 {
					upstream.downloadFails[format] = remaining - 1
				}
				upstream.downloads[format]++
				upstream.mu.Unlock()
				if remaining > 0 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			} else {
				upstream.mu.Lock()
				upstream.parentDownloads++
				upstream.mu.Unlock()
			}
			w.Header().Set("Content-Type", lifecycleMediaType(name))
			_, _ = w.Write(lifecycleAssetBytes(name))
		default:
			http.NotFound(w, r)
		}
	}))
	upstream.server = server
	t.Cleanup(server.Close)
	return upstream
}

func lifecycleAssetBytes(name string) []byte {
	if strings.HasSuffix(strings.ToLower(name), ".glb") {
		return modelinputtest.Triangle(modelinputtest.Options{})
	}
	if strings.HasSuffix(strings.ToLower(name), ".png") {
		var body bytes.Buffer
		if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			panic(err)
		}
		return body.Bytes()
	}
	if strings.HasSuffix(strings.ToLower(name), ".gltf") {
		return []byte(lifecycleGLTFBytes)
	}
	return []byte("lifecycle-bytes:" + name)
}

func (u *lifecycleUpstream) assetURL(name string, extension string) string {
	return u.server.URL + "/assets/" + name + "." + extension
}

func (u *lifecycleUpstream) snapshot() (converts []string, downloads map[string]int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.converts...), copyLifecycleCounts(u.downloads)
}

func (u *lifecycleUpstream) failChild(format string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.childStatus[strings.ToLower(format)] = "failed"
}

func (u *lifecycleUpstream) failDownloads(format string, times int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.downloadFails[strings.ToLower(format)] = times
}

// allowDownloads 撤销下载失败注入（模拟上游/CDN 恢复）。
func (u *lifecycleUpstream) allowDownloads(format string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.downloadFails[strings.ToLower(format)] = 0
}

func copyLifecycleCounts(values map[string]int) map[string]int {
	cloned := make(map[string]int, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func formatExtension(format string) string {
	switch strings.ToLower(format) {
	case "gltf":
		return "gltf"
	case "png":
		return "png"
	default:
		return strings.ToLower(format)
	}
}

func lifecycleMediaType(name string) string {
	switch {
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".glb"):
		return "model/gltf-binary"
	case strings.HasSuffix(name, ".gltf"):
		return "model/gltf+json"
	default:
		return "application/octet-stream"
	}
}

func writeLifecycleJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// ── 目录 fixture：只改测试接线（本地 http 资产 + 本地主机白名单） ──

func lifecycleCatalog(t *testing.T, legacyAggregate bool) *catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "catalog", "default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.NewDecoder(strings.NewReader(string(raw))).Decode(&document); err != nil {
		t.Fatal(err)
	}
	application := document["application"].(map[string]any)
	runtime := application["runtime"].(map[string]any)
	for _, item := range runtime["provider_connections"].([]any) {
		connection := item.(map[string]any)
		if connection["id"] == "tripo" {
			// 测试接线：允许本地 httptest 的 http 资产（生产声明不变）。
			connection["allow_http_assets"] = true
		}
	}
	for _, item := range document["offerings"].([]any) {
		offering := item.(map[string]any)
		id, _ := offering["id"].(string)
		if !strings.HasPrefix(id, "tripo/") {
			continue
		}
		if id == lifecycleOffering {
			// These cases model Tasks created under the persisted pre-migration
			// Offering revision. Re-enable it only in this isolated historical
			// snapshot fixture; the default Catalog keeps the Offering disabled.
			offering["enabled"] = true
			offering["revision"] = "2026-10-16.213.1"
			offering["estimated_credits"] = 20
			offering["cost"] = map[string]any{
				"extra_credits": 5, "selection_parameter": "output_formats",
				"consent_parameter": "authorize_extra_credits", "extra_label": "附加导出格式",
			}
			offering["cost_policy"] = map[string]any{"max_authorized_extra_credits": 30}
			delete(offering, "native_pricing")
		}
		offering["asset_hosts"] = []any{"127.0.0.1"}
		if !legacyAggregate {
			continue
		}
		// 历史快照形状：剥离逐项展开声明（P2 兼容路径）。
		for _, outputItem := range offering["outputs"].([]any) {
			output := outputItem.(map[string]any)
			if output["role"] == lifecycleConverted {
				delete(output, "variant_from")
				delete(output, "variant_case")
			}
		}
	}
	discovery := application["provider_discovery"].(map[string]any)
	legacyModels, ok := discovery["legacy_models"].([]any)
	if !ok {
		t.Fatal("historical snapshot fixture requires provider_discovery.legacy_models")
	}
	legacyModels = append(legacyModels, map[string]any{
		"model_id": "tripo-v3.1", "provider_key": "tripo-3d", "recommended": false,
	})
	discovery["legacy_models"] = legacyModels
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.Load(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return taskCatalog
}

// ── 内核 harness：真实 SQLite + 真实本地资产存储 + 真实 Tripo 适配器 ──

type lifecycleHarness struct {
	module     *generation.Module
	repository generation.Repository
	assets     *local.Store
	clock      *fakeAcmeClock
	upstream   *lifecycleUpstream
	dbPath     string
	legacy     bool
}

func newLifecycleHarness(
	t *testing.T,
	upstream *lifecycleUpstream,
	legacyAggregate bool,
	repositoryWrapper func(generation.Repository) generation.Repository,
) *lifecycleHarness {
	t.Helper()
	taskCatalog := lifecycleCatalog(t, legacyAggregate)
	root := t.TempDir()
	dbPath := filepath.Join(root, "lifecycle.db")
	repository, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if _, err := repository.CreateSession(context.Background(), chat.Session{
		ID: lifecycleSessionID, OwnerID: string(lifecycleOwnerID), OwnerName: "test",
		Title: "lifecycle", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	assetStore, err := local.New(filepath.Join(root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = assetStore.Close() })
	registry := providers.NewRegistry()
	settings := map[string]string{
		"tripo_base_url": upstream.server.URL,
		"tripo_api_key":  "lifecycle-test-key",
	}
	if err := registerProviders(
		Config{ProviderMode: "live", RuntimeSettings: settings},
		taskCatalog,
		registry,
		nil, nil,
		discardLogger(),
	); err != nil {
		t.Fatal(err)
	}
	var wired generation.Repository = repository
	if repositoryWrapper != nil {
		wired = repositoryWrapper(repository)
	}
	clock := &fakeAcmeClock{now: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}
	repository.SetClock(clock.Now)
	module, err := generation.New(generation.Dependencies{
		Repository: wired,
		Catalog:    taskCatalog,
		Providers:  registry,
		Assets:     assetStore,
		Clock:      clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	return &lifecycleHarness{
		module:     module,
		repository: repository,
		assets:     assetStore,
		clock:      clock,
		upstream:   upstream,
		dbPath:     dbPath,
		legacy:     legacyAggregate,
	}
}

// rebuild 在同一数据库与同一资产存储上重建模块（真实重启语义：进程内
// 状态全部丢弃，只保留持久化事实）。
func (h *lifecycleHarness) rebuild(
	t *testing.T,
	repository generation.Repository,
	assetStore *local.Store,
) *generation.Module {
	t.Helper()
	registry := providers.NewRegistry()
	if err := registerProviders(
		Config{ProviderMode: "live", RuntimeSettings: map[string]string{
			"tripo_base_url": h.upstream.server.URL,
			"tripo_api_key":  "lifecycle-test-key",
		}},
		h.catalog(t),
		registry,
		nil, nil,
		discardLogger(),
	); err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{
		Repository: repository,
		Catalog:    h.catalog(t),
		Providers:  registry,
		Assets:     assetStore,
		Clock:      h.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	return module
}

func (h *lifecycleHarness) catalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	return lifecycleCatalog(t, h.legacy)
}

func (h *lifecycleHarness) submit(t *testing.T, input string) generation.TaskID {
	t.Helper()
	ctx := context.Background()
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: lifecycleOwnerID, SessionID: lifecycleSessionID,
		Capability: generation.TextTo3D, OfferingID: lifecycleOffering,
		Input: json.RawMessage(input),
	})
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("lifecycle-%d", lifecycleSubmissionSequence.Add(1))
	operationID := "lifecycle:" + string(prepared.ID)
	grants := executiongrant.Service{Grants: h.repository.(*sqlite.Repository), Prepared: h.repository.(*sqlite.Repository)}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: executiongrant.Principal{OwnerID: string(lifecycleOwnerID), ActorID: "lifecycle-test-human", Source: executiongrant.HumanBrowser},
		SessionID: lifecycleSessionID, OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: key, OperationID: operationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Decide(ctx, executiongrant.Principal{
		OwnerID: string(lifecycleOwnerID), ActorID: "lifecycle-test-human", Source: executiongrant.HumanBrowser,
	}, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	accepted, err := h.module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		OwnerID: lifecycleOwnerID, SessionID: lifecycleSessionID,
		PreparedInputID: prepared.ID, IdempotencyKey: key,
		OperationID: operationID, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}

func (h *lifecycleHarness) reconcile(t *testing.T) {
	t.Helper()
	if _, err := h.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
		WorkerID:  "lifecycle-worker",
		LeaseFor:  time.Minute,
		BatchSize: 10,
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(61 * time.Second)
}

func (h *lifecycleHarness) task(t *testing.T, id generation.TaskID) generation.Task {
	t.Helper()
	task, err := h.module.Get(context.Background(), lifecycleOwnerID, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// runToSucceeded 推进到父任务 SUCCEEDED（必需产物已本地落盘）。
func (h *lifecycleHarness) runToSucceeded(t *testing.T, id generation.TaskID) generation.Task {
	t.Helper()
	for range 20 {
		h.reconcile(t)
		task := h.task(t, id)
		if task.State == generation.TaskSucceeded {
			return task
		}
		if task.State.Terminal() {
			t.Fatalf("task terminal before success: %s %#v", task.State, task.Failure)
		}
	}
	t.Fatal("task did not reach SUCCEEDED")
	return generation.Task{}
}

// drivePostSuccess 推进成功后观察，直至资源全部终态且调度清空。
func (h *lifecycleHarness) drivePostSuccess(
	t *testing.T,
	id generation.TaskID,
	rounds int,
) generation.Task {
	t.Helper()
	for range rounds {
		h.reconcile(t)
		task := h.task(t, id)
		if !generation.ResourcesPending(task.Resources) && task.NextActionAt.IsZero() {
			return task
		}
	}
	return h.task(t, id)
}

func lifecycleSlot(
	t *testing.T,
	task generation.Task,
	variant string,
) generation.ResourceState {
	t.Helper()
	key := lifecycleConverted + "|" + variant + "|"
	state, ok := task.Resources[key]
	if !ok {
		t.Fatalf("缺少槽位 %q：%#v", key, task.Resources)
	}
	return state
}

func lifecycleInputs(formats ...string) string {
	encoded, _ := json.Marshal(map[string]any{
		"prompt":                  "a lantern",
		"output_formats":          formats,
		"authorize_extra_credits": true,
	})
	return string(encoded)
}

// ── P0：首个格式失败不得取消其余已授权格式；混合结果逐项公开 ──

func TestRealTripoFirstFormatFailureKeepsRemainingAuthorizedFormats(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.failChild("GLTF")
	h := newLifecycleHarness(t, upstream, false, nil)
	id := h.submit(t, lifecycleInputs("GLTF", "FBX", "OBJ"))
	h.runToSucceeded(t, id)
	final := h.drivePostSuccess(t, id, 30)

	converts, downloads := upstream.snapshot()
	if len(converts) != 3 {
		t.Fatalf("convert calls = %v; want 3（首个失败后其余已授权格式必须继续）", converts)
	}
	if final.State != generation.TaskSucceeded || final.Progress != 100 {
		t.Fatalf("parent regressed: %s progress=%d", final.State, final.Progress)
	}
	if state := lifecycleSlot(t, final, "converted.gltf"); state.State != generation.ResourceFailed {
		t.Fatalf("converted.gltf = %#v; want failed", state)
	}
	for _, variant := range []string{"converted.fbx", "converted.obj"} {
		if state := lifecycleSlot(t, final, variant); state.State != generation.ResourceReady {
			t.Fatalf("%s = %#v; want ready", variant, state)
		}
	}
	if generation.ResourcesPending(final.Resources) {
		t.Fatalf("全部选中项都已解决，调度必须收尾：%#v", final.Resources)
	}
	assertLifecycleAssets(t, h, final, map[string]string{
		"converted.fbx": "lifecycle-bytes:converted-fbx.fbx",
		"converted.obj": "lifecycle-bytes:converted-obj.obj",
	})
	if downloads["gltf"] != 0 {
		t.Fatalf("失败的转换不得下载产物：%v", downloads)
	}
}

func TestRealTripoMiddleFormatFailureExposesMixedOutcomes(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.failChild("FBX")
	h := newLifecycleHarness(t, upstream, false, nil)
	id := h.submit(t, lifecycleInputs("GLTF", "FBX", "OBJ"))
	h.runToSucceeded(t, id)
	final := h.drivePostSuccess(t, id, 30)

	converts, _ := upstream.snapshot()
	if len(converts) != 3 {
		t.Fatalf("convert calls = %v; want 3", converts)
	}
	want := map[string]generation.ResourceStateCode{
		"converted.gltf": generation.ResourceReady,
		"converted.fbx":  generation.ResourceFailed,
		"converted.obj":  generation.ResourceReady,
	}
	for variant, expected := range want {
		if state := lifecycleSlot(t, final, variant); state.State != expected {
			t.Fatalf("%s = %#v; want %s（混合结果必须逐项公开）", variant, state, expected)
		}
	}
	if final.State != generation.TaskSucceeded {
		t.Fatalf("parent state = %s", final.State)
	}
	assertLifecycleAssets(t, h, final, map[string]string{
		"converted.gltf": lifecycleGLTFBytes,
		"converted.obj":  "lifecycle-bytes:converted-obj.obj",
	})
}

// ── P0/P2：选中 3 项只处理 1 项时不得收尾 ──

func TestRealTripoSelectedThreeProcessedOneCannotFinish(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.runningRounds = 1 << 20 // 子作业永不完成
	h := newLifecycleHarness(t, upstream, false, nil)
	id := h.submit(t, lifecycleInputs("GLTF", "FBX", "OBJ"))
	h.runToSucceeded(t, id)
	// 只推进一轮成功后观察：派发第一个格式。
	h.reconcile(t)
	mid := h.task(t, id)
	converts, _ := upstream.snapshot()
	if len(converts) != 1 {
		t.Fatalf("convert calls = %v; want 1（每轮至多一次派发）", converts)
	}
	if !generation.ResourcesPending(mid.Resources) {
		t.Fatalf("仍有 2 个选中格式未处理，资源不得收尾：%#v", mid.Resources)
	}
	if mid.NextActionAt.IsZero() {
		t.Fatal("仍有未决槽位时调度不得结束（next_action_at 被清空）")
	}
	if mid.State != generation.TaskSucceeded {
		t.Fatalf("parent state = %s", mid.State)
	}
	for _, variant := range []string{"converted.gltf", "converted.fbx", "converted.obj"} {
		if state := lifecycleSlot(t, mid, variant); state.State != generation.ResourcePending {
			t.Fatalf("%s = %#v; want pending", variant, state)
		}
	}
}

// ── P1：上游完成但下载失败 → 后续成功；重试绝不触发付费重转换 ──

func TestRealTripoDownloadFailureRetriesWithoutPaidRegeneration(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.failDownloads("GLTF", 2)
	h := newLifecycleHarness(t, upstream, false, nil)
	id := h.submit(t, lifecycleInputs("GLTF"))
	h.runToSucceeded(t, id)
	mid := h.drivePostSuccess(t, id, 2)
	if state := lifecycleSlot(t, mid, "converted.gltf"); state.State == generation.ResourceReady {
		t.Fatalf("下载全部失败时不得 READY：%#v", state)
	}
	final := h.drivePostSuccess(t, id, 20)
	converts, downloads := upstream.snapshot()
	if len(converts) != 1 {
		t.Fatalf("convert calls = %v; want 1（下载重试绝不重发付费转换）", converts)
	}
	if downloads["gltf"] < 3 {
		t.Fatalf("downloads = %v; want ≥3（两次 503 后成功）", downloads)
	}
	if state := lifecycleSlot(t, final, "converted.gltf"); state.State != generation.ResourceReady {
		t.Fatalf("converted.gltf = %#v; want ready", state)
	}
	assertLifecycleAssets(t, h, final, map[string]string{
		"converted.gltf": lifecycleGLTFBytes,
	})
}

// ── P1/A1：永久下载失败不得饿死其余独立已授权格式 ──
//
// 回归背景（resources-review REJECT，A1）：旧派发顺序把"未兑现重发"排在
// "派发下一个未开始格式"之前——首个（或中间）格式的本地下载永久失败时，
// 每一轮都被该格式的重发占据，其余独立已授权格式**永远得不到派发**，
// 直到 deadline 全体 incomplete。修复后的顺序：歧义遗留 → 在途轮询 →
// 派发未开始的已授权格式 → 未兑现重发 → 完成断言。

// assertConvertsExactlyOnce 校验每个选中格式的付费转换恰好派发一次
// （多重集合相等：不多发、不重发、不漏发）。
func assertConvertsExactlyOnce(t *testing.T, converts []string, want ...string) {
	t.Helper()
	counts := map[string]int{}
	for _, format := range converts {
		counts[strings.ToUpper(format)]++
	}
	if len(converts) != len(want) {
		t.Fatalf("convert calls = %v; want 每个格式恰好一次 %v", converts, want)
	}
	for _, format := range want {
		if counts[strings.ToUpper(format)] != 1 {
			t.Fatalf("convert calls = %v; %s 必须恰好派发一次", converts, format)
		}
	}
}

// assertNoConvertedAsset 校验任务没有指定变体的本地 converted 资产。
func assertNoConvertedAsset(t *testing.T, task generation.Task, variant string) {
	t.Helper()
	for _, asset := range task.Assets {
		if asset.Role == lifecycleConverted && asset.Variant == variant {
			t.Fatalf("%s 不得存在本地资产（下载永久失败）：%#v", variant, asset)
		}
	}
}

func TestRealTripoPermanentDownloadFailureDoesNotStarveUnstartedFormats(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.failDownloads("GLTF", 1<<20) // 首个格式下载永久 503
	h := newLifecycleHarness(t, upstream, false, nil)
	id := h.submit(t, lifecycleInputs("GLTF", "FBX", "OBJ"))
	h.runToSucceeded(t, id)

	// GLTF 上游转换完成但本地兑现永久失败；FBX/OBJ 是独立已授权格式，
	// 必须继续被派发并兑现（重发不得占据未开始格式的派发轮次）。
	mid := h.drivePostSuccess(t, id, 12)
	converts, _ := upstream.snapshot()
	assertConvertsExactlyOnce(t, converts, "GLTF", "FBX", "OBJ")
	if mid.State != generation.TaskSucceeded || mid.Progress != 100 {
		t.Fatalf("parent regressed: %s progress=%d", mid.State, mid.Progress)
	}
	if state := lifecycleSlot(t, mid, "converted.gltf"); state.State == generation.ResourceReady {
		t.Fatalf("下载永久失败的槽位不得 READY：%#v", state)
	}
	assertNoConvertedAsset(t, mid, "converted.gltf")
	for _, variant := range []string{"converted.fbx", "converted.obj"} {
		if state := lifecycleSlot(t, mid, variant); state.State != generation.ResourceReady {
			t.Fatalf("%s = %#v; want ready（独立已授权格式不得被重发饿死）", variant, state)
		}
	}
	assertLifecycleAssets(t, h, mid, map[string]string{
		"converted.fbx": "lifecycle-bytes:converted-fbx.fbx",
		"converted.obj": "lifecycle-bytes:converted-obj.obj",
	})

	// deadline 之后：只有未兑现槽位收尾 incomplete；已交付的幸存者保持
	// READY（终态不可逆），零新增付费调用。
	convertsBefore := len(converts)
	h.clock.Advance(25 * time.Hour)
	final := h.drivePostSuccess(t, id, 4)
	if state := lifecycleSlot(t, final, "converted.gltf"); state.State != generation.ResourceIncomplete {
		t.Fatalf("deadline 后 converted.gltf = %#v; want incomplete", state)
	}
	for _, variant := range []string{"converted.fbx", "converted.obj"} {
		if state := lifecycleSlot(t, final, variant); state.State != generation.ResourceReady {
			t.Fatalf("deadline 后 %s = %#v; want ready（已交付幸存者不得被收尾为 incomplete）", variant, state)
		}
	}
	if generation.ResourcesPending(final.Resources) {
		t.Fatalf("deadline 后资源必须全部终态：%#v", final.Resources)
	}
	if !final.NextActionAt.IsZero() {
		t.Fatalf("deadline 后调度必须清空：%v", final.NextActionAt)
	}
	convertsAfter, _ := upstream.snapshot()
	if len(convertsAfter) != convertsBefore {
		t.Fatalf("重发/deadline 期间重发了付费转换：%v → %v", converts, convertsAfter)
	}
	if final.State != generation.TaskSucceeded {
		t.Fatalf("parent state = %s（成功后观察绝不回退父任务）", final.State)
	}
	assertLifecycleAssets(t, h, final, map[string]string{
		"converted.fbx": "lifecycle-bytes:converted-fbx.fbx",
		"converted.obj": "lifecycle-bytes:converted-obj.obj",
	})
}

func TestRealTripoMiddlePermanentDownloadFailureStillDeliversRemainingFormats(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.failDownloads("FBX", 1<<20) // 中间格式下载永久 503
	h := newLifecycleHarness(t, upstream, false, nil)
	id := h.submit(t, lifecycleInputs("GLTF", "FBX", "OBJ"))
	h.runToSucceeded(t, id)

	mid := h.drivePostSuccess(t, id, 12)
	converts, _ := upstream.snapshot()
	assertConvertsExactlyOnce(t, converts, "GLTF", "FBX", "OBJ")
	if mid.State != generation.TaskSucceeded || mid.Progress != 100 {
		t.Fatalf("parent regressed: %s progress=%d", mid.State, mid.Progress)
	}
	if state := lifecycleSlot(t, mid, "converted.fbx"); state.State == generation.ResourceReady {
		t.Fatalf("下载永久失败的槽位不得 READY：%#v", state)
	}
	assertNoConvertedAsset(t, mid, "converted.fbx")
	for _, variant := range []string{"converted.gltf", "converted.obj"} {
		if state := lifecycleSlot(t, mid, variant); state.State != generation.ResourceReady {
			t.Fatalf("%s = %#v; want ready（末位已授权格式不得被中间格式的重发饿死）", variant, state)
		}
	}
	assertLifecycleAssets(t, h, mid, map[string]string{
		"converted.gltf": lifecycleGLTFBytes,
		"converted.obj":  "lifecycle-bytes:converted-obj.obj",
	})

	convertsBefore := len(converts)
	h.clock.Advance(25 * time.Hour)
	final := h.drivePostSuccess(t, id, 4)
	if state := lifecycleSlot(t, final, "converted.fbx"); state.State != generation.ResourceIncomplete {
		t.Fatalf("deadline 后 converted.fbx = %#v; want incomplete", state)
	}
	for _, variant := range []string{"converted.gltf", "converted.obj"} {
		if state := lifecycleSlot(t, final, variant); state.State != generation.ResourceReady {
			t.Fatalf("deadline 后 %s = %#v; want ready", variant, state)
		}
	}
	convertsAfter, _ := upstream.snapshot()
	if len(convertsAfter) != convertsBefore {
		t.Fatalf("deadline 期间重发了付费转换：%v → %v", converts, convertsAfter)
	}
	if final.State != generation.TaskSucceeded {
		t.Fatalf("parent state = %s", final.State)
	}
	assertLifecycleAssets(t, h, final, map[string]string{
		"converted.gltf": lifecycleGLTFBytes,
		"converted.obj":  "lifecycle-bytes:converted-obj.obj",
	})
}

// ── P1 公平性（第三波，父任务裁决落地）：多个未兑现重发之间按
// DeliveryAttempts 最少者轮换 ──
//
// 回归背景（§11.9 P1 未决政策 → 用户批准落地）：旧选择器按格式名字典序
// 恒取首个 done+pending 项——字典序靠前的格式（FBX）下载永久失败时，它
// 独占全部重发轮次，下载已恢复的靠后格式（GLTF）在 deadline 前永不重发
// （第二波外部探针实证：下载计数 fbx=25 / gltf=1，轮数预算相关）。新选择
// 器：DeliveryAttempts 最少者优先（平局取字典序），轮换进度随私有状态
// 持久化、跨重启不丢；重发的唯一界限仍是 Schema `post_success.deadline_
// seconds`（86400s，本波不变），不新增任何尝试次数上限。
//
// RED（旧选择器）：阶段一轮次预算耗尽时 gltf 下载计数停在 1；
// GREEN（公平选择器）：轮换在预算内把重发轮交给 GLTF，重启后首个重发
// 仍按持久化的 DeliveryAttempts 选中 GLTF。

func TestRealTripoRedeliveryFairnessRotatesLeastDeliveryAttempts(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.failDownloads("FBX", 1<<20) // 字典序首个格式：下载永久 503
	upstream.failDownloads("GLTF", 2)    // 靠后格式：前两次下载 503，其后恢复
	h := newLifecycleHarness(t, upstream, false, nil)
	id := h.submit(t, lifecycleInputs("GLTF", "FBX"))
	h.runToSucceeded(t, id)

	// 阶段 1：驱动到轮换把重发轮交给 GLTF——下载计数 gltf=2（首次交付 +
	// 一次重发，均 503）且 fbx≥3（FBX 的 DeliveryAttempts 严格大于 GLTF：
	// 首次交付 + 至少两次重发）。公平路径至多 7 轮（派发×2 + 轮询交付×2 +
	// 轮换×3），预算 12 轮；旧选择器下 gltf 恒为 1，预算耗尽即 RED。
	rotated := false
	var converts []string
	var downloads map[string]int
	for range 12 {
		h.reconcile(t)
		converts, downloads = upstream.snapshot()
		if downloads["fbx"] >= 3 && downloads["gltf"] == 2 {
			rotated = true
			break
		}
	}
	if !rotated {
		t.Fatalf("重发轮换未在轮次预算 12 内把重发轮交给已恢复的格式：downloads fbx=%d / gltf=%d；converts=%v"+
			"（旧字典序选择器下 gltf 停在 1：永久失败的 FBX 独占每一轮）",
			downloads["fbx"], downloads["gltf"], converts)
	}
	assertConvertsExactlyOnce(t, converts, "GLTF", "FBX")
	mid := h.task(t, id)
	if mid.State != generation.TaskSucceeded || mid.Progress != 100 {
		t.Fatalf("parent regressed: %s progress=%d", mid.State, mid.Progress)
	}
	for _, variant := range []string{"converted.gltf", "converted.fbx"} {
		if state := lifecycleSlot(t, mid, variant); state.State != generation.ResourcePending {
			t.Fatalf("恢复兑现前 %s = %#v; want pending", variant, state)
		}
	}
	assertNoConvertedAsset(t, mid, "converted.gltf")
	assertNoConvertedAsset(t, mid, "converted.fbx")

	// 阶段 2：真实重启（同一 SQLite + 同一资产存储）。轮换进度
	// （DeliveryAttempts：FBX > GLTF）必须随私有状态持久化——重启后首个
	// 重发必须选中 DeliveryAttempts 最少的 GLTF（其第 3 次下载成功 →
	// READY，字节一致），不得回退为字典序 FBX，也不得新增任何付费调用。
	fbxBefore, gltfBefore := downloads["fbx"], downloads["gltf"]
	h.module = h.rebuild(t, h.repository, h.assets)
	h.reconcile(t)
	converts, downloads = upstream.snapshot()
	if downloads["gltf"] == gltfBefore {
		if downloads["fbx"] != fbxBefore {
			t.Fatalf("重启后首个重发被字典序 FBX 占据：fbx %d→%d gltf=%d",
				fbxBefore, downloads["fbx"], downloads["gltf"])
		}
		h.reconcile(t) // 防御：任务尚未到期时空轮一次（不得改变付费计数）
		converts, downloads = upstream.snapshot()
	}
	if downloads["gltf"] != gltfBefore+1 || downloads["fbx"] != fbxBefore {
		t.Fatalf("重启后轮换未先选 DeliveryAttempts 最少的 GLTF：fbx %d→%d gltf %d→%d",
			fbxBefore, downloads["fbx"], gltfBefore, downloads["gltf"])
	}
	assertConvertsExactlyOnce(t, converts, "GLTF", "FBX")
	recovered := h.task(t, id)
	if state := lifecycleSlot(t, recovered, "converted.gltf"); state.State != generation.ResourceReady {
		t.Fatalf("converted.gltf = %#v; want ready（恢复的下载经重发必须兑现）", state)
	}
	if state := lifecycleSlot(t, recovered, "converted.fbx"); state.State != generation.ResourcePending {
		t.Fatalf("converted.fbx = %#v; want pending（下载仍永久失败，不得假兑现）", state)
	}
	assertNoConvertedAsset(t, recovered, "converted.fbx")
	assertLifecycleAssets(t, h, recovered, map[string]string{
		"converted.gltf": lifecycleGLTFBytes,
	})
	if recovered.State != generation.TaskSucceeded {
		t.Fatalf("parent state = %s", recovered.State)
	}

	// 阶段 3：deadline（86400s，Schema 不变）之后只有未兑现的 FBX 收尾
	// incomplete；已 READY 的 GLTF 终态不可逆，零新增付费调用，父任务不回退。
	h.clock.Advance(25 * time.Hour)
	final := h.drivePostSuccess(t, id, 4)
	if state := lifecycleSlot(t, final, "converted.fbx"); state.State != generation.ResourceIncomplete {
		t.Fatalf("deadline 后 converted.fbx = %#v; want incomplete", state)
	}
	if state := lifecycleSlot(t, final, "converted.gltf"); state.State != generation.ResourceReady {
		t.Fatalf("deadline 后 converted.gltf = %#v; want ready（终态不可逆）", state)
	}
	if generation.ResourcesPending(final.Resources) {
		t.Fatalf("deadline 后资源必须全部终态：%#v", final.Resources)
	}
	if !final.NextActionAt.IsZero() {
		t.Fatalf("deadline 后调度必须清空：%v", final.NextActionAt)
	}
	convertsAfter, _ := upstream.snapshot()
	assertConvertsExactlyOnce(t, convertsAfter, "GLTF", "FBX")
	if final.State != generation.TaskSucceeded {
		t.Fatalf("parent state = %s（成功后观察绝不回退父任务）", final.State)
	}
	assertLifecycleAssets(t, h, final, map[string]string{
		"converted.gltf": lifecycleGLTFBytes,
	})
}

// ── P1：上游完成与下载之间重启 → 证据不丢、零重复付费 ──

func TestRealTripoRestartBetweenUpstreamCompletionAndDownload(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.failDownloads("GLTF", 1<<20) // 重启前下载持续 503
	h := newLifecycleHarness(t, upstream, false, nil)
	id := h.submit(t, lifecycleInputs("GLTF"))
	h.runToSucceeded(t, id)
	// 推进到子作业完成但下载持续失败（资源仍 pending）。
	for range 6 {
		h.reconcile(t)
	}
	before := h.task(t, id)
	if state := lifecycleSlot(t, before, "converted.gltf"); state.State != generation.ResourcePending {
		t.Fatalf("重启前槽位应为 pending：%#v", state)
	}
	convertsBefore, downloadsBefore := upstream.snapshot()
	if downloadsBefore["gltf"] == 0 {
		t.Fatalf("重启前应已尝试下载：%v", downloadsBefore)
	}
	// 真实重启：同一 SQLite + 同一资产存储上重建模块；上游/CDN 恢复。
	upstream.allowDownloads("GLTF")
	h.module = h.rebuild(t, h.repository, h.assets)
	final := h.drivePostSuccess(t, id, 20)
	convertsAfter, downloads := upstream.snapshot()
	if len(convertsAfter) != len(convertsBefore) || len(convertsAfter) != 1 {
		t.Fatalf("重启后重发了付费转换：%v → %v", convertsBefore, convertsAfter)
	}
	if state := lifecycleSlot(t, final, "converted.gltf"); state.State != generation.ResourceReady {
		t.Fatalf("converted.gltf = %#v; want ready（重启后必须能兑现）", state)
	}
	if downloads["gltf"] <= downloadsBefore["gltf"] {
		t.Fatalf("downloads = %v; 重启后应继续下载（重启前 %v）", downloads, downloadsBefore)
	}
	assertLifecycleAssets(t, h, final, map[string]string{
		"converted.gltf": lifecycleGLTFBytes,
	})
}

// ── P1：提交 CAS 冲突不丢下载证据、不假 READY ──

type lifecycleConflictRepo struct {
	generation.Repository
	mu       sync.Mutex
	failures int
	failed   int
}

func (r *lifecycleConflictRepo) Commit(ctx context.Context, commit generation.Commit) error {
	r.mu.Lock()
	injected := 0
	if r.failed < r.failures && carriesConvertedAsset(commit.NewAssets) {
		r.failed++
		injected = 1
	}
	r.mu.Unlock()
	if injected == 1 {
		return generation.RepositoryConflict
	}
	return r.Repository.Commit(ctx, commit)
}

func (r *lifecycleConflictRepo) CreatePreparedInput(ctx context.Context, input generation.PreparedInput) error {
	return r.Repository.(*sqlite.Repository).CreatePreparedInput(ctx, input)
}

func (r *lifecycleConflictRepo) GetPreparedInput(ctx context.Context, owner generation.OwnerID, id generation.PreparedInputID) (generation.PreparedInput, error) {
	return r.Repository.(*sqlite.Repository).GetPreparedInput(ctx, owner, id)
}

func (r *lifecycleConflictRepo) AdmittedPreparedInput(ctx context.Context, owner generation.OwnerID, task generation.TaskID) (generation.PreparedInputID, error) {
	return r.Repository.(*sqlite.Repository).AdmittedPreparedInput(ctx, owner, task)
}

func (r *lifecycleConflictRepo) OwnsSession(ctx context.Context, owner, session string) (bool, error) {
	return r.Repository.(*sqlite.Repository).OwnsSession(ctx, owner, session)
}

// carriesConvertedAsset 判定提交是否携带转换产物（只对成功后合并注入
// 冲突，避免误伤父任务物化提交）。
func carriesConvertedAsset(assets []generation.Asset) bool {
	for _, asset := range assets {
		if asset.Role == lifecycleConverted {
			return true
		}
	}
	return false
}

func (r *lifecycleConflictRepo) injected() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}

func TestRealTripoCommitConflictKeepsEvidenceAndNeverFakesReady(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	wrapper := &lifecycleConflictRepo{failures: 1}
	h := newLifecycleHarness(t, upstream, false, func(repository generation.Repository) generation.Repository {
		wrapper.Repository = repository
		return wrapper
	})
	id := h.submit(t, lifecycleInputs("GLTF"))
	h.runToSucceeded(t, id)
	// 推进到"转换产物已下载但携带它的提交被注入版本冲突"：资产已写盘
	// 而 DB 无记录（丢 CAS 的孤儿 blob 留给离线回收，绝不删除可能被
	// 其他赢家引用的 blob）。
	var conflicted generation.Task
	for range 8 {
		h.reconcile(t)
		if wrapper.injected() == 0 {
			continue
		}
		conflicted = h.task(t, id)
		break
	}
	if wrapper.injected() != 1 {
		t.Fatalf("冲突注入未生效：injected=%d", wrapper.injected())
	}
	// CAS 冲突后不得出现**无资产证据**的 READY（P1/A2）：若槽位声称
	// READY，任务必须携带已提交、可打开且字节一致的匹配本地资产；
	// 冲突提交整体不落库（资源态与资产行同一事务）→ 槽位应保持 pending。
	//（A2 整改：旧断言方向写反——只在"READY 且**存在** converted 资产"
	// 时失败，把合法证据当违规，而对"READY 且零证据"的真实缺陷保持沉默。）
	if state := lifecycleSlot(t, conflicted, "converted.gltf"); state.State == generation.ResourceReady {
		evidence := ""
		for _, asset := range conflicted.Assets {
			if asset.Role != lifecycleConverted || asset.Variant != "converted.gltf" {
				continue
			}
			body, openErr := h.assets.Open(context.Background(), asset)
			if openErr != nil {
				t.Fatalf("READY 但匹配的本地资产不可打开：%v", openErr)
			}
			content, readErr := io.ReadAll(body)
			closeErr := body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("READY 但匹配的本地资产读取失败：%v %v", readErr, closeErr)
			}
			evidence = string(content)
		}
		if evidence != lifecycleGLTFBytes {
			t.Fatalf("CAS 失败后不得出现无匹配本地资产证据的 READY：%#v evidence=%q", state, evidence)
		}
	}
	final := h.drivePostSuccess(t, id, 20)
	converts, _ := upstream.snapshot()
	if len(converts) != 1 {
		t.Fatalf("convert calls = %v; want 1（冲突恢复不得重发付费转换）", converts)
	}
	if state := lifecycleSlot(t, final, "converted.gltf"); state.State != generation.ResourceReady {
		t.Fatalf("converted.gltf = %#v; want ready", state)
	}
	assertLifecycleAssets(t, h, final, map[string]string{
		"converted.gltf": lifecycleGLTFBytes,
	})
}

// ── P1/P2：零本地资产永不 READY；deadline 本地收尾 ──

func TestRealTripoZeroLocalAssetsNeverReadyAndDeadlineFinalizes(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.failDownloads("GLTF", 1<<20) // 下载永远 503
	h := newLifecycleHarness(t, upstream, false, nil)
	id := h.submit(t, lifecycleInputs("GLTF"))
	h.runToSucceeded(t, id)
	// 推进若干轮：上游子作业已完成，但本地零资产 → 槽位必须保持 pending。
	for range 6 {
		h.reconcile(t)
	}
	mid := h.task(t, id)
	if state := lifecycleSlot(t, mid, "converted.gltf"); state.State != generation.ResourcePending {
		t.Fatalf("零本地资产时槽位 = %#v; want pending（上游完成 ≠ 本地 READY）", state)
	}
	for _, asset := range mid.Assets {
		if asset.Role == lifecycleConverted {
			t.Fatalf("下载全部失败却出现 converted 资产：%#v", asset)
		}
	}
	convertsBefore, _ := upstream.snapshot()
	// 越过 deadline（86400s）：本地 finalizer 收尾，零新增上游调用。
	h.clock.Advance(25 * time.Hour)
	final := h.drivePostSuccess(t, id, 4)
	if state := lifecycleSlot(t, final, "converted.gltf"); state.State != generation.ResourceIncomplete {
		t.Fatalf("deadline 后槽位 = %#v; want incomplete", state)
	}
	if !final.NextActionAt.IsZero() {
		t.Fatalf("deadline 后调度必须清空：%v", final.NextActionAt)
	}
	convertsAfter, _ := upstream.snapshot()
	if len(convertsAfter) != len(convertsBefore) {
		t.Fatalf("deadline 收尾期间重发了付费转换：%v → %v", convertsBefore, convertsAfter)
	}
	if final.State != generation.TaskSucceeded {
		t.Fatalf("parent state = %s（成功后观察绝不回退父任务）", final.State)
	}
}

// ── P2：缺同意 → 零付费请求 ──

func TestRealTripoMissingConsentMakesZeroPaidRequests(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	h := newLifecycleHarness(t, upstream, false, nil)
	// 缺同意：提交期即拒绝（共享评估器），零 convert 调用。
	_, err := h.module.Submit(context.Background(), generation.SubmitCommand{
		OwnerID:    lifecycleOwnerID,
		Capability: generation.TextTo3D,
		OfferingID: lifecycleOffering,
		Input:      json.RawMessage(`{"prompt":"a lantern","output_formats":["GLTF","FBX"]}`),
	})
	if err == nil {
		t.Fatal("缺同意必须提交期拒绝")
	}
	if converts, _ := upstream.snapshot(); len(converts) != 0 {
		t.Fatalf("缺少同意时不得发送付费请求：%v", converts)
	}
	page, err := h.module.List(context.Background(), generation.OwnTasks(lifecycleOwnerID), generation.ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 0 {
		t.Fatalf("未获附加费用同意的请求不得创建任务：%#v", page.Tasks)
	}
}

// ── P2：历史聚合快照不追溯改写、不产生新付费义务 ──

func TestRealTripoLegacyAggregateSnapshotKeepsShapeAndNoNewPaidDuties(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	h := newLifecycleHarness(t, upstream, true, nil)
	// 历史形状（无 variant_from）：只有一个聚合前缀槽位，不追溯展开。
	id := h.submit(t, lifecycleInputs("GLTF", "FBX"))
	succeeded := h.runToSucceeded(t, id)
	if _, ok := succeeded.Resources[lifecycleConverted+"|converted.|"]; !ok {
		t.Fatalf("历史快照必须保持聚合槽位形状：%#v", succeeded.Resources)
	}
	for key := range succeeded.Resources {
		if strings.HasPrefix(key, lifecycleConverted+"|converted.") && key != lifecycleConverted+"|converted.|" {
			t.Fatalf("历史快照被追溯展开为逐项槽位：%#v", succeeded.Resources)
		}
	}
	final := h.drivePostSuccess(t, id, 30)
	converts, _ := upstream.snapshot()
	if len(converts) != 2 {
		t.Fatalf("convert calls = %v; want 2（只兑现既有授权，不新增付费义务）", converts)
	}
	if state := lifecycleSlot(t, final, "converted."); state.State != generation.ResourceReady {
		t.Fatalf("聚合槽位 = %#v; want ready", state)
	}
	// 历史任务再次调度：零新增付费调用（无追溯重放）。
	before := len(converts)
	for range 3 {
		h.reconcile(t)
	}
	convertsAfter, _ := upstream.snapshot()
	if len(convertsAfter) != before {
		t.Fatalf("历史任务被重放付费转换：%v → %v", converts, convertsAfter)
	}
}

// ── 存储证据：本地资产真实可读且内容一致 ──

func assertLifecycleAssets(
	t *testing.T,
	h *lifecycleHarness,
	task generation.Task,
	want map[string]string,
) {
	t.Helper()
	found := map[string]generation.Asset{}
	for _, asset := range task.Assets {
		if asset.Role == lifecycleConverted {
			found[asset.Variant] = asset
		}
	}
	for variant, expected := range want {
		asset, ok := found[variant]
		if !ok {
			t.Fatalf("缺少本地资产 %s（已落盘资产：%v）", variant, found)
		}
		body, err := h.assets.Open(context.Background(), asset)
		if err != nil {
			t.Fatalf("资产 %s 不可读：%v", variant, err)
		}
		content, readErr := io.ReadAll(body)
		closeErr := body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("读取资产 %s 失败：%v %v", variant, readErr, closeErr)
		}
		if string(content) != expected {
			t.Fatalf("资产 %s 内容 = %q; want %q", variant, content, expected)
		}
		if asset.Size != int64(len(expected)) {
			t.Fatalf("资产 %s size = %d; want %d", variant, asset.Size, len(expected))
		}
	}
	for variant := range found {
		if _, ok := want[variant]; !ok {
			t.Errorf("出现未预期的 converted 资产 %s", variant)
		}
	}
}
