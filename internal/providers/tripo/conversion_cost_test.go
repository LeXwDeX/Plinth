package tripo_test

// conversion_cost_test.go 落实 §5.5 Tripo 转换消费者用例（全部本地
// httptest fixture、零付费调用）：授权内恰好 1 次 Checkpoint + 1 次
// convert；缺同意/超上限/未知成本/未知格式 → 0 次 Checkpoint、0 次
// convert 调用；consumed_credit（v3 实测字段 credits_consumed）证据
// 落 Provider 私有 Opaque；与提交期 Normalize 的行为 parity。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// conversionUpstream 是本地假上游：计数 convert 端点命中并记录请求体。
type conversionUpstream struct {
	server *httptest.Server
	hits   atomic.Int32
	mu     sync.Mutex
	bodies []map[string]any
}

func newConversionUpstream(t *testing.T) *conversionUpstream {
	t.Helper()
	upstream := &conversionUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/models/convert":
				payload, _ := io.ReadAll(r.Body)
				var body map[string]any
				_ = json.Unmarshal(payload, &body)
				upstream.mu.Lock()
				upstream.bodies = append(upstream.bodies, body)
				upstream.mu.Unlock()
				upstream.hits.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":    0,
					"message": "success",
					"data":    map[string]string{"task_id": "convert-task-1"},
				})
			case "/tasks/convert-task-1":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 0,
					"data": map[string]any{
						"task_id":          "convert-task-1",
						"status":           "success",
						"progress":         100,
						"credits_consumed": json.RawMessage(`10`),
						"output": map[string]any{
							"model_url": "https://cdn.tripo3d.ai/converted/convert-task-1.gltf",
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

func (u *conversionUpstream) snapshot() (int, []map[string]any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return int(u.hits.Load()), u.bodies
}

// conversionOffering 取默认目录的冻结转换 offering（真源声明）。
func conversionOffering(t *testing.T) generation.Offering {
	t.Helper()
	taskCatalog := catalogtest.LoadDefaultWithTripoConversion(t)
	offering, exists := taskCatalog.Get("tripo/tripo-v3.1/format-conversion")
	if !exists {
		t.Fatal("默认目录缺少格式转换 offering")
	}
	return offering
}

func conversionStartRequest(
	offering generation.Offering,
	input string,
) generation.StartRequest {
	return generation.StartRequest{
		TaskID:        "task-conv-1",
		AttemptID:     "att-conv-1",
		SubmissionKey: "task-conv-1:1:att-conv-1",
		Offering:      offering,
		Input:         json.RawMessage(input),
		TaskDependencies: map[string]generation.TaskDependency{
			"source_task": {
				TaskID:      "task-source-1",
				UpstreamJob: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "source-remote-9"},
			},
		},
	}
}

// ── 授权内：恰好 1 次 Checkpoint + 1 次 convert ──

func TestConversionAuthorizedSendsExactlyOnce(t *testing.T) {
	checkpointer := &recordingCheckpointer{}
	upstream := newConversionUpstream(t)
	adapter := newAdapter(t, upstream.server)
	offering := conversionOffering(t)

	observation, err := adapter.Start(
		generation.WithDispatchCheckpointer(context.Background(), checkpointer),
		conversionStartRequest(
			offering,
			`{"source_task":"task-source-1","format":"FBX","quad":true,"authorize_extra_credits":true}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls := checkpointer.count(); calls != 1 {
		t.Fatalf("checkpoint calls = %d; want 1", calls)
	}
	hits, bodies := upstream.snapshot()
	if hits != 1 {
		t.Fatalf("convert endpoint hits = %d; want 1", hits)
	}
	body := bodies[0]
	if body["input"] != "source-remote-9" {
		t.Fatalf("wire input = %#v; want 源任务上游 remote id", body["input"])
	}
	if body["format"] != "FBX" || body["quad"] != true {
		t.Fatalf("wire body = %#v", body)
	}
	if _, exists := body["source_task"]; exists {
		t.Fatalf("source_task 参数名不得直传上游: %#v", body)
	}
	if _, exists := body["authorize_extra_credits"]; exists {
		t.Fatalf("同意参数是本地证据，不得上行: %#v", body)
	}
	if observation.Dispatch == nil ||
		observation.Dispatch.Result != generation.DispatchResultDispatched {
		t.Fatalf("dispatch receipt = %#v", observation.Dispatch)
	}
	if observation.Job == nil || observation.Job.RemoteID != "convert-task-1" {
		t.Fatalf("job = %#v", observation.Job)
	}
	// intent 自含阶段与协调标识（stage=models/convert）。
	var intent struct {
		Stage         string         `json:"stage"`
		SubmissionKey string         `json:"submission_key"`
		Details       map[string]any `json:"details"`
	}
	if err := json.Unmarshal(checkpointer.intent, &intent); err != nil {
		t.Fatal(err)
	}
	if intent.Stage != "models/convert" || intent.SubmissionKey == "" ||
		intent.Details["source_task"] != "source-remote-9" {
		t.Fatalf("intent = %s", checkpointer.intent)
	}
}

// ── 基础转换（无附加选项）：无需同意参数，1 次发送 ──

func TestConversionBaseOnlyRequiresNoConsent(t *testing.T) {
	checkpointer := &recordingCheckpointer{}
	upstream := newConversionUpstream(t)
	adapter := newAdapter(t, upstream.server)

	if _, err := adapter.Start(
		generation.WithDispatchCheckpointer(context.Background(), checkpointer),
		conversionStartRequest(
			conversionOffering(t),
			`{"source_task":"task-source-1","format":"GLTF"}`,
		),
	); err != nil {
		t.Fatal(err)
	}
	if calls, hits := checkpointer.count(), func() int { h, _ := upstream.snapshot(); return h }(); calls != 1 || hits != 1 {
		t.Fatalf("checkpoints = %d, hits = %d; want 1/1", calls, hits)
	}
}

// ── 拒绝路径：零 Checkpoint、零 convert 调用 ──

func TestConversionDenialsYieldZeroCheckpointsAndZeroSends(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(offering *generation.Offering)
		input   string
		wantErr string
	}{
		{
			name:    "缺同意",
			input:   `{"source_task":"task-source-1","format":"FBX","quad":true}`,
			wantErr: "请先勾选额外积分消耗同意",
		},
		{
			name: "超上限",
			mutate: func(offering *generation.Offering) {
				offering.CostPolicy.MaxAuthorizedExtraCredits = 0
			},
			input:   `{"source_task":"task-source-1","format":"FBX","quad":true,"authorize_extra_credits":true}`,
			wantErr: "超出本次提交的授权上限",
		},
		{
			name: "未知成本（缺声明）",
			mutate: func(offering *generation.Offering) {
				offering.Cost = nil
			},
			input:   `{"source_task":"task-source-1","format":"FBX"}`,
			wantErr: "缺少可核对的成本声明",
		},
		{
			name: "未知格式（冻结 options 外）",
			mutate: func(offering *generation.Offering) {
				for index := range offering.Parameters {
					if offering.Parameters[index].Name == "format" {
						offering.Parameters[index].Options = nil
					}
				}
			},
			input:   `{"source_task":"task-source-1","format":"GLB"}`,
			wantErr: "不在供给项声明的可选范围内",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			checkpointer := &recordingCheckpointer{}
			upstream := newConversionUpstream(t)
			adapter := newAdapter(t, upstream.server)
			offering := conversionOffering(t)
			if testCase.mutate != nil {
				testCase.mutate(&offering)
			}
			_, err := adapter.Start(
				generation.WithDispatchCheckpointer(context.Background(), checkpointer),
				conversionStartRequest(offering, testCase.input),
			)
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("err = %v; want 包含 %q", err, testCase.wantErr)
			}
			if calls := checkpointer.count(); calls != 0 {
				t.Fatalf("checkpoint calls = %d; want 0（拒绝必须先于 Checkpoint）", calls)
			}
			if hits, _ := upstream.snapshot(); hits != 0 {
				t.Fatalf("convert endpoint hits = %d; want 0（零付费调用）", hits)
			}
		})
	}
}

// ── 恢复期栅栏：pending 检查点下 0 次发送 ──

func TestConversionPendingFenceYieldsZeroSends(t *testing.T) {
	checkpointer := &recordingCheckpointer{pended: true}
	upstream := newConversionUpstream(t)
	adapter := newAdapter(t, upstream.server)

	_, err := adapter.Start(
		generation.WithDispatchCheckpointer(context.Background(), checkpointer),
		conversionStartRequest(
			conversionOffering(t),
			`{"source_task":"task-source-1","format":"GLTF"}`,
		),
	)
	if err == nil || !errors.Is(err, generation.ErrDispatchPending) {
		t.Fatalf("err = %v; want ErrDispatchPending", err)
	}
	if hits, _ := upstream.snapshot(); hits != 0 {
		t.Fatalf("convert endpoint hits = %d; want 0", hits)
	}
}

// ── consumed_credit 证据（官方 v3 字段 credits_consumed）──

func TestConversionObservePersistsConsumedCreditEvidence(t *testing.T) {
	upstream := newConversionUpstream(t)
	adapter := newAdapter(t, upstream.server)
	offering := conversionOffering(t)

	started, err := adapter.Start(
		context.Background(),
		conversionStartRequest(
			offering,
			`{"source_task":"task-source-1","format":"GLTF"}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil {
		t.Fatal("Start 必须返回上游作业")
	}
	observed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID:    "task-conv-1",
		AttemptID: "att-conv-1",
		Offering:  offering,
		Job:       *started.Job,
		Opaque:    started.Opaque,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Phase != generation.UpstreamCompleted {
		t.Fatalf("phase = %s; want completed", observed.Phase)
	}
	// 产物按声明别名携带 converted 变体（对齐 required 输出契约）。
	if len(observed.Artifacts) != 1 ||
		observed.Artifacts[0].Role != "model" ||
		observed.Artifacts[0].Variant != "converted" {
		t.Fatalf("artifacts = %#v", observed.Artifacts)
	}
	// 私有证据：源任务、格式、原始 credits_consumed（不截断、不上公开面）。
	var evidence struct {
		SourceRemoteID string          `json:"source_remote_id"`
		Format         string          `json:"format"`
		ConsumedCredit json.RawMessage `json:"consumed_credit"`
	}
	if err := json.Unmarshal(observed.Opaque, &evidence); err != nil {
		t.Fatalf("opaque = %s: %v", observed.Opaque, err)
	}
	if evidence.SourceRemoteID != "source-remote-9" || evidence.Format != "GLTF" {
		t.Fatalf("evidence = %#v", evidence)
	}
	if strings.TrimSpace(string(evidence.ConsumedCredit)) != "10" {
		t.Fatalf("consumed_credit = %s; want 原始数值 10", evidence.ConsumedCredit)
	}
}

func TestConversionObservePreservesFractionalConsumedCredit(t *testing.T) {
	// VIP 折扣可产生两位小数（官方 SDK 注释）；证据必须原样保存。
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"task_id":          "convert-task-1",
					"status":           "running",
					"progress":         40,
					"credits_consumed": json.RawMessage(`7.50`),
					"output":           map[string]any{},
				},
			})
		},
	))
	t.Cleanup(upstream.Close)
	adapter := newAdapter(t, upstream)
	observed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		Offering: conversionOffering(t),
		Job:      generation.UpstreamJob{ProviderID: "tripo", RemoteID: "convert-task-1"},
		Opaque:   json.RawMessage(`{"source_remote_id":"source-remote-9","format":"USDZ"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		SourceRemoteID string          `json:"source_remote_id"`
		Format         string          `json:"format"`
		ConsumedCredit json.RawMessage `json:"consumed_credit"`
	}
	if err := json.Unmarshal(observed.Opaque, &evidence); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(evidence.ConsumedCredit)) != "7.50" {
		t.Fatalf("consumed_credit = %s; want 原样 7.50", evidence.ConsumedCredit)
	}
	if evidence.SourceRemoteID != "source-remote-9" || evidence.Format != "USDZ" {
		t.Fatalf("滚动合并丢失 Start 证据: %#v", evidence)
	}
}

// ── 提交期/规划器行为 parity：Normalize 拒绝的输入，直连规划器也拒绝 ──

func TestConversionPlannerParityWithSubmitTime(t *testing.T) {
	taskCatalog := catalogtest.LoadDefaultWithTripoConversion(t)
	offering := conversionOffering(t)
	inputs := []string{
		`{"source_task":"task-source-1","format":"GLTF"}`,
		`{"source_task":"task-source-1","format":"FBX","quad":true,"authorize_extra_credits":true}`,
		`{"source_task":"task-source-1","format":"FBX","quad":true}`,
		`{"source_task":"task-source-1","format":"OBJ","face_limit":150000,"authorize_extra_credits":true}`,
	}
	for _, input := range inputs {
		_, _, normalizeErr := taskCatalog.Normalize(
			generation.FormatConversion,
			"tripo/tripo-v3.1/format-conversion",
			json.RawMessage(input),
		)
		upstream := newConversionUpstream(t)
		adapter := newAdapter(t, upstream.server)
		checkpointer := &recordingCheckpointer{}
		_, startErr := adapter.Start(
			generation.WithDispatchCheckpointer(context.Background(), checkpointer),
			conversionStartRequest(offering, input),
		)
		hits, _ := upstream.snapshot()
		if normalizeErr != nil {
			if startErr == nil {
				t.Fatalf("parity broken：Normalize 拒绝 %s 但规划器放行", input)
			}
			if hits != 0 || checkpointer.count() != 0 {
				t.Fatalf("parity broken：被拒输入 %s 产生了发送", input)
			}
			continue
		}
		if startErr != nil {
			t.Fatalf("parity broken：Normalize 接受 %s 但规划器拒绝: %v", input, startErr)
		}
		if hits != 1 || checkpointer.count() != 1 {
			t.Fatalf("parity broken：接受输入 %s 发送计数 = %d/%d", input, hits, checkpointer.count())
		}
	}
}

// ── Adapter 构造辅助（沿用既有 harness 的映射声明） ──

var _ = tripo.New
