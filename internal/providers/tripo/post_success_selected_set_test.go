package tripo_test

// post_success_selected_set_test.go 锁定回顾性整改 P0 的 Tripo 私有状态机
// 契约：完整冻结选中集治理完成——首个/中间/末位格式失败都不得静默取消
// 其余已授权的独立格式；未开始的格式存在时不得终结整个 converted 槽位；
// 失败项只暴露自身结果（混合结果公开）。全部本地 httptest fixture、零付费
// 调用。
//
// 说明：父任务回顾性探针（临时文件 retrospective_tripo_test.go）按旧的
// "单一聚合槽位"形状断言"零资源更新"。新批准行为要求**逐项**暴露失败
// （P0/P2：每个被请求项都有独立身份与结果），故本永久测试保留探针意图
// （未开始的 FBX 与聚合槽位不得被终结）并收紧为逐项断言。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// selectedSetUpstream 是本地假上游：GLTF 子作业失败，FBX 转换可发送并完成。
type selectedSetUpstream struct {
	server        *httptest.Server
	mu            sync.Mutex
	convertHits   int
	convertBodies []map[string]any
}

func newSelectedSetUpstream(t *testing.T) *selectedSetUpstream {
	t.Helper()
	upstream := &selectedSetUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/models/convert"):
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				upstream.mu.Lock()
				upstream.convertHits++
				upstream.convertBodies = append(upstream.convertBodies, body)
				upstream.mu.Unlock()
				format, _ := body["format"].(string)
				writeJSON(w, map[string]any{
					"code":    0,
					"message": "success",
					"data":    map[string]string{"task_id": "child-" + strings.ToLower(format)},
				})
			case strings.HasSuffix(r.URL.Path, "/tasks/child-gltf"):
				// The GLTF child fails on every attempt (#460 A1: a failed task is
				// not charged, so the same format is resent with backoff).
				writeJSON(w, map[string]any{
					"code": 0,
					"data": map[string]any{
						"task_id": "child-gltf", "status": "failed", "progress": 100,
						"credits_consumed": 0, "error_code": 1001,
					},
				})
			case strings.HasSuffix(r.URL.Path, "/tasks/child-fbx"):
				writeJSON(w, map[string]any{
					"code": 0,
					"data": map[string]any{
						"task_id":          "child-fbx",
						"status":           "success",
						"progress":         100,
						"credits_consumed": json.RawMessage(`5`),
						"output": map[string]any{
							"model_url": "https://cdn.tripo3d.ai/converted/child-fbx.fbx",
						},
					},
				})
			default:
				http.NotFound(w, r)
			}
		},
	))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *selectedSetUpstream) converts() (int, []map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.convertHits, append([]map[string]any(nil), u.convertBodies...)
}

func selectedSetOffering(t *testing.T) generation.Offering {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := taskCatalog.Get("tripo/tripo-v3.1/text-to-3d")
	if !ok {
		t.Fatal("默认目录缺少 tripo 文生 3D offering")
	}
	// This retrospective fixture represents an already frozen pre-#218 Task.
	// New catalog pricing must not reinterpret its historical conversion Grant.
	offering.Revision = "2026-10-16.213.1"
	offering.NativePricing = nil
	offering.EstimatedCredits = 20
	offering.Cost = &generation.CostDeclaration{ExtraCredits: 5, SelectionParameter: "output_formats", ConsentParameter: "authorize_extra_credits", ExtraLabel: "附加导出格式"}
	offering.CostPolicy = &generation.CostPolicy{MaxAuthorizedExtraCredits: 30}
	return offering
}

func observeSelectedSet(
	t *testing.T,
	adapter interface {
		Observe(context.Context, generation.ObserveRequest) (generation.Observation, error)
	},
	opaque string,
) generation.Observation {
	t.Helper()
	obs, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID:      "retrospective-parent",
		AttemptID:   "retrospective-attempt",
		Offering:    selectedSetOffering(t),
		PostSuccess: true,
		Job:         generation.UpstreamJob{ProviderID: "tripo", RemoteID: "parent"},
		Input: json.RawMessage(
			`{"output_formats":["GLTF","FBX"],"authorize_extra_credits":true}`,
		),
		Opaque: json.RawMessage(opaque),
	})
	if err != nil {
		t.Fatal(err)
	}
	return obs
}

// TestRetrospectiveFirstFailedFormatMustNotFinishUnstartedFormats：GLTF 子
// 作业失败时 FBX 仍未开始——实际 Tripo 适配器不得终结整个 converted 槽位，
// 也不得把未开始的 FBX 记为失败（#460 A1 起该格式先进入退避重提）。
func TestRetrospectiveFirstFailedFormatMustNotFinishUnstartedFormats(t *testing.T) {
	upstream := newSelectedSetUpstream(t)
	adapter := newAdapterWithImage(t, upstream.server, nil, selectedSetOffering(t))
	obs := observeSelectedSet(
		t,
		adapter,
		`{"conversions":{"GLTF":{"status":"submitted","remote_id":"child-gltf"}}}`,
	)
	if len(obs.ResourceUpdates) != 0 {
		t.Fatalf("updates = %#v; want none while GLTF may still be resent", obs.ResourceUpdates)
	}
	if obs.Phase != generation.UpstreamRunning {
		t.Fatalf("phase = %s; a pending retry keeps the post-success run going", obs.Phase)
	}
	var state struct {
		Conversions map[string]struct {
			Status string `json:"status"`
		} `json:"conversions"`
	}
	if err := json.Unmarshal(obs.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	if got := state.Conversions["GLTF"].Status; got != "retry_pending" {
		t.Fatalf("GLTF = %q, want retry_pending（失败子作业按官方不计费、按退避重提）", got)
	}
	if _, started := state.Conversions["FBX"]; started {
		t.Fatalf("FBX 在本轮之前不得被派发（每轮至多一次动作）：%s", obs.Opaque)
	}
	// 重提预算用尽后（每次子作业都失败）才断言 GLTF 自身失败；FBX 由后续
	// 轮次派发，不由本轮的失败连带取消。
	for round := 0; round < 20 && len(obs.ResourceUpdates) == 0; round++ {
		next, err := adapter.Observe(context.Background(), generation.ObserveRequest{
			TaskID: "retrospective-parent", AttemptID: "retrospective-attempt",
			Offering: selectedSetOffering(t), PostSuccess: true,
			Job:    generation.UpstreamJob{ProviderID: "tripo", RemoteID: "parent"},
			Input:  json.RawMessage(`{"output_formats":["GLTF","FBX"],"authorize_extra_credits":true}`),
			Opaque: obs.Opaque,
		})
		if err != nil {
			t.Fatal(err)
		}
		obs = next
	}
	if len(obs.ResourceUpdates) != 1 || obs.ResourceUpdates[0].Variant != "converted.gltf" ||
		obs.ResourceUpdates[0].State != generation.ResourceFailed {
		t.Fatalf("updates = %#v; want exactly the exhausted GLTF item", obs.ResourceUpdates)
	}
	if err := json.Unmarshal(obs.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Conversions["GLTF"].Status) == 0 {
		t.Fatalf("GLTF state missing: %s", obs.Opaque)
	}
	// 六个 GLTF 尝试全部来自同一个格式，别的格式没有被连带重提。
	if _, bodies := upstream.converts(); len(bodies) != 6 {
		t.Fatalf("convert calls = %d, want six GLTF attempts: %v", len(bodies), bodies)
	}
}

// TestRetrospectiveRemainingAuthorizedFormatContinuesAfterFailure：GLTF 失败
// 后的下一轮先按官方重提 GLTF（失败不计费）；预算用尽后才派发仍被授权的
// FBX（独立格式互不取消），FBX 完成时只交付自身产物，不断言任何整槽终态。
func TestRetrospectiveRemainingAuthorizedFormatContinuesAfterFailure(t *testing.T) {
	upstream := newSelectedSetUpstream(t)
	offering := selectedSetOffering(t)
	adapter := newAdapterWithImage(t, upstream.server, nil, offering)
	first := observeSelectedSet(
		t,
		adapter,
		`{"conversions":{"GLTF":{"status":"submitted","remote_id":"child-gltf"}}}`,
	)
	second, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID:      "retrospective-parent",
		AttemptID:   "retrospective-attempt",
		Offering:    offering,
		PostSuccess: true,
		Job:         generation.UpstreamJob{ProviderID: "tripo", RemoteID: "parent"},
		Input: json.RawMessage(
			`{"output_formats":["GLTF","FBX"],"authorize_extra_credits":true}`,
		),
		Opaque:        first.Opaque,
		ResourceSlots: []generation.ResourceSlotState{{Role: "converted_model", Variant: "converted.gltf", State: generation.ResourceFailed}, {Role: "converted_model", Variant: "converted.fbx", State: generation.ResourcePending}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 先重提 GLTF（失败不计费），预算用尽后才派发 FBX。
	current := second
	for round := 0; round < 20; round++ {
		next, err := adapter.Observe(context.Background(), generation.ObserveRequest{
			TaskID: "retrospective-parent", AttemptID: "retrospective-attempt",
			Offering: offering, PostSuccess: true,
			Job:    generation.UpstreamJob{ProviderID: "tripo", RemoteID: "parent"},
			Input:  json.RawMessage(`{"output_formats":["GLTF","FBX"],"authorize_extra_credits":true}`),
			Opaque: current.Opaque,
		})
		if err != nil {
			t.Fatal(err)
		}
		current = next
		if _, bodies := upstream.converts(); len(bodies) >= 6 {
			break
		}
	}
	// 原始提交在冻结状态里，不重新计数：五 次重提（预算）之后才是 FBX。
	hits, bodies := upstream.converts()
	if hits != 6 {
		t.Fatalf("convert calls = %d; want 6（五次 GLTF 重提加一次 FBX）：%v", hits, bodies)
	}
	for _, body := range bodies[:5] {
		if format, _ := body["format"].(string); format != "GLTF" {
			t.Fatalf("first five convert formats = %v; want the GLTF resubmissions", bodies)
		}
	}
	if format, _ := bodies[5]["format"].(string); format != "FBX" {
		t.Fatalf("last convert format = %v; want FBX", bodies[5]["format"])
	}
	if second.Phase != generation.UpstreamRunning {
		t.Fatalf("派发轮次必须保持父任务终态语义（RUNNING，不回退）：%s", second.Phase)
	}
	if !strings.Contains(string(second.Opaque), `"GLTF":{"status":"submitted"`) {
		t.Fatalf("GLTF 重提事实未写入私有状态：%s", second.Opaque)
	}
	for _, update := range second.ResourceUpdates {
		t.Fatalf("派发轮次不得断言资源终态：%#v", update)
	}
}

// TestRetrospectiveSelectedThreeProcessedOneCannotFinish：三个选中格式只
// 处理了一个时，适配器不得断言任何终态（完整冻结选中集治理完成）。
func TestRetrospectiveSelectedThreeProcessedOneCannotFinish(t *testing.T) {
	upstream := newSelectedSetUpstream(t)
	adapter := newAdapterWithImage(t, upstream.server, nil, selectedSetOffering(t))
	obs, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID:      "retrospective-parent",
		AttemptID:   "retrospective-attempt",
		Offering:    selectedSetOffering(t),
		PostSuccess: true,
		Job:         generation.UpstreamJob{ProviderID: "tripo", RemoteID: "parent"},
		Input: json.RawMessage(
			`{"output_formats":["GLTF","FBX","OBJ"],"authorize_extra_credits":true}`,
		),
		Opaque: json.RawMessage(
			`{"conversions":{"GLTF":{"status":"failed","remote_id":"child-gltf"}}}`,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range obs.ResourceUpdates {
		if update.Variant == "" || update.Variant == "converted.gltf" {
			continue
		}
		t.Fatalf("未处理的选中格式被断言终态：%#v", update)
	}
	hits, _ := upstream.converts()
	if hits != 1 {
		t.Fatalf("convert calls = %d; want 1（本轮派发下一个未开始格式）", hits)
	}
}
