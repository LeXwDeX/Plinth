package app

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
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
// charged.
func TestRodinGameCharacterRunSucceedsThroughTheWholeStack(t *testing.T) {
	harness, _, _ := fakeStackHarness(t, func(origin string) map[string]string {
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
}
