package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

type driftCatalog struct {
	generation.Catalog
	drift bool
}

func (c driftCatalog) Normalize(cap generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	offering, normalized, err := c.Catalog.Normalize(cap, id, input)
	if c.drift {
		offering.Revision += ".changed"
	}
	return offering, normalized, err
}
func (c driftCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	offering, ok := c.Catalog.Get(id)
	if c.drift {
		offering.Revision += ".changed"
	}
	return offering, ok
}

type changingInputResolver struct {
	generation.InputAssetResolver
	changed      bool
	changedBytes bool
}

func (r *changingInputResolver) Resolve(ctx context.Context, owner generation.OwnerID, offering generation.Offering, input json.RawMessage) (map[string][]generation.InputAsset, error) {
	assets, err := r.InputAssetResolver.Resolve(ctx, owner, offering, input)
	if r.changed {
		for name := range assets {
			for i := range assets[name] {
				assets[name][i].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
			}
		}
	}
	return assets, err
}
func (r *changingInputResolver) Open(ctx context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	if r.changedBytes {
		return io.NopCloser(bytes.NewReader([]byte("tampered bytes"))), nil
	}
	return r.InputAssetResolver.Open(ctx, asset)
}

func preparedModuleWith(t *testing.T, repository generation.Repository, catalogue generation.Catalog, inputs generation.InputAssetResolver) *generation.Module {
	t.Helper()
	registry := providers.NewRegistry()
	for _, key := range []string{"openai-images.v1", "meshy.v2"} {
		if err := registry.Register(key, mock.New()); err != nil {
			t.Fatal(err)
		}
	}
	module, err := generation.New(generation.Dependencies{Repository: repository, Catalog: catalogue,
		Providers: registry, Assets: failingAssetStore{}, Inputs: inputs, SchemaVersion: "catalog-test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestPreparedInputIsImmutableOwnerScopedAndReplaysOriginalTask(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	now := h.clock.Now()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner", Title: "prepared", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"a brass robot"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ID == "" || len(prepared.InputHash) != 64 || prepared.OfferingRevision == "" || prepared.SchemaVersion == "" {
		t.Fatalf("incomplete prepared input: %#v", prepared)
	}
	if _, err := h.module.GetPreparedInput(ctx, "owner-b", prepared.ID); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("cross owner read = %v", err)
	}
	command := authorizePrepared(t, ctx, h.repository, prepared, "prepared-key")
	accepted, err := h.module.SubmitPrepared(ctx, command)
	if err != nil || accepted.Replayed {
		t.Fatalf("first submit = %#v, %v", accepted, err)
	}
	replayed, err := h.module.SubmitPrepared(ctx, command)
	if err != nil || !replayed.Replayed || replayed.TaskID != accepted.TaskID {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}
	other, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"different"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	otherCommand := command
	otherCommand.PreparedInputID = other.ID
	if _, err := h.module.SubmitPrepared(ctx, otherCommand); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("same key with changed input = %v", err)
	}
	if _, err := h.module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{OwnerID: "owner-b",
		PreparedInputID: prepared.ID, IdempotencyKey: "foreign"}); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("cross owner submit = %v", err)
	}
	if _, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a",
		Capability: generation.TextToImage, OfferingID: "openai/gpt-image-2/text-to-image",
		Input: []byte(`{"prompt":"paint"}`), SceneID: "unverified-scene"}); !generation.IsCode(err, generation.ErrUnavailable) {
		t.Fatalf("unverified scene identity = %v", err)
	}
}

func TestPreparedInputRejectsRevisionAndAssetDriftBeforeNewPaidTask(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	base, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner", Title: "drift", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	asset, err := h.inputs.Upload(ctx, "owner-a", bytes.NewReader([]byte("glTF source")), "model/gltf-binary", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &changingInputResolver{InputAssetResolver: h.inputs}
	module := preparedModuleWith(t, h.repository, driftCatalog{Catalog: base}, resolver)
	if _, err := module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: "session-a", Capability: generation.Rigging,
		OfferingID: "meshy/processing/rigging",
		Input:      []byte(`{"source_url":"https://example.test/mutable.glb"}`),
	}); !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("mutable source URL was prepared without byte evidence: %v", err)
	}
	prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.Rigging, OfferingID: "meshy/processing/rigging",
		Input: []byte(`{"source_asset":"` + string(asset.ID) + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Assets) != 1 || prepared.Assets[0].ID != asset.ID || prepared.Assets[0].SHA256 != asset.SHA256 {
		t.Fatalf("asset snapshot = %#v", prepared.Assets)
	}
	resolver.changed = true
	if _, err := module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, prepared, "asset-drift")); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("changed asset metadata = %v", err)
	}
	resolver.changed = false
	resolver.changedBytes = true
	if _, err := module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, prepared, "asset-byte-drift")); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("changed asset bytes = %v", err)
	}
	resolver.changedBytes = false
	drift := preparedModuleWith(t, h.repository, driftCatalog{Catalog: base, drift: true}, resolver)
	if _, err := drift.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, prepared, "revision-drift")); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("offering revision drift = %v", err)
	}
	stableCommand := authorizePrepared(t, ctx, h.repository, prepared, "stable-key")
	accepted, err := module.SubmitPrepared(ctx, stableCommand)
	if err != nil {
		t.Fatal(err)
	}
	// A task accepted under the frozen revision remains recoverable even when
	// today's catalog no longer offers that revision or the source asset drifts.
	resolver.changed = true
	replayed, err := drift.SubmitPrepared(ctx, stableCommand)
	if err != nil || !replayed.Replayed || replayed.TaskID != accepted.TaskID {
		t.Fatalf("original task replay after drift = %#v, %v", replayed, err)
	}
}

func TestPreparedInputFreezesOwnedSession(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	now := time.Now().UTC()
	_, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "chat", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	base := generation.PrepareInputCommand{SessionID: "session-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"paint"}`)}
	foreign := base
	foreign.OwnerID = "owner-b"
	if _, err := h.module.PrepareInput(ctx, foreign); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("foreign session prepare = %v", err)
	}
	base.OwnerID = "owner-a"
	prepared, err := h.module.PrepareInput(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{OwnerID: "owner-a",
		SessionID: "session-b", PreparedInputID: prepared.ID, IdempotencyKey: "session-key"}); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("changed session submit = %v", err)
	}
	accepted, err := h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, prepared, "session-key"))
	if err != nil || accepted.TaskID == "" {
		t.Fatalf("owned session submit = %#v, %v", accepted, err)
	}
}
