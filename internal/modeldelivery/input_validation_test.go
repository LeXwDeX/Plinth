package modeldelivery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestReadOnlySourceValidationRejectsBeforeCompletionPlan(t *testing.T) {
	f := newFixture(t)
	f.meshyText("source", "owner")
	f.unavailable["meshy/processing/convert"] = true
	// This test isolates the local validator; the first unavailable route in
	// the full catalog legitimately supplies its own reason text.
	for _, conversion := range f.service.schema.Conversions {
		if conversion.OfferingID == "local/static-glb/to-gltf" {
			f.service.schema.Conversions = []catalog.ModelConversionSchema{conversion}
			break
		}
	}
	f.inputValidation = func(_ context.Context, owner generation.OwnerID, id generation.OfferingID, revision string, input json.RawMessage) error {
		var values map[string]string
		if json.Unmarshal(input, &values) != nil || owner != "owner" || id != "local/static-glb/to-gltf" || revision == "" || values["source_asset"] != "source-a0" {
			t.Fatal("source check lost exact owned binding")
		}
		return &generation.Error{Code: generation.ErrUnsupportedModelInput}
	}
	for i := 0; i < 2; i++ {
		coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "source")
		if err != nil {
			t.Fatal(err)
		}
		row := formatByID(t, coverage, "gltf")
		if row.Status != StatusUnavailable || row.Reason != reasonSourceIncompatible || coverage.CanComplete {
			t.Fatalf("unsupported input advertised: %+v", row)
		}
	}
	if _, err := f.service.Complete(context.Background(), "owner", "source"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("rejected completion: %v", err)
	}
	if f.validationCalls != 3 || f.submitted != 0 || len(f.store.plans) != 0 {
		t.Fatalf("validation/writes %d/%d/%d", f.validationCalls, f.submitted, len(f.store.plans))
	}
}

func TestActiveCompletionDoesNotRepeatInputConversion(t *testing.T) {
	f := newFixture(t)
	f.meshyText("source", "owner")
	f.unavailable["meshy/processing/convert"] = true
	if _, err := f.service.Complete(context.Background(), "owner", "source"); err != nil {
		t.Fatal(err)
	}
	if len(f.store.plans) != 1 {
		t.Fatal("expected one free conversion plan")
	}
	f.validationCalls = 0
	f.inputValidation = func(context.Context, generation.OwnerID, generation.OfferingID, string, json.RawMessage) error {
		t.Fatal("active plan revalidated by a read")
		return nil
	}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "source")
	if err != nil || formatByID(t, coverage, "gltf").Status != StatusConverting {
		t.Fatal("active conversion lost")
	}
	if _, err = f.service.Complete(context.Background(), "owner", "source"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("duplicate complete: %v", err)
	}
	if f.validationCalls != 0 || f.submitted != 0 || len(f.store.plans) != 1 {
		t.Fatal("active read wrote/rechecked work")
	}
}
