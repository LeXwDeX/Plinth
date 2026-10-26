package lineage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type fakeTasks struct {
	tasks []generation.Task
	err   error
	pages int
}

func (f *fakeTasks) List(_ context.Context, scope generation.TaskListScope, query generation.ListQuery) (generation.TaskPage, error) {
	f.pages++
	if f.err != nil {
		return generation.TaskPage{}, f.err
	}
	var matched []generation.Task
	for _, task := range f.tasks {
		if task.OwnerID == scope.OwnerID() && task.SessionID == query.SessionID &&
			(len(query.States) == 0 || task.State == query.States[0]) {
			matched = append(matched, task)
		}
	}
	start := 0
	if query.Cursor != "" {
		fmt.Sscanf(query.Cursor, "%d", &start)
	}
	end := min(start+query.Limit, len(matched))
	summaries := make([]generation.Task, 0, end-start)
	for _, task := range matched[start:end] {
		// A summary carries the Offering identity but no frozen input.
		task.Input, task.Offering = nil, generation.Offering{ID: task.Offering.ID}
		summaries = append(summaries, task)
	}
	page := generation.TaskPage{Tasks: summaries, Total: len(matched)}
	if end < len(matched) {
		page.NextCursor = fmt.Sprint(end)
	}
	return page, nil
}

func (f *fakeTasks) GetScoped(_ context.Context, scope generation.TaskListScope, id generation.TaskID) (generation.Task, error) {
	for _, task := range f.tasks {
		if task.ID == id && task.OwnerID == scope.OwnerID() {
			full := task
			return full, nil
		}
	}
	return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get"}
}

type fakeCaptures struct{ records []capture.Record }

func (f fakeCaptures) ListSession(_ context.Context, owner, session string) ([]capture.Record, error) {
	var out []capture.Record
	for _, record := range f.records {
		if record.OwnerID == owner && record.SessionID == session {
			out = append(out, record)
		}
	}
	return out, nil
}

type fakeInputs map[generation.AssetID]generation.InputAsset

func (f fakeInputs) LoadInputAssets(_ context.Context, owner generation.OwnerID, ids []generation.AssetID) (map[generation.AssetID]generation.InputAsset, error) {
	out := map[generation.AssetID]generation.InputAsset{}
	for _, id := range ids {
		if asset, ok := f[id]; ok && asset.OwnerID == owner {
			out[id] = asset
		}
	}
	return out, nil
}

var start = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

var (
	imageOffering = generation.Offering{ID: "image-gen", Parameters: []schema.Parameter{
		{Name: "prompt", Type: "text"},
		{Name: "reference_images", Type: "asset-array", Semantic: schema.SemanticReferenceImage},
		{Name: "mask", Type: "asset", Semantic: schema.SemanticMask},
	}}
	modelOffering = generation.Offering{ID: "image-to-3d", Parameters: []schema.Parameter{
		{Name: "image", Type: "asset", Semantic: schema.SemanticReferenceImage},
	}}
	processOffering = generation.Offering{ID: "retexture", Parameters: []schema.Parameter{
		{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask},
		{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset},
	}}
)

func input(t *testing.T, values map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func task(id string, minutes int, offering generation.Offering, in json.RawMessage, assets ...generation.Asset) generation.Task {
	for index := range assets {
		assets[index].TaskID = generation.TaskID(id)
		assets[index].StorageKey = "k/" + string(assets[index].ID)
		assets[index].Ordinal = index
	}
	return generation.Task{
		ID: generation.TaskID(id), OwnerID: "owner-a", SessionID: "session-1",
		Capability: "capability", Offering: offering, Input: in, State: generation.TaskSucceeded,
		CreatedAt: start.Add(time.Duration(minutes) * time.Minute), Assets: assets,
	}
}

func imageAsset(id, hash string) generation.Asset {
	return generation.Asset{ID: generation.AssetID(id), Role: "image", MediaType: "image/png", SHA256: hash}
}

func modelAsset(id, hash string) generation.Asset {
	return generation.Asset{ID: generation.AssetID(id), Role: "model", MediaType: "model/gltf-binary", SHA256: hash}
}

func service(t *testing.T, tasks []generation.Task, records []capture.Record, inputs fakeInputs) *Service {
	t.Helper()
	svc, err := New(&fakeTasks{tasks: tasks}, fakeCaptures{records: records}, inputs, Config{MaxTasks: 50})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func edgeOf(graph Graph, to string) []Edge {
	var edges []Edge
	for _, edge := range graph.Edges {
		if edge.To == to {
			edges = append(edges, edge)
		}
	}
	return edges
}

func nodeOf(graph Graph, id string) (Node, bool) {
	for _, node := range graph.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return Node{}, false
}

func TestChainFrom2DThrough3DAndCaptureBackTo2DAnd3D(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{"prompt": "a fox"}), imageAsset("img-1-a", "h-img1")),
		// The chat re-uploaded the generated picture: new ID, same bytes.
		task("mdl-1", 1, modelOffering, input(t, map[string]any{"image": "up-1"}), modelAsset("mdl-1-a", "h-mdl1")),
		// The screenshot is a recorded capture of the first model.
		task("img-2", 2, imageOffering, input(t, map[string]any{"prompt": "more", "reference_images": []string{"shot-1"}}), imageAsset("img-2-a", "h-img2")),
		// Image to 3D again, this time directly from a task asset ID.
		task("mdl-2", 3, modelOffering, input(t, map[string]any{"image": "img-2-a"}), modelAsset("mdl-2-a", "h-mdl2")),
		task("tex-1", 4, processOffering, input(t, map[string]any{"source_task": "mdl-2"}), modelAsset("tex-1-a", "h-tex1")),
	}
	records := []capture.Record{{Command: capture.Command{
		ID: "cap-1", OwnerID: "owner-a", SessionID: "session-1", SourceTaskID: "mdl-1",
		ModelAssetID: "mdl-1-a", ScreenshotAssetID: "shot-1",
	}, PixelWidth: 1200, PixelHeight: 800, CreatedAt: start.Add(90 * time.Second)}}
	inputs := fakeInputs{
		"up-1":   {ID: "up-1", OwnerID: "owner-a", SHA256: "H-IMG1"},
		"shot-1": {ID: "shot-1", OwnerID: "owner-a", SHA256: "h-shot"},
	}
	graph, err := service(t, tasks, records, inputs).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 6 || graph.Truncated {
		t.Fatalf("nodes = %+v truncated=%v", graph.Nodes, graph.Truncated)
	}
	for index, want := range []string{"img-1", "mdl-1", "capture:cap-1", "img-2", "mdl-2", "tex-1"} {
		if graph.Nodes[index].ID != want {
			t.Fatalf("node %d = %s, want %s (oldest first)", index, graph.Nodes[index].ID, want)
		}
	}
	cases := map[string]Edge{
		"mdl-1": {From: "img-1", To: "mdl-1", Kind: EdgeReference, Evidence: EvidenceSameBytes, AssetID: "img-1-a"},
		// The screenshot is a version of its own, taken from the model...
		"capture:cap-1": {From: "mdl-1", To: "capture:cap-1", Kind: EdgeCapture, Evidence: EvidenceRecorded, AssetID: "mdl-1-a", CaptureID: "cap-1"},
		// ...and what is made from it points at the screenshot.
		"img-2": {From: "capture:cap-1", To: "img-2", Kind: EdgeReference, Evidence: EvidenceRecorded, AssetID: "shot-1", CaptureID: "cap-1"},
		"mdl-2": {From: "img-2", To: "mdl-2", Kind: EdgeReference, Evidence: EvidenceRecorded, AssetID: "img-2-a"},
		"tex-1": {From: "mdl-2", To: "tex-1", Kind: EdgeSourceTask, Evidence: EvidenceRecorded},
	}
	for to, want := range cases {
		got := edgeOf(graph, to)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("edges into %s = %+v, want %+v", to, got, want)
		}
	}
	if len(edgeOf(graph, "img-1")) != 0 {
		t.Fatalf("a first version has no parent: %+v", edgeOf(graph, "img-1"))
	}
	shot, ok := nodeOf(graph, "capture:cap-1")
	if !ok || shot.Kind != KindCapture || shot.CaptureID != "cap-1" || shot.TaskID != "" ||
		shot.PixelWidth != 1200 || shot.PixelHeight != 800 || len(shot.Assets) != 1 || shot.Assets[0].ID != "shot-1" {
		t.Fatalf("capture node = %+v", shot)
	}
}

func TestCaptureWithoutChildrenIsStillAVersion(t *testing.T) {
	tasks := []generation.Task{
		task("mdl-1", 0, modelOffering, input(t, map[string]any{}), modelAsset("mdl-1-a", "h-mdl1")),
	}
	records := []capture.Record{
		{Command: capture.Command{ID: "cap-1", OwnerID: "owner-a", SessionID: "session-1", SourceTaskID: "mdl-1", ModelAssetID: "mdl-1-a", ScreenshotAssetID: "shot-1"}, CreatedAt: start.Add(time.Minute)},
		// A screenshot of a model outside this session is not invented as a version.
		{Command: capture.Command{ID: "cap-2", OwnerID: "owner-a", SessionID: "session-1", SourceTaskID: "elsewhere", ScreenshotAssetID: "shot-2"}, CreatedAt: start.Add(2 * time.Minute)},
	}
	graph, err := service(t, tasks, records, fakeInputs{}).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 || graph.Nodes[1].ID != "capture:cap-1" {
		t.Fatalf("nodes = %+v", graph.Nodes)
	}
	if edges := edgeOf(graph, "capture:cap-1"); len(edges) != 1 || edges[0].From != "mdl-1" || edges[0].Kind != EdgeCapture {
		t.Fatalf("capture edges = %+v", edges)
	}
}

func TestUploadedPictureIsAVersionWhenATaskUsedIt(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{}), imageAsset("img-1-a", "h-1")),
		task("img-2", 1, imageOffering, input(t, map[string]any{"reference_images": []string{"img-1-a", "sketch"}}), imageAsset("img-2-a", "h-2")),
		// The sketch used again stays one node.
		task("img-3", 2, imageOffering, input(t, map[string]any{"reference_images": []string{"sketch"}}), imageAsset("img-3-a", "h-3")),
		// An output of another session is the owner's asset but not an upload: still unknown.
		task("img-4", 3, imageOffering, input(t, map[string]any{"reference_images": []string{"elsewhere"}}), imageAsset("img-4-a", "h-4")),
	}
	inputs := fakeInputs{
		"sketch":    {ID: "sketch", OwnerID: "owner-a", AssetType: "image", MediaType: "image/png", SHA256: "h-sketch", CreatedAt: start.Add(-time.Minute)},
		"elsewhere": {ID: "elsewhere", OwnerID: "owner-a", MediaType: "image/png", SHA256: "h-else"},
	}
	graph, err := service(t, tasks, nil, inputs).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if graph.Nodes[0].ID != "upload:sketch" || graph.Nodes[0].Kind != KindUpload || len(graph.Nodes[0].Assets) != 1 || graph.Nodes[0].Assets[0].ID != "sketch" {
		t.Fatalf("the upload comes first, as the oldest node: %+v", graph.Nodes)
	}
	for _, id := range []string{"img-2", "img-3"} {
		found := false
		for _, edge := range edgeOf(graph, id) {
			if edge.From == "upload:sketch" && edge.Kind == EdgeReference && edge.Evidence == EvidenceRecorded && edge.AssetID == "sketch" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s should link to the upload: %+v", id, edgeOf(graph, id))
		}
	}
	if edges := edgeOf(graph, "img-4"); len(edges) != 1 || edges[0].Evidence != EvidenceUnknown {
		t.Fatalf("an output of another session is not an upload: %+v", edges)
	}
}

func TestMaskedEditIsAnInpaint(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{}), imageAsset("img-1-a", "h1")),
		task("img-2", 1, imageOffering, input(t, map[string]any{"reference_images": []string{"img-1-a"}, "mask": "mask-1"}), imageAsset("img-2-a", "h2")),
		task("img-3", 2, imageOffering, input(t, map[string]any{"reference_images": []string{"img-1-a"}}), imageAsset("img-3-a", "h3")),
	}
	graph, err := service(t, tasks, nil, fakeInputs{}).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"img-1": "", "img-2": OperationInpaint, "img-3": ""} {
		if node, _ := nodeOf(graph, id); node.Operation != want {
			t.Fatalf("%s operation = %q, want %q", id, node.Operation, want)
		}
	}
}

func TestUnknownOriginsAreShownAsUnknownNeverInvented(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{}), imageAsset("img-1-a", "h-1")),
		// A user upload nobody generated here.
		task("mdl-a", 1, modelOffering, input(t, map[string]any{"image": "upload-x"}), modelAsset("mdl-a-1", "h-a")),
		// The same upload used again: still one unknown node.
		task("mdl-b", 2, modelOffering, input(t, map[string]any{"image": "upload-x"}), modelAsset("mdl-b-1", "h-b")),
		// A source task that is not in this session.
		task("tex-1", 3, processOffering, input(t, map[string]any{"source_task": "someone-elses-task"}), modelAsset("tex-1-a", "h-t")),
		// An asset ID that resolves to nothing.
		task("mdl-c", 4, modelOffering, input(t, map[string]any{"image": "missing"}), modelAsset("mdl-c-1", "h-c")),
	}
	inputs := fakeInputs{"upload-x": {ID: "upload-x", OwnerID: "owner-a", SHA256: "h-upload"}}
	graph, err := service(t, tasks, nil, inputs).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	a, b := edgeOf(graph, "mdl-a"), edgeOf(graph, "mdl-b")
	if len(a) != 1 || len(b) != 1 || a[0].From != b[0].From || a[0].Evidence != EvidenceUnknown {
		t.Fatalf("shared upload should be one unknown parent: %+v %+v", a, b)
	}
	node, ok := nodeOf(graph, a[0].From)
	if !ok || node.Kind != KindUnknown || node.TaskID != "" || len(node.Assets) != 0 {
		t.Fatalf("unknown node = %+v", node)
	}
	for _, id := range []string{"tex-1", "mdl-c"} {
		edges := edgeOf(graph, id)
		if len(edges) != 1 || edges[0].Evidence != EvidenceUnknown {
			t.Fatalf("%s edges = %+v", id, edges)
		}
		if parent, _ := nodeOf(graph, edges[0].From); parent.Kind != KindUnknown {
			t.Fatalf("%s parent = %+v", id, parent)
		}
	}
	// The unresolved platform ID is never echoed to the browser.
	raw, _ := json.Marshal(graph)
	for _, secret := range []string{"someone-elses-task", "upload-x", "missing"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("graph leaks %q: %s", secret, raw)
		}
	}
}

func TestAmbiguousBytesAreUnknownNotGuessed(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{}), imageAsset("img-1-a", "same")),
		task("img-2", 1, imageOffering, input(t, map[string]any{}), imageAsset("img-2-a", "same")),
		task("mdl-1", 2, modelOffering, input(t, map[string]any{"image": "up-1"}), modelAsset("mdl-1-a", "m")),
		// Two assets of one task with identical bytes: the asset is ambiguous.
		task("img-3", 3, imageOffering, input(t, map[string]any{}), imageAsset("img-3-a", "twin"), imageAsset("img-3-b", "twin")),
		task("mdl-2", 4, modelOffering, input(t, map[string]any{"image": "up-2"}), modelAsset("mdl-2-a", "n")),
	}
	inputs := fakeInputs{
		"up-1": {ID: "up-1", OwnerID: "owner-a", SHA256: "same"},
		"up-2": {ID: "up-2", OwnerID: "owner-a", SHA256: "twin"},
	}
	graph, err := service(t, tasks, nil, inputs).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"mdl-1", "mdl-2"} {
		edges := edgeOf(graph, id)
		if len(edges) != 1 || edges[0].Evidence != EvidenceUnknown {
			t.Fatalf("%s edges = %+v", id, edges)
		}
	}
}

func TestMaskIsNotAVersionParent(t *testing.T) {
	tasks := []generation.Task{
		task("img-1", 0, imageOffering, input(t, map[string]any{}), imageAsset("img-1-a", "h1")),
		task("img-2", 1, imageOffering, input(t, map[string]any{"mask": "img-1-a"}), imageAsset("img-2-a", "h2")),
	}
	graph, err := service(t, tasks, nil, fakeInputs{}).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 0 {
		t.Fatalf("mask produced edges: %+v", graph.Edges)
	}
}

func TestOwnerAndSessionIsolation(t *testing.T) {
	foreign := task("img-x", 0, imageOffering, input(t, map[string]any{}), imageAsset("img-x-a", "hx"))
	foreign.OwnerID = "owner-b"
	otherSession := task("img-y", 0, imageOffering, input(t, map[string]any{}), imageAsset("img-y-a", "hy"))
	otherSession.SessionID = "session-2"
	mine := task("mdl-1", 1, modelOffering, input(t, map[string]any{"image": "img-x-a"}), modelAsset("mdl-1-a", "m"))
	failed := task("img-f", 2, imageOffering, input(t, map[string]any{}), imageAsset("img-f-a", "hf"))
	failed.State = generation.TaskFailed
	records := []capture.Record{{Command: capture.Command{
		ID: "cap-x", OwnerID: "owner-b", SessionID: "session-1", SourceTaskID: "img-x", ScreenshotAssetID: "img-x-a",
	}}}
	graph, err := service(t, []generation.Task{foreign, otherSession, mine, failed}, records, fakeInputs{
		"img-x-a": {ID: "img-x-a", OwnerID: "owner-b", SHA256: "hx"},
	}).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) < 1 {
		t.Fatal("own task missing")
	}
	for _, node := range graph.Nodes {
		if node.ID == "img-x" || node.ID == "img-y" || node.ID == "img-f" {
			t.Fatalf("foreign, other-session or failed task listed: %+v", node)
		}
	}
	edges := edgeOf(graph, "mdl-1")
	if len(edges) != 1 || edges[0].Evidence != EvidenceUnknown {
		t.Fatalf("another owner's asset must be unknown: %+v", edges)
	}
}

func TestTruncationAndConfiguration(t *testing.T) {
	var tasks []generation.Task
	for index := 0; index < 5; index++ {
		tasks = append(tasks, task(fmt.Sprintf("t-%d", index), index, imageOffering, input(t, map[string]any{}), imageAsset(fmt.Sprintf("a-%d", index), fmt.Sprintf("h%d", index))))
	}
	svc, err := New(&fakeTasks{tasks: tasks}, nil, fakeInputs{}, Config{MaxTasks: 3})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 3 || !graph.Truncated {
		t.Fatalf("nodes=%d truncated=%v", len(graph.Nodes), graph.Truncated)
	}
	if _, err := New(nil, nil, fakeInputs{}, Config{MaxTasks: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc.Session(context.Background(), "", "s"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	boom := errors.New("down")
	broken, _ := New(&fakeTasks{err: boom}, nil, fakeInputs{}, Config{MaxTasks: 3})
	if _, err := broken.Session(context.Background(), "owner-a", "session-1"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptySessionYieldsEmptyListsNotNull(t *testing.T) {
	graph, err := service(t, nil, nil, fakeInputs{}).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(graph)
	if string(raw) != `{"nodes":[],"edges":[],"truncated":false}` {
		t.Fatalf("json = %s", raw)
	}
}

type fakeGroups struct {
	groups []Group
	err    error
}

func (f fakeGroups) LineageGroups(context.Context, string, string) ([]Group, error) {
	return f.groups, f.err
}

func TestGroupsMarkTheirFinishedMembersAndNameNothingElse(t *testing.T) {
	tasks := []generation.Task{
		task("front", 0, imageOffering, input(t, map[string]any{"prompt": "front"}), imageAsset("front-a", "h-f")),
		task("side", 1, imageOffering, input(t, map[string]any{"prompt": "side"}), imageAsset("side-a", "h-s")),
		task("alone", 2, imageOffering, input(t, map[string]any{"prompt": "alone"}), imageAsset("alone-a", "h-a")),
	}
	svc := service(t, tasks, nil, nil)
	// No group source: the graph lists none.
	plain, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil || len(plain.Groups) != 0 {
		t.Fatalf("groups without a source = %+v %v", plain.Groups, err)
	}
	svc.SetGroups(fakeGroups{groups: []Group{
		{ID: "run-1", Kind: GroupThreeView, Label: "views", SourceSlot: "front", CreatedAt: start, Members: []GroupMember{
			{Slot: "front", Label: "Front", TaskID: "front"},
			{Slot: "side", Label: "Side", TaskID: "side"},
			// Not in the graph (unfinished, deleted or another user's): never named.
			{Slot: "back", Label: "Back", TaskID: "back-not-in-graph"},
		}},
		// A group with no member in the graph is left out.
		{ID: "run-2", Kind: GroupThreeView, Members: []GroupMember{{Slot: "front", TaskID: "elsewhere"}}},
	}})
	graph, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Groups) != 1 || graph.Groups[0].ID != "run-1" || len(graph.Groups[0].Members) != 2 {
		t.Fatalf("groups = %+v", graph.Groups)
	}
	for _, member := range graph.Groups[0].Members {
		if member.TaskID == "back-not-in-graph" {
			t.Fatalf("a task outside the graph was named: %+v", graph.Groups[0])
		}
	}
	for id, want := range map[string][2]string{"front": {"run-1", "front"}, "side": {"run-1", "side"}, "alone": {"", ""}} {
		node, ok := nodeOf(graph, id)
		if !ok || node.Group != want[0] || node.Slot != want[1] {
			t.Fatalf("node %s = %+v ok=%v", id, node, ok)
		}
	}
	svc.SetGroups(fakeGroups{err: errors.New("down")})
	if _, err := svc.Session(context.Background(), "owner-a", "session-1"); err == nil {
		t.Fatal("a failing group source must fail the projection, not hide groups")
	}
}

// #326: the graph keeps which files are required outputs of their task.
func TestTaskNodesKeepRequiredFiles(t *testing.T) {
	character := modelAsset("character", "h-char")
	character.Required = true
	tasks := []generation.Task{task("rig", 0, processOffering, input(t, map[string]any{}), modelAsset("skeleton", "h-skel"), character)}
	graph, err := service(t, tasks, nil, fakeInputs{}).Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 1 || len(graph.Nodes[0].Assets) != 2 || graph.Nodes[0].Assets[0].Required || !graph.Nodes[0].Assets[1].Required {
		t.Fatalf("graph = %#v", graph.Nodes)
	}
}
