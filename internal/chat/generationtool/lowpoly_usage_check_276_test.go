package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #276: the Tripo decimation of the fake upstream delivers a textured, unwrapped
// model at the face count its request asked for (fakeupstream.LowPolyModelFile,
// the bytes the fake's model_url serves). inspect_model judges those delivered
// bytes against the game purposes of the shipped Schema: a game character on a
// phone and on a PC, and a game prop on a phone, each decimated to the upper end
// of its triangle budget (Tripo's own cap for a triangle mesh is 20000), pass the
// triangle, UV and texture-size checks and fail none. The kernel-level run that
// produces this very file through Generation and the asset store is
// TestGameCharacterBriefDecimatesAHighDetailModelIntoTheBudget in internal/app.
func TestInspectModelPassesTheGameBudgetsForTheFakesBakedDecimation(t *testing.T) {
	// The check is live: the plain 72-triangle fixture is far under a character's
	// minimum, and a decimation that stayed at that size would fail it.
	plain, err := fakeupstream.LowPolyModelFile(72)
	if err != nil {
		t.Fatal(err)
	}
	task := reviewTask("task-plain", "owner-a", "session-a", plain.Data, generation.TaskSucceeded)
	executor, _ := reviewExecutor(t, newReviewGateway(task), newReviewAssets(map[generation.AssetID][]byte{"task-plain-glb": plain.Data}), nil)
	command := reviewCommand("inspect_model", `{"model":"task-plain"}`)
	command.Options.ProductionBrief = chat.ProductionBrief{"purpose": json.RawMessage(`"game_character"`)}
	result, err := executor.InspectModel(context.Background(), command)
	if err != nil || result.Error != "" {
		t.Fatalf("inspect = %+v err=%v", result, err)
	}
	if got := reviewUsageItem(t, reviewPayload(t, result), "triangles")["status"]; got != "fail" {
		t.Fatalf("a 72-triangle character passed the triangle check: %v", got)
	}

	for _, tc := range []struct {
		name, purpose, platform string
		faceLimit               int
	}{
		{"character on a phone", "game_character", "mobile", 20000},
		{"character on a PC", "game_character", "pc_console", 20000},
		{"character without a platform", "game_character", "", 20000},
		{"prop on a phone", "game_prop", "mobile", 8000},
		{"prop on a PC", "game_prop", "pc_console", 20000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := fakeupstream.LowPolyModelFile(tc.faceLimit)
			if err != nil {
				t.Fatal(err)
			}
			task := reviewTask("task-a", "owner-a", "session-a", file.Data, generation.TaskSucceeded)
			task.Capability = generation.Remesh
			task.Offering = generation.Offering{ID: "tripo/geometry/decimate-triangle", ProviderID: "tripo", ProviderName: "Tripo3D", ModelName: "Tripo Retopology Triangle"}
			gateway := newReviewGateway(task)
			assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": file.Data})
			executor, _ := reviewExecutor(t, gateway, assets, nil)

			command := reviewCommand("inspect_model", `{"model":"task-a"}`)
			command.Options.ProductionBrief = chat.ProductionBrief{"purpose": json.RawMessage(`"` + tc.purpose + `"`)}
			if tc.platform != "" {
				command.Options.ProductionBrief["target_platform"] = json.RawMessage(`"` + tc.platform + `"`)
			}
			result, err := executor.InspectModel(context.Background(), command)
			if err != nil || result.Error != "" {
				t.Fatalf("inspect = %+v err=%v", result, err)
			}
			payload := reviewPayload(t, result)
			usage, _ := payload["usage_check"].(map[string]any)
			if usage["purpose"] != tc.purpose || usage["purpose_source"] != "production_brief" {
				t.Fatalf("usage purpose = %v/%v", usage["purpose"], usage["purpose_source"])
			}
			if tc.platform != "" && usage["platform"] != tc.platform {
				t.Fatalf("usage platform = %v, want %s", usage["platform"], tc.platform)
			}
			for _, check := range []string{"triangles", "uv", "texture_size"} {
				if got := reviewUsageItem(t, payload, check)["status"]; got != "pass" {
					t.Errorf("%s check = %v, want pass: %v", check, got, reviewUsageItem(t, payload, check))
				}
			}
			// The delivered file is textured, as a bake=true decimation is.
			if reviewNumber(t, payload, "report", "texture_count") < 1 {
				t.Error("the decimated model has no texture")
			}
			// Nothing the purpose judges fails (unknown is allowed: the purpose
			// does not ask for it or the file holds too little to judge it).
			items, _ := usage["checks"].([]any)
			for _, raw := range items {
				if item, _ := raw.(map[string]any); item["status"] == "fail" {
					t.Errorf("usage check failed: %v", item)
				}
			}
		})
	}
}
