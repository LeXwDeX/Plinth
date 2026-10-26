package processing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestPlanDeclarationPreviewAcceptsLocalForwardReverseWithoutFakeSources(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	forward, _ := product.Get("local/static-glb/to-gltf")
	reverse, _ := product.Get("local/static-gltf/to-glb")
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: generation.TextTo3D, State: generation.TaskSucceeded, Offering: generation.Offering{ID: "provider/original", ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model", TaskID: "source", Role: "model", Variant: "glb", StorageKey: "saved", MediaType: "model/gltf-binary", Size: 1024, SHA256: strings.Repeat("a", 64)}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	store := &memoryStore{plans: map[string]Plan{}}
	service, err := New(store, alwaysAvailableCatalog{product}, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model", IdempotencyKey: "declared-preview-chain-183", Steps: []StepRequest{{OfferingID: forward.ID, ExpectedOfferingRevision: forward.Revision, Input: json.RawMessage(`{}`)}, {OfferingID: reverse.ID, ExpectedOfferingRevision: reverse.Revision, Input: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatalf("declared local forward/reverse plan rejected before preparation: %v", err)
	}
	if len(gateway.commands) != 0 || len(gateway.prepared) != 0 {
		t.Fatal("preview prepared or submitted work")
	}
	for _, step := range plan.Steps {
		if string(step.Input) != "{}" {
			t.Fatalf("prediction leaked a fake source: %s", step.Input)
		}
	}
}
