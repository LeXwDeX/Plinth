package chat_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

type harnessTaskGateway struct{ tasks []generation.Task }

func (g harnessTaskGateway) OfferingAvailable(generation.OfferingID) bool { return true }
func (g harnessTaskGateway) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	for _, task := range g.tasks {
		if task.ID == id && task.OwnerID == owner {
			return task, nil
		}
	}
	return generation.Task{}, generation.RepositoryNotFound
}
func (g harnessTaskGateway) List(_ context.Context, scope generation.TaskListScope, q generation.ListQuery) (generation.TaskPage, error) {
	out := generation.TaskPage{}
	for _, task := range g.tasks {
		if task.OwnerID == scope.OwnerID() && task.SessionID == q.SessionID {
			out.Tasks = append(out.Tasks, task)
		}
	}
	return out, nil
}

func TestAgentSourceSnapshotUsesVerifiedAppliedSelectionOnly(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "unverified", true: "verified"}[valid], func(t *testing.T) {
			verifier := &recordingVerifier{held: map[string]map[string]bool{}, models: map[string]bool{"two": true}}
			service, _, _ := newHandleService(t, verifier)
			session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "Agent source")
			if err != nil {
				t.Fatal(err)
			}
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			o, _ := c.Get("meshy/processing/remesh")
			selection := harness.DefaultSelection("owner-a", session.ID)
			selection.OperationID = "remesh"
			selection.EnabledOperations = []string{"remesh"}
			selection.OfferingID = string(o.ID)
			selection.OfferingRevision = o.Revision
			selection.Sources = []harness.Source{{Kind: harness.SourceTaskAsset, SelectionMode: "agent", Parameter: "source_task"}}
			selection, _ = harness.NormalizeSelection(selection)
			gateway := harnessTaskGateway{}
			for _, id := range []string{"one", "two"} {
				gateway.tasks = append(gateway.tasks, generation.Task{ID: generation.TaskID(id), OwnerID: "owner-a", SessionID: session.ID, State: generation.TaskSucceeded, Capability: generation.TextTo3D, Offering: generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ProviderID: "meshy"}, Assets: []generation.Asset{{ID: generation.AssetID(id + "-glb"), TaskID: generation.TaskID(id), Role: "model", Variant: "glb", StorageKey: "private", SHA256: "hash", Size: 100, MediaType: "model/gltf-binary"}}})
			}
			verifier.held["owner-a/"+session.ID] = map[string]bool{"two": valid, "two-glb": valid}
			service.SetAttachmentVerifier(verifier)
			resolver := resolve.Service{Schema: c.Application().Chat.Harness, Tools: c.Application().Chat.Tools, Catalog: c, Gateway: gateway}
			service.SetHarnessSnapshotResolver(func(ctx context.Context, _, _ string) (resolve.Snapshot, error) {
				if len(verifier.calls) == 0 {
					t.Fatal("Harness frozen before attachment verification")
				}
				return resolver.ResolveSelection(ctx, selection)
			})
			assistant, err := service.StartTurn(context.Background(), chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "use current", Selection: []chat.AttachmentRef{{Kind: chat.AttachmentKindModel, TaskID: "two", AssetID: "two-glb"}}, Options: chat.TurnOptions{Selection: []chat.Attachment{{Kind: chat.AttachmentKindModel, TaskID: "one", AssetID: "one-glb"}}}})
			if err != nil {
				t.Fatal(err)
			}
			turn, err := service.GetTurn(context.Background(), "owner-a", session.ID, assistant.ID)
			if err != nil {
				t.Fatal(err)
			}
			if valid {
				if turn.Options.Harness.DefaultSource == nil || turn.Options.Harness.DefaultSource.TaskID != "two" {
					t.Fatal("verified current object not frozen")
				}
			} else if turn.Options.Harness.DefaultSource != nil {
				t.Fatal("unverified or client options source became a default")
			}
		})
	}
}
