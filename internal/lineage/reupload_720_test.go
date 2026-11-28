package lineage

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// derivingInputs also records how a fitted copy was made (#276), like the
// SQLite repository's InputAssetDerivation.
type derivingInputs struct {
	fakeInputs
	derived map[generation.AssetID]generation.InputAssetDerivation
}

func (f derivingInputs) InputAssetDerivation(_ context.Context, owner generation.OwnerID, id generation.AssetID) (generation.InputAssetDerivation, bool, error) {
	asset, ok := f.fakeInputs[id]
	if !ok || asset.OwnerID != owner {
		return generation.InputAssetDerivation{}, false, nil
	}
	derivation, ok := f.derived[id]
	return derivation, ok, nil
}

func noUploadNodes(t *testing.T, graph Graph) {
	t.Helper()
	for _, node := range graph.Nodes {
		if node.Kind == KindUpload {
			t.Fatalf("a copy of a version is not an upload (#720): %+v in %+v", node, graph.Nodes)
		}
	}
}

func pageOf(t *testing.T, svc *Service) Graph {
	t.Helper()
	graph, err := svc.SessionPage(context.Background(), "owner-a", "session-1", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

// #720: image to image copies the version it refines into a new input asset
// (the chat reference resolver's copy, asset_type image). The fake answers
// every request of one size with the same bytes, so the result of the edit has
// the bytes of its own source: the copy then matched two Tasks, fell through to
// "upload" and the strip showed 「以 上传 作为参考图生成」. A Task cannot be the
// source of itself, nor can a later one: only earlier Tasks are candidates.
func TestCopyOfAVersionPointsAtThatVersionEvenWhenTheResultHasTheSameBytes_720(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{"prompt": "a fox"}), imageAsset("img-1-a", "fixed")),
		task("img-2", 1, imageOffering, input(t, map[string]any{"prompt": "redder", "reference_images": []string{"copy-1"}}), imageAsset("img-2-a", "fixed")),
	}
	inputs := fakeInputs{"copy-1": {ID: "copy-1", OwnerID: "owner-a", AssetType: "image", MediaType: "image/png", SHA256: "fixed", CreatedAt: start.Add(30e9)}}
	svc := service(t, tasks, nil, inputs)
	session, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	for name, graph := range map[string]Graph{"session": session, "page": pageOf(t, svc)} {
		noUploadNodes(t, graph)
		want := Edge{From: "img-1", To: "img-2", Kind: EdgeReference, Evidence: EvidenceSameBytes, AssetID: "img-1-a"}
		if edges := edgeOf(graph, "img-2"); len(edges) != 1 || edges[0] != want {
			t.Fatalf("%s: edges into img-2 = %+v, want %+v", name, edges, want)
		}
	}
}

// A copy whose bytes match several earlier versions names none of them, and it
// is still not the person's upload: its bytes are a generated picture's.
func TestCopyMatchingSeveralEarlierVersionsIsUnknownNotAnUpload_720(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{}), imageAsset("img-1-a", "fixed")),
		task("img-2", 1, imageOffering, input(t, map[string]any{}), imageAsset("img-2-a", "fixed")),
		task("img-3", 2, imageOffering, input(t, map[string]any{"reference_images": []string{"copy-2"}}), imageAsset("img-3-a", "fixed")),
	}
	inputs := fakeInputs{"copy-2": {ID: "copy-2", OwnerID: "owner-a", AssetType: "image", MediaType: "image/png", SHA256: "fixed"}}
	graph, err := service(t, tasks, nil, inputs).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	noUploadNodes(t, graph)
	if edges := edgeOf(graph, "img-3"); len(edges) != 1 || edges[0].Evidence != EvidenceUnknown {
		t.Fatalf("edges into img-3 = %+v", edges)
	}
}

// The input fitter (#276) may re-encode a reference to fit the Offering: the
// frozen input then names the fitted copy, whose bytes no version has. The
// derivation record names what it was made from exactly; lineage follows it.
func TestFittedReferencePointsAtTheVersionItWasMadeFrom_720(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{}), imageAsset("img-1-a", "h-1")),
		// The chat copied img-1 and the fitter compressed the copy.
		task("img-2", 1, imageOffering, input(t, map[string]any{"reference_images": []string{"fit-copy"}}), imageAsset("img-2-a", "h-2")),
		// The fitter compressed a Task picture named directly.
		task("img-3", 2, imageOffering, input(t, map[string]any{"reference_images": []string{"fit-task"}}), imageAsset("img-3-a", "h-3")),
		// A fitted upload stays one node: the person's original upload.
		task("img-4", 3, imageOffering, input(t, map[string]any{"reference_images": []string{"fit-sketch"}}), imageAsset("img-4-a", "h-4")),
	}
	inputs := derivingInputs{
		fakeInputs: fakeInputs{
			"copy-1":     {ID: "copy-1", OwnerID: "owner-a", AssetType: "image", MediaType: "image/png", SHA256: "h-1"},
			"fit-copy":   {ID: "fit-copy", OwnerID: "owner-a", AssetType: "image", MediaType: "image/jpeg", SHA256: "h-fit-copy"},
			"fit-task":   {ID: "fit-task", OwnerID: "owner-a", MediaType: "image/jpeg", SHA256: "h-fit-task"},
			"sketch":     {ID: "sketch", OwnerID: "owner-a", AssetType: "image", MediaType: "image/png", SHA256: "h-sketch", CreatedAt: start.Add(-60e9)},
			"fit-sketch": {ID: "fit-sketch", OwnerID: "owner-a", AssetType: "image", MediaType: "image/jpeg", SHA256: "h-fit-sketch"},
		},
		derived: map[generation.AssetID]generation.InputAssetDerivation{
			"fit-copy":   {SourceAssetID: "copy-1", Method: "lossy", SourceSize: 9},
			"fit-task":   {SourceAssetID: "img-1-a", Method: "lossy", SourceSize: 9},
			"fit-sketch": {SourceAssetID: "sketch", Method: "lossy", SourceSize: 9},
		},
	}
	svc, err := New(&fakeTasks{tasks: tasks}, fakeCaptures{}, inputs, Config{MaxTasks: 50})
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	for name, graph := range map[string]Graph{"session": session, "page": pageOf(t, svc)} {
		want := map[string]Edge{
			"img-2": {From: "img-1", To: "img-2", Kind: EdgeReference, Evidence: EvidenceSameBytes, AssetID: "img-1-a"},
			"img-3": {From: "img-1", To: "img-3", Kind: EdgeReference, Evidence: EvidenceRecorded, AssetID: "img-1-a"},
			"img-4": {From: "upload:sketch", To: "img-4", Kind: EdgeReference, Evidence: EvidenceRecorded, AssetID: "sketch"},
		}
		for to, edge := range want {
			if got := edgeOf(graph, to); len(got) != 1 || got[0] != edge {
				t.Fatalf("%s: edges into %s = %+v, want %+v", name, to, got, edge)
			}
		}
		uploads := 0
		for _, node := range graph.Nodes {
			if node.Kind == KindUpload {
				uploads++
				if node.ID != "upload:sketch" {
					t.Fatalf("%s: only the person's own upload is an upload node: %+v", name, node)
				}
			}
		}
		if uploads != 1 {
			t.Fatalf("%s: upload nodes = %d in %+v", name, uploads, graph.Nodes)
		}
	}
}
