package generationtool_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #292: a processing step's exports are written at the texture edge the session
// purpose allows, but the GLB the upstream returns keeps its own resolution (a
// Tripo segmentation comes back with 8192-pixel base colors whatever the purpose
// says). inspect_model therefore judges the texture budget on the deliverable the
// purpose sized, declared on the Offering's output (texture_size_parameter), and
// reports the native file's real edge beside it: not a pass, not a failure. A Task
// that declares no sized output keeps the file's own verdict.
//
// #305: a rig and a decimation export their GLB too, and that GLB is the model
// slot. It is a file the inspector can read, so it is judged by its own bytes (a
// conversion that did not write the size fails), and the upstream's own GLB is the
// original file beside it.

// glbWithTextureEdge is the GLB with the header of its embedded PNG rewritten to
// declare an edge x edge image, the way a native Tripo result carries 8K
// textures. The report reads the header, never the pixels.
func glbWithTextureEdge(t *testing.T, glb []byte, edge int) []byte {
	t.Helper()
	at := bytes.Index(glb, []byte("\x89PNG\r\n\x1a\n"))
	if at < 0 {
		t.Fatal("the fixture GLB embeds no PNG")
	}
	patched := append([]byte(nil), glb...)
	binary.BigEndian.PutUint32(patched[at+16:], uint32(edge))
	binary.BigEndian.PutUint32(patched[at+20:], uint32(edge))
	binary.BigEndian.PutUint32(patched[at+29:], crc32.ChecksumIEEE(patched[at+12:at+29]))
	return patched
}

// processedTask is a succeeded Tripo segmentation Task of the shipped Schema, an
// Offering whose native GLB is the model slot and whose exports are sized: its
// frozen Offering and input, the native GLB (declaring nativeEdge) and the FBX
// export.
func processedTask(t *testing.T, nativeEdge int, changes map[string]any) (generation.Task, []byte) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"source_task": "source", "authorize_extra_credits": true}
	for name, value := range changes {
		input[name] = value
	}
	encoded, _ := json.Marshal(input)
	offering, frozen, err := product.Normalize("mesh-segmentation", "tripo/geometry/segment", encoded)
	if err != nil {
		t.Fatal(err)
	}
	file, err := fakeupstream.LowPolyModelFile(72)
	if err != nil {
		t.Fatal(err)
	}
	glb := glbWithTextureEdge(t, file.Data, nativeEdge)
	task := reviewTask("task-rig", "owner-a", "session-a", glb, generation.TaskSucceeded)
	task.Capability = "mesh-segmentation"
	task.Offering = offering
	task.Input = frozen
	for index := range task.Assets {
		task.Assets[index].Required = task.Assets[index].Role == "model"
	}
	task.Assets = append(task.Assets, generation.Asset{
		ID: "task-rig-fbx", TaskID: "task-rig", Role: "converted_model", Variant: "fbx", Required: true,
		StorageKey: "key/fbx", MediaType: "model/fbx", Size: 1024,
	})
	return task, glb
}

func inspectProcessed(t *testing.T, task generation.Task, glb []byte, brief map[string]any) map[string]any {
	t.Helper()
	executor, _ := reviewExecutor(t, newReviewGateway(task), newReviewAssets(map[generation.AssetID][]byte{"task-rig-glb": glb}), nil)
	command := reviewCommand("inspect_model", `{"model":"task-rig"}`)
	command.Options.ProductionBrief = purposeBrief(t, brief)
	result, err := executor.InspectModel(context.Background(), command)
	if err != nil || result.Error != "" {
		t.Fatalf("inspect = %+v err=%v", result, err)
	}
	return reviewPayload(t, result)
}

// A game segmentation under the purpose's 2048 budget: the GLB is 8192, the FBX
// was exported at 2048. The budget item passes on the export, says so, and the full
// report still shows the GLB's real 8192 textures.
func TestInspectModelJudgesTheExportThePurposeSizedAndReportsTheNativeGLB(t *testing.T) {
	for name, test := range map[string]struct {
		brief   map[string]any
		changes map[string]any
		maximum float64
		export  float64
	}{
		"game character":            {map[string]any{"purpose": "game_character"}, map[string]any{"export_texture_size": "2k"}, 2048, 2048},
		"game character on a phone": {map[string]any{"purpose": "game_character", "target_platform": "mobile"}, map[string]any{"export_texture_size": "2k"}, 2048, 2048},
		"game character on a PC":    {map[string]any{"purpose": "game_character", "target_platform": "pc_console"}, map[string]any{"export_texture_size": "4k"}, 4096, 4096},
		"game prop":                 {map[string]any{"purpose": "game_prop"}, map[string]any{"export_texture_size": "2k"}, 2048, 2048},
	} {
		t.Run(name, func(t *testing.T) {
			task, glb := processedTask(t, 8192, test.changes)
			payload := inspectProcessed(t, task, glb, test.brief)
			item := reviewUsageItem(t, payload, "texture_size")
			if item["status"] != "pass" || item["actual"] != test.export || item["budget"].(map[string]any)["maximum"] != test.maximum {
				t.Fatalf("texture_size = %v, want a pass on the %v-pixel export", item, test.export)
			}
			note, _ := item["note"].(string)
			for _, want := range []string{"8192", strconv.Itoa(int(test.export)), "FBX", "GLB"} {
				if !strings.Contains(note, want) {
					t.Fatalf("note %q does not mention %q", note, want)
				}
			}
			// The native file is not hidden: the report is the GLB's own.
			slots := payload["report"].(map[string]any)["materials"].([]any)[0].(map[string]any)["slots"].([]any)
			sawNative := false
			for _, raw := range slots {
				if slot, _ := raw.(map[string]any); slot["present"] == true && slot["width"] == float64(8192) {
					sawNative = true
				}
			}
			if !sawNative {
				t.Fatalf("the report does not show the GLB's own 8192-pixel texture: %v", slots)
			}
			if item["fix_hint"] != nil {
				t.Fatalf("a passing item carries a fix: %v", item)
			}
		})
	}
}

// Under the display purpose the budget is 8192, the export is 8192 and so is the
// GLB: nothing differs, so the item is exactly what it was before, with no note.
func TestInspectModelKeepsTheDisplayPurposeVerdictUnchanged(t *testing.T) {
	task, glb := processedTask(t, 8192, map[string]any{"export_texture_size": "8k"})
	item := reviewUsageItem(t, inspectProcessed(t, task, glb, map[string]any{"purpose": "render"}), "texture_size")
	if item["status"] != "pass" || item["actual"] != float64(8192) || item["note"] != nil || item["fix_hint"] != nil {
		t.Fatalf("texture_size = %v, want the plain pass of an 8192-pixel texture", item)
	}
	// No brief at all: the default purpose, the same verdict.
	if item := reviewUsageItem(t, inspectProcessed(t, task, glb, nil), "texture_size"); item["status"] != "pass" || item["note"] != nil {
		t.Fatalf("texture_size without a brief = %v", item)
	}
}

// A size the Agent chose above the purpose's bound is the deliverable's failure,
// and the fix is to size it again, not to downsample the native file.
func TestInspectModelFailsAnExportOverTheBudgetAndSaysHowToFixIt(t *testing.T) {
	task, glb := processedTask(t, 8192, map[string]any{"export_texture_size": "4k"})
	item := reviewUsageItem(t, inspectProcessed(t, task, glb, map[string]any{"purpose": "game_character", "target_platform": "mobile"}), "texture_size")
	if item["status"] != "fail" || item["actual"] != float64(4096) {
		t.Fatalf("texture_size = %v, want a failure of the 4096-pixel export", item)
	}
	hint, _ := item["fix_hint"].(string)
	for _, want := range []string{"FBX", "4096", "2048", "导出贴图尺寸"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("fix hint %q does not mention %q", hint, want)
		}
	}
	if note, _ := item["note"].(string); !strings.Contains(note, "8192") {
		t.Fatalf("note %q does not say what the native GLB is", note)
	}
}

// Without the declaration (a Task frozen by an earlier revision) or without the
// delivered export, the inspected file decides, as it always did: its 8192-pixel
// textures fail a 2048 budget and the general fix applies.
func TestInspectModelJudgesTheFileWhenNoSizedExportExists(t *testing.T) {
	task, glb := processedTask(t, 8192, map[string]any{"export_texture_size": "2k"})

	undeclared := task
	undeclared.Offering.Outputs = append([]generation.AssetRequirement(nil), task.Offering.Outputs...)
	for index := range undeclared.Offering.Outputs {
		undeclared.Offering.Outputs[index].TextureSizeParameter = ""
	}
	missing := task
	missing.Assets = nil
	for _, asset := range task.Assets {
		if asset.Variant != "fbx" {
			missing.Assets = append(missing.Assets, asset)
		}
	}
	for name, candidate := range map[string]generation.Task{"an Offering that declares no sized output": undeclared, "a sized output that was not delivered": missing} {
		t.Run(name, func(t *testing.T) {
			item := reviewUsageItem(t, inspectProcessed(t, candidate, glb, map[string]any{"purpose": "game_character"}), "texture_size")
			if item["status"] != "fail" || item["actual"] != float64(8192) || item["note"] != nil {
				t.Fatalf("texture_size = %v, want the file's own failure", item)
			}
			if hint, _ := item["fix_hint"].(string); hint == "" || strings.Contains(hint, "导出贴图尺寸") {
				t.Fatalf("fix hint %q is not the general fix", hint)
			}
		})
	}
}

// The plan checkpoints judge the same Task by the same rule: the executor names
// the sized edge for the saved GLB the checkpoint reads, and nothing for a Task
// without a sized output or for the sized file itself.
func TestSizedTextureEdgeNamesTheDeliverableACheckpointJudges(t *testing.T) {
	task, glb := processedTask(t, 8192, map[string]any{"export_texture_size": "2k"})
	executor, _ := reviewExecutor(t, newReviewGateway(task), newReviewAssets(map[generation.AssetID][]byte{"task-rig-glb": glb}), nil)
	if edge := executor.SizedTextureEdge(task, "task-rig-glb"); edge != 2048 {
		t.Fatalf("sized edge for the GLB = %d, want 2048", edge)
	}
	if edge := executor.SizedTextureEdge(task, "task-rig-fbx"); edge != 0 {
		t.Fatalf("sized edge when the sized file itself is read = %d, want 0", edge)
	}
	plain := reviewTask("plain", "owner-a", "session-a", glb, generation.TaskSucceeded)
	if edge := executor.SizedTextureEdge(plain, "plain-glb"); edge != 0 {
		t.Fatalf("sized edge of a Task without a sized output = %d, want 0", edge)
	}
}

// rigTask is a succeeded Tripo rigging Task of the shipped Schema since #305: its
// model slot is the GLB its GLTF conversion wrote (declaring glbEdge), beside the
// FBX export and the original file the upstream returned (8192 pixels).
func rigTask(t *testing.T, glbEdge int, changes map[string]any) (generation.Task, map[generation.AssetID][]byte) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"source_task": "source", "authorize_extra_credits": true}
	for name, value := range changes {
		input[name] = value
	}
	encoded, _ := json.Marshal(input)
	offering, frozen, err := product.Normalize(generation.Rigging, "tripo/processing/rigging-v3", encoded)
	if err != nil {
		t.Fatal(err)
	}
	file, err := fakeupstream.LowPolyModelFile(72)
	if err != nil {
		t.Fatal(err)
	}
	converted := glbWithTextureEdge(t, file.Data, glbEdge)
	original := glbWithTextureEdge(t, file.Data, 8192)
	task := reviewTask("task-rig", "owner-a", "session-a", converted, generation.TaskSucceeded)
	task.Capability = generation.Rigging
	task.Offering = offering
	task.Input = frozen
	for index := range task.Assets {
		task.Assets[index].Required = task.Assets[index].Role == "model"
	}
	task.Assets = append(task.Assets,
		generation.Asset{ID: "task-rig-fbx", TaskID: "task-rig", Role: "model", Variant: "fbx", Required: true, StorageKey: "key/fbx", MediaType: "model/fbx", Size: 1024},
		generation.Asset{ID: "task-rig-original", TaskID: "task-rig", Role: "original_model", Variant: "glb", Required: true, StorageKey: "key/original", MediaType: "model/gltf-binary", Size: int64(len(original))},
	)
	return task, map[generation.AssetID][]byte{"task-rig-glb": converted, "task-rig-original": original}
}

func inspectRig(t *testing.T, task generation.Task, contents map[generation.AssetID][]byte, brief map[string]any) map[string]any {
	t.Helper()
	executor, _ := reviewExecutor(t, newReviewGateway(task), newReviewAssets(contents), nil)
	command := reviewCommand("inspect_model", `{"model":"task-rig"}`)
	command.Options.ProductionBrief = purposeBrief(t, brief)
	result, err := executor.InspectModel(context.Background(), command)
	if err != nil || result.Error != "" {
		t.Fatalf("inspect = %+v err=%v", result, err)
	}
	return reviewPayload(t, result)
}

// The model the person and the Agent inspect is the converted GLB, and it is
// measured as it is: a game rig exported at 2048 passes on its own 2048-pixel
// textures, with no export standing in for it, and the original file's 8192 pixels
// do not count against the budget. The result still says they are there.
func TestInspectModelJudgesTheConvertedGLBOfARigByItsOwnBytes(t *testing.T) {
	task, contents := rigTask(t, 2048, map[string]any{"export_texture_size": "2k"})
	payload := inspectRig(t, task, contents, map[string]any{"purpose": "game_character", "target_platform": "mobile"})
	item := reviewUsageItem(t, payload, "texture_size")
	if item["status"] != "pass" || item["actual"] != float64(2048) || item["budget"].(map[string]any)["maximum"] != float64(2048) {
		t.Fatalf("texture_size = %v, want a pass on the GLB's own 2048 pixels", item)
	}
	if item["fix_hint"] != nil {
		t.Fatalf("a pass carries a fix: %v", item)
	}
	slots := payload["report"].(map[string]any)["materials"].([]any)[0].(map[string]any)["slots"].([]any)
	for _, raw := range slots {
		if slot, _ := raw.(map[string]any); slot["present"] == true && slot["width"] != float64(2048) {
			t.Fatalf("the report is not the converted GLB's own: %v", slots)
		}
	}
}

// The upstream's own GLB a rig keeps as its original file is no model source for the
// tools, and the budget does not judge it; but the result does not let a converted
// 2048-pixel model stand for the whole delivery: the item names the original's
// textures, measured from the saved file, and only when they differ or could be
// measured at all.
func TestInspectModelNamesTheOriginalFileKeptBesideTheConvertedGLB(t *testing.T) {
	brief := map[string]any{"purpose": "game_character", "target_platform": "mobile"}
	t.Run("a larger original is named and not counted", func(t *testing.T) {
		task, contents := rigTask(t, 2048, map[string]any{"export_texture_size": "2k"})
		item := reviewUsageItem(t, inspectRig(t, task, contents, brief), "texture_size")
		if item["status"] != "pass" || item["actual"] != float64(2048) {
			t.Fatalf("texture_size = %v, want a pass on the converted GLB", item)
		}
		note, _ := item["note"].(string)
		for _, want := range []string{"8192", "原始 GLB", "不计入预算"} {
			if !strings.Contains(note, want) {
				t.Fatalf("note %q does not mention %q", note, want)
			}
		}
	})
	t.Run("an original the tools cannot select is not a model source", func(t *testing.T) {
		task, contents := rigTask(t, 2048, map[string]any{"export_texture_size": "2k"})
		executor, _ := reviewExecutor(t, newReviewGateway(task), newReviewAssets(contents), nil)
		result, err := executor.InspectModel(context.Background(), reviewCommand("inspect_model", `{"model":"task-rig","source_asset_id":"task-rig-original"}`))
		if err != nil || result.Error == "" {
			t.Fatalf("inspecting the original file: %+v err=%v, want a refusal", result, err)
		}
	})
	t.Run("an original that cannot be read is not described", func(t *testing.T) {
		task, contents := rigTask(t, 2048, map[string]any{"export_texture_size": "2k"})
		delete(contents, "task-rig-original")
		item := reviewUsageItem(t, inspectRig(t, task, contents, brief), "texture_size")
		if item["status"] != "pass" || item["note"] != nil {
			t.Fatalf("texture_size = %v, want a plain pass with no claim about a file that was not measured", item)
		}
	})
	t.Run("a Task with no original has nothing to name", func(t *testing.T) {
		task, contents := rigTask(t, 2048, map[string]any{"export_texture_size": "2k"})
		assets := task.Assets[:0:0]
		for _, asset := range task.Assets {
			if asset.Role != "original_model" {
				assets = append(assets, asset)
			}
		}
		task.Assets = assets
		item := reviewUsageItem(t, inspectRig(t, task, contents, brief), "texture_size")
		if item["status"] != "pass" || item["note"] != nil {
			t.Fatalf("texture_size = %v, want a plain pass", item)
		}
	})
	t.Run("an original of the same edge adds nothing", func(t *testing.T) {
		task, contents := rigTask(t, 8192, map[string]any{"export_texture_size": "8k"})
		item := reviewUsageItem(t, inspectRig(t, task, contents, map[string]any{"purpose": "render"}), "texture_size")
		if item["status"] != "pass" || item["actual"] != float64(8192) || item["note"] != nil {
			t.Fatalf("texture_size = %v, want the plain pass of an 8192-pixel texture", item)
		}
	})
}

// A conversion that did not write the size it was asked for is not hidden by the
// frozen size of its sibling export: the GLB is measured, fails the budget, and
// the fix is to size the step again.
func TestInspectModelFailsAConvertedGLBWhoseTexturesAreOverTheBudget(t *testing.T) {
	for name, test := range map[string]struct {
		edge    int
		changes map[string]any
	}{
		"the Agent asked for more than the purpose allows": {4096, map[string]any{"export_texture_size": "4k"}},
		"the conversion kept the original's resolution":    {8192, map[string]any{"export_texture_size": "2k"}},
	} {
		t.Run(name, func(t *testing.T) {
			task, contents := rigTask(t, test.edge, test.changes)
			item := reviewUsageItem(t, inspectRig(t, task, contents, map[string]any{"purpose": "game_character", "target_platform": "mobile"}), "texture_size")
			if item["status"] != "fail" || item["actual"] != float64(test.edge) {
				t.Fatalf("texture_size = %v, want the GLB's own failure of %d pixels", item, test.edge)
			}
			hint, _ := item["fix_hint"].(string)
			for _, want := range []string{"GLB", "FBX", strconv.Itoa(test.edge), "2048", "导出贴图尺寸"} {
				if !strings.Contains(hint, want) {
					t.Fatalf("fix hint %q does not mention %q", hint, want)
				}
			}
		})
	}
}

// The plan checkpoint reads the same GLB the same way: nothing stands in for the
// sized file itself, while the original file, which no parameter sizes, is judged by
// the exports the purpose sized and the sized edge names them.
func TestSizedTextureEdgeOfARigLeavesItsSizedGLBToItsOwnBytes(t *testing.T) {
	task, contents := rigTask(t, 2048, map[string]any{"export_texture_size": "2k"})
	executor, _ := reviewExecutor(t, newReviewGateway(task), newReviewAssets(contents), nil)
	for asset, want := range map[generation.AssetID]int{"task-rig-glb": 0, "task-rig-fbx": 0, "task-rig-original": 2048} {
		if got := executor.SizedTextureEdge(task, asset); got != want {
			t.Errorf("sized edge for %s = %d, want %d", asset, got, want)
		}
	}
	// A Task frozen before the GLB was exported keeps the earlier rule: its GLB is
	// the upstream's own and the FBX export stands in for it.
	old := task
	old.Offering.Outputs = append([]generation.AssetRequirement(nil), task.Offering.Outputs...)
	for index := range old.Offering.Outputs {
		if old.Offering.Outputs[index].Role == "model" && old.Offering.Outputs[index].Variant == "glb" {
			old.Offering.Outputs[index].TextureSizeParameter = ""
		}
	}
	if got := executor.SizedTextureEdge(old, "task-rig-glb"); got != 2048 {
		t.Errorf("sized edge for the native GLB of an earlier revision = %d, want 2048", got)
	}
}
