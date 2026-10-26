package resolve

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestTaskAssetsFingerprintUsesTheBinderSelectedAsset(t *testing.T) {
	source := generation.Task{ID: "source", State: generation.TaskSucceeded, Capability: generation.FormatConversion, Offering: generation.Offering{ID: "local/forward", ProviderID: "local"}, Assets: []generation.Asset{
		{ID: "preferred-but-incompatible", TaskID: "source", Role: "model", Variant: "glb", StorageKey: "one.glb", MediaType: "model/gltf-binary", Size: 10, SHA256: strings.Repeat("a", 64)},
		{ID: "selected", TaskID: "source", Role: "model", Variant: "gltf", StorageKey: "two.gltf", MediaType: "model/gltf+json", Size: 10, SHA256: strings.Repeat("b", 64)},
	}}
	parameter := generation.Parameter{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask, TaskSourceKind: schema.TaskSourceAssets, TaskOfferings: []generation.OfferingID{"local/forward"}, MediaTypes: []string{"model/gltf+json"}, Extensions: []string{".gltf"}, MaxBytes: 100}
	binding, err := generation.BindModelSource(generation.Offering{ProviderID: "local", Parameters: []generation.Parameter{parameter}}, source, "", []string{"glb"})
	if err != nil {
		t.Fatal(err)
	}
	asset, ok := fingerprintAsset(source, harness.Source{TaskID: "source"}, binding, []string{"glb"})
	if !ok || asset.ID != "selected" || binding.AssetID != asset.ID {
		t.Fatalf("fingerprint changed selected bytes: %+v binding=%+v", asset, binding)
	}
	if _, ok = fingerprintAsset(source, harness.Source{TaskID: "source", AssetID: "preferred-but-incompatible"}, binding, nil); ok {
		t.Fatal("conflicting chosen identity accepted")
	}
}

func TestPrerequisiteProposalKeepsTheFrozenAssetWhenPreferenceChanges(t *testing.T) {
	s, _, g := fixture(t)
	choice := choose(t, s, "animation")
	choice.EnabledOperations = []string{"remesh", "rigging", "animation"}
	choice.AllowPrerequisites = true
	choice.Sources = []harness.Source{{Parameter: "source_task", TaskID: "model-task"}}
	choice.FixedParameters = map[string]json.RawMessage{"action_id": json.RawMessage(`1`)}
	g.task.Assets[0].Variant = "obj"
	selected, err := s.Update(context.Background(), "owner", "session", 0, choice)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ResolveSelection(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.SourceFingerprints) != 1 || snapshot.SourceFingerprints[0].AssetID != "model-asset" {
		t.Fatalf("missing frozen asset: %+v", snapshot)
	}
	other := g.task.Assets[0]
	other.ID = "new-preferred"
	other.Variant = "glb"
	g.task.Assets = append(g.task.Assets, other)
	if _, err = s.Propose(context.Background(), snapshot, []string{"rigging"}); err != nil {
		t.Fatalf("proposal reselected another model: %v", err)
	}
	if g.paid != 0 {
		t.Fatal("source proof triggered execution")
	}
}
