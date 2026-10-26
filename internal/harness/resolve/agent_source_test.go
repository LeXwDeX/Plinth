package resolve

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

type candidateGateway struct {
	*readGateway
	tasks []generation.Task
	next  string
}

func (g *candidateGateway) List(_ context.Context, scope generation.TaskListScope, q generation.ListQuery) (generation.TaskPage, error) {
	if scope.OwnerID() != "owner" || q.SessionID != "session" {
		panic("unscoped candidate scan")
	}
	return generation.TaskPage{Tasks: g.tasks, NextCursor: g.next}, g.err
}
func TestAgentConversationScopeKeepsMultipleRealCandidatesWithoutPickingLatest(t *testing.T) {
	s, store, g := fixture(t)
	second := g.task
	second.ID = "second"
	second.Assets = append([]generation.Asset(nil), g.task.Assets...)
	second.Assets[0].ID = "second-asset"
	second.Assets[0].TaskID = second.ID
	s.Gateway = &candidateGateway{readGateway: g, tasks: []generation.Task{g.task, second}}
	selection := choose(t, s, "remesh")
	json.Unmarshal([]byte(`[{"kind":"task_asset","selection_mode":"agent","parameter":"source_task"}]`), &selection.Sources)
	updated, err := s.Update(context.Background(), "owner", "session", 0, selection)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(snapshot)
	var view struct {
		Candidates []json.RawMessage `json:"source_candidates"`
		Default    json.RawMessage   `json:"default_source"`
	}
	json.Unmarshal(b, &view)
	if snapshot.Status != StatusReady || len(view.Candidates) != 2 || len(view.Default) > 0 || len(snapshot.Input["source_task"]) > 0 || snapshot.Selection.Digest != updated.Digest || store.writes != 1 || g.paid != 0 {
		t.Fatalf("scope not frozen without premature choice: %s", b)
	}
}

func (g *candidateGateway) Get(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	for _, task := range g.tasks {
		if task.ID == id && task.OwnerID == owner {
			return task, g.err
		}
	}
	return generation.Task{}, generation.RepositoryNotFound
}
func TestAgentSourceDefaultRespectsVerifiedCurrentObjectAndTruncation(t *testing.T) {
	s, _, g := fixture(t)
	second := g.task
	second.ID = "second"
	second.Assets = append([]generation.Asset(nil), g.task.Assets...)
	second.Assets[0].ID = "second-asset"
	second.Assets[0].TaskID = second.ID
	gateway := &candidateGateway{readGateway: g, tasks: []generation.Task{g.task, second}}
	s.Gateway = gateway
	selection := choose(t, s, "remesh")
	selection.Sources = []harness.Source{{Kind: harness.SourceTaskAsset, SelectionMode: "agent", Parameter: "source_task"}}
	selection, _ = harness.NormalizeSelection(selection)
	preferred := WithPreferredSources(context.Background(), []harness.Source{{TaskID: "second", AssetID: "second-asset"}})
	snapshot, err := s.ResolveSelection(preferred, selection)
	if err != nil || snapshot.DefaultSource == nil || snapshot.DefaultSource.TaskID != "second" {
		t.Fatal("current object was not preferred", err)
	}
	gateway.tasks = gateway.tasks[:1]
	snapshot, err = s.ResolveSelection(context.Background(), selection)
	if err != nil || snapshot.DefaultSource == nil || snapshot.DefaultSource.TaskID != "model-task" {
		t.Fatal("unique complete scope not recognized", err)
	}
	gateway.next = "more"
	snapshot, err = s.ResolveSelection(context.Background(), selection)
	if err != nil || snapshot.DefaultSource != nil || !snapshot.SourceScanTruncated || snapshot.Status != StatusReady {
		t.Fatal("partial scan invented uniqueness", err)
	}
}
func TestAgentSourceRejectsInvalidOwnerSessionStateAndRoute(t *testing.T) {
	for _, mutate := range []func(*generation.Task){func(t *generation.Task) { t.OwnerID = "foreign" }, func(t *generation.Task) { t.SessionID = "foreign" }, func(t *generation.Task) { t.State = generation.TaskRunning }, func(t *generation.Task) { t.Offering.ProviderID = "other" }, func(t *generation.Task) { t.Assets = nil }} {
		s, _, g := fixture(t)
		task := g.task
		mutate(&task)
		s.Gateway = &candidateGateway{readGateway: g, tasks: []generation.Task{task}}
		selection := choose(t, s, "remesh")
		selection.Sources = []harness.Source{{Kind: harness.SourceTaskAsset, SelectionMode: "agent", Parameter: "source_task"}}
		selection, _ = harness.NormalizeSelection(selection)
		snapshot, err := s.ResolveSelection(context.Background(), selection)
		if err != nil || snapshot.Status != StatusNeedsInput || len(snapshot.SourceCandidates) != 0 || snapshot.CanPropose {
			t.Fatal("invalid source became ready/proposable", err)
		}
	}
}
