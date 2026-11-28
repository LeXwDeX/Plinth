package meshy_test

// #704: a real Meshy 7.1 Image to 3D task frozen with should_texture=false
// (2026-11-28) delivered a model GLB with no texture and no image. The Meshy fake
// delivers the same for a request with should_texture=false. This test freezes
// the input through the Catalog, runs the adapter against the fake and checks the
// wire body the fake received and the model GLB the adapter claims.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

func TestTextureOffImageTo3DSendsShouldTextureFalseAndClaimsAnUntexturedGLB(t *testing.T) {
	image := testImagePNG(t, 32, 32)
	// deliver freezes the input, runs the task and returns the wire body and the
	// texture count of the claimed model GLB.
	deliver := func(t *testing.T, input string) (map[string]any, int) {
		t.Helper()
		upstream := newPostRecorder(t)
		taskCatalog, offering := upgradeOffering(t, "meshy/meshy-7.1/image-to-3d", upstream.server)
		_, frozen, err := taskCatalog.Normalize(offering.Capability, offering.ID, json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.server.URL + "/meshy", APIKey: "test", HTTPClient: upstream.server.Client(),
			InputAssets: memoryInputAssets{content: image}, AllowHTTPAssets: true})
		started, err := adapter.Start(context.Background(), generation.StartRequest{
			TaskID: "task-704", AttemptID: "attempt-704", SubmissionKey: "submit-704", Offering: offering, Input: frozen,
			InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-input", MediaType: "image/png"}}},
		})
		if err != nil || started.Job == nil {
			t.Fatalf("the fake refused the request: %+v %v", started, err)
		}
		request := generation.ObserveRequest{TaskID: "task-704", AttemptID: "attempt-704", Offering: offering, Job: *started.Job, Input: frozen}
		var claims []generation.ArtifactClaim
		for range 20 {
			observed, err := adapter.Observe(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			request.Opaque = observed.Opaque
			if observed.Phase == generation.UpstreamCompleted {
				claims = observed.Artifacts
				break
			}
		}
		bodies := upstream.bodies("/meshy/openapi/v1/image-to-3d")
		if len(bodies) != 1 {
			t.Fatalf("image-to-3d requests = %d, want 1", len(bodies))
		}
		for _, claim := range claims {
			if claim.Role != "model" || !strings.HasSuffix(strings.Split(claim.Locator, "?")[0], ".glb") {
				continue
			}
			response, err := upstream.server.Client().Get(claim.Locator)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if len(data) < 20 || string(data[:4]) != "glTF" {
				t.Fatalf("the model claim is not a GLB: %+v", claim)
			}
			var document struct{ Textures, Images []json.RawMessage }
			size := binary.LittleEndian.Uint32(data[12:16])
			if err := json.Unmarshal(data[20:20+size], &document); err != nil {
				t.Fatal(err)
			}
			if len(document.Textures) != len(document.Images) {
				t.Fatalf("textures %d, images %d", len(document.Textures), len(document.Images))
			}
			return bodies[0], len(document.Textures)
		}
		t.Fatalf("no model GLB claim: %+v", claims)
		return nil, 0
	}

	t.Run("texture off", func(t *testing.T) {
		body, textures := deliver(t, `{"reference_assets":["asset-input"],"should_texture":false}`)
		if body["should_texture"] != false {
			t.Fatalf("wire body should_texture=%v, want false", body["should_texture"])
		}
		assertAbsent(t, "untextured image to 3D", body, "enable_pbr")
		if textures != 0 {
			t.Fatalf("the model GLB has %d textures, want none", textures)
		}
	})
	t.Run("default texture on", func(t *testing.T) {
		body, textures := deliver(t, `{"reference_assets":["asset-input"]}`)
		if body["should_texture"] != true || body["enable_pbr"] != true {
			t.Fatalf("wire body should_texture=%v enable_pbr=%v, want true and true", body["should_texture"], body["enable_pbr"])
		}
		if textures == 0 {
			t.Fatal("the default model GLB is untextured")
		}
	})
}
