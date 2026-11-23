package tripo_test

// post_success_recovery_test.go 锁定 #478 P0-1：post-success 私有状态被私有派发
// 意图覆盖后必须能恢复。崩溃前检查点把 {"stage":"models/convert",...,
// "details":{"conversions":{...}}} 原样写进 opaque，顶层 conversions 为空；
// 恢复失败会把已派发格式当成未开始、再次尝试付费派发，最后撞上未回执检查点
// 陷入 ErrDispatchPending 死循环（零上游调用、状态不再推进）。全部本地
// httptest fixture、零付费调用。

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// TestPostSuccessDispatchIntentRecoversInsteadOfRepaying：读到 dispatch intent
// （GLTF 仍是 dispatching）必须恢复成顶层 conversions，让 hasDispatchingRemnant
// 保守收尾（abandoned），而不是再次派发 GLTF。
func TestPostSuccessDispatchIntentRecoversInsteadOfRepaying(t *testing.T) {
	upstream := newSelectedSetUpstream(t)
	offering := selectedSetOffering(t)
	adapter := newAdapterWithImage(t, upstream.server, nil, offering)
	// 模拟「存在未回执的私有派发检查点」：修复前若再次派发 GLTF，这里会返回
	// ErrDispatchPending；修复后应根本不派发。
	checkpointer := &recordingCheckpointer{pended: true}
	ctx := generation.WithDispatchCheckpointer(context.Background(), checkpointer)
	obs, err := adapter.Observe(ctx, generation.ObserveRequest{
		TaskID:      "retrospective-parent",
		AttemptID:   "retrospective-attempt",
		Offering:    offering,
		PostSuccess: true,
		Job:         generation.UpstreamJob{ProviderID: "tripo", RemoteID: "parent"},
		Input:       json.RawMessage(`{"output_formats":["GLTF","FBX"],"authorize_extra_credits":true}`),
		// 崩溃后检查点写进 opaque 的派发意图：GLTF 本轮派发但回执丢失。
		Opaque: json.RawMessage(`{"stage":"models/convert","details":{"conversions":{"GLTF":{"status":"dispatching"}}}}`),
	})
	if err != nil {
		if errors.Is(err, generation.ErrDispatchPending) {
			t.Fatalf("派发意图未被恢复，仍尝试重派 GLTF（应保守收尾）: %v", err)
		}
		t.Fatal(err)
	}
	if hits, _ := upstream.converts(); hits != 0 {
		t.Fatalf("convert 调用 = %d；want 0（不得再次付费派发）", hits)
	}
	// GLTF（dispatching 残留）与未开始的 FBX 都应保守收尾为 abandoned。
	var state struct {
		Conversions map[string]struct {
			Status string `json:"status"`
		} `json:"conversions"`
	}
	if err := json.Unmarshal(obs.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	if got := state.Conversions["GLTF"].Status; got != "abandoned" {
		t.Fatalf("GLTF = %q；want abandoned（dispatching 残留保守收尾）", got)
	}
	if got := state.Conversions["FBX"].Status; got != "abandoned" {
		t.Fatalf("FBX = %q；want abandoned（未开始格式随遗留一并收尾，不再付费）", got)
	}
}