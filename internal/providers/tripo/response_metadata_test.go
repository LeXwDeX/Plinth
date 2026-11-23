package tripo

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A model conversion reports the origin move it made as output.normalize_scale
// and output.normalize_translation. The Offering declares those field names under
// response_metadata; the Adapter must carry them onto the model claim's metadata
// instead of dropping them (the field names are read from the declaration, never
// hardcoded).
func TestObservationKeepsTheDeclaredResponseMetadata(t *testing.T) {
	_, statusMapping := realSampleMappings(t)
	adapter := &Adapter{statusMapping: statusMapping}
	offering := generation.Offering{
		ID: "tripo/test/convert",
		ProviderOptions: json.RawMessage(`{"endpoint":"models/convert",` +
			`"response_aliases":[{"key":"model_url","claim_id":"model:converted","role":"model","variant":"converted","fields":["model_url"]}],` +
			`"response_metadata":["normalize_scale","normalize_translation"]}`),
	}
	request := generation.ObserveRequest{Offering: offering, AttemptID: "attempt"}
	value := task{
		Status: "success",
		Output: map[string]any{
			"model_url":             "https://assets.example.test/x.glb",
			"normalize_scale":       json.Number("1"),
			"normalize_translation": []any{0.0, 0.0, 0.0},
		},
	}
	observation, err := adapter.observation(request, value)
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Artifacts) != 1 || observation.Artifacts[0].Role != "model" {
		t.Fatalf("artifacts = %+v", observation.Artifacts)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(observation.Artifacts[0].Metadata, &metadata); err != nil {
		t.Fatalf("model claim metadata = %s: %v", observation.Artifacts[0].Metadata, err)
	}
	for _, name := range []string{"normalize_scale", "normalize_translation"} {
		if _, ok := metadata[name]; !ok {
			t.Fatalf("model claim dropped %s: %s", name, observation.Artifacts[0].Metadata)
		}
	}
}

// The same fields survive a private export: the converted GLB a processing step
// delivers is the child conversion's output, and its model claim carries the move
// the child reported.
func TestPrivateExportModelClaimCarriesTheNormalizeFields(t *testing.T) {
	run := driveExports(t, shippedRigging(t, "tripo/processing/rigging-v3", map[string]any{"export_texture_size": "2k"}), "animations/rig")
	for _, claim := range run.observed.Artifacts {
		var metadata map[string]json.RawMessage
		if json.Unmarshal(claim.Metadata, &metadata) != nil {
			continue
		}
		if _, ok := metadata["normalize_scale"]; !ok {
			continue
		}
		if _, ok := metadata["normalize_translation"]; !ok {
			t.Fatalf("claim %s has normalize_scale but no normalize_translation: %s", claim.ID, claim.Metadata)
		}
		return
	}
	t.Fatalf("no claim carries the conversion's normalize fields: %+v", run.observed.Artifacts)
}
