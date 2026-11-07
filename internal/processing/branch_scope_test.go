package processing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// branchGateway is the ordinary processing fake plus the read-only branch
// ancestry scope. The stored source Task stays in its ancestor session; the
// plan's own session is the branch.
type branchGateway struct {
	*gatewayFake
	allowed map[string]bool
}

func (g *branchGateway) TaskInSession(_ context.Context, owner generation.OwnerID, session string, task generation.TaskID) (bool, error) {
	return owner == "owner" && g.allowed[session+"\x00"+string(task)], nil
}

func TestProcessingPlanAcceptsAnAncestorSourceInABranch(t *testing.T) {
	for _, test := range []struct {
		name    string
		session string
		allowed bool
	}{
		{"direct parent", "branch", true},
		{"grandparent", "grandbranch", true},
		{"sibling", "sibling", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "parent",
				Capability: "text-to-3d", State: generation.TaskSucceeded,
				Offering: generation.Offering{ProviderID: "meshy"},
				Assets:   []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
			catalog := memoryCatalog{
				"remesh": generation.Offering{ID: "remesh", Revision: "v1", ProviderID: "meshy",
					Capability: "remesh", Domain: generation.DomainProcessing, Enabled: true,
					Parameters: []generation.Parameter{{Name: "source_task", Type: "task",
						TaskCapabilities: []generation.Capability{"text-to-3d"}}}},
			}
			gateway := &branchGateway{
				gatewayFake: &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source},
					byKey: map[string]generation.TaskID{}},
				allowed: map[string]bool{},
			}
			if test.allowed {
				gateway.allowed[test.session+"\x00source"] = true
			}
			service, err := New(&memoryStore{plans: map[string]Plan{}}, catalog, gateway, nil)
			if err != nil {
				t.Fatal(err)
			}
			request := CreateCommand{OwnerID: "owner", SessionID: test.session,
				SourceTaskID: "source", SourceAssetID: "model", IdempotencyKey: "branch-source-plan-key",
				Steps: []StepRequest{{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}}}
			_, err = service.Create(context.Background(), request)
			if test.allowed {
				if err != nil {
					t.Fatalf("ancestor source rejected: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("sibling source accepted: %v", err)
			}
		})
	}
}
