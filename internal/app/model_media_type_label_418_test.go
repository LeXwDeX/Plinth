package app

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #418: some CDNs label model files from the host's mime table. The fake Tripo
// CDN answers a task's GLB with application/x-tgif; the output slot has no MIME
// contract, so the kernel stores that label. The read-only inspection must
// still judge the saved bytes and inspect the GLB, both through the platform
// entry and through the outward MCP core that shares the same loader.
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
	// This is the condition the issue is about: the saved GLB carries the
	// CDN's label, not model/gltf-binary.
	if model.MediaType != "application/x-tgif" {
		t.Fatalf("stored model media type = %q, want the fake CDN's application/x-tgif", model.MediaType)
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
