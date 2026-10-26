package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

type manifestOutputProbe struct {
	output generation.PreparedContextOutput
}

func (*manifestOutputProbe) VerifyPreparedContext(context.Context, generation.OwnerID, string, string, string, string) error {
	return nil
}
func (p *manifestOutputProbe) VerifiedPreparedOutput(context.Context, generation.OwnerID, string, string, string, string) (generation.PreparedContextOutput, error) {
	return p.output, nil
}

func TestPreparedManifestMustBeExactProviderInput(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	_, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a",
		OwnerName: "Owner", Title: "manifest", CreatedAt: h.clock.Now(), UpdatedAt: h.clock.Now()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := h.inputs.Upload(ctx, "owner-a", bytes.NewReader([]byte("image-a")), "image/png", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.inputs.Upload(ctx, "owner-a", bytes.NewReader([]byte("image-b")), "image/png", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("openai-images.v1", mock.New()); err != nil {
		t.Fatal(err)
	}
	proof := &manifestOutputProbe{output: generation.PreparedContextOutput{
		AssetID: a.ID, SHA256: a.SHA256, Size: a.Size}}
	module, err := generation.New(generation.Dependencies{Repository: h.repository, Catalog: catalogue,
		Providers: registry, Assets: failingAssetStore{}, Inputs: h.inputs,
		PreparedContext: proof, SchemaVersion: "catalog-test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	command := generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.ImageToImage, OfferingID: "openai/gpt-image-2/image-to-image",
		SceneID: "session-a", ManifestID: "render-a", ManifestHash: a.SHA256}
	input := func(ids ...generation.AssetID) json.RawMessage {
		encoded, _ := json.Marshal(map[string]any{"prompt": "render this", "reference_assets": ids})
		return encoded
	}
	command.Input = input(b.ID)
	if _, err := module.PrepareInput(ctx, command); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("manifest A attested unrelated B: %v", err)
	}
	command.Input = input(a.ID, b.ID)
	if _, err := module.PrepareInput(ctx, command); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("manifest A attested extra input: %v", err)
	}
	command.Input = input()
	if _, err := module.PrepareInput(ctx, command); err == nil {
		t.Fatal("manifest attested absent image input")
	}
	command.Input = input(a.ID)
	prepared, err := module.PrepareInput(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	proof.output = generation.PreparedContextOutput{AssetID: b.ID, SHA256: b.SHA256, Size: b.Size}
	if _, err := module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, prepared, "manifest-drift")); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("changed manifest output admitted paid task: %v", err)
	}
}
