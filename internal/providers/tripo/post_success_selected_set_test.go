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
				writeJSON(w, map[string]any{
					"code": 0,
					"data": map[string]any{
						"task_id": "child-gltf", "status": "failed", "progress": 100,
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
// 作业失败时 FBX 仍未开始——实际 Tripo 适配器不得终结整个 converted 槽位
// （旧行为：completionUpdate 只看状态表已有条目，anyFailed → 整槽 failed，
// 静默取消 FBX 这一独立已授权格式）。允许且只允许暴露 GLTF 自身的失败。
func TestRetrospectiveFirstFailedFormatMustNotFinishUnstartedFormats(t *testing.T) {
	upstream := newSelectedSetUpstream(t)
	adapter := newAdapterWithImage(t, upstream.server, nil, selectedSetOffering(t))
	obs := observeSelectedSet(
		t,
		adapter,
		`{"conversions":{"GLTF":{"status":"submitted","remote_id":"child-gltf"}}}`,
	)
	for _, update := range obs.ResourceUpdates {
		if update.VariantPrefix != "" {
			t.Fatalf(
				"FBX never started, but actual Tripo adapter terminated whole slot: state=%s opaque=%s",
				update.State,
				obs.Opaque,
			)
		}
		if update.Variant == "converted.fbx" {
			t.Fatalf("unstarted FBX was terminated: %#v", update)
		}
		if update.Variant != "converted.gltf" || update.State != generation.ResourceFailed {
			t.Fatalf("unexpected assertion %#v（只允许暴露失败项自身）", update)
		}
	}
	if len(obs.ResourceUpdates) != 1 {
		t.Fatalf("updates = %#v; want exactly the failed GLTF item", obs.ResourceUpdates)
	}
	if obs.Phase != generation.UpstreamCompleted {
		t.Fatalf("phase = %s; 父任务终态语义不得回退", obs.Phase)
	}
	var state struct {
		Conversions map[string]struct {
			Status string `json:"status"`
		} `json:"conversions"`
	}
	if err := json.Unmarshal(obs.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	if _, started := state.Conversions["FBX"]; started {
		t.Fatalf("FBX 在本轮之前不得被派发（每轮至多一次动作）：%s", obs.Opaque)
	}
}

// TestRetrospectiveRemainingAuthorizedFormatContinuesAfterFailure：GLTF 失败
// 后的下一轮必须继续派发仍被授权的 FBX（独立格式互不取消），FBX 完成时
// 只交付自身产物，且不断言任何整槽终态。
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
	hits, bodies := upstream.converts()
	if hits != 1 {
		t.Fatalf("convert calls = %d; want 1（剩余已授权格式必须继续）：%v", hits, bodies)
	}
	if format, _ := bodies[0]["format"].(string); format != "FBX" {
		t.Fatalf("convert format = %v; want FBX", bodies[0]["format"])
	}
	if second.Phase != generation.UpstreamRunning {
		t.Fatalf("派发轮次必须保持父任务终态语义（RUNNING，不回退）：%s", second.Phase)
	}
	if !strings.Contains(string(second.Opaque), `"FBX":{"status":"submitted"`) {
		t.Fatalf("FBX 派发事实未写入私有状态：%s", second.Opaque)
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
