package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// #294 follow-up: the one paid Rodin run of the game-character request (2026-11-13,
// dev-test) through the whole stack against the Hyper3D fake: the real Catalog, the
// kernel's Prepare, Grant, Submit and materialization, and the real Adapter. The
// request is the run's frozen input (Quad, 10000 faces, the 2K texture, T/A pose,
// normal bake, the extra-credit consent), the Task is what the person sees, and its
// assets are what the run delivered: a required GLB model and a WebP thumbnail. The
// wire and the files themselves are checked at the Adapter
// (internal/providers/hyper3d/rodin_game_character_294_test.go); this is the
// public result, with the Catalog's frozen quote of 1 credit that the run was
// charged. Since #303 the stored model is the run's too: 21,800 triangles for the
// 10,000 quads and three 2048x2048 textures, read back from the asset store the way
// the product reads a delivered model.
func TestRodinGameCharacterRunSucceedsThroughTheWholeStack(t *testing.T) {
	harness, product, _ := fakeStackHarness(t, func(origin string) map[string]string {
		return map[string]string{
			"hyper3d_base_url": origin + "/hyper3d/api/v2",
			"hyper3d_api_key":  fakeUpstreamKeyPrefix + "test-key",
		}
	})
	// The values that leave the advised defaults carry the reason the purpose gives.
	reason := "the game character purpose asks for it"
	accepted := submitStep(t, harness, generation.TextTo3D, "hyper3d/rodin-gen-2.5/text-to-3d",
		`{"prompt":"a game character standing in an A pose","mesh_mode":"Quad","quality_override":10000,"texture_mode":"high","TAPose":true,"quad_normal":true,"authorize_extra_credits":true}`,
		"rodin-game-character", map[string]string{"mesh_mode": reason, "quality_override": reason, "texture_mode": reason})
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state = %s, failure = %#v", task.State, task.Failure)
	}

	// The frozen input is the run's, and the frozen quote is the 1 credit it was charged.
	var frozen map[string]any
	if err := json.Unmarshal(task.Input, &frozen); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]any{
		"mesh_mode": "Quad", "quality_override": float64(10000), "texture_mode": "high", "TAPose": true, "quad_normal": true,
	} {
		if frozen[name] != want {
			t.Errorf("frozen %s = %v, want %v", name, frozen[name], want)
		}
	}
	if task.NativeQuote == nil || task.NativeQuote.TotalAmount != "1" || task.NativeQuote.Unit != "hyper3d.credit" {
		t.Fatalf("frozen quote = %+v, want 1 hyper3d.credit", task.NativeQuote)
	}

	// The delivered slots: the required GLB model and the preview as a WebP thumbnail.
	var model, thumbnail *generation.Asset
	for index := range task.Assets {
		switch asset := &task.Assets[index]; asset.Role {
		case "model":
			if model != nil {
				t.Fatalf("a second model was delivered: %+v", task.Assets)
			}
			model = asset
		case "thumbnail":
			thumbnail = asset
		default:
			t.Fatalf("unexpected asset %s/%s", asset.Role, asset.Variant)
		}
	}
	if model == nil || model.Variant != "glb" || model.MediaType != "model/gltf-binary" || !model.Required || model.Size == 0 {
		t.Fatalf("model = %+v", model)
	}
	if thumbnail == nil || thumbnail.Variant != "primary" || thumbnail.MediaType != "image/webp" || thumbnail.Required || thumbnail.Size == 0 {
		t.Fatalf("thumbnail = %+v", thumbnail)
	}
	if len(task.Assets) != 2 {
		t.Fatalf("assets = %d, want the model and the thumbnail only: %+v", len(task.Assets), task.Assets)
	}

	// The stored model is what the run delivered (#303): the triangles of 10,000
	// quads and three PNG textures of 2048x2048, inside the PC and console budget
	// the run's platform resolves to.
	report := storedRodinModel(t, harness, task, *model)
	if report.Triangles != 21800 || report.TextureCount != 3 {
		t.Fatalf("stored model: %d triangles and %d textures, want the run's 21800 and 3", report.Triangles, report.TextureCount)
	}
	assertRodinModelInBudget(t, report, gamePurposeBudget(t, product, "game_character", "pc_console"), 2048)
}

// The same request as the phone budget asks for it (8000 Quad faces, the
// purpose's override for phones, the web, XR and a session without a platform):
// the stored model has 17,440 triangles and 2048 textures, which is inside the
// 20,000-triangle and 2048-pixel budget of the phone class. This is the pass the
// purpose's numbers were chosen for, measured on a delivered file.
func TestRodinGameCharacterOnAPhoneStoresAModelInsideTheBudget(t *testing.T) {
	harness, product, _ := fakeStackHarness(t, func(origin string) map[string]string {
		return map[string]string{
			"hyper3d_base_url": origin + "/hyper3d/api/v2",
			"hyper3d_api_key":  fakeUpstreamKeyPrefix + "test-key",
		}
	})
	reason := "the game character purpose asks for it"
	accepted := submitStep(t, harness, generation.TextTo3D, "hyper3d/rodin-gen-2.5/text-to-3d",
		`{"prompt":"a game character standing in an A pose","mesh_mode":"Quad","quality_override":8000,"texture_mode":"high","TAPose":true,"quad_normal":true,"authorize_extra_credits":true}`,
		"rodin-game-character-phone", map[string]string{"mesh_mode": reason, "quality_override": reason, "texture_mode": reason})
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state = %s, failure = %#v", task.State, task.Failure)
	}
	var model *generation.Asset
	for index := range task.Assets {
		if task.Assets[index].Role == "model" {
			model = &task.Assets[index]
		}
	}
	if model == nil {
		t.Fatalf("no model was delivered: %+v", task.Assets)
	}
	report := storedRodinModel(t, harness, task, *model)
	if report.Triangles != 17440 || report.TextureCount != 3 {
		t.Fatalf("stored model: %d triangles and %d textures, want 17440 for 8000 quads and 3", report.Triangles, report.TextureCount)
	}
	budget := gamePurposeBudget(t, product, "game_character", "mobile")
	assertRodinModelInBudget(t, report, budget, 2048)
	// The PC count is over the phone class: the budget separates the two.
	if report.Triangles > int64(budget.MaxTriangles) {
		t.Fatalf("%d triangles are over the phone budget %d", report.Triangles, budget.MaxTriangles)
	}
}

// storedRodinModel reads a Task's stored model back through the kernel the way
// the product opens a delivered asset, and inspects it.
func storedRodinModel(t *testing.T, harness *acmeHarness, task generation.Task, model generation.Asset) modelinspect.Report {
	t.Helper()
	content, err := harness.module.OpenAsset(context.Background(), "owner-acme", task.ID, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(content.Body)
	_ = content.Body.Close()
	if readErr != nil || int64(len(data)) != model.Size {
		t.Fatalf("stored model: %d bytes (%v), the asset says %d", len(data), readErr, model.Size)
	}
	parsed, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
	if err != nil {
		t.Fatalf("the stored model cannot be read: %v", err)
	}
	return modelinspect.Inspect(parsed, modelinspect.DefaultInspectOptions())
}

// assertRodinModelInBudget holds a stored model to a purpose budget: the triangle
// range, UVs when required, and every PBR texture at the expected edge, which must
// also be inside the budget's texture size.
func assertRodinModelInBudget(t *testing.T, report modelinspect.Report, budget catalog.ChatModel3DUsageBudget, edge int) {
	t.Helper()
	minimum := int64(0)
	if budget.MinTriangles != nil {
		minimum = int64(*budget.MinTriangles)
	}
	if report.Triangles < minimum || report.Triangles > int64(budget.MaxTriangles) {
		t.Errorf("%d triangles are outside the %d-%d budget", report.Triangles, minimum, budget.MaxTriangles)
	}
	if budget.RequireUV && !report.UV.AllHaveUV {
		t.Error("the model has no UVs the budget requires")
	}
	if budget.MaxTextureSize == nil || edge > *budget.MaxTextureSize {
		t.Errorf("a %d texture is over the budget %v", edge, budget.MaxTextureSize)
	}
	present := 0
	for _, material := range report.Materials {
		for _, slot := range material.Slots {
			if !slot.Present {
				continue
			}
			present++
			if slot.Width != edge || slot.Height != edge {
				t.Errorf("slot %s is %dx%d, want %dx%d", slot.Slot, slot.Width, slot.Height, edge, edge)
			}
		}
	}
	if present != 3 {
		t.Errorf("%d texture slots are present, want the three of the PBR pack", present)
	}
}
