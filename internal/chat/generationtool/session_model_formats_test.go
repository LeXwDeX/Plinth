package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #321 (note 98949): the Agent said a Rodin model could be exported as FBX and
// OBJ while the format coverage (the 格式 tab) showed them unavailable. The
// model listing now carries that coverage, so the Agent reads which formats
// are there, which can be converted and which cannot.
func TestSessionModelsCarryTheFormatCoverage(t *testing.T) {
	h := newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		return []generation.Task{rodinTask(h)}
	})
	h.executor.SetModelFormats(func(_ context.Context, owner generation.OwnerID, task generation.TaskID) (map[string]string, error) {
		if owner != "owner-a" || task != "rodin-1" {
			t.Errorf("coverage read for %s/%s", owner, task)
		}
		return map[string]string{"glb": "available", "gltf": "obtainable", "fbx": "unavailable", "obj": "unavailable"}, nil
	})
	raw, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Models []struct {
			TaskID        string            `json:"task_id"`
			ExportFormats map[string]string `json:"export_formats"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil || len(listed.Models) != 1 {
		t.Fatalf("listing = %s err=%v", raw, err)
	}
	formats := listed.Models[0].ExportFormats
	if formats["fbx"] != "unavailable" || formats["gltf"] != "obtainable" || formats["glb"] != "available" {
		t.Fatalf("export formats = %v", formats)
	}
}
