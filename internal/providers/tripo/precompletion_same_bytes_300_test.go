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
)

// #300: the full-format Offering fulfils the model slot with the GLTF export (the
// self-contained binary glTF), so the model claim and converted.gltf are one file
// at one locator. The adapter built both from that file, so it is the one that
// says so; no other claim is declared identical to anything.
func TestFullFormatModelClaimDeclaresTheGLTFExportAsItsBytes(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/base":
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
				"task_id": "base", "status": "success", "progress": 100,
				"output": map[string]any{"model_url": upstream.URL + "/base.glb", "rendered_image": upstream.URL + "/preview.webp"},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/models/convert":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			format, _ := body["format"].(string)
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
	offering, ok := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !ok {
		t.Fatal("missing full-format offering")
	}
	adapter := newAdapter(t, upstream)
	request := generation.ObserveRequest{
		AttemptID: "attempt", Offering: offering,
		Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "base"}, Input: fullTextFrozenInput(t, c),
	}
	for round := 0; round < 30; round++ {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase != generation.UpstreamCompleted {
			request.Opaque = observation.Opaque
			continue
		}
		byID := map[string]generation.ArtifactClaim{}
		for _, claim := range observation.Artifacts {
			byID[claim.ID] = claim
		}
		var model, gltf generation.ArtifactClaim
		for _, claim := range observation.Artifacts {
			switch {
			case claim.Role == "model":
				model = claim
			case claim.Variant == "converted.gltf":
				gltf = claim
			}
		}
		if model.ID == "" || gltf.ID == "" {
			t.Fatalf("no model or converted.gltf claim: %#v", observation.Artifacts)
		}
		if model.SameBytesAs != gltf.ID {
			t.Fatalf("model declares %q, want the GLTF export %q", model.SameBytesAs, gltf.ID)
		}
		if _, named := byID[model.SameBytesAs]; !named || model.Locator == "" || model.Locator != gltf.Locator {
			t.Fatalf("the declaration is not backed by one file: model %#v, gltf %#v", model, gltf)
		}
		for _, claim := range observation.Artifacts {
			if claim.ID != model.ID && claim.SameBytesAs != "" {
				t.Fatalf("%s/%s declares %q though it has its own file", claim.Role, claim.Variant, claim.SameBytesAs)
			}
		}
		return
	}
	t.Fatal("conversions did not complete")
}
