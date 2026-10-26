package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #326: a Meshy Rigging result as dev-test stored task_8dd90530 (#325 note
// 99105 F1). Its first saved files are the basic-animation attachments (the
// first GLB is a 79,600-byte skeleton); the required rigged character comes
// last. The result is past Meshy's retention, so animation cannot refer to it.
func riggedSourceTasks(characterBytes int64) func(h *processingHarness) []generation.Task {
	return func(h *processingHarness) []generation.Task {
		old := time.Now().UTC().Add(-9 * 24 * time.Hour)
		model := modelTask("old-model", "owner-a", "session-a", h.meshy, "text-to-3d")
		model.CreatedAt = old
		model.Assets = append([]generation.Asset{{ID: "old-model-glb", TaskID: model.ID, Role: "model", Variant: "glb",
			StorageKey: "key/model", MediaType: "model/gltf-binary", Size: 13_564_852, Required: true, Ordinal: 1}}, model.Assets...)
		rigging, _ := h.gateway.product.Get("meshy/processing/rigging")
		rig := generation.Task{ID: "old-rig", OwnerID: "owner-a", SessionID: "session-a", Capability: "rigging",
			State: generation.TaskSucceeded, Offering: rigging, CreatedAt: old.Add(time.Hour),
			Input: json.RawMessage(`{"source_asset":"old-model-glb"}`)}
		files := []struct {
			variant, media string
			size           int64
			required       bool
		}{
			{"basic_animations.running_armature_glb_url", "model/gltf-binary", 79_600, false},
			{"basic_animations.running_glb_url", "model/gltf-binary", 3_000_000, false},
			{"basic_animations.running_fbx_url", "application/octet-stream", 3_000_000, false},
			{"basic_animations.walking_armature_glb_url", "model/gltf-binary", 79_600, false},
			{"basic_animations.walking_glb_url", "model/gltf-binary", 3_000_000, false},
			{"basic_animations.walking_fbx_url", "application/octet-stream", 3_000_000, false},
			{"rigged_character_fbx_url", "application/octet-stream", characterBytes / 2, true},
			{"rigged_character_glb_url", "model/gltf-binary", characterBytes, true},
		}
		for index, file := range files {
			rig.Assets = append(rig.Assets, generation.Asset{ID: generation.AssetID("rig-file-" + string(rune('1'+index))), TaskID: rig.ID,
				Role: "model", Variant: file.variant, MediaType: file.media, Size: file.size, Required: file.required,
				Ordinal: index + 1, StorageKey: "key/rig/" + file.variant, SHA256: strings.Repeat("a", 64)})
		}
		return []generation.Task{model, rig}
	}
}

// The chat recorded the handles the way it does today: @model2 names the file
// the result listed first, the skeleton.
func riggedSourceHistory() []chat.Message {
	return []chat.Message{{Role: "assistant", Attachments: []chat.Attachment{
		{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "old-model", AssetID: "old-model-glb", ImageSlot: 1},
		{Handle: "@model2", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "old-rig", AssetID: "rig-file-1", ImageSlot: 2},
	}}}
}

const riggedCharacterBytes = 105_271_888

// internalNames are words of the providers' result fields the Agent must never
// see or repeat (#326: the reply quoted basic_animations.running_glb_url).
var internalNames = []string{"basic_animations", "armature", "rigged_character", "_glb_url", "_fbx_url"}

func expectNoInternalNames(t *testing.T, text string) {
	t.Helper()
	for _, name := range internalNames {
		if strings.Contains(text, name) {
			t.Fatalf("tool result names the internal field %q: %s", name, text)
		}
	}
}

// The rigged character (105 MB) is the model; Meshy Rigging takes at most
// 73.4 MB (70 MiB, the #329 data URI bound; decimal MB since #327), so rigging it again is not a way to animate it. The Agent says so,
// as the 加工 panel does, and offers what works: rigging the model it was made
// from, then animating.
func TestAnimationOfAnExpiredRiggedResultSaysWhyRiggingAgainCannotHelp(t *testing.T) {
	h := newProcessingHarness(t, "auto", riggedSourceTasks(riggedCharacterBytes))
	result, _ := h.runAs("owner-a", "session-a", "process_model",
		`{"step":"animation","source_task_id":"@model2","action_id":14}`, riggedSourceHistory())
	h.expectRejected(result, "73.4 MB")
	message, _ := h.content(result)["error"].(string)
	for _, want := range []string{"105.28 MB", "rigging", "@model1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("refusal %q does not contain %q", message, want)
		}
	}
	expectNoInternalNames(t, result.AgentContent)
}

// A rigged character that Meshy Rigging takes can be rigged again first: the
// panel brings that step in, and the Agent says the same.
func TestAnimationOfAnExpiredRiggedResultOffersRiggingAgainWhenItFits(t *testing.T) {
	h := newProcessingHarness(t, "auto", riggedSourceTasks(30_000_000))
	result, _ := h.runAs("owner-a", "session-a", "process_model",
		`{"step":"animation","source_task_id":"@model2","action_id":14}`, riggedSourceHistory())
	h.expectRejected(result, "plan_model_processing")
	message, _ := h.content(result)["error"].(string)
	if !strings.Contains(message, "Meshy Rigging") || strings.Contains(message, "73.4 MB") {
		t.Fatalf("refusal %q; want rigging again offered", message)
	}
	expectNoInternalNames(t, result.AgentContent)
}

// list_session_models describes the same model: its files by public format,
// the main one marked, and the steps the main model can feed. Rigging is not
// one of them for the 105 MB character, though the skeleton would fit.
func TestSessionModelListNamesFormatsAndStepsOfTheMainModel(t *testing.T) {
	h := newProcessingHarness(t, "auto", riggedSourceTasks(riggedCharacterBytes))
	listed, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	expectNoInternalNames(t, string(listed))
	var decoded struct {
		Models []struct {
			TaskID      string   `json:"task_id"`
			Formats     []string `json:"formats"`
			ModelAssets []struct {
				AssetID string `json:"asset_id"`
				Format  string `json:"format"`
				Main    bool   `json:"main"`
			} `json:"model_assets"`
			NextSteps []struct {
				Step string `json:"step"`
			} `json:"next_steps"`
		} `json:"models"`
	}
	if err := json.Unmarshal(listed, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, model := range decoded.Models {
		if model.TaskID != "old-rig" {
			continue
		}
		if strings.Join(model.Formats, ",") != "glb,fbx" {
			t.Fatalf("formats = %v; want the public formats of the result", model.Formats)
		}
		main := ""
		for _, asset := range model.ModelAssets {
			if asset.Main {
				main = asset.AssetID
			}
		}
		if main != "rig-file-8" {
			t.Fatalf("main file = %q; want the rigged character", main)
		}
		for _, step := range model.NextSteps {
			if step.Step == "rigging" || step.Step == "animation" {
				t.Fatalf("next steps = %+v; the 105 MB character cannot be rigged and the original task is gone", model.NextSteps)
			}
		}
		return
	}
	t.Fatalf("old-rig missing: %s", listed)
}

// A rigging step on the handle binds the rigged character, never the skeleton
// the handle recorded: the same file the stage and the panel use.
func TestRiggedResultHandleBindsTheRiggedCharacter(t *testing.T) {
	h := newProcessingHarness(t, "auto", riggedSourceTasks(30_000_000))
	result, _ := h.runAs("owner-a", "session-a", "process_model",
		`{"step":"rigging","source_task_id":"@model2"}`, riggedSourceHistory())
	if result.NoSubmission || len(h.gateway.frozen) != 1 {
		t.Fatalf("rigging refused: %s", result.AgentContent)
	}
	if source := h.frozenInput(0)["source_asset"]; source != "rig-file-8" {
		t.Fatalf("frozen source = %v; want the rigged character", source)
	}
}
