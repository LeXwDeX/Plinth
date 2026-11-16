package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
)

// #303: the Hyper3D fake delivers the model its request implies, so inspect_model's
// purpose check (the triangle and texture-size items of usage_check) can be
// regressed on Rodin results. The bytes are the PBR pack the fake serves for a
// request (hyper3dapiv2.DeliveredModel, the same file its file route hands out),
// saved as a succeeded Rodin Task and judged against the shipped game purposes.
// What the product reads is what the file holds: the drawable triangles of its
// index buffer and the edge of each embedded PNG.

// rodinRequest is a valid Gen-2.5 text-to-3D form with the game purposes' values.
func rodinRequest(extra map[string]string) map[string]string {
	fields := map[string]string{
		"tier": "Gen-2.5-Extreme-High", "geometry_file_format": "glb", "material": "PBR", "prompt": "a game character",
		"mesh_mode": "Quad", "quality": "high", "quality_override": "8000", "texture_mode": "high", "quad_normal": "true",
	}
	for name, value := range extra {
		fields[name] = value
	}
	return fields
}

func inspectRodin(t *testing.T, fields map[string]string, purpose, platform string) map[string]any {
	t.Helper()
	file, err := hyper3dapiv2.DeliveredModel(fields)
	if err != nil {
		t.Fatal(err)
	}
	task := reviewTask("task-rodin", "owner-a", "session-a", file.Data, generation.TaskSucceeded)
	task.Capability = generation.TextTo3D
	task.Offering = generation.Offering{
		ID: "hyper3d/rodin-gen-2.5/text-to-3d", ProviderID: "hyper3d", ProviderName: "Hyper3D", ModelName: "Rodin Gen-2.5",
	}
	executor, _ := reviewExecutor(t, newReviewGateway(task), newReviewAssets(map[generation.AssetID][]byte{"task-rodin-glb": file.Data}), nil)
	command := reviewCommand("inspect_model", `{"model":"task-rodin"}`)
	command.Options.ProductionBrief = chat.ProductionBrief{"purpose": json.RawMessage(`"` + purpose + `"`)}
	if platform != "" {
		command.Options.ProductionBrief["target_platform"] = json.RawMessage(`"` + platform + `"`)
	}
	result, err := executor.InspectModel(context.Background(), command)
	if err != nil || result.Error != "" {
		t.Fatalf("inspect = %+v err=%v", result, err)
	}
	return reviewPayload(t, result)
}

// The game purposes' own values for Rodin (Quad face counts and the 2K texture
// mode) deliver a model that passes the triangle, UV and texture-size checks of
// the platform's budget, and nothing the purpose judges fails.
func TestInspectModelPassesTheGameBudgetsForTheRodinFakesDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, purpose, platform string
		fields                  map[string]string
		triangles               float64
	}{
		{"character on a phone", "game_character", "mobile", rodinRequest(map[string]string{"TAPose": "true"}), 17440},
		{"character without a platform", "game_character", "", rodinRequest(map[string]string{"TAPose": "true"}), 17440},
		{"character on a PC", "game_character", "pc_console", rodinRequest(map[string]string{"TAPose": "true", "quality_override": "10000"}), 21800},
		{"prop on a phone", "game_prop", "mobile", rodinRequest(map[string]string{"quality_override": "3200"}), 6976},
		{"prop on a PC", "game_prop", "pc_console", rodinRequest(map[string]string{"quality_override": "4000"}), 8720},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := inspectRodin(t, tc.fields, tc.purpose, tc.platform)
			usage, _ := payload["usage_check"].(map[string]any)
			if usage["purpose"] != tc.purpose || usage["purpose_source"] != "production_brief" {
				t.Fatalf("usage purpose = %v/%v", usage["purpose"], usage["purpose_source"])
			}
			if got := reviewNumber(t, payload, "report", "triangles"); got != tc.triangles {
				t.Errorf("the model has %v triangles, want %v", got, tc.triangles)
			}
			if got := reviewNumber(t, payload, "report", "texture_count"); got != 3 {
				t.Errorf("the model has %v textures, want the three of the PBR pack", got)
			}
			for _, check := range []string{"triangles", "uv", "texture_size"} {
				item := reviewUsageItem(t, payload, check)
				if item["status"] != "pass" {
					t.Errorf("%s check = %v, want pass: %v", check, item["status"], item)
				}
			}
			if edge := reviewUsageItem(t, payload, "texture_size")["actual"]; edge != float64(2048) {
				t.Errorf("texture_size actual = %v, want 2048", edge)
			}
			items, _ := usage["checks"].([]any)
			for _, raw := range items {
				if item, _ := raw.(map[string]any); item["status"] == "fail" {
					t.Errorf("usage check failed: %v", item)
				}
			}
		})
	}
}

// The display request (what a user who names Rodin without a purpose gets: Raw,
// 2,000,000 faces, texture_mode extreme-high) delivers the fixture's ceiling of
// triangles and a 6144 base color. Judged against a game purpose both checks
// fail, as the real file would, and the texture one reports the edge it read:
// this is the regression the fixed 72-triangle, 64-pixel fixture could not give.
func TestInspectModelFailsTheGameBudgetsForTheRodinFakesDisplayDelivery(t *testing.T) {
	display := map[string]string{
		"tier": "Gen-2.5-Extreme-High", "geometry_file_format": "glb", "material": "PBR", "prompt": "a plain cube",
		"mesh_mode": "Raw", "quality": "high", "quality_override": "2000000", "texture_mode": "extreme-high",
	}
	for _, platform := range []string{"mobile", "pc_console"} {
		t.Run(platform, func(t *testing.T) {
			payload := inspectRodin(t, display, "game_character", platform)
			triangles := reviewUsageItem(t, payload, "triangles")
			if triangles["status"] != "fail" || triangles["actual"] != float64(121032) {
				t.Errorf("triangles = %v, want a failure at the fixture's 121032", triangles)
			}
			texture := reviewUsageItem(t, payload, "texture_size")
			if texture["status"] != "fail" || texture["actual"] != float64(6144) {
				t.Errorf("texture_size = %v, want a failure at the 6144 base color", texture)
			}
		})
	}
	// The texture override alone brings the display model's textures inside the budget.
	payload := inspectRodin(t, map[string]string{
		"tier": "Gen-2.5-Extreme-High", "geometry_file_format": "glb", "material": "PBR", "prompt": "a plain cube",
		"mesh_mode": "Raw", "quality": "high", "quality_override": "2000000", "texture_mode": "high",
	}, "game_character", "pc_console")
	if got := reviewUsageItem(t, payload, "texture_size")["status"]; got != "pass" {
		t.Errorf("texture_size with texture_mode high = %v, want pass", got)
	}
}
