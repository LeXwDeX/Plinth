package modeldelivery

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #502: a Tripo result keeps the model and converted.gltf over the same GLB
// bytes, both required and both at ordinal 1 (each role counts its own
// ordinals), while production asset IDs are random. The GLB row and the
// conversion source must be the model whatever the IDs sort as: a converted
// copy cannot be bound as a step's source, so picking it by ID order made the
// glTF row unavailable (source_incompatible) for about half of the tasks.

// tripoTiedTask stores a finished Tripo result whose model and converted.gltf
// tie on Required and Ordinal and carry the given asset IDs.
func (f *fixture) tripoTiedTask(id string, modelID, gltfID, fbxID generation.AssetID) generation.Task {
	task := f.addTask(id, "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary", body: sharedGLB},
		assetSpec{role: "converted_model", variant: "converted.gltf", mediaType: "model/gltf-binary", body: sharedGLB},
		assetSpec{role: "converted_model", variant: "converted.fbx", mediaType: "application/octet-stream", body: gameFBX})
	for index, assetID := range []generation.AssetID{modelID, gltfID, fbxID} {
		asset := &task.Assets[index]
		f.content[assetID] = f.content[asset.ID]
		delete(f.content, asset.ID)
		asset.ID, asset.StorageKey, asset.Ordinal, asset.Required = assetID, "key-"+string(assetID), 1, true
	}
	f.tasks[task.ID] = task
	return task
}

func TestTripoTiedModelAndConvertedGltfPickTheModelWhateverTheIDOrder(t *testing.T) {
	orders := []struct {
		name                   string
		modelID, gltfID, fbxID generation.AssetID
	}{
		{name: "converted-first", modelID: "asset_f0", gltfID: "asset_0a", fbxID: "asset_77"},
		{name: "model-first", modelID: "asset_0a", gltfID: "asset_f0", fbxID: "asset_77"},
		{name: "fbx-first", modelID: "asset_9c", gltfID: "asset_5b", fbxID: "asset_01"},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			f := newFixture(t)
			f.tripoTiedTask("tripo-tie", order.modelID, order.gltfID, order.fbxID)
			coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-tie")
			if err != nil {
				t.Fatal(err)
			}
			if glb := formatByID(t, coverage, "glb"); glb.Status != StatusAvailable || glb.AssetID != order.modelID {
				t.Errorf("glb row = %#v; want the model %s", glb, order.modelID)
			}
			if fbx := formatByID(t, coverage, "fbx"); fbx.Status != StatusAvailable || fbx.AssetID != order.fbxID {
				t.Errorf("fbx row = %#v", fbx)
			}
			gltf := formatByID(t, coverage, "gltf")
			if gltf.Status != StatusObtainable || gltf.Reason != reasonNotConverted || gltf.Conversion == nil ||
				gltf.Conversion.OfferingID != generatedGltf {
				t.Errorf("gltf row = %#v; want the free local conversion from the model", gltf)
			}
			for _, step := range coverage.Steps {
				for _, format := range step.Formats {
					if format == "gltf" && step.OfferingID != generatedGltf {
						t.Fatalf("gltf step = %#v", step)
					}
				}
			}

			// The one-click ZIP keeps the model as model.glb and names the
			// converted copy as the same file, whichever ID sorts first.
			archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "tripo-tie")
			if err != nil {
				t.Fatal(err)
			}
			files := readZip(t, archive)
			if files["model.glb"] != sharedGLB {
				t.Fatalf("archive files = %v", keys(files))
			}
			readme := files["README.txt"]
			if !strings.Contains(readme, "- converted.gltf 与 model.glb 内容相同，只收录一份\n") {
				t.Fatalf("README:\n%s", readme)
			}
		})
	}
}

// The conversion source follows BindModelSource's rule on its own: only the
// task's model role converts, even where a converted copy holds the GLB row
// (a required converted.gltf beside an attachment model), and a task whose
// only GLB is a converted copy has no source.
func TestConversionSourceTakesOnlyTheModelRole(t *testing.T) {
	f := newFixture(t)
	converted := generation.Asset{ID: "asset_00", Role: convertedRole, Variant: "converted.gltf", MediaType: "model/gltf-binary", StorageKey: "k0", Ordinal: 1, Required: true}
	model := generation.Asset{ID: "asset_ff", Role: modelRole, MediaType: "model/gltf-binary", StorageKey: "k1", Ordinal: 1}
	for _, assets := range [][]generation.Asset{{converted, model}, {model, converted}} {
		if own, _ := f.service.classify(assets); own["glb"].ID != converted.ID {
			t.Fatalf("glb row = %s; the required file keeps the row", own["glb"].ID)
		}
		if source, ok := f.service.conversionSource(assets, "glb"); !ok || source.ID != model.ID {
			t.Fatalf("source = %s %v; want the model %s", source.ID, ok, model.ID)
		}
	}
	if source, ok := f.service.conversionSource([]generation.Asset{converted}, "glb"); ok {
		t.Fatalf("a converted copy became a source: %s", source.ID)
	}
}
