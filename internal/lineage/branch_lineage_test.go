package lineage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func branchTask(id, session string, minute int, input json.RawMessage, assets ...generation.Asset) generation.Task {
	for index := range assets {
		assets[index].TaskID = generation.TaskID(id)
		assets[index].StorageKey = "k/" + string(assets[index].ID)
		assets[index].Ordinal = index
	}
	return generation.Task{
		ID: generation.TaskID(id), OwnerID: "owner-a", SessionID: session,
		Capability: "text-to-3d", Offering: processOffering, Input: input,
		State: generation.TaskSucceeded, CreatedAt: start.Add(time.Duration(minute) * time.Minute),
		Assets: assets,
	}
}

func TestBranchLineageIncludesAncestorTasksAndContinuesVersionNumbers(t *testing.T) {
	parent := branchTask("parent-task", "session-1", 1, nil, modelAsset("parent-model", "h1"))
	branch := branchTask("branch-task", "branch", 2,
		json.RawMessage(`{"source_task":"parent-task"}`), modelAsset("branch-model", "h2"))
	second := branchTask("second-task", "branch-2", 3,
		json.RawMessage(`{"source_task":"branch-task"}`), modelAsset("second-model", "h3"))
	sibling := branchTask("sibling-task", "sibling", 2, nil, modelAsset("sibling-model", "h4"))
	record := capture.Record{
		Command: capture.Command{ID: "capture-1", OwnerID: "owner-a", SessionID: "branch",
			SourceTaskID: "parent-task", ModelAssetID: "parent-model",
			ScreenshotAssetID: "screenshot-1", View: capture.View{
				CameraOrbit: "0 0 5", CameraTarget: "0 0 0", FieldOfView: "45",
				Environment: "studio", RendererVersion: "three"}},
		PixelWidth: 640, PixelHeight: 480, SizeBytes: 128, SHA256: "shot",
		CreatedAt: start.Add(2*time.Minute + 30*time.Second),
	}
	tasks := &fakeTasks{
		tasks: []generation.Task{parent, branch, second, sibling},
		refs: map[string]map[generation.TaskID]bool{
			"branch":   {"parent-task": true},
			"branch-2": {"parent-task": true, "branch-task": true},
		},
	}
	service, err := New(tasks, fakeCaptures{records: []capture.Record{record}}, fakeInputs{}, Config{MaxTasks: 50})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := service.Session(context.Background(), "owner-a", "branch")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]lineageNode{}
	for _, node := range graph.Nodes {
		if node.Kind == KindTask {
			ids[node.TaskID] = lineageNode{version: node.Version, createdAt: node.CreatedAt}
		}
	}
	if _, ok := ids["parent-task"]; !ok {
		t.Fatalf("branch graph lacks the ancestor task: %+v", graph.Nodes)
	}
	if _, ok := ids["sibling-task"]; ok {
		t.Fatalf("branch graph leaked a sibling task: %+v", graph.Nodes)
	}
	versions, err := service.SessionVersions(context.Background(), "owner-a", "branch")
	if err != nil {
		t.Fatal(err)
	}
	if versions["parent-task"] != 1 || versions["branch-task"] != 2 {
		t.Fatalf("branch versions = %v", versions)
	}
	// A branch of the branch sees both earlier generations and numbers the
	// new Task after them.
	secondVersions, err := service.SessionVersions(context.Background(), "owner-a", "branch-2")
	if err != nil {
		t.Fatal(err)
	}
	if secondVersions["parent-task"] != 1 || secondVersions["branch-task"] != 2 || secondVersions["second-task"] != 3 {
		t.Fatalf("branch-2 versions = %v", secondVersions)
	}
	page, err := service.SessionPage(context.Background(), "owner-a", "branch", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	taskNodes := 0
	for _, node := range page.Nodes {
		if node.Kind == KindTask && node.Version > 0 {
			taskNodes++
		}
	}
	if taskNodes < 2 {
		t.Fatalf("paged branch lineage = %+v", page.Nodes)
	}
}

type lineageNode struct {
	version   int
	createdAt *time.Time
}

func TestBranchLineageDoesNotWidenWithoutRefs(t *testing.T) {
	parent := branchTask("parent-task", "session-1", 1, nil, modelAsset("parent-model", "h1"))
	branch := branchTask("branch-task", "branch", 2,
		json.RawMessage(`{"source_task":"parent-task"}`), modelAsset("branch-model", "h2"))
	service, err := New(&fakeTasks{tasks: []generation.Task{parent, branch}}, nil, fakeInputs{}, Config{MaxTasks: 50})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := service.Session(context.Background(), "owner-a", "branch")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.TaskID == "parent-task" {
			t.Fatalf("branch without refs read its ancestor: %+v", graph.Nodes)
		}
	}
}
