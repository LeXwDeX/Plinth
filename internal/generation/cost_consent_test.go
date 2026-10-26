package generation_test

// cost_consent_test.go 落实 §5.5 的内核侧用例：历史任务永不追溯转换
//（终态任务 next_action_at 恒零、无新 Attempt/Start）、转换任务恢复期
// 歧义不重放（pending 栅栏 0 次额外付费发送）、伪造回执零生命周期突变。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// conversionFlowAdapter 双行为替身：t2d 走"接受→完成"常规流；转换能力
// 在 Start 内 Checkpoint+发送（计数），可配置为发送后崩溃（返回歧义
// 失败、无回执）。
type conversionFlowAdapter struct {
	mu             sync.Mutex
	starts         int
	convertSends   int
	checkpoints    int
	crashAfterSend bool
	lastAttempt    generation.AttemptID
}

func (a *conversionFlowAdapter) Start(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	a.starts++
	a.lastAttempt = request.AttemptID
	crash := a.crashAfterSend
	a.mu.Unlock()
	if request.Offering.Capability != generation.FormatConversion {
		return generation.Observation{
			Phase:     generation.UpstreamAccepted,
			Job:       &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "gen-1"},
			PollAfter: time.Millisecond,
			DedupeKey: "gen-1:accepted",
		}, nil
	}
	receipt, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{
		Bytes: json.RawMessage(`{"stage":"models/convert"}`),
	})
	a.mu.Lock()
	a.checkpoints++
	a.mu.Unlock()
	if err != nil {
		return generation.Observation{}, err
	}
	a.mu.Lock()
	a.convertSends++
	a.mu.Unlock()
	if crash {
		// 模拟"已发送但未取得可回执结果"的歧义崩溃：非幂等提交后进程
		// 丢失响应。返回 SubmissionUnknown 且不携带回执。
		return generation.Observation{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureSubmissionUnknown,
				Ambiguous:  true,
				SafeDetail: "转换提交后丢失上游响应（fixture 歧义崩溃）",
			},
		}
	}
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "convert-1"},
		PollAfter: time.Millisecond,
		DedupeKey: "convert-1:accepted",
		Dispatch:  receipt,
	}, nil
}

func (a *conversionFlowAdapter) Observe(
	_ context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if request.Offering.Capability != generation.FormatConversion {
		// W6 起 tripo 生成 Offering 声明 thumbnail background 槽位：
		// fixture 与线上行为对齐（completed 响应携带 rendered_image 缩略图），
		// 否则未选格式的任务也会因缩略图 pending 进入成功后观察。
		return generation.Observation{
			Phase:     generation.UpstreamCompleted,
			Job:       &request.Job,
			Progress:  100,
			DedupeKey: "gen-1:completed",
			Artifacts: []generation.ArtifactClaim{{
				ID:        "gen-1:model",
				Role:      "model",
				Inline:    validGLB("cost source fixture"),
				MediaType: "model/gltf-binary",
			}, {
				ID:        "gen-1:thumbnail",
				Role:      "thumbnail",
				Variant:   "rendered_image",
				Inline:    []byte("png thumbnail fixture"),
				MediaType: "image/png",
			}},
		}, nil
	}
	return generation.Observation{
		Phase:     generation.UpstreamCompleted,
		Job:       &request.Job,
		Progress:  100,
		DedupeKey: "convert-1:completed",
		Artifacts: []generation.ArtifactClaim{{
			ID:        "convert-1:model",
			Role:      "model",
			Variant:   "converted",
			Inline:    validGLB("cost converted fixture"),
			MediaType: "model/gltf-binary",
		}},
	}, nil
}

func (a *conversionFlowAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

func (a *conversionFlowAdapter) snapshot() (starts, sends, checkpoints int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.starts, a.convertSends, a.checkpoints
}

// ── 历史任务永不追溯转换 ──

func TestHistoricalTasksNeverConvertRetroactively(t *testing.T) {
	adapter := &conversionFlowAdapter{}
	h := newConversionCheckpointHarness(
		t,
		adapter,
		filepath.Join(t.TempDir(), "cost-history.db"),
	)
	sourceID := h.submit(t)
	task := h.runUntilTerminal(t, sourceID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("source task = %s", task.State)
	}
	// 终态后继续调度多轮：无新 Attempt、无 Start、next_action_at 恒零
	//（不存在任何自动/追溯转换路径；转换只能来自用户新发起的公开任务）。
	aggregate, err := h.repository.Load(context.Background(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	terminalAttempt := aggregate.CurrentAttempt.ID
	terminalVersion := aggregate.Task.Version
	for range 6 {
		h.reconcile(t)
		h.clock.Advance(5 * time.Second)
	}
	starts, sends, _ := adapter.snapshot()
	if starts != 1 || sends != 0 {
		t.Fatalf("starts = %d, convert sends = %d; want 1/0（零追溯转换）", starts, sends)
	}
	after, err := h.repository.Load(context.Background(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Task.State != generation.TaskSucceeded ||
		after.Task.Version != terminalVersion ||
		after.CurrentAttempt.ID != terminalAttempt ||
		!after.Task.NextActionAt.IsZero() {
		t.Fatalf(
			"历史任务被扰动：state=%s version=%d attempt=%s next=%v",
			after.Task.State,
			after.Task.Version,
			after.CurrentAttempt.ID,
			after.Task.NextActionAt,
		)
	}
}

// ── 转换任务：授权提交 → 恰好一次发送并成功落地 converted 资产 ──

func TestConversionTaskAuthorizedFlow(t *testing.T) {
	adapter := &conversionFlowAdapter{}
	h := newConversionCheckpointHarness(
		t,
		adapter,
		filepath.Join(t.TempDir(), "cost-authorized.db"),
	)
	sourceID := h.submit(t)
	if task := h.runUntilTerminal(t, sourceID); task.State != generation.TaskSucceeded {
		t.Fatalf("source task = %s", task.State)
	}
	input := `{"source_task":"` + string(sourceID) + `","format":"FBX","quad":true,"authorize_extra_credits":true}`
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-dispatch",
		Capability: generation.FormatConversion,
		OfferingID: "tripo/tripo-v3.1/format-conversion",
		Input:      json.RawMessage(input),
	})
	if err != nil {
		t.Fatal(err)
	}
	conversion := h.runUntilTerminal(t, accepted.TaskID)
	if conversion.State != generation.TaskSucceeded || conversion.Failure != nil {
		t.Fatalf("conversion = %s failure=%#v", conversion.State, conversion.Failure)
	}
	_, sends, checkpoints := adapter.snapshot()
	if sends != 1 || checkpoints != 1 {
		t.Fatalf("convert sends = %d checkpoints = %d; want 1/1", sends, checkpoints)
	}
	if len(conversion.Assets) != 1 ||
		conversion.Assets[0].Variant != "converted" {
		t.Fatalf("assets = %#v（须落 converted 契约）", conversion.Assets)
	}
}

// ── 提交期同意裁决作用于模块入口 ──

func TestConversionSubmitEnforcesConsent(t *testing.T) {
	adapter := &conversionFlowAdapter{}
	h := newConversionCheckpointHarness(
		t,
		adapter,
		filepath.Join(t.TempDir(), "cost-consent.db"),
	)
	_, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-dispatch",
		Capability: generation.FormatConversion,
		OfferingID: "tripo/tripo-v3.1/format-conversion",
		Input:      json.RawMessage(`{"source_task":"missing-source","format":"FBX","quad":true}`),
	})
	if err == nil {
		t.Fatal("缺同意必须在提交期被拒绝")
	}
	var domainErr *generation.Error
	if ok := errorsAs(err, &domainErr); !ok || domainErr.Code != generation.ErrInvalidInput {
		t.Fatalf("err = %v; want invalid_input（缺同意在提交期拒绝）", err)
	}
	if _, sends, _ := adapter.snapshot(); sends != 0 {
		t.Fatalf("convert sends = %d; want 0", sends)
	}
}

// ── 恢复/歧义：发送后崩溃 → 歧义终态；重启后 pending 栅栏 0 次重放 ──

func TestConversionAmbiguousCrashNeverReplays(t *testing.T) {
	adapter := &conversionFlowAdapter{crashAfterSend: true}
	dbPath := filepath.Join(t.TempDir(), "cost-ambiguous.db")
	h := newConversionCheckpointHarness(t, adapter, dbPath)
	sourceID := h.submit(t)
	if task := h.runUntilTerminal(t, sourceID); task.State != generation.TaskSucceeded {
		t.Fatalf("source task = %s", task.State)
	}
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-dispatch",
		Capability: generation.FormatConversion,
		OfferingID: "tripo/tripo-v3.1/format-conversion",
		Input: json.RawMessage(
			`{"source_task":"` + string(sourceID) + `","format":"GLTF"}`,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 第一轮：Checkpoint → 发送 1 次 → 歧义失败（无回执）→ 任务终态。
	crashed := h.runUntilTerminal(t, accepted.TaskID)
	if crashed.State != generation.TaskFailed {
		t.Fatalf("crash task = %s; want FAILED", crashed.State)
	}
	if crashed.Failure == nil || !crashed.Failure.Ambiguous {
		t.Fatalf("failure = %#v; want ambiguous", crashed.Failure)
	}
	if _, sends, checkpoints := adapter.snapshot(); sends != 1 || checkpoints != 1 {
		t.Fatalf("sends = %d checkpoints = %d; want 1/1", sends, checkpoints)
	}
	// 真实重启同一数据库：检查点处于 dispatching 无回执 → 任何新的
	// Checkpoint 都拿不到许可（ErrDispatchPending），零额外付费发送。
	restarted := h.restart(t, adapter)
	for range 6 {
		restarted.reconcile(t)
		restarted.clock.Advance(5 * time.Second)
	}
	_, sends, checkpoints := adapter.snapshot()
	if sends != 1 || checkpoints < 1 {
		t.Fatalf(
			"restart sends = %d checkpoints = %d; want 保持 1 次发送（pending 栅栏禁止重放）",
			sends,
			checkpoints,
		)
	}
	// 检查点列停留在 dispatching（回执丢失，永不自动清除）。
	state := readCheckpointColumn(t, restarted.dbPath, string(adapter.lastAttempt))
	if !containsState(state, "dispatching") {
		t.Fatalf("checkpoint state = %q; want dispatching（保守歧义）", state)
	}
}

func errorsAs(err error, target *(*generation.Error)) bool {
	for err != nil {
		if candidate, ok := err.(*generation.Error); ok {
			*target = candidate
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

func containsState(stateJSON, want string) bool {
	var state struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return false
	}
	return state.State == want
}
