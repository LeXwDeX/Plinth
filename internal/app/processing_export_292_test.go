package app

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// noReferences resolves no pictures: the executor below only reads Schema tables.
type noReferences struct{}

func (noReferences) Resolve(context.Context, references.ReferenceScope, []string) ([]generation.AssetID, error) {
	return nil, nil
}

// #292: the files a Tripo processing step exports follow the session purpose. The
// whole stack runs against the fake upstream (the real Catalog, the kernel, the
// Tripo adapter with its private exports, the asset store with its delivery
// checks); the step's input is what the shared resolver (chat.PurposeParameters,
// the one the Agent's tools and the direct task API use) gives it for the brief,
// so the assertion is on what reached the upstream: under a game purpose the
// export requests of a rigging and of a decimation carry PNG textures of 2048
// pixels, and without a purpose the 8192 every export has always carried.

func briefOf(t *testing.T, fields map[string]string) chat.ProductionBrief {
	t.Helper()
	brief := chat.ProductionBrief{}
	for name, value := range fields {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		brief[name] = encoded
	}
	return brief
}

func TestGamePurposeRiggingAndDecimationExportPNGAtThePurposeTextureSize(t *testing.T) {
	for _, tc := range []struct {
		name  string
		brief map[string]string
		size  float64
	}{
		{"game character on a phone", map[string]string{"purpose": "game_character", "target_platform": "mobile"}, 2048},
		{"game character on a PC", map[string]string{"purpose": "game_character", "target_platform": "pc_console"}, 4096},
		{"display", map[string]string{"purpose": "render"}, 8192},
		{"no purpose", nil, 8192},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness, product, log := lowPolyHarness(t)
			chatSchema := product.Application().Chat
			resolver := chat.PurposeParameters{Model3D: chatSchema.Model3DUsage, Image2D: chatSchema.Image2DUsage}

			// The model both steps work on: a Tripo v3.1 generation with the GLB as
			// its only delivered format.
			source := submitStep(t, harness, generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d",
				`{"prompt":"a game character in an A pose","authorize_extra_credits":true,"face_limit":400000,"output_formats":["GLTF"]}`,
				"export-source-"+tc.name, map[string]string{"face_limit": "the source keeps its detail", "output_formats": "only the GLB feeds the steps"})
			sourceTask := harness.runUntilTerminal(t, "owner-acme", source.TaskID)
			if sourceTask.State != generation.TaskSucceeded {
				t.Fatalf("source state = %s, failure = %#v", sourceTask.State, sourceTask.Failure)
			}

			// The executor inspect_model and the plan checkpoints judge with.
			executor, err := generationtool.New(generationtool.Config{
				Gateway: harness.module, References: noReferences{}, Chat: chatSchema,
			})
			if err != nil {
				t.Fatal(err)
			}

			for _, step := range []struct {
				capability generation.Capability
				offering   generation.OfferingID
				formats    []string
				animation  bool
			}{
				{generation.Rigging, "tripo/processing/rigging-v3", []string{"FBX"}, true},
				{generation.Remesh, "tripo/geometry/decimate-triangle", []string{"3MF", "FBX", "OBJ", "STL", "USDZ"}, false},
			} {
				offering, ok := product.Get(step.offering)
				if !ok {
					t.Fatalf("missing %s", step.offering)
				}
				// What the purpose gives the step: the request leaves the export size
				// unset, the resolver fills it as it does for every path.
				request := `{"source_task":"` + string(sourceTask.ID) + `","authorize_extra_credits":true}`
				applied, err := resolver.ApplyToDirectInput(briefOf(t, tc.brief), offering, json.RawMessage(request), nil)
				if err != nil {
					t.Fatal(err)
				}
				before := len(log.bodies("/models/convert"))
				accepted := submitStep(t, harness, step.capability, step.offering, string(applied.Input),
					"export-"+string(step.capability)+"-"+tc.name, applied.Reasons)
				task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
				if task.State != generation.TaskSucceeded {
					t.Fatalf("%s state = %s, failure = %#v", step.offering, task.State, task.Failure)
				}
				// Every export of the step, and only those, with the purpose's size.
				converts := log.bodies("/models/convert")[before:]
				sent := []string{}
				for _, body := range converts {
					format, _ := body["format"].(string)
					sent = append(sent, format)
					if body["texture_format"] != "PNG" || body["texture_size"] != tc.size || body["bake"] != true ||
						body["with_animation"] != step.animation || body["fbx_preset"] != "blender" {
						t.Errorf("%s %s export = %v, want PNG textures of %v pixels", step.offering, format, body, tc.size)
					}
				}
				slices.Sort(sent)
				if !slices.Equal(sent, step.formats) {
					t.Errorf("%s exports = %v, want %v", step.offering, sent, step.formats)
				}
				// The step still delivers every required file: the exports were
				// downloaded, checked and saved like the 8K ones.
				delivered := map[string]bool{}
				for _, asset := range task.Assets {
					delivered[asset.Role+":"+asset.Variant] = true
				}
				for _, format := range step.formats {
					role := "converted_model"
					if step.capability == generation.Rigging {
						role = "model"
					}
					if variant := map[string]string{"FBX": "fbx", "OBJ": "obj", "STL": "stl", "USDZ": "usdz", "3MF": "3mf"}[format]; !delivered[role+":"+variant] {
						t.Errorf("%s did not deliver the %s export: %v", step.offering, format, delivered)
					}
				}
				// The Task the kernel froze carries the declaration and the size: the
				// resolver that inspect_model and the plan checkpoints share names the
				// export edge the purpose gave, for the GLB they read, whatever
				// resolution the upstream wrote that file at.
				var glb generation.AssetID
				for _, asset := range task.Assets {
					if asset.Role == "model" && asset.Variant == "glb" {
						glb = asset.ID
					}
				}
				if glb == "" {
					t.Fatalf("%s delivered no GLB: %v", step.offering, delivered)
				}
				if edge := executor.SizedTextureEdge(task, glb); float64(edge) != tc.size {
					t.Errorf("%s: the sized export edge = %d, want %v", step.offering, edge, tc.size)
				}
			}
		})
	}
}
