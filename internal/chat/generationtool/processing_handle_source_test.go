package generationtool_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// meshyImageTask is a finished Meshy image-to-3D result as dev-test stored it
// (#321 B): its first saved model file is the 3MF package, the GLB the stage
// shows comes third. A model handle recorded in the chat names the first file.
func meshyImageTask(h *processingHarness) generation.Task {
	task := modelTask("meshy71-1", "owner-a", "session-a", h.meshy, "image-to-3d")
	task.Assets = []generation.Asset{
		{ID: "meshy71-3mf", TaskID: task.ID, Role: "model", Variant: "3mf", StorageKey: "key/3mf", MediaType: "application/zip", Size: 994055},
		{ID: "meshy71-fbx", TaskID: task.ID, Role: "model", Variant: "fbx", StorageKey: "key/fbx", MediaType: "application/octet-stream", Size: 66788124},
		{ID: "meshy71-glb", TaskID: task.ID, Role: "model", Variant: "glb", StorageKey: "key/glb", MediaType: "model/gltf-binary", Size: 29096916},
		{ID: "meshy71-thumb", TaskID: task.ID, Role: "thumbnail", StorageKey: "key/thumb", MediaType: "image/png"},
	}
	return task
}

func meshyImageHistory() []chat.Message {
	return []chat.Message{{Role: "assistant", ImageURLs: []string{"/thumb"}, Attachments: []chat.Attachment{
		{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated,
			TaskID: "meshy71-1", AssetID: "meshy71-3mf", ImageSlot: 1}}}}
}

// #321 B: the 加工 panel binds the GLB the stage shows and lists Smart
// Segmentation for this model; the Agent, given the chat handle of the same
// model, must reach the same answer instead of reporting that no service
// accepts it because the handle happened to record the 3MF file.
func TestModelHandleBindsTheSameFileThePanelBinds(t *testing.T) {
	h := newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		return []generation.Task{meshyImageTask(h)}
	})
	result, _ := h.runAs("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"@model1"}`, meshyImageHistory())
	if result.NoSubmission || len(h.gateway.frozen) != 1 {
		t.Fatalf("the Agent refused a step the panel offers: %s", result.AgentContent)
	}
	if h.gateway.frozen[0].OfferingID != "tripo/geometry/smartsegment-model" || h.frozenInput(0)["source_asset"] != "meshy71-glb" {
		t.Fatalf("frozen = %+v input %v", h.gateway.frozen[0], h.frozenInput(0))
	}

	// list_session_models offers the same step for the model.
	listed, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil || !strings.Contains(string(listed), `"step":"mesh-segmentation"`) {
		t.Fatalf("list_session_models = %s err=%v", listed, err)
	}
}

// An explicit file the Agent names keeps its own contract: the 3MF package is
// not a GLB, so Smart Segmentation is refused and nothing is swapped silently.
func TestExplicitSourceAssetIsNotReplaced(t *testing.T) {
	h := newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		return []generation.Task{meshyImageTask(h)}
	})
	result, _ := h.runAs("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"meshy71-1","source_asset_id":"meshy71-3mf"}`, nil)
	h.expectRejected(result, "mesh-segmentation")
}

// The plan tool freezes the same source file the panel's plan uses.
func TestPlanFromAModelHandleUsesThePanelsFile(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", false)
	h.gateway.tasks["meshy71-1"] = meshyImageTask(h.processingHarness)
	command := h.command(`{"source_task_id":"@model1","steps":[{"capability":"mesh-segmentation","parameters":{}}]}`)
	command.History = meshyImageHistory()
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := h.content(result)
	id, _ := content["plan_id"].(string)
	if id == "" {
		t.Fatalf("plan from the handle refused: %v", content)
	}
	plan := h.get(id)
	if plan.SourceAssetID != "meshy71-glb" || len(plan.Steps) != 1 || plan.Steps[0].OfferingID != "tripo/geometry/smartsegment-model" {
		t.Fatalf("plan = %+v", plan)
	}
}
