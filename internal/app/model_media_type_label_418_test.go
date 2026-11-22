package app

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #418: some CDNs label model files from the host's mime table. The fake Tripo
// CDN answers a task's GLB with application/x-tgif; the output slot has no MIME
// contract, so the response label reaches the store. The read-only inspection
// must still judge the saved bytes and inspect the GLB, both through the
// platform entry and through the outward MCP core that shares the same loader.
//
// #424: the store also keeps the format the bytes are (model/gltf-binary), so
// the processing entry, which judges a source by its stored label, can bind the
// file. A file stored before that correction keeps its CDN label, which is why
// the read-only review still judges the bytes.
func TestInspectModelReadsAMislabeledGLBByItsBytes(t *testing.T) {
	harness, product, _, _ := fakeStackHarnessWithInputs(t, func(origin string) map[string]string {
		return map[string]string{
			"tripo_base_url": origin + "/tripo/v3",
			"tripo_api_key":  fakeUpstreamKeyPrefix + string(fakeupstream.ScenarioArtifactMislabeled),
		}
	})
	accepted := submitStep(t, harness, generation.TextTo3D, "tripo/tripo-p2/text-to-3d",
		`{"prompt":"a robot","authorize_extra_credits":true}`, "media-type-418", nil)
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state = %s, failure = %#v", task.State, task.Failure)
	}

	var model generation.Asset
	for _, asset := range task.Assets {
		if asset.Role == "model" && asset.StorageKey != "" {
			model = asset
		}
	}
	if model.ID == "" {
		t.Fatalf("the task delivered no model file: %+v", task.Assets)
	}
	// The CDN's application/x-tgif reached the store; the bytes are a GLB, so
	// the stored label is the format the file is, not the CDN's mime-table entry.
	if model.MediaType != "model/gltf-binary" {
		t.Fatalf("stored model media type = %q, want model/gltf-binary", model.MediaType)
	}
	// The processing entry judges a source by its stored label: an Offering that
	// takes model/gltf-binary must bind this file (#424).
	forward, ok := product.Get("local/static-glb/to-gltf")
	if !ok {
		t.Fatal("the GLB conversion route is not declared")
	}
	binding, err := generation.BindModelSource(forward, task, model.ID, nil)
	if err != nil {
		t.Fatalf("bind the mislabeled GLB to %s: %v", forward.ID, err)
	}
	if binding.AssetID != model.ID || binding.Role != schema.SemanticSourceAsset {
		t.Fatalf("binding = %+v, want the saved model asset", binding)
	}

	executor, err := generationtool.New(generationtool.Config{
		Gateway: harness.module, References: noReferences{},
		ModelAssets: harness.module, Chat: product.Application().Chat,
	})
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := executor.InspectReviewedModel(context.Background(), generationtool.ModelInspectRequest{
		Owner: "owner-acme", TaskID: task.ID, AssetID: model.ID,
	})
	if err != nil {
		t.Fatalf("inspect the mislabeled GLB: %v", err)
	}
	if inspected.AssetID != model.ID || inspected.Report.FileBytes != model.Size || inspected.Report.Triangles == 0 {
		t.Fatalf("inspection = %+v, want the saved %d-byte GLB", inspected, model.Size)
	}
}
