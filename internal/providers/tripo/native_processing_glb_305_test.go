package tripo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #305: a rig or a decimation delivers the GLB its GLTF conversion writes (PNG
// textures at the purpose's size, the origin at the bottom center) as the model,
// and keeps the GLB the upstream returned as the original file. The tests use the
// shipped Product Schema and the real adapter against the Tripo v3 fake, which
// writes the textures, the skin and the origin a conversion asks for and reports
// the move the way the paid run of 2026-11-16 showed.

func claimsBySlot(t *testing.T, claims []generation.ArtifactClaim) map[string]generation.ArtifactClaim {
	t.Helper()
	bySlot := map[string]generation.ArtifactClaim{}
	for _, claim := range claims {
		slot := claim.Role + ":" + claim.Variant
		if _, duplicate := bySlot[slot]; duplicate {
			t.Fatalf("the slot %s is claimed twice", slot)
		}
		bySlot[slot] = claim
	}
	return bySlot
}

func finalState(t *testing.T, run exportRun) nativeProcessingState {
	t.Helper()
	var state nativeProcessingState
	if err := json.Unmarshal(run.observed.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

// The rig's model slot is the conversion of its GLB; the GLB itself is the
// original file, bound to the bytes the conversions were made from; the FBX is
// the second export.
func TestRiggingDeliversItsConvertedGLBAsTheModelAndKeepsTheNativeOneAsTheOriginal(t *testing.T) {
	for _, id := range []string{"tripo/processing/rigging-v3", "tripo/processing/rigging"} {
		t.Run(id, func(t *testing.T) {
			run := driveExports(t, shippedRigging(t, id, map[string]any{"export_texture_size": "2k"}), "animations/rig")
			claims := claimsBySlot(t, run.observed.Artifacts)
			if len(claims) != 4 {
				t.Fatalf("claims = %+v, want the original, the model, the FBX and the thumbnail", run.observed.Artifacts)
			}
			state := finalState(t, run)

			original := claims["original_model:glb"]
			if original.Locator != run.model || original.MediaType != "model/gltf-binary" || !strings.HasSuffix(original.ID, ":model:model_url") {
				t.Fatalf("original = %+v, want the GLB the exports were made from (%s)", original, run.model)
			}
			if binding, err := decodeNativeSourceMaterializationBinding(original.Metadata, 150000000); err != nil ||
				binding.SHA256 != state.ConversionSource.SHA256 || binding.Size != state.ConversionSource.Size {
				t.Fatalf("the original is not bound to the bytes the exports were made from: %s (%v)", original.Metadata, err)
			}

			model := claims["model:glb"]
			if model.ID != "convert:GLTF" || model.MediaType != "model/gltf-binary" || model.Locator == run.model || len(model.Metadata) != 0 {
				t.Fatalf("model = %+v, want the GLTF conversion", model)
			}
			if want := state.Conversions["GLTF"].Locator; model.Locator != want || !strings.HasSuffix(strings.Split(model.Locator, "?")[0], "/model.glb") {
				t.Fatalf("the model is at %s, the conversion delivered %s", model.Locator, want)
			}
			if fbx := claims["model:fbx"]; fbx.ID != "convert:FBX" || fbx.MediaType != "model/fbx" || fbx.Locator != state.Conversions["FBX"].Locator {
				t.Fatalf("fbx = %+v", fbx)
			}
			if thumbnail := claims["thumbnail:rendered_image"]; thumbnail.Locator == "" {
				t.Fatalf("thumbnail = %+v", thumbnail)
			}
			// The two conversions were paid and recorded like any private export.
			for _, format := range []string{"GLTF", "FBX"} {
				if item := state.Conversions[format]; item.Status != conversionItemDone || string(item.CreditsConsumed) != "10" {
					t.Fatalf("%s export = %+v, want one finished advanced conversion", format, item)
				}
			}
		})
	}
}

// The decimation does the same, with its five other exports around the GLB.
func TestDecimationDeliversItsConvertedGLBAsTheModelAndKeepsTheNativeOneAsTheOriginal(t *testing.T) {
	r := geometryCatalogFixture(t, "decimate-triangle")
	var values map[string]json.RawMessage
	_ = json.Unmarshal(r.Input, &values)
	values["export_texture_size"] = json.RawMessage(`"2k"`)
	r.Input, _ = json.Marshal(values)
	run := driveExports(t, r, "mesh/decimate")
	claims := claimsBySlot(t, run.observed.Artifacts)
	state := finalState(t, run)
	for slot, want := range map[string]string{
		"model:glb": "convert:GLTF", "converted_model:fbx": "convert:FBX", "converted_model:obj": "convert:OBJ",
		"converted_model:stl": "convert:STL", "converted_model:usdz": "convert:USDZ", "converted_model:3mf": "convert:3MF",
	} {
		if claims[slot].ID != want || claims[slot].Locator == run.model {
			t.Errorf("%s = %+v, want the %s export", slot, claims[slot], want)
		}
	}
	if original := claims["original_model:glb"]; original.Locator != run.model || len(original.Metadata) == 0 {
		t.Errorf("original = %+v, want the bound GLB the exports were made from", original)
	}
	if len(claims) != 8 {
		t.Errorf("claims = %d, want the original, the six exports and the thumbnail", len(claims))
	}
	// The GLB export is the one with the pivot, and the decimation keeps animation off.
	if gltf := run.converts["GLTF"]; gltf["pivot_to_center_bottom"] != true || gltf["with_animation"] != false || gltf["texture_size"] != float64(2048) {
		t.Errorf("GLTF export = %v", gltf)
	}
	for format := range run.converts {
		if _, pivot := run.converts[format]["pivot_to_center_bottom"]; pivot != (format == "GLTF") {
			t.Errorf("the %s export carries the pivot = %v", format, pivot)
		}
	}
	if state.ProgressUnits != nativeConversionProgressUnits(6) {
		t.Errorf("progress units = %d, want %d", state.ProgressUnits, nativeConversionProgressUnits(6))
	}
}

// A GLB export that fails fails the step: the native GLB is never delivered as the
// model in its place, and the FBX export that follows is never started.
func TestAFailedGLBExportFailsTheStepAndNeverDeliversTheNativeGLBAsTheModel(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", map[string]any{"export_texture_size": "2k"})
	r.Offering.AssetHosts = []string{exportAssetHost}
	stack := newExportStack(t, nil)
	stack.recorder.scenario = func(path, body string) string {
		if strings.HasSuffix(path, "/models/convert") && strings.Contains(body, `"format":"GLTF"`) {
			return "task-failed"
		}
		return ""
	}
	source := fakeTask(t, stack.fixture, "generation/text-to-model", map[string]any{"model": "v3.1-20260211", "prompt": "a robot"})
	dependency := r.TaskDependencies["source_task"]
	dependency.UpstreamJob.RemoteID = source
	r.TaskDependencies = map[string]generation.TaskDependency{"source_task": dependency}

	ctx := generation.WithDispatchCheckpointer(context.Background(), &nativeDeliveryPermit{})
	accepted, err := stack.adapter().Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	q := rigObserve(r, accepted)
	var observed generation.Observation
	for i := 0; i < 60 && observed.Phase != generation.UpstreamFailed && observed.Phase != generation.UpstreamCompleted; i++ {
		if observed, err = stack.adapter().Observe(ctx, q); err != nil {
			t.Fatal(err)
		}
		q.Opaque = observed.Opaque
	}
	if observed.Phase != generation.UpstreamFailed || observed.Failure == nil || observed.Failure.Retryable {
		t.Fatalf("observation = %+v, want a failed step", observed)
	}
	if len(observed.Artifacts) != 0 {
		t.Fatalf("a failed export left claims behind: %+v", observed.Artifacts)
	}
	if exports := stack.recorder.posts("models/convert"); len(exports) != 1 || !strings.Contains(exports[0], `"format":"GLTF"`) {
		t.Fatalf("exports = %v, want the GLB export alone", exports)
	}
}

// Rolled back to the shape an earlier revision froze: the Offering a Task was
// accepted with is what runs it, so a rig frozen before the GLB was exported (#305)
// or before the size was a parameter (#292) still delivers its native GLB as the
// model and its one FBX export.
func rolledBackRigging(t *testing.T, r generation.StartRequest, revision string, sizeIsAConstant bool) generation.StartRequest {
	t.Helper()
	legacy := r.Offering
	legacy.Revision = revision
	legacy.Parameters = nil
	for _, parameter := range r.Offering.Parameters {
		switch parameter.Name {
		case "export_pivot_bottom_center":
			continue
		case "export_texture_size":
			if sizeIsAConstant {
				continue
			}
		case "output_formats":
			parameter.Default = json.RawMessage(`["FBX"]`)
			parameter.Options = []generation.ParameterOption{{Value: "FBX", Label: "FBX"}}
			parameter.MinItems, parameter.MaxItems = 1, 1
		}
		legacy.Parameters = append(legacy.Parameters, parameter)
	}
	legacy.Outputs = nil
	for _, output := range r.Offering.Outputs {
		if output.Role == "original_model" {
			continue
		}
		if output.Role == "model" && output.Variant == "glb" {
			output.TextureSizeParameter = ""
		}
		legacy.Outputs = append(legacy.Outputs, output)
	}
	policy := *r.Offering.CostPolicy
	policy.MaxAuthorizedExtraCredits = 10
	legacy.CostPolicy = &policy

	var options map[string]any
	if err := json.Unmarshal(r.Offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	aliases := options["response_aliases"].([]any)
	aliases[0].(map[string]any)["role"] = "model"
	native := options["native_conversion"].(map[string]any)
	native["formats"] = native["formats"].([]any)[1:] // the FBX alone
	native["bindings"] = native["bindings"].([]any)[:1]
	if sizeIsAConstant {
		delete(native, "bindings")
		native["parameters"].(map[string]any)["texture_size"] = 8192
	}
	legacy.ProviderOptions, _ = json.Marshal(options)

	var values map[string]json.RawMessage
	if err := json.Unmarshal(r.Input, &values); err != nil {
		t.Fatal(err)
	}
	delete(values, "export_pivot_bottom_center")
	values["output_formats"] = json.RawMessage(`["FBX"]`)
	if sizeIsAConstant {
		delete(values, "export_texture_size")
	}
	r.Offering = legacy
	r.Input, _ = json.Marshal(values)
	if err := ValidateOffering(legacy); err != nil {
		t.Fatalf("the %s snapshot's declaration is refused: %v", revision, err)
	}
	return r
}

func TestTaskFrozenBeforeTheGLBWasExportedKeepsTheNativeGLBAsItsModel(t *testing.T) {
	r := rolledBackRigging(t, shippedRigging(t, "tripo/processing/rigging-v3", map[string]any{"export_texture_size": "2k"}), "2026-11-12.292.1", false)
	run := driveExports(t, r, "animations/rig")
	if len(run.converts) != 1 || run.sent["FBX"] == "" {
		t.Fatalf("exports = %v, want the FBX alone", run.converts)
	}
	if _, pivot := run.converts["FBX"]["pivot_to_center_bottom"]; pivot || run.converts["FBX"]["texture_size"] != float64(2048) {
		t.Fatalf("FBX export = %v, want the 292 request: 2048 pixels and no pivot", run.converts["FBX"])
	}
	claims := claimsBySlot(t, run.observed.Artifacts)
	if _, original := claims["original_model:glb"]; original {
		t.Fatalf("a Task frozen without the original slot delivers one: %+v", run.observed.Artifacts)
	}
	native := claims["model:glb"]
	if native.Locator != run.model || native.ID == "convert:GLTF" || len(native.Metadata) == 0 {
		t.Fatalf("model = %+v, want the bound native GLB the Task was accepted with", native)
	}
	if len(claims) != 3 {
		t.Fatalf("claims = %+v", run.observed.Artifacts)
	}
}

// A Task frozen before the size was a parameter keeps the Offering it was
// accepted with: the export size is a constant of that snapshot, its input has
// no export_texture_size, and the declaration, the requests and the checkpoint
// are the ones it always had. The snapshot is the shipped rigging taken back to
// the shape of revision 2026-11-12.277.1.
func TestTaskFrozenBeforeTheSizeWasAParameterKeepsItsConstantExport(t *testing.T) {
	r := rolledBackRigging(t, shippedRigging(t, "tripo/processing/rigging-v3", map[string]any{"export_texture_size": "2k"}), "2026-11-12.277.1", true)
	run := driveExports(t, r, "animations/rig")
	want := fmt.Sprintf(`{"bake":true,"fbx_preset":"blender","format":"FBX","input":%q,"texture_format":"PNG","texture_size":8192,"with_animation":true}`, run.model)
	if len(run.converts) != 1 || run.sent["FBX"] != want {
		t.Fatalf("the snapshot's exports = %v, want the one constant FBX %s", run.sent, want)
	}
	if claims := claimsBySlot(t, run.observed.Artifacts); claims["model:glb"].Locator != run.model {
		t.Fatalf("model = %+v", claims["model:glb"])
	}
}

// Materialization checks the identity of the original file only: it is the bytes
// the exports were made from. The converted GLB is an ordinary download, so a
// conversion that returns other bytes than the source is not mistaken for tampering.
func TestMaterializationBindsTheOriginalSlotNotTheConvertedGLB(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", nil)
	original := generation.ArtifactClaim{ID: "attempt:model:model_url", Role: "original_model", Variant: "glb", MediaType: "model/gltf-binary", Locator: "https://assets.example/original.glb"}
	converted := generation.ArtifactClaim{ID: "convert:GLTF", Role: "model", Variant: "glb", MediaType: "model/gltf-binary", Locator: "https://assets.example/converted.glb"}
	if c, err := nativeSourceMaterializationOptions(r.Offering, original); err != nil || c == nil {
		t.Fatalf("the original is not bound: %v %v", c, err)
	}
	if c, err := nativeSourceMaterializationOptions(r.Offering, converted); err != nil || c != nil {
		t.Fatalf("the converted GLB is bound as a source: %v %v", c, err)
	}

	// The binding is enforced on the original's bytes: the same URL with other bytes
	// is refused before the store commits it.
	bytesAtFirst, bytesNow := nativeBoundGLB("A"), nativeBoundGLB("B")
	current := bytesAtFirst
	r.Offering.AssetHosts = []string{"assets.example"}
	a := &Adapter{nativeSourceClient: nativeDeliveryTestSourceClient(func() []byte { return current }, nil)}
	original.Metadata = nativeBoundMetadata(bytesAtFirst)
	reader, _, err := a.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: r.Offering, Claim: original})
	if err != nil {
		t.Fatal(err)
	}
	if asset, putErr, _ := nativeBoundStore(t, reader); putErr != nil || asset.SHA256 != uploadedDigest(bytesAtFirst) {
		t.Fatalf("the original's own bytes were refused: %v", putErr)
	}
	reader.Close()
	current = bytesNow
	reader, _, err = a.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: r.Offering, Claim: original})
	if err != nil {
		assertNativeBoundIntegrity(t, err)
		return
	}
	_, putErr, _ := nativeBoundStore(t, reader)
	reader.Close()
	assertNativeBoundIntegrity(t, putErr)
}

// The declaration refuses what would leave the model slot without its GLB or the
// original without its identity: a declaration is checked before a paid job exists.
func TestGLBExportDeclarationsAreRefusedWhenTheySplitTheContract(t *testing.T) {
	base := shippedOffering(t, "tripo/processing/rigging-v3")
	if err := ValidateOffering(base); err != nil {
		t.Fatalf("the shipped declaration is refused: %v", err)
	}
	mutate := func(change func(options map[string]any, offering *generation.Offering)) generation.Offering {
		var options map[string]any
		if err := json.Unmarshal(base.ProviderOptions, &options); err != nil {
			t.Fatal(err)
		}
		broken := base
		broken.Outputs = append([]generation.AssetRequirement(nil), base.Outputs...)
		broken.Parameters = append([]generation.Parameter(nil), base.Parameters...)
		change(options, &broken)
		broken.ProviderOptions, _ = json.Marshal(options)
		return broken
	}
	slot := func(o *generation.Offering, role, variant string) *generation.AssetRequirement {
		for index := range o.Outputs {
			if o.Outputs[index].Role == role && o.Outputs[index].Variant == variant {
				return &o.Outputs[index]
			}
		}
		t.Fatalf("no %s:%s slot", role, variant)
		return nil
	}
	for name, broken := range map[string]generation.Offering{
		"the GLB export is aimed at the original's slot": mutate(func(options map[string]any, _ *generation.Offering) {
			native := options["native_conversion"].(map[string]any)
			native["formats"].([]any)[0].(map[string]any)["role"] = "original_model"
		}),
		"the original is no required slot": mutate(func(_ map[string]any, o *generation.Offering) {
			zero := 0
			slot(o, "original_model", "glb").Minimum = &zero
		}),
		"the original may be anything": mutate(func(_ map[string]any, o *generation.Offering) {
			slot(o, "original_model", "glb").MediaTypes = []string{"model/gltf-binary", "model/gltf+json"}
		}),
		"no required model slot is left": mutate(func(options map[string]any, o *generation.Offering) {
			slot(o, "model", "glb").Role = "converted_model"
			native := options["native_conversion"].(map[string]any)
			native["formats"].([]any)[0].(map[string]any)["role"] = "converted_model"
		}),
		"the model slot is no GLB": mutate(func(options map[string]any, o *generation.Offering) {
			slot(o, "model", "glb").MediaTypes = []string{"model/fbx"}
		}),
		"the pivot is bound to a format that is not exported": mutate(func(options map[string]any, _ *generation.Offering) {
			native := options["native_conversion"].(map[string]any)
			native["bindings"].([]any)[1].(map[string]any)["formats"] = []any{"USDZ"}
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateOffering(broken); err == nil {
				t.Fatal("the declaration was accepted")
			}
		})
	}
}

// The pivot is a public, fixed parameter of the Offering that the exports read:
// the GLB export carries it as pivot_to_center_bottom, the processing request and
// the other exports never do.
func TestTheGLBExportPivotIsAFixedBoundParameter(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", nil)
	var pivot *generation.Parameter
	for index := range r.Offering.Parameters {
		if r.Offering.Parameters[index].Name == "export_pivot_bottom_center" {
			pivot = &r.Offering.Parameters[index]
		}
	}
	if pivot == nil || pivot.Type != "boolean" || !pivot.Fixed || string(pivot.Default) != "true" {
		t.Fatalf("pivot parameter = %+v", pivot)
	}
	_, _, body, options, err := nativeProcessingInput(r.Offering, r.Input)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := body["export_pivot_bottom_center"]; leaked || body["pivot_to_center_bottom"] != nil {
		t.Fatalf("the pivot reached the processing request: %v", body)
	}
	for _, format := range options.NativeConversion.Formats {
		export, err := nativeConversionBody(r.Offering, options.NativeConversion, r.Input, "https://assets.example/x.glb", format.Format)
		if err != nil {
			t.Fatal(err)
		}
		_, has := export["pivot_to_center_bottom"]
		if has != (format.Format == "GLTF") || has && export["pivot_to_center_bottom"] != true {
			t.Fatalf("%s export = %v", format.Format, export)
		}
	}
	// A frozen input that holds a value other than the fixed one never reaches the wire.
	var values map[string]json.RawMessage
	_ = json.Unmarshal(r.Input, &values)
	values["export_pivot_bottom_center"] = json.RawMessage(`false`)
	changed, _ := json.Marshal(values)
	if _, _, _, _, err := nativeProcessingInput(r.Offering, changed); err == nil {
		t.Fatal("a pivot the Offering fixes was changed")
	}
}

// The fake's answer to the GLTF export is the model the paid run described: the
// same skin, textures of the requested size, the feet on the floor.
func TestTheFakeWritesWhatTheGLBExportAsksFor(t *testing.T) {
	run := driveExports(t, shippedRigging(t, "tripo/processing/rigging-v3", map[string]any{"export_texture_size": "2k"}), "animations/rig")
	state := finalState(t, run)
	_, _, polled := run.fake.Do(http.MethodGet, "/tripo/v3/tasks/"+state.Conversions["GLTF"].RemoteID, "test-key", nil)
	output, _ := polled["data"].(map[string]any)["output"].(map[string]any)
	if output["normalize_scale"] == nil || !reflect.DeepEqual(len(output["normalize_translation"].([]any)), 3) {
		t.Fatalf("the finished GLB export reports no move: %v", output)
	}
	if translation := output["normalize_translation"].([]any); translation[1].(float64) != 0 {
		t.Fatalf("the feet were moved: normalize_translation = %v", translation)
	}
}
