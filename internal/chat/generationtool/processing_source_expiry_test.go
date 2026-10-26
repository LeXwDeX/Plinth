package generationtool_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #325: Meshy keeps API tasks at most three days; the Schema declares two days
// for its task references. An older Meshy model is processed from its saved
// file when the service takes one, and a service that only takes the original
// task is refused before anything is frozen, with the expiry as the reason.

func expiredMeshyTasks(h *processingHarness) []generation.Task {
	old := time.Now().UTC().Add(-5 * 24 * time.Hour)
	model := modelTask("old-model", "owner-a", "session-a", h.meshy, "text-to-3d", "glb")
	model.CreatedAt = old
	rig := modelTask("old-rig", "owner-a", "session-a", h.meshy, "rigging", "glb", "fbx")
	rig.CreatedAt = old
	retextured := modelTask("old-retexture", "owner-a", "session-a", h.meshy, "retexture", "glb")
	retextured.CreatedAt = time.Now().UTC().Add(-50 * time.Hour)
	return []generation.Task{model, rig, retextured, modelTask("new-model", "owner-a", "session-a", h.meshy, "text-to-3d", "glb")}
}

func TestRetextureOfAnExpiredMeshyModelUsesTheSavedFile(t *testing.T) {
	for _, source := range []string{"old-model", "old-retexture"} {
		t.Run(source, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", expiredMeshyTasks)
			result, _ := h.run("retexture_model", `{"source_task_id":"`+source+`","prompt":"walnut"}`)
			if result.NoSubmission || h.gateway.submitCount != 1 {
				t.Fatalf("retexture refused: %s", result.AgentContent)
			}
			input := h.frozenInput(0)
			if input["source_asset"] != source+"-glb" || input["source_task"] != nil {
				t.Fatalf("frozen input = %v; want the saved GLB and no task reference", input)
			}
		})
	}
}

func TestRetextureOfANewMeshyModelKeepsTheOriginalTask(t *testing.T) {
	h := newProcessingHarness(t, "auto", expiredMeshyTasks)
	result, _ := h.run("retexture_model", `{"source_task_id":"new-model","prompt":"walnut"}`)
	if result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("retexture refused: %s", result.AgentContent)
	}
	if input := h.frozenInput(0); input["source_task"] != "new-model" || input["source_asset"] != nil {
		t.Fatalf("frozen input = %v; want the original task", input)
	}
}

func TestAnimationOfAnExpiredRiggingTaskSaysWhy(t *testing.T) {
	h := newProcessingHarness(t, "auto", expiredMeshyTasks)
	result, _ := h.run("process_model", `{"step":"animation","source_task_id":"old-rig","action_id":3}`)
	// #326: the reason per service, then what works: rigging the saved file again first.
	h.expectRejected(result, "no longer keeps (past its retention)")
	h.expectRejected(result, "rigging can run on old-rig first")

	listed, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	// The expired rigging task is still offered for steps that take its file, never for animation.
	for _, model := range strings.Split(string(listed), `"task_id"`) {
		if strings.Contains(model, `"old-rig"`) && strings.Contains(model, `"step":"animation"`) {
			t.Fatalf("animation listed for an expired rigging task: %s", listed)
		}
	}
	if !strings.Contains(string(listed), `"step":"retexture"`) && !strings.Contains(string(listed), `retexture_model`) {
		t.Fatalf("retexture not listed: %s", listed)
	}
}

func TestPlanOnAnExpiredMeshyModelBindsTheSavedFile(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", false)
	old := h.gateway.tasks["model-1"]
	old.CreatedAt = time.Now().UTC().Add(-5 * 24 * time.Hour)
	h.gateway.tasks["model-1"] = old
	result, err := h.executor.Execute(context.Background(), h.command(`{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":{"target_polycount":20000}}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	content := h.content(result)
	if id, _ := content["plan_id"].(string); id == "" {
		t.Fatalf("plan refused: %v", content)
	}
}

func TestPlanAnimationOnAnExpiredRiggingTaskSaysWhy(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", false)
	rig := modelTask("old-rig", "owner-a", "session-a", h.meshy, "rigging", "glb")
	rig.CreatedAt = time.Now().UTC().Add(-5 * 24 * time.Hour)
	h.gateway.tasks[rig.ID] = rig
	result, err := h.executor.Execute(context.Background(), h.command(`{"source_task_id":"old-rig","steps":[{"capability":"animation","parameters":{"action_id":10}}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	message, _ := h.content(result)["error"].(string)
	if !result.NoSubmission || !strings.Contains(message, "no longer keeps (past its retention)") {
		t.Fatalf("plan refusal = %v", h.content(result))
	}
}
