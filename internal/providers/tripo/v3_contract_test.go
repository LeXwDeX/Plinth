package tripo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// 本文件基于 Tripo 官方文档做全量 TDD。
// 文档真源：
//   - 任务查询（status/output 字段）：https://docs.tripo3d.ai/task-query/get-your-task-result.html
//   - text-to-model 参数：https://docs.tripo3d.ai/model-generation/text-to-model-v3-0-v3-1.html
//   - image-to-model 参数：https://docs.tripo3d.ai/model-generation/image-to-model-v3-0-v3-1.html
//   - 错误码：https://docs.tripo3d.ai/get-started/errors-and-error-handling.html
//   - v2→v3 迁移：官方 migration-v2-to-v3.pdf

// ── 任务状态映射（文档列出 8 种 status）──

func TestTaskStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		wantPhase  generation.UpstreamPhase
		wantFailed bool
	}{
		// Ongoing
		{name: "queued → Accepted", status: "queued", wantPhase: generation.UpstreamAccepted},
		{name: "running → Running", status: "running", wantPhase: generation.UpstreamRunning},
		// Finalized
		{name: "success → Completed", status: "success", wantPhase: generation.UpstreamCompleted},
		{name: "failed → Failed", status: "failed", wantPhase: generation.UpstreamFailed, wantFailed: true},
		{name: "banned → Failed", status: "banned", wantPhase: generation.UpstreamFailed, wantFailed: true},
		{name: "expired → Failed", status: "expired", wantPhase: generation.UpstreamFailed, wantFailed: true},
		{name: "cancelled → Canceled", status: "cancelled", wantPhase: generation.UpstreamCanceled, wantFailed: true},
		// unknown 状态应返回 error（协议错误）；wantPhase 留空占位，error 路径提前 return 不被断言
		{name: "unknown → Protocol error", status: "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newObserveServer(t, tc.status, 50, nil)
			defer upstream.Close()
			adapter := newAdapter(t, upstream)
			obs, err := adapter.Observe(context.Background(), generation.ObserveRequest{
				AttemptID: "att-1",
				Offering:  tripoOffering(generation.TextTo3D),
				Job:       generation.UpstreamJob{ProviderID: "tripo", RemoteID: "uuid-1"},
			})
			if err != nil {
				if tc.status != "unknown" {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if tc.status == "unknown" {
				t.Fatalf("期望 unknown 状态返回 error，实际 nil")
			}
			if obs.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", obs.Phase, tc.wantPhase)
			}
			if tc.wantFailed && (obs.Failure == nil) {
				t.Errorf("期望有 Failure，实际为空")
			}
			if !tc.wantFailed && obs.Failure != nil {
				t.Errorf("不期望 Failure，实际有: %v", obs.Failure)
			}
		})
	}
}

// ── progress 值映射（文档：queued=0, running=N, success=100）──

func TestProgressMapping(t *testing.T) {
	cases := []struct {
		name          string
		status        string
		inputProgress int
		wantProgress  int
	}{
		{name: "queued progress=0", status: "queued", inputProgress: 0, wantProgress: 0},
		{name: "running progress=42", status: "running", inputProgress: 42, wantProgress: 42},
		{name: "success progress 强制 100", status: "success", inputProgress: 99, wantProgress: 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newObserveServer(t, tc.status, tc.inputProgress, nil)
			defer upstream.Close()
			adapter := newAdapter(t, upstream)
			obs, err := adapter.Observe(context.Background(), generation.ObserveRequest{
				AttemptID: "att-1",
				Offering:  tripoOffering(generation.TextTo3D),
				Job:       generation.UpstreamJob{ProviderID: "tripo", RemoteID: "uuid-1"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if obs.Progress != tc.wantProgress {
				t.Errorf("progress = %d, want %d", obs.Progress, tc.wantProgress)
			}
		})
	}
}

// ── output 字段解析（兼容 v2/v3 两种字段名）──
// v2 文档 task-query 用 model/pbr_model/base_model/rendered_image
// v3 SDK _normalize_v3_task_data 显示 v3 mesh 任务用 model_url/pbr_model_url/...
// 由于无法实测确认 text/image-to-model 走 v3 端点时返回哪种字段名，
// adapter 必须同时兼容两种，避免模型 URL 漏提取（导致 3D 模型无贴图）。

func TestOutputFieldParsing(t *testing.T) {
	t.Run("v3 _url 后缀字段", func(t *testing.T) {
		output := map[string]any{
			"model_url":          "https://tripo3d.ai/model.glb",
			"pbr_model_url":      "https://tripo3d.ai/pbr.glb",
			"base_model_url":     "https://tripo3d.ai/base.glb",
			"rendered_image_url": "https://tripo3d.ai/preview.webp",
		}
		assertArtifacts(t, output, 4)
	})

	t.Run("v2 无后缀字段（v3 也可能返回此格式）", func(t *testing.T) {
		output := map[string]any{
			"model":          "https://tripo3d.ai/model.glb",
			"pbr_model":      "https://tripo3d.ai/pbr.glb",
			"base_model":     "https://tripo3d.ai/base.glb",
			"rendered_image": "https://tripo3d.ai/preview.webp",
		}
		assertArtifacts(t, output, 4)
	})

	t.Run("重复 URL 去重（model 和 base_model 相同）", func(t *testing.T) {
		dup := "https://tripo3d.ai/same.glb"
		output := map[string]any{
			"model":      dup,
			"base_model": dup,
		}
		upstream := newObserveServer(t, "success", 100, output)
		defer upstream.Close()
		adapter := newAdapter(t, upstream)
		obs, err := adapter.Observe(context.Background(), generation.ObserveRequest{
			AttemptID: "att-1",
			Offering: generation.Offering{
				Capability:      generation.TextTo3D,
				AssetHosts:      []string{"tripo3d.ai"},
				ProviderOptions: []byte(`{"observe_endpoint":"tasks/{task_id}"}`),
			},
			Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "uuid-1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		modelCount := 0
		for _, a := range obs.Artifacts {
			if a.Role == "model" {
				modelCount++
			}
		}
		if modelCount != 1 {
			t.Errorf("重复 URL 应去重，model count = %d, want 1", modelCount)
		}
	})

	t.Run("output 为空时无 artifacts", func(t *testing.T) {
		upstream := newObserveServer(t, "success", 100, map[string]any{})
		defer upstream.Close()
		adapter := newAdapter(t, upstream)
		obs, err := adapter.Observe(context.Background(), generation.ObserveRequest{
			AttemptID: "att-1",
			Offering:  tripoOffering(generation.TextTo3D),
			Job:       generation.UpstreamJob{ProviderID: "tripo", RemoteID: "uuid-1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(obs.Artifacts) != 0 {
			t.Errorf("empty output should yield 0 artifacts, got %d", len(obs.Artifacts))
		}
	})
}

func assertArtifacts(t *testing.T, output map[string]any, wantCount int) {
	t.Helper()
	upstream := newObserveServer(t, "success", 100, output)
	defer upstream.Close()
	adapter := newAdapter(t, upstream)
	obs, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "att-1",
		Offering: generation.Offering{
			Capability:      generation.TextTo3D,
			AssetHosts:      []string{"tripo3d.ai"},
			ProviderOptions: []byte(`{"observe_endpoint":"tasks/{task_id}"}`),
		},
		Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "uuid-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Artifacts) != wantCount {
		t.Fatalf("artifacts count = %d, want %d; artifacts=%#v", len(obs.Artifacts), wantCount, obs.Artifacts)
	}
	roles := map[string]bool{}
	for _, a := range obs.Artifacts {
		roles[a.Role] = true
	}
	if !roles["model"] || !roles["thumbnail"] {
		t.Errorf("missing roles: %#v", roles)
	}
}

// ── generate_parts 互斥参数注入（文档：与 texture=true/pbr=true/quad=true 不兼容）──
// W2 起 forced_when 由 Offering 参数声明、catalog.Normalize 服务端最终裁决，
// adapter 只原样转发规范化输入（不再内嵌身份分支）。

func TestGeneratePartsMutexForcedBySchemaDeclaration(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !exists {
		t.Fatal("默认 Catalog 缺少 tripo 文生 3D Offering")
	}
	_, normalized, err := taskCatalog.Normalize(
		generation.TextTo3D,
		offering.ID,
		[]byte(`{"prompt":"cat","generate_parts":true,"texture":true,"pbr":true,"quad":true,"authorize_extra_credits":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(normalized, &values); err != nil {
		t.Fatal(err)
	}
	if values["texture"] != false {
		t.Errorf("texture = %v, want false", values["texture"])
	}
	if values["pbr"] != false {
		t.Errorf("pbr = %v, want false", values["pbr"])
	}
	if values["quad"] != false {
		t.Errorf("quad = %v, want false", values["quad"])
	}
	if values["geometry_quality"] != "detailed" {
		t.Errorf("geometry_quality = %v, want detailed", values["geometry_quality"])
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/generation/text-to-model" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["generate_parts"] == true {
				if body["texture"] != false || body["pbr"] != false || body["quad"] != false {
					t.Errorf("上游 body 互斥参数未被强制: %#v", body)
				}
				if body["geometry_quality"] != "detailed" {
					t.Errorf("上游 body geometry_quality=%v, want detailed", body["geometry_quality"])
				}
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"task_id": "uuid-genparts"}})
		}
	}))
	defer upstream.Close()
	adapter := newAdapter(t, upstream)
	if _, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID:     "att-1",
		SubmissionKey: "sub-1",
		Offering:      offering,
		Input:         normalized,
	}); err != nil {
		t.Fatal(err)
	}
}

// ── v3 参数：model 而非 model_version ──

func TestV3UsesModelNotModelVersion(t *testing.T) {
	var capturedBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/generation/text-to-model" {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"task_id": "uuid-1"}})
		}
	}))
	defer upstream.Close()
	adapter := newAdapter(t, upstream)
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID:     "att-1",
		SubmissionKey: "sub-1",
		Offering: generation.Offering{
			Capability:      generation.TextTo3D,
			ProviderOptions: []byte(`{"model":"v3.1-20260211","endpoint":"generation/text-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
		},
		Input: []byte(`{"prompt":"cat"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturedBody["model"] != "v3.1-20260211" {
		t.Errorf("body.model = %v, want v3.1-20260211", capturedBody["model"])
	}
	if _, hasOld := capturedBody["model_version"]; hasOld {
		t.Errorf("body 不应包含 model_version（v3 已改名 model）")
	}
	if _, hasType := capturedBody["type"]; hasType {
		t.Errorf("body 不应包含 type（v3 端点路径已区分类型）")
	}
}

// ── v3 端点路径按 capability 分 ──

func TestV3EndpointRouting(t *testing.T) {
	t.Run("text-to-3d → generation/text-to-model", func(t *testing.T) {
		var path string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"task_id": "uuid-1"}})
		}))
		defer upstream.Close()
		adapter := newAdapter(t, upstream)
		_, _ = adapter.Start(context.Background(), generation.StartRequest{
			Offering: tripoOffering(generation.TextTo3D),
			Input:    []byte(`{"prompt":"cat"}`),
		})
		if path != "/generation/text-to-model" {
			t.Errorf("path = %q, want /generation/text-to-model", path)
		}
	})
	t.Run("image-to-3d → generation/image-to-model", func(t *testing.T) {
		var path string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/generation/image-to-model" {
				path = r.URL.Path
			}
			// upload 和 task 创建都返回成功
			writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"task_id": "uuid-1", "file_token": "file-x"}})
		}))
		defer upstream.Close()
		adapter := newAdapterWithImage(t, upstream, []byte("pngdata"))
		_, err := adapter.Start(context.Background(), generation.StartRequest{
			Offering: tripoOffering(generation.ImageTo3D),
			Input:    []byte(`{}`),
			InputAssets: map[string][]generation.InputAsset{
				"reference_assets": {{ID: "a-1", MediaType: "image/png"}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if path != "/generation/image-to-model" {
			t.Errorf("path = %q, want /generation/image-to-model", path)
		}
	})
}

// ── 任务查询走 v3 tasks/{id} 端点 ──

func TestObserveUsesV3TasksEndpoint(t *testing.T) {
	var path string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		writeJSON(w, map[string]any{
			"code": 0,
			"data": map[string]any{
				"task_id":  "uuid-1",
				"status":   "queued",
				"progress": 0,
				"output":   map[string]any{},
			},
		})
	}))
	defer upstream.Close()
	adapter := newAdapter(t, upstream)
	_, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "att-1",
		Offering:  tripoOffering(generation.TextTo3D),
		Job:       generation.UpstreamJob{ProviderID: "tripo", RemoteID: "uuid-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tasks/uuid-1" {
		t.Errorf("path = %q, want /tasks/uuid-1", path)
	}
}

// ── 辅助函数 ──

func newAdapter(t *testing.T, upstream *httptest.Server) *tripo.Adapter {
	return newAdapterWithImage(t, upstream, nil)
}

func newAdapterWithImage(t *testing.T, upstream *httptest.Server, img []byte, offerings ...generation.Offering) *tripo.Adapter {
	t.Helper()
	errorMapping, statusMapping := declaredTripoMappings(t)
	cfg := tripo.Config{
		BaseURL:         upstream.URL,
		APIKey:          "tripo-key",
		HTTPClient:      upstream.Client(),
		AllowHTTPAssets: true,
		OfferingRoutes:  offerings,
		ErrorMapping:    errorMapping,
		StatusMapping:   statusMapping,
	}
	if img != nil {
		cfg.InputAssets = memoryInputAssets{content: img}
	}
	adapter, err := tripo.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func newObserveServer(t *testing.T, status string, progress int, output map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if output == nil {
			output = map[string]any{}
		}
		writeJSON(w, map[string]any{
			"code": 0,
			"data": map[string]any{
				"task_id":  "uuid-1",
				"status":   status,
				"progress": progress,
				"output":   output,
			},
		})
	}))
}

func tripoOffering(cap generation.Capability) generation.Offering {
	endpoint := "generation/text-to-model"
	if cap == generation.ImageTo3D {
		endpoint = "generation/image-to-model"
	}
	options, _ := json.Marshal(map[string]string{"model": "v3.1-20260211", "endpoint": endpoint, "observe_endpoint": "tasks/{task_id}", "upload_endpoint": "files"})
	return generation.Offering{ProviderID: "tripo", Capability: cap, PollInterval: time.Second, AssetHosts: []string{"tripo3d.ai"}, ProviderOptions: options}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
