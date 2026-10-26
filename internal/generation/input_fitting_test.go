package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// replacingFitter stands in for the input asset service's Fit: it swaps one
// asset for its re-encoded copy, or refuses.
type replacingFitter struct {
	from, to generation.AssetID
	err      error
	calls    int
}

func (fitter *replacingFitter) Fit(_ context.Context, _ generation.OwnerID, _ generation.Offering, input json.RawMessage) (json.RawMessage, error) {
	fitter.calls++
	if fitter.err != nil {
		return nil, fitter.err
	}
	return bytes.ReplaceAll(input, []byte(fitter.from), []byte(fitter.to)), nil
}

func referenceIDs(t *testing.T, input json.RawMessage) []generation.AssetID {
	t.Helper()
	var values struct {
		References []generation.AssetID `json:"reference_assets"`
	}
	if err := json.Unmarshal(input, &values); err != nil {
		t.Fatal(err)
	}
	return values.References
}

// #276: the kernel lets the InputFitter replace an image over the Offering's
// limit before inputs are resolved and frozen; the frozen input is submitted
// as is, and a refusal stops before anything is prepared. Direct Submit calls
// the same step (module.go) but needs an admission these fixtures do not
// grant.
func TestKernelFreezesFittedInput(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a",
		OwnerName: "Owner", Title: "fit", CreatedAt: h.clock.Now(), UpdatedAt: h.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	large, err := h.inputs.Upload(ctx, "owner-a", bytes.NewReader([]byte("large-image")), "image/png", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	fitted, err := h.inputs.Upload(ctx, "owner-a", bytes.NewReader([]byte("fitted-image")), "image/png", 1<<20)
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
	fitter := &replacingFitter{from: large.ID, to: fitted.ID}
	module, err := generation.New(generation.Dependencies{Repository: h.repository, Catalog: catalogue,
		Providers: registry, Assets: h.assets, Inputs: h.inputs, InputFitter: fitter,
		Clock: h.clock, SchemaVersion: "catalog-test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	input, _ := json.Marshal(map[string]any{"prompt": "fix the door", "reference_assets": []generation.AssetID{large.ID}})

	prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.ImageToImage, OfferingID: "openai/gpt-image-2/image-to-image", Input: input})
	if err != nil {
		t.Fatal(err)
	}
	if got := referenceIDs(t, prepared.NormalizedInput); !slices.Equal(got, []generation.AssetID{fitted.ID}) {
		t.Fatalf("prepared references = %v, want the fitted copy", got)
	}
	// SubmitPrepared submits the frozen input; fitting it again finds nothing
	// over a limit and leaves it, so the prepared hash still matches.
	submitted, err := module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, prepared, "fit-prepared"))
	if err != nil {
		t.Fatalf("frozen fitted input refused: %v", err)
	}
	if task, _ := module.Get(ctx, "owner-a", submitted.TaskID); !slices.Equal(referenceIDs(t, task.Input), []generation.AssetID{fitted.ID}) {
		t.Fatalf("prepared task input = %s", task.Input)
	}

	refusal := &generation.Error{Code: generation.ErrInvalidInput, Operation: "fit_input_assets", Detail: "configured refusal"}
	fitter.err = refusal
	if _, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.ImageToImage, OfferingID: "openai/gpt-image-2/image-to-image", Input: input}); !errors.Is(err, refusal) {
		t.Fatalf("prepare refusal = %v", err)
	}
}
