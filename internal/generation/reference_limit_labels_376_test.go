package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// #376: a direct preparation that gives a model more pictures than it takes
// keeps the first ones (the Agent's tools do the same before they build the
// input). The marks of the pictures, one per picture by position, are cut to the
// same count, so the Hyper3D Adapter's rule, one mark per picture or none, holds
// on this path as well as on the Agent's. The Adapter here is the real one: it
// is what refuses a list that stops matching.

const labelledOffering = "hyper3d/rodin-gen-2.5/image-to-3d"

func TestDirectPreparationCutsTheMarksWithThePictures(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	base, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner", Title: "marks", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for index := 0; index < 7; index++ {
		asset, err := h.inputs.Upload(ctx, "owner-a", bytes.NewReader([]byte(fmt.Sprintf("image-%d", index))), "image/png", 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, string(asset.ID))
	}
	registry := providers.NewRegistry()
	for _, connection := range base.Application().Runtime.ProviderConnections {
		if connection.ID != "hyper3d" {
			continue
		}
		adapter, err := hyper3d.New(hyper3d.Config{
			BaseURL: "http://127.0.0.1:1", APIKey: "synthetic-key", AuthHeader: connection.AuthHeader, AuthPrefix: connection.AuthPrefix,
			StatusMapping: connection.StatusMapping, ErrorMapping: connection.ErrorMapping, InputAssets: h.inputs,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.Register("hyper3d.rodin.v1", adapter); err != nil {
			t.Fatal(err)
		}
	}
	module, err := generation.New(generation.Dependencies{Repository: h.repository, Catalog: base, Providers: registry,
		Assets: failingAssetStore{}, Inputs: h.inputs, SchemaVersion: "catalog-test-v1"})
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(keep bool, pictures int, marks []string) (generation.PreparedInput, error) {
		input := map[string]any{"prompt": "a small robot", "authorize_extra_credits": true, "reference_assets": ids[:pictures]}
		if marks != nil {
			input["reference_image_labels"] = marks
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a",
			Capability: generation.ImageTo3D, OfferingID: labelledOffering, Input: encoded, KeepFirstReferences: keep})
	}
	frozen := func(prepared generation.PreparedInput) (pictures, marks []string) {
		var values struct {
			Pictures []string `json:"reference_assets"`
			Marks    []string `json:"reference_image_labels"`
		}
		if err := json.Unmarshal(prepared.NormalizedInput, &values); err != nil {
			t.Fatal(err)
		}
		return values.Pictures, values.Marks
	}
	sevenMarks := []string{"F", "FL", "FR", "B", "BL", "BR", "L"}

	// Strict: two more pictures than the model takes are refused, marks or not.
	if _, err := prepare(false, 7, sevenMarks); !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("strict preparation of seven pictures = %v, want invalid_input", err)
	}

	// Kept: the first five pictures and the first five marks, and the note.
	prepared, err := prepare(true, 7, sevenMarks)
	if err != nil {
		t.Fatalf("seven pictures with seven marks were refused: %v", err)
	}
	pictures, marks := frozen(prepared)
	if !slices.Equal(pictures, ids[:5]) || !slices.Equal(marks, sevenMarks[:5]) {
		t.Fatalf("frozen pictures %v and marks %v, want the first five of each", pictures, marks)
	}
	if prepared.ReferenceNote == "" {
		t.Error("the cut of the pictures was not reported")
	}

	// One mark fewer than the pictures is still more than the model takes: the
	// marks that remain are the first five, one for each remaining picture.
	prepared, err = prepare(true, 7, sevenMarks[:6])
	if err != nil {
		t.Fatalf("seven pictures with six marks were refused: %v", err)
	}
	if pictures, marks = frozen(prepared); !slices.Equal(pictures, ids[:5]) || !slices.Equal(marks, sevenMarks[:5]) {
		t.Fatalf("frozen pictures %v and marks %v", pictures, marks)
	}

	// A short list stays as it is; the Adapter completes it with the official
	// unknown mark when it sends the request (#460 C31), so preparation does
	// not refuse it.
	prepared, err = prepare(true, 7, sevenMarks[:3])
	if err != nil {
		t.Fatalf("seven pictures with three marks were refused: %v", err)
	}
	if pictures, marks = frozen(prepared); !slices.Equal(pictures, ids[:5]) || !slices.Equal(marks, sevenMarks[:3]) {
		t.Fatalf("frozen pictures %v and marks %v", pictures, marks)
	}

	// No marks: nothing is added, and the pictures are cut as before.
	prepared, err = prepare(true, 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pictures, marks = frozen(prepared); !slices.Equal(pictures, ids[:5]) || marks != nil {
		t.Fatalf("frozen pictures %v and marks %v, want five pictures and no marks", pictures, marks)
	}

	// Within the limit nothing is cut.
	prepared, err = prepare(true, 3, sevenMarks[:3])
	if err != nil {
		t.Fatal(err)
	}
	if pictures, marks = frozen(prepared); !slices.Equal(pictures, ids[:3]) || !slices.Equal(marks, sevenMarks[:3]) || prepared.ReferenceNote != "" {
		t.Fatalf("frozen pictures %v and marks %v", pictures, marks)
	}
}
