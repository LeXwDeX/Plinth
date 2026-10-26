package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

func TestLineageWireNamesScreenshotsAndUploadsByTheirOwnAssetRoute(t *testing.T) {
	t.Parallel()
	taskAsset, err := routing.ParseTwo("/api/v1/tasks/{taskID}/assets/{assetID}", "taskID", "assetID")
	if err != nil {
		t.Fatal(err)
	}
	assetItem, err := routing.ParseOne("/api/v1/assets/{assetID}", "assetID")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{taskAssetRoute: taskAsset, assetItemRoute: assetItem}
	wire := server.lineageWire("", lineage.Graph{Nodes: []lineage.Node{
		{ID: "task-1", Kind: lineage.KindTask, TaskID: "task-1", Operation: lineage.OperationInpaint,
			Assets: []lineage.Asset{{ID: "a1", Role: "image", MediaType: "image/png"}}},
		{ID: "capture:c1", Kind: lineage.KindCapture, TaskID: "task-0", CaptureID: "c1", PixelWidth: 1280, PixelHeight: 720,
			Assets: []lineage.Asset{{ID: "shot", Role: "screenshot", MediaType: "image/png"}}},
		{ID: "upload:u1", Kind: lineage.KindUpload,
			Assets: []lineage.Asset{{ID: "u1", Role: "input", MediaType: "image/png"}}},
	}})
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{
		`"operation":"inpaint"`,
		`"capture_id":"c1"`, `"pixel_width":1280`, `"pixel_height":720`,
		`"url":"/api/v1/tasks/task-1/assets/a1"`,
		`"url":"/api/v1/assets/shot"`, `"url":"/api/v1/assets/u1"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("lineage wire lacks %s: %s", want, text)
		}
	}
}

// #326: a version's model is its required output, not an attachment listed
// first; the wire says which files are required so the stage can tell.
func TestLineageWireMarksRequiredFiles(t *testing.T) {
	t.Parallel()
	taskAsset, err := routing.ParseTwo("/api/v1/tasks/{taskID}/assets/{assetID}", "taskID", "assetID")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{taskAssetRoute: taskAsset}
	wire := server.lineageWire("", lineage.Graph{Nodes: []lineage.Node{
		{ID: "rig", Kind: lineage.KindTask, TaskID: "rig", Assets: []lineage.Asset{
			{ID: "skeleton", Role: "model", MediaType: "model/gltf-binary", Ordinal: 1},
			{ID: "character", Role: "model", MediaType: "model/gltf-binary", Ordinal: 8, Required: true},
		}},
	}})
	if assets := wire.Nodes[0].Assets; len(assets) != 2 || assets[0].Required || !assets[1].Required {
		t.Fatalf("assets = %#v", assets)
	}
	encoded, _ := json.Marshal(wire)
	if strings.Count(string(encoded), `"required":true`) != 1 {
		t.Fatalf("wire lacks required: %s", encoded)
	}
}

// #330 second follow-ups: a format conversion folded into its source version
// is named on the wire, so the version strip maps it to that version instead
// of showing it as a version of its own. A chain without one omits the list.
func TestLineageWireNamesFoldedConversions330(t *testing.T) {
	t.Parallel()
	server := &Server{}
	encoded, err := json.Marshal(server.lineageWire("", lineage.Graph{Conversions: map[string]string{"convert-2": "source", "convert-1": "source"}}))
	if err != nil {
		t.Fatal(err)
	}
	want := `"conversions":[{"task_id":"convert-1","source_task_id":"source"},{"task_id":"convert-2","source_task_id":"source"}]`
	if !strings.Contains(string(encoded), want) {
		t.Fatalf("wire = %s", encoded)
	}
	encoded, _ = json.Marshal(server.lineageWire("", lineage.Graph{}))
	if strings.Contains(string(encoded), "conversions") {
		t.Fatalf("an empty list must be omitted: %s", encoded)
	}
}

// A conversion made before the fold keeps its node and says it is one.
func TestLineageWireMarksKeptConversions330(t *testing.T) {
	t.Parallel()
	taskAsset, err := routing.ParseTwo("/api/v1/tasks/{taskID}/assets/{assetID}", "taskID", "assetID")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{taskAssetRoute: taskAsset}
	encoded, _ := json.Marshal(server.lineageWire("", lineage.Graph{Nodes: []lineage.Node{
		{ID: "convert-1", Kind: lineage.KindTask, TaskID: "convert-1", Version: 2, Conversion: true},
		{ID: "gen-2", Kind: lineage.KindTask, TaskID: "gen-2", Version: 3},
	}}))
	if text := string(encoded); strings.Count(text, `"conversion":true`) != 1 || !strings.Contains(text, `"version":2,"conversion":true`) {
		t.Fatalf("wire = %s", text)
	}
}
