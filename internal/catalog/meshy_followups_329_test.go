package catalog_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #329 follow-up C: a generation request that names a retired model's
// Offering directly is refused before anything is frozen or sent, with a
// reason the person can act on (the model that replaces it), as invalid input.
func TestRetiredModelOfferingIsRefusedWithSuccessor(t *testing.T) {
	taskCatalog := meshy329Catalog(t)
	for capability, input := range map[generation.Capability]string{
		generation.TextTo3D:  `{"prompt":"castle"}`,
		generation.ImageTo3D: `{"reference_assets":["asset-1"]}`,
	} {
		id := generation.OfferingID("meshy/meshy-7/" + string(capability))
		_, _, err := taskCatalog.Normalize(capability, id, json.RawMessage(input))
		var domainErr *schema.Error
		if !errors.As(err, &domainErr) || domainErr.Code != schema.ErrInvalidInput {
			t.Fatalf("%s: err = %v, want invalid_input", id, err)
		}
		reason := schema.UserFacingDetail(err)
		if !strings.Contains(strings.ReplaceAll(reason, "Meshy 7.1", ""), "Meshy 7") || !strings.Contains(reason, "Meshy 7.1") {
			t.Fatalf("%s: reason %q does not name the retired model and its successor", id, reason)
		}
	}
	// An Offering that is only switched off (not retired) keeps the old answer.
	_, _, err := taskCatalog.Normalize(generation.TextTo3D, "meshy/unknown/text-to-3d", json.RawMessage(`{"prompt":"x"}`))
	var domainErr *schema.Error
	if !errors.As(err, &domainErr) || domainErr.Code != schema.ErrUnknownOffering || schema.UserFacingDetail(err) != "" {
		t.Fatalf("unknown offering err = %v", err)
	}
}

// The chat service reads the retired models and their successors from the
// Catalog to normalize a turn's model picks (#329 follow-up C).
func TestRetiredModelSuccessorsNameBothModels(t *testing.T) {
	successors := meshy329Catalog(t).RetiredModelSuccessors()
	if len(successors) != 1 {
		t.Fatalf("successors = %+v", successors)
	}
	got := successors[0]
	if got.ModelID != "meshy-7" || got.ModelName != "Meshy 7" || got.SuccessorModelID != "meshy-7.1" || got.SuccessorModelName != "Meshy 7.1" {
		t.Fatalf("successor = %+v", got)
	}
}
