package lineage

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type fakeCrops struct {
	crops []SessionCrop
	limit int
	calls int
}

func (f *fakeCrops) SessionCrops(_ context.Context, owner, session string, limit int) ([]SessionCrop, error) {
	f.calls++
	f.limit = limit
	if session != "session-1" {
		return nil, nil
	}
	return f.crops, nil
}

func crop(id string, minutes int, source string) SessionCrop {
	return SessionCrop{
		Asset: generation.InputAsset{
			ID: generation.AssetID(id), OwnerID: "owner-a", AssetType: "list_crop", MediaType: "image/png",
			CreatedAt: start.Add(time.Duration(minutes) * time.Minute),
		},
		SourceAssetID: source,
	}
}

// #587: a picture the Agent cut out has no Task; it still is a version of the
// session, an upload node marked as a crop, tied to the picture it was cut from.
func TestSessionListsCropsAsUploadNodesWithTheirSource(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{"prompt": "four scenes"}), imageAsset("img-1-a", "h-img1")),
	}
	crops := &fakeCrops{crops: []SessionCrop{
		crop("crop-2", 2, "img-1-a"),
		crop("crop-1", 1, "img-1-a"),
		// A crop of a crop resolves through the other crop.
		crop("crop-3", 3, "crop-1"),
		// Another owner's crop never shows, even if the source returned it.
		{Asset: generation.InputAsset{ID: "foreign", OwnerID: "owner-b", MediaType: "image/png"}, SourceAssetID: "img-1-a"},
	}}
	inputs := fakeInputs{"crop-1": crop("crop-1", 1, "").Asset}
	svc := service(t, tasks, nil, inputs)
	svc.SetCrops(crops)
	graph, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if crops.limit != 50 {
		t.Fatalf("crop bound = %d, want MaxTasks", crops.limit)
	}
	for _, id := range []string{"upload:crop-1", "upload:crop-2", "upload:crop-3"} {
		node, ok := nodeOf(graph, id)
		if !ok || node.Kind != KindUpload || node.Operation != OperationCrop || node.TaskID != "" {
			t.Fatalf("%s = %+v (%v)", id, node, ok)
		}
		if len(node.Assets) != 1 || node.Assets[0].Role != "image" || node.Assets[0].MediaType != "image/png" {
			t.Fatalf("%s assets = %+v", id, node.Assets)
		}
	}
	if _, ok := nodeOf(graph, "upload:foreign"); ok {
		t.Fatalf("foreign crop listed: %+v", graph.Nodes)
	}
	for _, id := range []string{"upload:crop-1", "upload:crop-2"} {
		edges := edgeOf(graph, id)
		if len(edges) != 1 || edges[0].From != "img-1" || edges[0].Kind != EdgeSourceAsset || edges[0].Evidence != EvidenceRecorded || edges[0].AssetID != "img-1-a" {
			t.Fatalf("%s edges = %+v", id, edges)
		}
	}
	if edges := edgeOf(graph, "upload:crop-3"); len(edges) != 1 || edges[0].From != "upload:crop-1" {
		t.Fatalf("crop of crop edges = %+v", edges)
	}
	// Oldest first, like every node.
	if graph.Nodes[0].ID != "img-1" || graph.Nodes[1].ID != "upload:crop-1" {
		t.Fatalf("order = %+v", graph.Nodes)
	}
}

// A crop a later Task used keeps one node, marked as a crop, and both edges.
func TestCropUsedByATaskKeepsOneCropNode(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{"prompt": "scenes"}), imageAsset("img-1-a", "h-img1")),
		task("mdl-1", 5, modelOffering, input(t, map[string]any{"image": "crop-1"}), modelAsset("mdl-1-a", "h-mdl1")),
	}
	inputs := fakeInputs{"crop-1": crop("crop-1", 1, "").Asset}
	svc := service(t, tasks, nil, inputs)
	svc.SetCrops(&fakeCrops{crops: []SessionCrop{crop("crop-1", 1, "img-1-a")}})
	graph, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, node := range graph.Nodes {
		if node.ID == "upload:crop-1" {
			count++
			if node.Operation != OperationCrop {
				t.Fatalf("crop node = %+v", node)
			}
		}
	}
	if count != 1 {
		t.Fatalf("crop nodes = %d in %+v", count, graph.Nodes)
	}
	if edges := edgeOf(graph, "mdl-1"); len(edges) != 1 || edges[0].From != "upload:crop-1" {
		t.Fatalf("task edges = %+v", edges)
	}
	if edges := edgeOf(graph, "upload:crop-1"); len(edges) != 1 || edges[0].From != "img-1" {
		t.Fatalf("crop edges = %+v", edges)
	}
}

// Without recorded provenance or with a source outside the session, a crop is
// still listed; its parent is unknown, never guessed.
func TestCropWithUnknownSource(t *testing.T) {
	svc := service(t, nil, nil, fakeInputs{})
	svc.SetCrops(&fakeCrops{crops: []SessionCrop{crop("crop-1", 1, "elsewhere"), crop("crop-2", 2, "")}})
	graph, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeOf(graph, "upload:crop-2"); !ok || len(edgeOf(graph, "upload:crop-2")) != 0 {
		t.Fatalf("crop without provenance = %+v", graph)
	}
	edges := edgeOf(graph, "upload:crop-1")
	if len(edges) != 1 || edges[0].Evidence != EvidenceUnknown {
		t.Fatalf("crop of an unknown source = %+v", edges)
	}
}

// Crops are not Tasks: they do not count toward a page's limit or move the
// cursor. The first page carries every crop (with an edge to a source that may
// be on an older page); later pages carry none.
func TestSessionPageCarriesCropsOnTheFirstPageOnly(t *testing.T) {
	ctx := context.Background()
	tasks := []generation.Task{
		task("new", 3, imageOffering, input(t, map[string]any{"prompt": "b"}), imageAsset("new-a", "new")),
		task("old", 1, imageOffering, input(t, map[string]any{"prompt": "a"}), imageAsset("old-a", "old")),
	}
	var many []SessionCrop
	for index := range 24 {
		many = append(many, crop("crop-"+string(rune('a'+index)), 2, "old-a"))
	}
	crops := &fakeCrops{crops: many}
	svc := service(t, tasks, nil, fakeInputs{})
	svc.SetCrops(crops)
	first, err := svc.SessionPage(ctx, "owner-a", "session-1", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	tasksOnPage, cropsOnPage := 0, 0
	for _, node := range first.Nodes {
		switch {
		case node.Kind == KindTask:
			tasksOnPage++
		case node.Operation == OperationCrop:
			cropsOnPage++
			if node.Version != 0 {
				t.Fatalf("a crop takes no version number: %+v", node)
			}
		}
	}
	if tasksOnPage != 1 || cropsOnPage != 24 || first.NextCursor == "" || !first.Truncated {
		t.Fatalf("first page tasks=%d crops=%d cursor=%q", tasksOnPage, cropsOnPage, first.NextCursor)
	}
	// The source Task is on the next page; the edge already names it.
	if edges := edgeOf(first, "upload:crop-a"); len(edges) != 1 || edges[0].From != "old" || edges[0].Evidence != EvidenceRecorded {
		t.Fatalf("cross-page crop source = %+v", edges)
	}
	second, err := svc.SessionPage(ctx, "owner-a", "session-1", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range second.Nodes {
		if node.Kind != KindTask {
			t.Fatalf("an older page repeats a crop: %+v", second.Nodes)
		}
	}
	if crops.calls != 1 {
		t.Fatalf("crop reads = %d, want only the first page", crops.calls)
	}
}

// The projection never lists more crops than it may read Tasks.
func TestSessionCropsAreBoundedByMaxTasks(t *testing.T) {
	svc, err := New(&fakeTasks{}, fakeCaptures{}, fakeInputs{}, Config{MaxTasks: 2})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetCrops(&fakeCrops{crops: []SessionCrop{crop("c1", 3, ""), crop("c2", 2, ""), crop("c3", 1, "")}})
	graph, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %+v", graph.Nodes)
	}
}

// A crop a Task of an older page used is marked a crop there too: the page
// carries no crops of its own, the asset's type still says what it is.
func TestCropMetOnALaterPageIsStillACrop(t *testing.T) {
	ctx := context.Background()
	tasks := []generation.Task{
		task("new", 3, imageOffering, input(t, map[string]any{"prompt": "b"}), imageAsset("new-a", "new")),
		task("old", 1, modelOffering, input(t, map[string]any{"image": "crop-1"}), modelAsset("old-a", "old")),
	}
	svc := service(t, tasks, nil, fakeInputs{"crop-1": crop("crop-1", 0, "").Asset})
	svc.SetCrops(&fakeCrops{})
	first, err := svc.SessionPage(ctx, "owner-a", "session-1", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.SessionPage(ctx, "owner-a", "session-1", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if node, ok := nodeOf(second, "upload:crop-1"); !ok || node.Operation != OperationCrop {
		t.Fatalf("crop on a later page = %+v (%v)", node, ok)
	}
}
