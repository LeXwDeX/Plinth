package generationtool_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// TestModelReviewInspectCoreMatchesThePlatformEntry proves the outward MCP
// core of inspect_model (#332) reports what the platform tool reports: the same
// source, the same report and part truncation, and the same purpose check item
// by item. Only the checks are compared; the platform tool's self-check
// evidence belongs to its turn.
func TestModelReviewInspectCoreMatchesThePlatformEntry(t *testing.T) {
	glb := reviewModelGLB(t, 1, 3)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	gateway.offerings = map[generation.Capability][]generation.Offering{
		"remesh": {{ID: "test/remesh", Capability: "remesh", Enabled: true}},
	}
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, _ := reviewExecutor(t, gateway, assets, func(app *catalog.ApplicationSchema) {
		app.Chat.ModelReview.Inspect.MaxListedParts = 2
	})
	brief := chat.ProductionBrief{
		"purpose":         []byte(`"game_character"`),
		"triangle_budget": []byte(`100`),
	}
	command := reviewCommand("inspect_model", `{"model":"task-a"}`)
	command.Options.ProductionBrief = brief
	platform, err := executor.InspectModel(context.Background(), command)
	if err != nil || platform.Error != "" {
		t.Fatalf("platform inspect = %+v err=%v", platform, err)
	}
	inspected, err := executor.InspectReviewedModel(context.Background(), generationtool.ModelInspectRequest{
		Owner: "owner-a", TaskID: "task-a", SourceVariantPreference: []string{"glb"}, Brief: brief,
	})
	if err != nil {
		t.Fatalf("core inspect: %v", err)
	}
	if inspected.TaskID != "task-a" || inspected.AssetID != "task-a-glb" {
		t.Fatalf("core source = %s/%s, want task-a/task-a-glb", inspected.TaskID, inspected.AssetID)
	}
	payload := reviewPayload(t, platform)
	if got, want := inspected.PartsTotal, int(payload["parts_total"].(float64)); got != want {
		t.Fatalf("core parts_total = %d, platform = %d", got, want)
	}
	if got, want := inspected.PartsTruncated, payload["parts_truncated"].(bool); got != want {
		t.Fatalf("core parts_truncated = %v, platform = %v", got, want)
	}
	report := payload["report"].(map[string]any)
	if got, want := inspected.Report.Triangles, int64(reviewNumber(t, payload, "report", "triangles")); got != want {
		t.Fatalf("core triangles = %d, platform = %d", got, want)
	}
	if got, want := inspected.Report.Vertices, int64(reviewNumber(t, payload, "report", "vertices")); got != want {
		t.Fatalf("core vertices = %d, platform = %d", got, want)
	}
	platformParts := report["parts"].([]any)
	if len(inspected.Report.Parts) != len(platformParts) || len(platformParts) != 2 {
		t.Fatalf("core parts = %d, platform = %d, want both truncated to 2",
			len(inspected.Report.Parts), len(platformParts))
	}
	platformUV := report["uv"].(map[string]any)["primitives"].([]any)
	if len(inspected.Report.UV.Primitives) != len(platformUV) {
		t.Fatalf("core primitives = %d, platform = %d",
			len(inspected.Report.UV.Primitives), len(platformUV))
	}
	if inspected.UsageCheck == nil {
		t.Fatal("core inspection has no usage check")
	}
	platformUsage, err := json.Marshal(payload["usage_check"])
	if err != nil {
		t.Fatal(err)
	}
	coreUsage, err := json.Marshal(inspected.UsageCheck)
	if err != nil {
		t.Fatal(err)
	}
	var want, got generationtool.ModelReviewUsageCheck
	if err := json.Unmarshal(platformUsage, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(coreUsage, &got); err != nil {
		t.Fatal(err)
	}
	if got.Purpose != want.Purpose || got.PurposeLabel != want.PurposeLabel ||
		got.PurposeSource != want.PurposeSource || got.Platform != want.Platform ||
		got.PlatformLabel != want.PlatformLabel || got.Note != want.Note {
		t.Fatalf("core usage = %+v, platform = %+v", got, want)
	}
	if len(got.Checks) != len(want.Checks) {
		t.Fatalf("core checks = %d, platform = %d", len(got.Checks), len(want.Checks))
	}
	for index, wantItem := range want.Checks {
		gotItem := got.Checks[index]
		if gotItem.Check != wantItem.Check || gotItem.Status != wantItem.Status ||
			!reflect.DeepEqual(gotItem.Actual, wantItem.Actual) ||
			!reflect.DeepEqual(gotItem.Budget, wantItem.Budget) ||
			gotItem.FixHint != wantItem.FixHint || gotItem.Note != wantItem.Note {
			t.Fatalf("check %d = %+v, platform = %+v", index, gotItem, wantItem)
		}
	}
}

// TestModelReviewInspectCoreRefusalMatchesThePlatformText proves a refused
// outward inspection keeps the platform's localized refusal text.
func TestModelReviewInspectCoreRefusalMatchesThePlatformText(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	task := reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded)
	task.Assets[1].MediaType = "model/gltf+json"
	gateway := newReviewGateway(task)
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, _ := reviewExecutor(t, gateway, assets, nil)
	platform, err := executor.InspectModel(context.Background(), reviewCommand("inspect_model", `{"model":"task-a"}`))
	if err != nil || platform.Error == "" {
		t.Fatalf("platform inspect = %+v err=%v", platform, err)
	}
	inspected, err := executor.InspectReviewedModel(context.Background(), generationtool.ModelInspectRequest{
		Owner: "owner-a", TaskID: "task-a", AssetID: "task-a-glb",
	})
	var refusal *generationtool.ModelInspectRefusal
	if !errors.As(err, &refusal) || refusal.Kind != generationtool.ModelInspectNotGLB {
		t.Fatalf("core inspect = %+v err=%v, want a not_glb refusal", inspected, err)
	}
	if refusal.Message != platform.Error {
		t.Fatalf("core refusal = %q, platform = %q", refusal.Message, platform.Error)
	}
}
