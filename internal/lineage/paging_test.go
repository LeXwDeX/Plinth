package lineage

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestSessionPageCursorAndPendingParent(t *testing.T) {
	ctx := context.Background()
	svc := service(t, []generation.Task{
		task("new", 3, processOffering, input(t, map[string]any{"source_task": "old"}), modelAsset("new-a", "new")),
		task("middle", 2, processOffering, nil, modelAsset("middle-a", "middle")),
		task("old", 1, processOffering, nil, modelAsset("old-a", "old")),
	}, nil, fakeInputs{})
	first, err := svc.SessionPage(ctx, "owner-a", "session-1", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Nodes) != 1 || first.Nodes[0].ID != "new" || first.Nodes[0].Version != 3 || !first.Truncated || first.NextCursor == "" {
		t.Fatalf("first = %#v", first)
	}
	if len(first.Edges) != 1 || first.Edges[0].From != "old" || first.Edges[0].Evidence != EvidenceRecorded {
		t.Fatalf("cross-page parent = %#v", first.Edges)
	}
	second, err := svc.SessionPage(ctx, "owner-a", "session-1", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.Nodes[0].ID != "middle" || second.Nodes[0].Version != 2 {
		t.Fatalf("second = %#v", second)
	}
	third, err := svc.SessionPage(ctx, "owner-a", "session-1", second.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if third.Nodes[0].ID != "old" || third.Nodes[0].Version != 1 || third.NextCursor != "" || third.Truncated {
		t.Fatalf("third = %#v", third)
	}
	empty, err := svc.SessionPage(ctx, "owner-a", "session-1", encodePageCursor("owner-a", "session-1", "3"), 1)
	if err != nil || len(empty.Nodes) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty = %#v, %v", empty, err)
	}
}

func TestSessionPageRejectsMalformedAndCrossScopeCursor(t *testing.T) {
	svc := service(t, nil, nil, fakeInputs{})
	for _, cursor := range []string{"!broken", encodePageCursor("other", "session-1", "1"), encodePageCursor("owner-a", "other", "1")} {
		if _, err := svc.SessionPage(context.Background(), "owner-a", "session-1", cursor, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("cursor %q: %v", cursor, err)
		}
	}
	foreign := task("foreign", 1, processOffering, nil, modelAsset("foreign-a", "f"))
	foreign.OwnerID = "other"
	foreignSession := task("another-session", 2, processOffering, nil, modelAsset("other-a", "s"))
	foreignSession.SessionID = "other"
	child := task("child", 3, processOffering, input(t, map[string]any{"source_task": "foreign"}), modelAsset("child-a", "c"))
	graph, err := service(t, []generation.Task{child, foreign, foreignSession}, nil, fakeInputs{}).SessionPage(context.Background(), "owner-a", "session-1", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Edges[0].Evidence != EvidenceUnknown || graph.Edges[0].From == "foreign" {
		t.Fatalf("foreign relation = %#v", graph.Edges)
	}
	for _, node := range graph.Nodes {
		if node.TaskID == "foreign" || node.TaskID == "another-session" {
			t.Fatal("foreign node leaked")
		}
	}
}

func TestSessionPagePreservesListErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cause   error
		invalid bool
	}{
		{"invalid cursor", &generation.Error{Code: generation.ErrInvalidInput}, true},
		{"persistence failure", errors.New("database unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := &generation.Error{Code: generation.ErrUnavailable, Cause: tc.cause}
			svc, err := New(&fakeTasks{err: wrapped}, nil, fakeInputs{}, Config{MaxTasks: 10})
			if err != nil {
				t.Fatal(err)
			}
			_, err = svc.SessionPage(context.Background(), "owner-a", "session-1", encodePageCursor("owner-a", "session-1", "opaque"), 1)
			if errors.Is(err, ErrInvalid) != tc.invalid {
				t.Fatalf("invalid = %v, error = %v", tc.invalid, err)
			}
			if !tc.invalid && err != wrapped {
				t.Fatalf("persistence error was changed: %v", err)
			}
		})
	}
}

func TestPagedAttemptsUseFrozenSourcesAndDoNotOccupyVersions(t *testing.T) {
	parent := task("parent", 1, processOffering, nil, modelAsset("parent-a", "p"))
	failed := task("failed", 2, processOffering, input(t, map[string]any{"source_asset": "parent-a"}))
	failed.State = generation.TaskFailed
	failed.Failure = &generation.Failure{Class: generation.FailureInternal, SafeDetail: "do not expose"}
	retry := task("retry", 3, processOffering, input(t, map[string]any{"source_task": "parent"}))
	retry.State, retry.Progress = generation.TaskRunning, 45
	svc := service(t, []generation.Task{retry, failed, parent}, nil, fakeInputs{})
	graph, err := svc.SessionPage(context.Background(), "owner-a", "session-1", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("nodes = %#v", graph.Nodes)
	}
	for _, id := range []string{"failed", "retry"} {
		node, ok := nodeOf(graph, id)
		if !ok || node.Version != 0 || len(node.Assets) != 0 {
			t.Fatalf("attempt = %#v", node)
		}
		edges := edgeOf(graph, id)
		if len(edges) != 1 || edges[0].From != "parent" || edges[0].Evidence != EvidenceRecorded {
			t.Fatalf("parent = %#v", edges)
		}
	}
	source, _ := nodeOf(graph, "parent")
	if source.Version != 1 {
		t.Fatalf("attempts renumbered source: %#v", source)
	}
	legacy, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil || len(legacy.Nodes) != 1 || legacy.Nodes[0].State != "" || legacy.Nodes[0].Version != 0 {
		t.Fatalf("legacy changed: %#v %v", legacy, err)
	}
}

// #326 L: the Agent's tool results name a model by the number the version
// strip shows; SessionVersions gives the same numbers as SessionPage.
func TestSessionVersionsMatchThePagedVersionNumbers(t *testing.T) {
	first := task("first", 1, processOffering, nil, modelAsset("first-a", "f"))
	failed := task("failed", 2, processOffering, input(t, map[string]any{"source_asset": "first-a"}))
	failed.State = generation.TaskFailed
	second := task("second", 3, processOffering, input(t, map[string]any{"source_task": "first"}), modelAsset("second-a", "s"))
	svc := service(t, []generation.Task{second, failed, first}, nil, fakeInputs{})
	graph, err := svc.SessionPage(context.Background(), "owner-a", "session-1", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := svc.SessionVersions(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		node, ok := nodeOf(graph, id)
		if !ok || node.Version == 0 || versions[generation.TaskID(id)] != node.Version {
			t.Fatalf("%s: page version %d, tool version %d", id, node.Version, versions[generation.TaskID(id)])
		}
	}
	if versions["first"] != 1 || versions["second"] != 2 || versions["failed"] != 0 {
		t.Fatalf("versions = %v", versions)
	}
	if _, err := svc.SessionVersions(context.Background(), "", "session-1"); err == nil {
		t.Fatal("an empty owner is accepted")
	}
}
