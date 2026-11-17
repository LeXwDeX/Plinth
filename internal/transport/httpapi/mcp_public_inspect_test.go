package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// #332 step 7a: inspect_model is the platform's read-only inspection behind
// the external MCP listener. It reports the file's facts, the purpose check
// and the status block, and both a read key and a generate key see it.
func TestMCPInspectModelReportsTheInspectionAndTheStatusBlock(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	task := f.succeededTask("owner-a", "", "inspect-ok")
	f.inspector.result = generationtool.ModelInspectResult{
		TaskID:  task.ID,
		AssetID: "asset_checked",
		Report: modelinspect.Report{
			Triangles: 1200, Vertices: 800, Meshes: 1, MaterialCount: 1, TextureCount: 2, FileBytes: 4096,
			Parts:      []modelinspect.PartReport{},
			Materials:  []modelinspect.MaterialReport{},
			UV:         modelinspect.UVReport{Primitives: []modelinspect.PrimitiveUVReport{}, AllHaveUV: true},
			Skins:      []modelinspect.SkinReport{},
			Animations: []modelinspect.AnimationReport{},
			BBox:       modelinspect.BBoxReport{Unit: "meter"},
			Topology:   modelinspect.TopologyReport{ConnectedComponents: 1, Watertight: true},
			Warnings:   []string{},
		},
		UsageCheck: &generationtool.ModelReviewUsageCheck{
			Purpose: "game_prop", PurposeLabel: "游戏道具", PurposeSource: "production_brief",
			Checks: []generationtool.ModelReviewUsageCheckItem{{
				Check: "triangles", Status: "pass", Actual: float64(1200),
				Budget: map[string]any{"max": float64(20000)},
			}},
		},
		PartsTotal: 1,
	}
	key := f.issueKey("owner-a", "read")
	result := f.call(key, "inspect_model", map[string]any{"task_id": string(task.ID)})
	if result.code != http.StatusOK || result.isError {
		t.Fatalf("inspect_model = %d %s", result.code, result.raw)
	}
	if result.structured["task_id"] != string(task.ID) || result.structured["asset_id"] != "asset_checked" {
		t.Errorf("identifiers = %v", result.structured)
	}
	report := result.structured["report"].(map[string]any)
	if report["triangles"].(float64) != 1200 || report["vertices"].(float64) != 800 || report["file_bytes"].(float64) != 4096 {
		t.Errorf("report = %v", report)
	}
	usage := result.structured["usage_check"].(map[string]any)
	if usage["purpose"] != "game_prop" || usage["purpose_source"] != "production_brief" {
		t.Errorf("usage_check = %v", usage)
	}
	checks := usage["checks"].([]any)
	if len(checks) != 1 || checks[0].(map[string]any)["status"] != "pass" {
		t.Errorf("checks = %v", usage["checks"])
	}
	if result.structured["parts_total"].(float64) != 1 || result.structured["parts_truncated"] != false {
		t.Errorf("parts = %v", result.structured)
	}
	asset, ok := result.status(t)["asset"].(map[string]any)
	if !ok || asset["id"] != task.SessionID {
		t.Errorf("the status block is not about the task's asset: %s", result.raw)
	}
	assertServedSchema(t, f, "inspect_model", result)
	if len(f.inspector.calls) != 1 {
		t.Fatalf("the inspector was called %d times", len(f.inspector.calls))
	}
	request := f.inspector.calls[0]
	if request.Owner != generation.OwnerID("owner-a") || request.TaskID != task.ID || request.AssetID != "" ||
		len(request.Brief) != 0 ||
		len(request.SourceVariantPreference) != len(f.application.MCPPublic.Generation.SourceVariantPreference) {
		t.Errorf("inspection request = %+v", request)
	}
	// Both scopes see the read-only tool.
	for _, scopes := range [][]string{{"read"}, {"generate"}} {
		names := decodeToolResultRaw(t, f.post(f.issueKey("owner-a", scopes...), "tools/list", nil))["tools"].([]any)
		found := false
		for _, raw := range names {
			found = found || raw.(map[string]any)["name"] == "inspect_model"
		}
		if !found {
			t.Errorf("a key with scopes %v does not list inspect_model", scopes)
		}
	}
}

// A refusal of the inspection is a business error with the platform's own
// localized text: the tool never re-words it.
func TestMCPInspectModelMapsTheRefusalsToBusinessErrors(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	task := f.succeededTask("owner-a", "", "inspect-refusals")
	key := f.issueKey("owner-a", "read")
	for refusal, code := range map[string]string{
		generationtool.ModelInspectNotFound:    "asset_not_found",
		generationtool.ModelInspectNotReady:    "model_not_ready",
		generationtool.ModelInspectNotGLB:      "model_not_glb",
		generationtool.ModelInspectTooLarge:    "model_too_large",
		generationtool.ModelInspectUnreadable:  "model_unreadable",
		generationtool.ModelInspectUnavailable: "model_unavailable",
	} {
		message := "平台检查文案：" + refusal
		f.inspector.refuse = &generationtool.ModelInspectRefusal{Kind: refusal, Message: message}
		result := f.call(key, "inspect_model", map[string]any{"task_id": string(task.ID)})
		if result.code != http.StatusOK || !result.isError {
			t.Errorf("%s = %d isError=%v %s", refusal, result.code, result.isError, result.raw)
			continue
		}
		problem := result.structured["error"].(map[string]any)
		if problem["code"] != code || problem["message"] != message {
			t.Errorf("%s = %v, want %s %q", refusal, problem, code, message)
		}
		result.status(t)
		assertServedSchema(t, f, "inspect_model", result)
	}
}

// inspect_model never sees another person's task, and the inspector is not
// called for one.
func TestMCPInspectModelIsOwnerScoped(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	other := f.succeededTask("owner-b", "", "inspect-other")
	key := f.issueKey("owner-a", "read")
	result := f.call(key, "inspect_model", map[string]any{"task_id": string(other.ID)})
	if result.code != http.StatusOK || !result.isError || result.problemCode() != "task_not_found" {
		t.Fatalf("a foreign task = %d %s", result.code, result.raw)
	}
	if len(f.inspector.calls) != 0 {
		t.Fatalf("the inspector was called for another person's task: %+v", f.inspector.calls)
	}
}
