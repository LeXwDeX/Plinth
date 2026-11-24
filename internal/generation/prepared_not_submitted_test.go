package generation_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// #516: a frozen input SubmitPrepared refuses after it confirmed that no Task
// holds the key is marked ErrNotSubmitted, so the Agent turn hands the refusal
// back instead of replaying the same authorization forever. A conflict with a
// Task that does hold the key is a different answer and stays unmarked.
func TestSubmitPreparedMarksRefusalsBeforeAnyTaskWrite516(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	base, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a",
		OwnerName: "Owner", Title: "stale", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	asset, err := h.inputs.Upload(ctx, "owner-a", bytes.NewReader([]byte("glTF source")), "model/gltf-binary", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &changingInputResolver{InputAssetResolver: h.inputs}
	module := preparedModuleWith(t, h.repository, driftCatalog{Catalog: base}, resolver)
	prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.Rigging, OfferingID: "meshy/processing/rigging",
		Input: []byte(`{"source_asset":"` + string(asset.ID) + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	notSubmitted := func(name string, module *generation.Module, key string, code generation.ErrorCode) {
		t.Helper()
		_, err := module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, prepared, key))
		if !generation.IsCode(err, code) || !errors.Is(err, generation.ErrNotSubmitted) {
			t.Fatalf("%s = %v, want %s marked as not submitted", name, err, code)
		}
	}

	upgraded, err := generation.New(generation.Dependencies{Repository: h.repository, Catalog: driftCatalog{Catalog: base},
		Conversions: generation.NoConversions, Providers: preparedRegistry(t), Assets: failingAssetStore{},
		Inputs: resolver, SchemaVersion: "catalog-test-v2"})
	if err != nil {
		t.Fatal(err)
	}
	notSubmitted("changed Product Schema", upgraded, "schema-drift", generation.ErrConflict)
	notSubmitted("changed Offering revision",
		preparedModuleWith(t, h.repository, driftCatalog{Catalog: base, drift: true}, resolver), "revision-drift", generation.ErrConflict)
	withoutAdapter, err := generation.New(generation.Dependencies{Repository: h.repository, Catalog: driftCatalog{Catalog: base},
		Conversions: generation.NoConversions, Providers: providers.NewRegistry(), Assets: failingAssetStore{},
		Inputs: resolver, SchemaVersion: "catalog-test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	notSubmitted("missing adapter", withoutAdapter, "adapter-gone", generation.ErrUnavailable)
	resolver.changed = true
	notSubmitted("changed asset metadata", module, "asset-drift", generation.ErrConflict)
	resolver.changed, resolver.changedBytes = false, true
	notSubmitted("changed asset bytes", module, "asset-byte-drift", generation.ErrConflict)
	resolver.changedBytes = false

	// The Task accepted under this key is the answer of every replay; another
	// frozen input under the same key conflicts with that Task and is not a
	// refusal before a write.
	command := authorizePrepared(t, ctx, h.repository, prepared, "accepted-key")
	if _, err := module.SubmitPrepared(ctx, command); err != nil {
		t.Fatal(err)
	}
	second, err := h.inputs.Upload(ctx, "owner-a", bytes.NewReader([]byte("another glTF source")), "model/gltf-binary", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	other, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.Rigging, OfferingID: "meshy/processing/rigging",
		Input: []byte(`{"source_asset":"` + string(second.ID) + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	otherCommand := command
	otherCommand.PreparedInputID = other.ID
	if _, err := module.SubmitPrepared(ctx, otherCommand); !generation.IsCode(err, generation.ErrConflict) ||
		errors.Is(err, generation.ErrNotSubmitted) {
		t.Fatalf("conflict with the accepted Task = %v, want an unmarked conflict", err)
	}
	if replayed, err := upgraded.SubmitPrepared(ctx, command); err != nil || !replayed.Replayed {
		t.Fatalf("accepted Task replay after a Schema change = %#v, %v", replayed, err)
	}
}

func preparedRegistry(t *testing.T) *providers.Registry {
	t.Helper()
	registry := providers.NewRegistry()
	for _, key := range []string{"openai-images.v1", "meshy.v2"} {
		if err := registry.Register(key, mock.New()); err != nil {
			t.Fatal(err)
		}
	}
	return registry
}
