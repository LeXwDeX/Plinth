package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #334 (4): from the second step on, a plan binds the previous step's result
// itself. Meshy Rigging delivers the rigged FBX before the rigged GLB (both
// required); Meshy Resize takes a source file and accepts both media types.
// The step must get the GLB — the format the chat tool and the panel pick for
// a source by the Schema's source_variant_preference — not whichever file the
// Provider listed first.
func TestLaterPlanStepBindsThePreferredModelFormat(t *testing.T) {
	ctx := context.Background()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	revision := func(id generation.OfferingID) string {
		offering, ok := product.Get(id)
		if !ok {
			t.Fatalf("offering %s missing from the product catalog", id)
		}
		return offering.Revision
	}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ProviderID: "meshy"},
		Assets:   []generation.Asset{{ID: "model", Role: "model", Required: true, StorageKey: "ready", MediaType: "model/gltf-binary", Size: 1024}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	service, err := New(&memoryStore{plans: map[string]Plan{}}, alwaysAvailableCatalog{product}, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.WithSourcePreference(catalog.ProcessingSourcePreference(product.Application().Chat.Tools))
	if _, err := service.Create(ctx, CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: "rig-then-resize-plan-key",
		Steps: []StepRequest{
			{OfferingID: "meshy/processing/rigging", ExpectedOfferingRevision: revision("meshy/processing/rigging"), Input: json.RawMessage(`{}`)},
			{OfferingID: "meshy/processing/resize", ExpectedOfferingRevision: revision("meshy/processing/resize"), Input: json.RawMessage(`{"resize_height":1}`)},
		}}); err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 1 {
		t.Fatalf("first step submits = %d", len(gateway.commands))
	}
	rigged := gateway.tasks["step-1"]
	rigged.State = generation.TaskSucceeded
	rigged.Offering, _ = product.Get("meshy/processing/rigging")
	rigged.Assets = []generation.Asset{
		{ID: "rigged-fbx", Role: "model", Variant: "rigged_character_fbx_url", Required: true, Ordinal: 0,
			StorageKey: "ready", MediaType: "application/octet-stream", Size: 2048},
		{ID: "rigged-glb", Role: "model", Variant: "rigged_character_glb_url", Required: true, Ordinal: 1,
			StorageKey: "ready", MediaType: "model/gltf-binary", Size: 2048},
	}
	gateway.tasks[rigged.ID] = rigged
	// One pass records the first step's success; the next submits the second.
	for pass := 0; pass < 2; pass++ {
		if err := advanceApproved(ctx, service); err != nil {
			t.Fatal(err)
		}
	}
	if len(gateway.commands) != 2 {
		plans, _ := service.List(ctx, "owner", "session")
		t.Fatalf("second step submits = %d; plans %+v", len(gateway.commands), plans)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(gateway.commands[1].Input, &input); err != nil {
		t.Fatal(err)
	}
	if got := string(input["source_asset"]); got != `"rigged-glb"` {
		t.Fatalf("second step bound source_asset %s, want the rigged GLB; input %s", got, gateway.commands[1].Input)
	}
}
