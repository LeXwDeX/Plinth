package generation_test

// post_success_test.go 落实 §6.6 成功后观察生命周期用例（W6）：终态不
// 回退、不新建 Attempt/Start、迟到可选资产幂等合并、可选失败/歧义不使
// 任务失败、deadline 停止并只写一次 incomplete、重启越过 deadline 的
// 本地收尾、生成期多格式授权/拒绝成本、私有子作业运行中＝父完成资源
// 待定。全部真实文件 SQLite + 真实模块；上游为本地 fixture（零付费）。

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Historical tasks retain the old on-demand conversion contract. Make only
// that archived Offering available in this fixture; the default Catalog keeps
// it disabled for new production submissions.
func historicalPostSuccessCatalog(t *testing.T) (*catalog.Catalog, error) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "catalog", "default_catalog.json"))
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, err
	}
	var offerings []map[string]json.RawMessage
	if err := json.Unmarshal(document["offerings"], &offerings); err != nil {
		return nil, err
	}
	for _, offering := range offerings {
		var id string
		if err := json.Unmarshal(offering["id"], &id); err != nil {
			return nil, err
		}
		if id == "tripo/tripo-v3.1/text-to-3d" {
			offering["enabled"] = json.RawMessage(`true`)
			// This fixture exercises tasks frozen under the former per-format
			// Cost contract, not today's NativePricing revision.
			offering["revision"] = json.RawMessage(`"2026-10-16.213.1"`)
			offering["estimated_credits"] = json.RawMessage(`20`)
			offering["cost"] = json.RawMessage(`{"extra_credits":5,"selection_parameter":"output_formats","consent_parameter":"authorize_extra_credits","extra_label":"附加导出格式"}`)
			offering["cost_policy"] = json.RawMessage(`{"max_authorized_extra_credits":30}`)
			delete(offering, "native_pricing")
			var parameters []map[string]json.RawMessage
			if err := json.Unmarshal(offering["parameters"], &parameters); err != nil {
				return nil, err
			}
			for _, parameter := range parameters {
				var name string
				if err := json.Unmarshal(parameter["name"], &name); err != nil {
					return nil, err
				}
				if name == "output_formats" {
					delete(parameter, "default")
					delete(parameter, "required")
					delete(parameter, "min_items")
				}
			}
			offering["parameters"], err = json.Marshal(parameters)
			if err != nil {
				return nil, err
			}
		}
	}
	document["offerings"], err = json.Marshal(offerings)
	if err != nil {
		return nil, err
	}
	var application map[string]json.RawMessage
	if err := json.Unmarshal(document["application"], &application); err != nil {
		return nil, err
	}
	var discovery map[string]json.RawMessage
	if err := json.Unmarshal(application["provider_discovery"], &discovery); err != nil {
		return nil, err
	}
	var legacyModels []json.RawMessage
	if err := json.Unmarshal(discovery["legacy_models"], &legacyModels); err != nil {
		return nil, err
	}
	legacyModels = append(legacyModels, json.RawMessage(`{"model_id":"tripo-v3.1","provider_key":"tripo-3d","recommended":false}`))
	discovery["legacy_models"], err = json.Marshal(legacyModels)
	if err != nil {
		return nil, err
	}
	application["provider_discovery"], err = json.Marshal(discovery)
	if err != nil {
		return nil, err
	}
	document["application"], err = json.Marshal(application)
	if err != nil {
		return nil, err
	}
	body, err = json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return catalog.Load(bytes.NewReader(body))
}

func newHistoricalPostSuccessHarness(t *testing.T, adapter generation.ProviderPort, dbPath string) *checkpointHarness {
	t.Helper()
	return newCheckpointHarnessWithClock(t, adapter, dbPath, nil, true)
}

// postSuccessAdapter 模拟声明 post_success 的生成 Offering 的全流程：
// 常规路径 ACCEPTED→RUNNING→COMPLETED（model + thumbnail）；成功后按
// 冻结输入逐格式 Checkpoint+发送（计数），子作业可配置为若干轮运行中
// 后完成/失败；崩溃模式在发送后返回歧义错误（无回执）。
type postSuccessAdapter struct {
	mu sync.Mutex
	// 常规路径
	polls int
	// 成功后观察
	postSuccessCalls   int
	checkpoints        int
	convertSends       int
	redeliveries       int
	childRunningRounds int
	childFails         bool
	crashAfterSend     bool
	// 子作业状态：format → {remote, polls, done}
	children map[string]*postSuccessChild
	starts   int
}

type postSuccessChild struct {
	remote string
	polls  int
}

func newPostSuccessAdapter() *postSuccessAdapter {
	return &postSuccessAdapter{children: map[string]*postSuccessChild{}}
}

func postSuccessThumbnailPNG() []byte {
	var body bytes.Buffer
	if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		panic(err)
	}
	return body.Bytes()
}

func (a *postSuccessAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.starts++
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "ps-parent"},
		PollAfter: time.Millisecond,
		DedupeKey: "ps-parent:accepted",
	}, nil
}

func (a *postSuccessAdapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !request.PostSuccess {
		a.polls++
		if a.polls < 2 {
			return generation.Observation{
				Phase:     generation.UpstreamRunning,
				Job:       &request.Job,
				Progress:  40,
				PollAfter: time.Millisecond,
				DedupeKey: "ps-parent:running",
			}, nil
		}
		return generation.Observation{
			Phase:     generation.UpstreamCompleted,
			Job:       &request.Job,
			Progress:  100,
			DedupeKey: "ps-parent:completed",
			Artifacts: []generation.ArtifactClaim{{
				ID:        "ps:model",
				Role:      "model",
				Inline:    validGLB("post-success parent"),
				MediaType: "model/gltf-binary",
			}, {
				ID:        "ps:thumbnail",
				Role:      "thumbnail",
				Variant:   "rendered_image",
				Inline:    postSuccessThumbnailPNG(),
				MediaType: "image/png",
			}},
		}, nil
	}
	return a.observePostSuccessLocked(ctx, request)
}

// observePostSuccessLocked 镜像 tripo 私有编排协议（每轮至多一次动作；
// 一次 ack 至多一次发送；完成断言由状态表推导）。
func (a *postSuccessAdapter) observePostSuccessLocked(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	a.postSuccessCalls++
	var input struct {
		OutputFormats []string `json:"output_formats"`
	}
	_ = json.Unmarshal(request.Input, &input)
	// 私有状态（与 tripo 相同形态）
	var state postSuccessFixtureState
	if len(request.Opaque) > 0 {
		_ = json.Unmarshal(request.Opaque, &state)
	}
	if state.Conversions == nil {
		state.Conversions = map[string]postSuccessFixtureItem{}
	}
	base := generation.Observation{Job: &request.Job, PollAfter: 10 * time.Millisecond}
	encode := func() json.RawMessage {
		encoded, _ := json.Marshal(state)
		return encoded
	}
	// 1) 歧义遗留：abandon 全部未终态格式并立即收尾（后续派发被
	// pending 栅栏阻止——镜像 tripo 实现，不得继续派发）。
	hadDispatching := false
	for format, current := range state.Conversions {
		if current.Status == "dispatching" {
			current.Status = "abandoned"
			state.Conversions[format] = current
			hadDispatching = true
		}
	}
	if hadDispatching {
		for _, format := range input.OutputFormats {
			if _, exists := state.Conversions[format]; !exists {
				state.Conversions[format] = postSuccessFixtureItem{Status: "abandoned"}
			}
		}
		return completionObservation(base, request, state.Conversions, encode), nil
	}
	// 2) 在途子作业。
	for _, format := range sortedKeys(state.Conversions) {
		current := state.Conversions[format]
		if current.Status != "submitted" {
			continue
		}
		child := a.children[format]
		child.polls++
		if child.polls <= a.childRunningRounds {
			base.Phase = generation.UpstreamRunning
			base.Progress = 50
			base.DedupeKey = "convert:" + format + ":running"
			base.Opaque = encode()
			return base, nil
		}
		if a.childFails {
			current.Status = "failed"
			state.Conversions[format] = current
			return completionObservation(base, request, state.Conversions, encode), nil
		}
		current.Status = "done"
		state.Conversions[format] = current
		return generation.Observation{
			Phase:     generation.UpstreamCompleted,
			Job:       &request.Job,
			Progress:  100,
			DedupeKey: "convert:" + format + ":completed",
			Artifacts: []generation.ArtifactClaim{{
				ID:        "convert:" + format,
				Role:      "converted_model",
				Variant:   "converted." + lower(format),
				Inline:    validGLB("converted " + format),
				MediaType: "model/gltf-binary",
			}},
			Opaque: encode(),
		}, nil
	}
	// 3) 派发下一个未开始格式（规划器已由提交期保证；此处直接检查点+
	//    发送）。镜像 tripo A1 整改后的顺序：未开始的独立已授权格式优先
	//    于未兑现重发——某个格式的本地兑现失败不得饿死其余未开始格式。
	for _, format := range input.OutputFormats {
		if _, exists := state.Conversions[format]; exists {
			continue
		}
		state.Conversions[format] = postSuccessFixtureItem{Status: "dispatching"}
		receipt, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{
			Bytes: encode(),
		})
		a.checkpoints++
		if err != nil {
			return generation.Observation{}, err
		}
		a.convertSends++
		if a.crashAfterSend {
			return generation.Observation{}, &generation.ProviderError{
				Failure: &generation.Failure{
					Class:      generation.FailureSubmissionUnknown,
					Ambiguous:  true,
					SafeDetail: "转换提交后丢失上游响应（fixture 歧义崩溃）",
				},
			}
		}
		state.Conversions[format] = postSuccessFixtureItem{Status: "submitted", RemoteID: "child-" + lower(format)}
		a.children[format] = &postSuccessChild{remote: "child-" + lower(format)}
		base.Phase = generation.UpstreamRunning
		base.Progress = 50
		base.DedupeKey = "convert:" + format + ":submitted"
		base.Opaque = encode()
		base.Dispatch = receipt
		return base, nil
	}
	// 4) 无未开始格式。上游已完成但内核回报槽位仍未兑现（下载失败/重启/
	//    CAS 失败）：重发同一产物声明（fixture 用内联字节，零新增发送/
	//    零付费）。镜像 tripo P1 公平性选择（第三波）：多个未兑现
	//    done+pending 项之间取 DeliveryAttempts 最少者，平局取字典序
	//    （sortedKeys 稳定序 + 严格小于比较）。
	bestFormat := ""
	var bestItem postSuccessFixtureItem
	redelivering := false
	for _, format := range sortedKeys(state.Conversions) {
		current := state.Conversions[format]
		if current.Status != "done" {
			continue
		}
		variant := "converted." + lower(format)
		if !fixtureSlotPending(request.ResourceSlots, variant) {
			continue
		}
		if !redelivering || current.DeliveryAttempts < bestItem.DeliveryAttempts {
			bestFormat, bestItem, redelivering = format, current, true
		}
	}
	if redelivering {
		bestItem.DeliveryAttempts++
		state.Conversions[bestFormat] = bestItem
		a.redeliveries++
		return generation.Observation{
			Phase:     generation.UpstreamCompleted,
			Job:       &request.Job,
			Progress:  100,
			DedupeKey: "convert:" + bestFormat + ":redeliver:" + string(rune('0'+bestItem.DeliveryAttempts)),
			Artifacts: []generation.ArtifactClaim{{
				ID:        "convert:" + bestFormat,
				Role:      "converted_model",
				Variant:   "converted." + lower(bestFormat),
				Inline:    validGLB("converted " + bestFormat),
				MediaType: "model/gltf-binary",
			}},
			Opaque: encode(),
		}, nil
	}
	// 5) 全部终态：完成断言（任一成功 → ready；全失败 → failed）。
	return completionObservation(base, request, state.Conversions, encode), nil
}

func completionObservation(
	base generation.Observation,
	request generation.ObserveRequest,
	conversions map[string]postSuccessFixtureItem,
	encode func() json.RawMessage,
) generation.Observation {
	base.Phase = generation.UpstreamCompleted
	base.Progress = 100
	base.DedupeKey = "post-success:done"
	base.Opaque = encode()
	if len(conversions) == 0 {
		return base
	}
	// 镜像 tripo 新协议（回顾性整改 P0/P1/P2）：逐项断言失败族终态，只
	// 针对内核回报仍 pending 的槽位；done 项**不**断言 ready——READY 只
	// 来自内核的本地资产证据；仍有未开始的选中格式时不做整槽断言。
	for _, format := range sortedKeys(conversions) {
		item := conversions[format]
		var state generation.ResourceStateCode
		var reason string
		switch item.Status {
		case "abandoned":
			state, reason = generation.ResourceAbandoned, "post_success.conversion_abandoned"
		case "denied":
			state, reason = generation.ResourceDenied, "post_success.conversion_denied"
		case "failed":
			state, reason = generation.ResourceFailed, "post_success.conversion_failed"
		default:
			continue
		}
		variant := "converted." + lower(format)
		if !fixtureSlotPending(request.ResourceSlots, variant) {
			continue
		}
		base.ResourceUpdates = append(base.ResourceUpdates, generation.ResourceUpdate{
			Role:    "converted_model",
			Variant: variant,
			State:   state,
			Reason:  reason,
		})
	}
	return base
}

// fixtureSlotPending 镜像 tripo 的只读槽位判定：内核未提供槽位视图时保守
// 认为未决（由内核"仅 pending 可转移 + 终态不可逆"兜住幂等）。
func fixtureSlotPending(slots []generation.ResourceSlotState, variant string) bool {
	if len(slots) == 0 {
		return true
	}
	for _, slot := range slots {
		if slot.Role == "converted_model" && slot.Variant == variant {
			return slot.State == generation.ResourcePending
		}
	}
	return false
}

// postSuccessFixtureState 是 fixture 的私有转换表（与 tripo 形态一致）。
type postSuccessFixtureState struct {
	Conversions map[string]postSuccessFixtureItem `json:"conversions"`
}

type postSuccessFixtureItem struct {
	Status   string `json:"status"`
	RemoteID string `json:"remote_id,omitempty"`
	// DeliveryAttempts 镜像 tripo 私有状态：已就该格式重发过多少次产物
	// 声明（下载失败/重启后重发，绝不重发付费转换）。
	DeliveryAttempts int `json:"delivery_attempts,omitempty"`
}

// DecodeWebhook 让本 fixture 可作为 webhook 解码器：成功后观察的迟到
// webhook 载荷返回 COMPLETED + 新可选产物（converted_model 交付形态）。
func (a *postSuccessAdapter) DecodeWebhook(
	_ context.Context,
	request generation.WebhookDecodeRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:     generation.UpstreamCompleted,
		Job:       &generation.UpstreamJob{ProviderID: request.Job.ProviderID, RemoteID: request.Job.RemoteID},
		Progress:  100,
		DedupeKey: "webhook:convert:late",
		Artifacts: []generation.ArtifactClaim{{
			ID:        "webhook:convert:GLTF",
			Role:      "converted_model",
			Variant:   "converted.webhook",
			Inline:    validGLB("webhook delivered"),
			MediaType: "model/gltf-binary",
		}},
	}, nil
}

func (a *postSuccessAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

func (*postSuccessAdapter) DeleteProviderTask(context.Context, generation.DeleteProviderRequest) error {
	return nil
}

func (a *postSuccessAdapter) snapshot() (starts, checkpoints, sends, postCalls int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.starts, a.checkpoints, a.convertSends, a.postSuccessCalls
}

// redeliveryCount 报告"上游已完成但本地未兑现"的产物声明重发次数
// （回顾性整改 P1：下载重试永不触发付费重转换）。
func (a *postSuccessAdapter) redeliveryCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.redeliveries
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func lower(value string) string {
	out := []byte(value)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}

// convertedSlot 读取逐项 converted 槽位（回顾性整改 P2：每个被请求的格式
// 都有独立的持久身份与结果，聚合前缀槽位只属于历史快照形状）。
func convertedSlot(
	t *testing.T,
	task generation.Task,
	format string,
) generation.ResourceState {
	t.Helper()
	key := "converted_model|converted." + lower(format) + "|"
	state, ok := task.Resources[key]
	if !ok {
		t.Fatalf("缺少逐项槽位 %q：%#v", key, task.Resources)
	}
	return state
}

// submitPostSuccess 提交带格式选择的 tripo 文生 3D 任务并跑到终态。
func submitPostSuccess(
	t *testing.T,
	h *checkpointHarness,
	input string,
) generation.Task {
	t.Helper()
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-dispatch",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1/text-to-3d",
		Input:      json.RawMessage(input),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := h.runUntilTerminal(t, accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task state = %s failure=%#v", task.State, task.Failure)
	}
	return task
}

// drivePostSuccess 推进成功后观察直至资源全部终态或轮数用尽。
func drivePostSuccess(
	t *testing.T,
	h *checkpointHarness,
	id generation.TaskID,
	rounds int,
) generation.Task {
	t.Helper()
	for range rounds {
		h.reconcile(t)
		h.clock.Advance(61 * time.Second)
		task, err := h.module.Get(context.Background(), "owner-dispatch", id)
		if err != nil {
			t.Fatal(err)
		}
		if !generation.ResourcesPending(task.Resources) &&
			task.NextActionAt.IsZero() {
			return task
		}
	}
	task, err := h.module.Get(context.Background(), "owner-dispatch", id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// ── 成功后不回退、不新增 Attempt/Start（§6.6 第 1 行） ──

func TestPostSuccessNeverRollsBackOrCreatesAttempts(t *testing.T) {
	adapter := newPostSuccessAdapter()
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-no-rollback.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF","FBX"],"authorize_extra_credits":true}`)
	aggregate, err := h.repository.Load(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminalAttempt := aggregate.CurrentAttempt.ID
	// 资源初始化断言：model ready、thumbnail ready（completed 已带）、
	// 每个选中格式一个独立 pending 槽位（P2 逐项身份）。
	if state := aggregate.Task.Resources["model||"]; state.State != generation.ResourceReady {
		t.Fatalf("model slot = %#v", aggregate.Task.Resources)
	}
	if state := aggregate.Task.Resources["thumbnail|rendered_image|"]; state.State != generation.ResourceReady {
		t.Fatalf("thumbnail slot = %#v", state)
	}
	for _, format := range []string{"GLTF", "FBX"} {
		if state := convertedSlot(t, aggregate.Task, format); state.State != generation.ResourcePending {
			t.Fatalf("converted.%s slot = %#v", lower(format), state)
		}
	}
	if aggregate.Task.NextActionAt.IsZero() {
		t.Fatal("存在 pending 资源时成功提交必须安排下一轮观察")
	}
	final := drivePostSuccess(t, h, task.ID, 12)
	if final.State != generation.TaskSucceeded {
		t.Fatalf("state rolled back: %s", final.State)
	}
	after, err := h.repository.Load(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CurrentAttempt.ID != terminalAttempt {
		t.Fatalf("attempt changed: %s → %s", terminalAttempt, after.CurrentAttempt.ID)
	}
	if after.CurrentAttempt.State != generation.AttemptUpstreamSucceeded {
		// 物化完成后的 Attempt 终态：成功路径中 materialize 结算后
		// attempt 处于 UPSTREAM_SUCCEEDED（不新建 attempt）。
		t.Fatalf("attempt state = %s", after.CurrentAttempt.State)
	}
	starts, _, _, _ := adapter.snapshot()
	if starts != 1 {
		t.Fatalf("port.Start called %d times; want 1", starts)
	}
}

// ── 多格式授权流：每个选中格式恰好一次 Checkpoint+发送，产物经标准
// 资源落地，槽位由 Provider 完成断言收尾（§6.6 + W5 移交） ──

func TestPostSuccessMultiFormatAuthorizedConversion(t *testing.T) {
	adapter := newPostSuccessAdapter()
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-multi.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF","FBX","OBJ"],"authorize_extra_credits":true}`)
	final := drivePostSuccess(t, h, task.ID, 16)
	_, checkpoints, sends, _ := adapter.snapshot()
	if checkpoints != 3 || sends != 3 {
		t.Fatalf("checkpoints = %d sends = %d; want 3/3（每个选中格式恰好一次）", checkpoints, sends)
	}
	var converted []generation.Asset
	for _, asset := range final.Assets {
		if asset.Role == "converted_model" {
			converted = append(converted, asset)
		}
	}
	if len(converted) != 3 {
		t.Fatalf("converted assets = %d; want 3: %#v", len(converted), final.Assets)
	}
	ordinals := map[string]int{}
	for _, asset := range converted {
		if asset.Ordinal <= 0 {
			t.Fatalf("converted ordinal invalid: %#v", asset)
		}
		ordinals[asset.Variant] = asset.Ordinal
	}
	if len(ordinals) != 3 {
		t.Fatalf("converted variants = %v", ordinals)
	}
	for _, format := range []string{"GLTF", "FBX", "OBJ"} {
		if state := convertedSlot(t, final, format); state.State != generation.ResourceReady {
			t.Fatalf("converted.%s slot = %#v（本地资产已落盘 → READY）", lower(format), state)
		}
	}
	if generation.ResourcesPending(final.Resources) || !final.NextActionAt.IsZero() {
		t.Fatalf("resources_pending 未收尾: %#v next=%v", final.Resources, final.NextActionAt)
	}
}

// ── 重复观察幂等合并：同 (role,variant) 去重、ordinal 稳定（§6.6 第 2 行） ──

func TestPostSuccessDuplicateObservationsMergeIdempotently(t *testing.T) {
	adapter := newPostSuccessAdapter()
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-idempotent.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF"],"authorize_extra_credits":true}`)
	// 完成后继续调度多轮：零新增资产、零新增发送。
	final := drivePostSuccess(t, h, task.ID, 10)
	_, checkpoints, sends, _ := adapter.snapshot()
	for range 4 {
		h.reconcile(t)
		h.clock.Advance(61 * time.Second)
	}
	after := drivePostSuccess(t, h, task.ID, 6)
	if len(after.Assets) != len(final.Assets) {
		t.Fatalf("assets grew: %d → %d", len(final.Assets), len(after.Assets))
	}
	_, checkpointsAfter, sendsAfter, _ := adapter.snapshot()
	if checkpointsAfter != checkpoints || sendsAfter != sends {
		t.Fatalf("duplicate rounds re-dispatched: checkpoints %d→%d sends %d→%d",
			checkpoints, checkpointsAfter, sends, sendsAfter)
	}
	if state := convertedSlot(t, after, "GLTF"); state.State != generation.ResourceReady {
		t.Fatalf("converted.gltf slot = %#v", state)
	}
}

// ── 可选失败不使任务失败：全部子作业失败 → 槽位 failed，任务保持
// SUCCEEDED（§6.6 第 3 行） ──

func TestPostSuccessOptionalFailuresNeverFailTask(t *testing.T) {
	adapter := newPostSuccessAdapter()
	adapter.childFails = true
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-optional-fail.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF"],"authorize_extra_credits":true}`)
	final := drivePostSuccess(t, h, task.ID, 10)
	if final.State != generation.TaskSucceeded || final.Failure != nil {
		t.Fatalf("optional failure broke task: %s %#v", final.State, final.Failure)
	}
	if state := convertedSlot(t, final, "GLTF"); state.State != generation.ResourceFailed {
		t.Fatalf("converted.gltf slot = %#v; want failed", state)
	}
	if generation.ResourcesPending(final.Resources) {
		t.Fatalf("slot should be terminal: %#v", final.Resources)
	}
}

// ── 私有子作业运行中：父已完成、资源待定（不回滚父进度） ──

func TestPostSuccessChildRunningKeepsParentCompleted(t *testing.T) {
	adapter := newPostSuccessAdapter()
	adapter.childRunningRounds = 2
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-child-running.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF"],"authorize_extra_credits":true}`)
	// 推进 1 轮：派发；再 2 轮：子作业 running → 父保持 SUCCEEDED、
	// 进度不回滚、converted pending。
	for range 3 {
		h.reconcile(t)
		h.clock.Advance(61 * time.Second)
		mid, err := h.module.Get(context.Background(), "owner-dispatch", task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if mid.State != generation.TaskSucceeded || mid.Progress != 100 {
			t.Fatalf("parent regressed: %s progress=%d", mid.State, mid.Progress)
		}
	}
	final := drivePostSuccess(t, h, task.ID, 8)
	if state := convertedSlot(t, final, "GLTF"); state.State != generation.ResourceReady {
		t.Fatalf("converted.gltf slot = %#v", state)
	}
}

// ── deadline：到期零 Observe、incomplete 只写一次；重启越过 deadline
// 由本地 finalizer 收尾（§6.6 第 4 行 + 恢复测试） ──

func TestPostSuccessDeadlineFinalizesOnceAndStopsObserving(t *testing.T) {
	adapter := newPostSuccessAdapter()
	adapter.childRunningRounds = 1 << 20 // 子作业永不完成 → 走 deadline
	dbPath := filepath.Join(t.TempDir(), "ps-deadline.db")
	h := newHistoricalPostSuccessHarness(t, adapter, dbPath)
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF"],"authorize_extra_credits":true}`)
	// 推进 2 轮（派发 + running），然后越过 deadline（86400s）。
	h.reconcile(t)
	h.clock.Advance(61 * time.Second)
	h.reconcile(t)
	_, _, _, postCalls := adapter.snapshot()
	h.clock.Advance(24 * time.Hour)
	// 真实重启：同一数据库上重建模块；调度器领取后本地收尾，零上游。
	restarted := h.restart(t, adapter)
	drivePostSuccess(t, restarted, task.ID, 4)
	finalTask, err := restarted.module.Get(context.Background(), "owner-dispatch", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalTask.State != generation.TaskSucceeded {
		t.Fatalf("state = %s", finalTask.State)
	}
	if state := convertedSlot(t, finalTask, "GLTF"); state.State != generation.ResourceIncomplete {
		t.Fatalf("converted.gltf slot = %#v; want incomplete", state)
	}
	if !finalTask.NextActionAt.IsZero() {
		t.Fatalf("next_action_at = %v; want cleared", finalTask.NextActionAt)
	}
	// incomplete 只写一次 + 到期后零新观察。
	events := loadPostSuccessEvents(t, restarted, task.ID)
	incompleteCount := 0
	for _, event := range events {
		if event.Type == "post_success.incomplete" {
			incompleteCount++
		}
	}
	if incompleteCount != 1 {
		t.Fatalf("incomplete events = %d; want 1", incompleteCount)
	}
	_, _, _, postCallsAfter := adapter.snapshot()
	if postCallsAfter != postCalls {
		// 重启后恰好一次领取即本地收尾：deadline 已过 → 零 port.Observe。
		t.Fatalf("post-success observes after deadline = %d → %d; want +0", postCalls, postCallsAfter)
	}
	// 继续调度多轮：无变化。
	for range 3 {
		restarted.reconcile(t)
		restarted.clock.Advance(time.Hour)
	}
	_, _, _, postCallsFinal := adapter.snapshot()
	if postCallsFinal != postCallsAfter {
		t.Fatalf("observes kept growing: %d → %d", postCallsAfter, postCallsFinal)
	}
}

func loadPostSuccessEvents(
	t *testing.T,
	h *checkpointHarness,
	id generation.TaskID,
) []generation.Event {
	t.Helper()
	events, err := h.module.Events(context.Background(), "owner-dispatch", id, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// ── 歧义发送后重启：该格式与剩余格式 abandoned，永不重发（零重复扣费） ──

func TestPostSuccessAmbiguousSendNeverReplays(t *testing.T) {
	adapter := newPostSuccessAdapter()
	adapter.crashAfterSend = true
	dbPath := filepath.Join(t.TempDir(), "ps-ambiguous.db")
	h := newHistoricalPostSuccessHarness(t, adapter, dbPath)
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF","FBX"],"authorize_extra_credits":true}`)
	h.clock.Advance(61 * time.Second)
	h.reconcile(t) // 派发 1 次 → 歧义崩溃（无回执）
	_, checkpoints, sends, _ := adapter.snapshot()
	if sends != 1 || checkpoints != 1 {
		t.Fatalf("sends = %d checkpoints = %d; want 1/1", sends, checkpoints)
	}
	h.clock.Advance(61 * time.Second)
	restarted := h.restart(t, adapter)
	final := drivePostSuccess(t, restarted, task.ID, 6)
	_, checkpointsAfter, sendsAfter, _ := adapter.snapshot()
	if sendsAfter != 1 || checkpointsAfter != 1 {
		t.Fatalf("ambiguous replayed: sends %d checkpoints %d; want 1/1", sendsAfter, checkpointsAfter)
	}
	// 歧义格式与未发送格式都诚实 abandoned（pending 栅栏阻止重发）。
	for _, format := range []string{"GLTF", "FBX"} {
		if state := convertedSlot(t, final, format); state.State != generation.ResourceAbandoned {
			t.Fatalf("converted.%s slot = %#v; want abandoned", lower(format), state)
		}
	}
	if final.State != generation.TaskSucceeded {
		t.Fatalf("state = %s", final.State)
	}
}

// ── 拒绝成本形态：超预算/缺同意在提交期拒绝（0 次发送） ──

func TestPostSuccessOverBudgetRejectedAtSubmit(t *testing.T) {
	adapter := newPostSuccessAdapter()
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-over-budget.db"))
	// 缺同意。
	_, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-dispatch",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1/text-to-3d",
		Input:      json.RawMessage(`{"prompt":"x","output_formats":["GLTF"]}`),
	})
	if err == nil {
		t.Fatal("缺同意必须提交期拒绝")
	}
	// 超上限：7 个格式不可能（max_items 6）；6 个 = 30 ≤ 30 合法，
	// 用重复格式测 options 校验（重复在数组内被逐项校验通过，但
	// Normalize 后仍计 7 项？——max_items=6 由校验拒绝）。
	_, err = h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-dispatch",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1/text-to-3d",
		Input:      json.RawMessage(`{"prompt":"x","output_formats":["GLTF","GLTF","FBX","OBJ","STL","USDZ","3MF"],"authorize_extra_credits":true}`),
	})
	if err == nil {
		t.Fatal("超出 max_items 必须提交期拒绝")
	}
	_, _, sends, _ := adapter.snapshot()
	if sends != 0 {
		t.Fatalf("sends = %d; want 0", sends)
	}
}

// ── 未选格式的 on-demand 槽位不进入观察（零成功后活动） ──

func TestPostSuccessUnselectedOnDemandNeverObserves(t *testing.T) {
	adapter := newPostSuccessAdapter()
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-unselected.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern"}`)
	for key := range task.Resources {
		if strings.HasPrefix(key, "converted_model|") {
			t.Fatalf("unselected on-demand slot initialized: %#v", task.Resources)
		}
	}
	if generation.ResourcesPending(task.Resources) {
		t.Fatalf("unselected task pending: %#v", task.Resources)
	}
	if !task.NextActionAt.IsZero() {
		t.Fatal("无 pending 不应安排成功后观察")
	}
	for range 3 {
		h.reconcile(t)
		h.clock.Advance(61 * time.Second)
	}
	_, checkpoints, sends, postCalls := adapter.snapshot()
	if checkpoints != 0 || sends != 0 || postCalls != 0 {
		t.Fatalf("unselected conversions ran: checkpoints=%d sends=%d post=%d",
			checkpoints, sends, postCalls)
	}
}

// ── Webhook 护栏（§6.2 触点 5）：SUCCEEDED+observe 放行进受约束合并；
// 删除后迟到 webhook 不可复活（tombstone 排除）。 ──

func TestPostSuccessWebhookMergeAndDeleteGuard(t *testing.T) {
	adapter := newPostSuccessAdapter()
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-webhook.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF"],"authorize_extra_credits":true}`)
	aggregate, err := h.repository.Load(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	remoteID := aggregate.CurrentAttempt.UpstreamJob.RemoteID
	// 放行：COMPLETED + 新可选产物 → 合并 converted.webhook 资产。
	result, err := h.module.IngestProviderWebhook(context.Background(), generation.ProviderWebhookCommand{
		ProviderID: "tripo",
		RemoteID:   remoteID,
		EventType:  "task.completed",
		Payload:    []byte(`{"status":"success"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Fatalf("webhook not applied: %#v", result)
	}
	after, err := h.module.Get(context.Background(), "owner-dispatch", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, asset := range after.Assets {
		if asset.Variant == "converted.webhook" {
			found = true
		}
	}
	if !found {
		t.Fatalf("webhook artifact not merged: %#v", after.Assets)
	}
	if after.State != generation.TaskSucceeded {
		t.Fatalf("webhook regressed task: %s", after.State)
	}
	// 删除（tombstone）后：同 remote 的迟到 webhook 不可复活。
	if err := h.module.Delete(context.Background(), generation.DeleteCommand{
		Scope:              generation.OwnTasks("owner-dispatch"),
		ActorID:            "owner-dispatch",
		TaskID:             task.ID,
		ExpectedCapability: generation.TextTo3D,
		Policy: generation.TaskDeletionPolicy{
			LocalRecordPolicy:     "tombstone",
			UpstreamPolicy:        "when-supported",
			UpstreamFailurePolicy: "abort",
			GeneratedAssetPolicy:  "retain",
			AuditEventType:        "task.deleted",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.module.IngestProviderWebhook(context.Background(), generation.ProviderWebhookCommand{
		ProviderID: "tripo",
		RemoteID:   remoteID,
		EventType:  "task.completed",
		Payload:    []byte(`{"status":"success"}`),
	}); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("deleted task webhook err = %v; want not_found", err)
	}
	if _, err := h.module.Get(context.Background(), "owner-dispatch", task.ID); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("deleted task resurrected: %v", err)
	}
}
