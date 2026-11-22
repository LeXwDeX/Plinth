package tripo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// legacy_full_format_test.go locks the other half of #462: a Task frozen before
// the mixed shape keeps its contract byte for byte. Its Offering declares every
// converted format as a required slot gated by `required_when`, has no
// `post_success`, and carries the old revision; the adapter must still convert
// every selected format before the task may succeed, exactly as it did.
//
// The Offering below is the pre-#462 declaration of the same three Offerings
// (revision 2026-11-12.278.1): all six conversions required, each gated on the
// selection, no post-success lifecycle.

func legacyFullFormatOffering(t *testing.T) generation.Offering {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	current, ok := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !ok {
		t.Fatal("default Catalog is missing the Tripo full-format Offering")
	}
	legacy := current
	legacy.Revision = "2026-11-12.278.1"
	legacy.PostSuccess = nil
	legacy.Outputs = append([]generation.AssetRequirement(nil), current.Outputs...)
	for index := range legacy.Outputs {
		output := &legacy.Outputs[index]
		if output.Role != "converted_model" || output.Variant == "converted.gltf" {
			continue
		}
		format := strings.ToUpper(strings.TrimPrefix(output.Variant, "converted."))
		output.Delivery = schema.AssetDeliveryRequired
		output.RequestedWhen = nil
		output.RequiredWhen = &schema.ParameterCondition{
			Parameter: "output_formats",
			Operator:  "contains",
			Value:     json.RawMessage(`"` + format + `"`),
		}
	}
	return legacy
}

func TestLegacyFullFormatTaskStillConvertsEverySelectedFormatBeforeSuccess(t *testing.T) {
	var converts []string
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/base":
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
				"task_id": "base", "status": "success", "progress": 100,
				"output": map[string]any{"model_url": upstream.URL + "/base.glb"},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/models/convert":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			format, _ := body["format"].(string)
			converts = append(converts, format)
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "child-" + strings.ToLower(format)}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/child-"):
			format := strings.TrimPrefix(r.URL.Path, "/tasks/child-")
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
				"task_id": "child-" + format, "status": "success", "progress": 100,
				"output": map[string]any{"model_url": upstream.URL + "/" + format + ".glb"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := legacyFullFormatOffering(t)
	// The frozen input of the legacy Task: the same selection, resolved by the
	// real Catalog normalization.
	_, input, err := c.Normalize(generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d",
		json.RawMessage(`{"prompt":"castle","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`))
	if err != nil {
		t.Fatal(err)
	}
	adapter := newAdapter(t, upstream)
	request := generation.ObserveRequest{
		AttemptID: "attempt", Offering: offering,
		Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "base"}, Input: input,
	}
	// The old shape runs every conversion before it may complete: no round
	// delivers a claim while a format is still missing.
	for round := 0; round < 60; round++ {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamFailed {
			t.Fatalf("legacy task failed: %+v", observation.Failure)
		}
		request.Opaque = observation.Opaque
		if observation.Phase != generation.UpstreamCompleted {
			if len(observation.Artifacts) != 0 {
				t.Fatalf("legacy task delivered claims before every format landed: %#v", observation.Artifacts)
			}
			continue
		}
		if len(converts) != 6 {
			t.Fatalf("converts = %v, want all six before success", converts)
		}
		claims := map[string]int{}
		for _, claim := range observation.Artifacts {
			if claim.Role != "model" && claim.Role != "converted_model" {
				continue
			}
			claims[claim.Role+"/"+claim.Variant]++
		}
		if claims["model/"] != 1 {
			t.Fatalf("the legacy primary model claim is missing: %v", claims)
		}
		for _, format := range []string{"gltf", "usdz", "fbx", "obj", "stl", "3mf"} {
			if claims["converted_model/converted."+format] != 1 {
				t.Fatalf("legacy claim set = %v, want exactly one converted.%s", claims, format)
			}
		}
		return
	}
	t.Fatal("legacy conversions did not complete")
}
