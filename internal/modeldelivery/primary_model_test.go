package modeldelivery

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #326: the 格式 tab, the 加工 panel and the Agent name the same file of a
// result: a required output before an attachment. A result whose attachment
// GLB is listed before its required GLB shows the required one in the GLB
// row, as the stage does.
func TestFormatRowsTakeTheRequiredFileBeforeAnAttachment(t *testing.T) {
	f := newFixture(t)
	task := f.addTask("result", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary", body: "attachment skeleton"},
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary", body: "main model"})
	task.Assets[1].Required = true
	f.tasks[task.ID] = task
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "result")
	if err != nil {
		t.Fatal(err)
	}
	if glb := formatByID(t, coverage, "glb"); glb.AssetID != task.Assets[1].ID {
		t.Fatalf("glb row = %#v; want the required file %s", glb, task.Assets[1].ID)
	}
}
