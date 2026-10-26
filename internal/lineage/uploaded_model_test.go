package lineage

import (
	"context"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"testing"
)

type nativeModelInputs struct {
	fakeInputs
	native map[generation.AssetID]generation.InputAsset
	err    error
	reads  int
}

func (n *nativeModelInputs) GetInputAsset(_ context.Context, id generation.AssetID) (generation.InputAsset, error) {
	n.reads++
	if n.err != nil {
		return generation.InputAsset{}, n.err
	}
	if asset, ok := n.native[id]; ok {
		return asset, nil
	}
	return generation.InputAsset{}, &generation.Error{Code: generation.ErrNotFound}
}
func TestUploadedModelLineageIsOwnedNativeAssetWithoutTask(t *testing.T) {
	for _, paged := range []bool{false, true} {
		t.Run(map[bool]string{false: "session", true: "page"}[paged], func(t *testing.T) {
			for _, kind := range []string{"owned", "foreign", "task-fallback", "storage-failure"} {
				t.Run(kind, func(t *testing.T) {
					offering := generation.Offering{Parameters: []schema.Parameter{{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, UploadedModel: &schema.UploadedModel{ProfileID: "static-glb-v1"}}}}
					child := task("processed", 1, offering, input(t, map[string]any{"source_asset": "original-upload"}), modelAsset("result", "result-hash"))
					uploaded := generation.InputAsset{ID: "original-upload", OwnerID: "owner-a", OriginalFilename: "model.glb", AssetType: "model", MediaType: "model/gltf-binary", SHA256: "actual-source-bytes", Size: 512, CreatedAt: start}
					native := &nativeModelInputs{fakeInputs: fakeInputs{uploaded.ID: uploaded}, native: map[generation.AssetID]generation.InputAsset{uploaded.ID: uploaded}}
					switch kind {
					case "foreign":
						uploaded.OwnerID = "other"
						native.native[uploaded.ID] = uploaded
					case "task-fallback":
						delete(native.native, uploaded.ID)
					case "storage-failure":
						native.err = errors.New("storage unavailable")
					}
					svc, err := New(&fakeTasks{tasks: []generation.Task{child}}, nil, native, Config{MaxTasks: 50})
					if err != nil {
						t.Fatal(err)
					}
					var graph Graph
					if paged {
						graph, err = svc.SessionPage(context.Background(), "owner-a", "session-1", "", 10)
					} else {
						graph, err = svc.Session(context.Background(), "owner-a", "session-1")
					}
					if kind == "storage-failure" {
						if err == nil {
							t.Fatal("storage failure hidden")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					node, ok := nodeOf(graph, "upload:original-upload")
					if kind != "owned" {
						if ok {
							t.Fatalf("non-native/foreign source exposed: %+v", node)
						}
						return
					}
					if !ok || node.Kind != KindUpload || node.TaskID != "" || len(node.Assets) != 1 || node.Assets[0].ID != string(uploaded.ID) || node.Assets[0].Role != "model" {
						t.Fatalf("model upload identity missing or fabricated: %+v", graph)
					}
					edges := edgeOf(graph, "processed")
					if len(edges) != 1 || edges[0].From != node.ID || edges[0].Evidence != EvidenceRecorded || edges[0].AssetID != string(uploaded.ID) {
						t.Fatalf("source lineage lost: %+v", edges)
					}
				})
			}
		})
	}
}
