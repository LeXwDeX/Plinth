package tripo_test

// #294: a real P2 task with quad=true (2026-11-12) delivered an FBX and
// no GLB, kept as a model of the role "model" with no variant and the media type
// application/octet-stream. The Tripo v3 fake delivers the same for a P2 request
// with quad=true. This test runs the shared adapter against that fake and shows
// what the adapter makes of it, so the game purposes' reason for leaving quad
// output off is a property of the delivered claims and not only of a comment:
// the quad task offers exactly one model file, and it is not the GLB the stage
// preview and inspect_model read.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

func TestP2QuadTaskDeliversOneFBXModelClaimAndNoGLB(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, found := product.Get(schema.OfferingID("tripo/tripo-p2/text-to-3d"))
	if !found {
		t.Fatal("P2 text Offering missing")
	}
	fake, err := fakeupstream.New(fakeupstream.Config{}, tripov3.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL: server.URL + tripov3.Prefix, APIKey: "tripo-key", HTTPClient: server.Client(),
		AllowHTTPAssets: true, OfferingRoutes: []generation.Offering{offering},
		ErrorMapping: errorMapping, StatusMapping: statusMapping,
	})
	if err != nil {
		t.Fatal(err)
	}

	deliver := func(t *testing.T, quad bool) []generation.ArtifactClaim {
		t.Helper()
		input := p2FrozenInput(t, offering, map[string]any{"prompt": "a game-ready knight", "quad": quad, "face_limit": 10000})
		started, err := adapter.Start(context.Background(), generation.StartRequest{
			AttemptID: "attempt-p2-quad", SubmissionKey: "submit-p2-quad", Offering: offering, Input: input,
		})
		if err != nil || started.Job == nil {
			t.Fatalf("the fake refused the P2 request: %+v %v", started, err)
		}
		request := generation.ObserveRequest{
			TaskID: "task-p2-quad", AttemptID: "attempt-p2-quad", Offering: offering, Job: *started.Job, Input: input,
		}
		for range 10 {
			observed, err := adapter.Observe(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			request.Opaque = observed.Opaque
			if observed.Phase == generation.UpstreamCompleted {
				return observed.Artifacts
			}
		}
		t.Fatal("the P2 task never completed")
		return nil
	}
	modelClaims := func(claims []generation.ArtifactClaim) []generation.ArtifactClaim {
		var models []generation.ArtifactClaim
		for _, claim := range claims {
			if claim.Role == "model" {
				models = append(models, claim)
			}
		}
		return models
	}

	t.Run("quad output is one FBX model with no variant", func(t *testing.T) {
		models := modelClaims(deliver(t, true))
		if len(models) != 1 {
			t.Fatalf("model claims = %+v, want exactly one", models)
		}
		model := models[0]
		if model.Variant != "" || model.MediaType != "application/octet-stream" ||
			!strings.HasSuffix(strings.Split(model.Locator, "?")[0], ".fbx") {
			t.Fatalf("the quad model claim = %+v, want an FBX of role model with no variant and the opaque media type", model)
		}
		response, err := server.Client().Get(model.Locator)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		head := make([]byte, 18)
		if _, err := io.ReadFull(response.Body, head); err != nil || response.StatusCode != http.StatusOK ||
			string(head) != "Kaydara FBX Binary" {
			t.Fatalf("the claimed file is not a binary FBX: %d %q %v", response.StatusCode, head, err)
		}
	})
	t.Run("triangle output is the GLB the preview reads", func(t *testing.T) {
		models := modelClaims(deliver(t, false))
		if len(models) != 1 || models[0].MediaType != "model/gltf-binary" ||
			!strings.HasSuffix(strings.Split(models[0].Locator, "?")[0], ".glb") {
			t.Fatalf("the triangle model claims = %+v, want one GLB", models)
		}
	})
}
