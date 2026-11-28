package tripo_test

// #704: a real v3.1 text task frozen with texture=false (2026-11-28) delivered a
// model GLB with no texture and no image. The Tripo v3 fake delivers the same for
// a request with texture=false (the full Offering's model GLB is the GLTF export
// of the generation). This test freezes the input through the Catalog, runs the
// shared adapter against the fake and checks the wire body the fake received and
// the model GLB the adapter claims.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

func TestTextureOffTextTaskSendsTextureFalseAndClaimsAnUntexturedGLB(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	id := schema.OfferingID("tripo/tripo-v3.1-full/text-to-3d")
	fake, err := fakeupstream.New(fakeupstream.Config{}, tripov3.Version())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sent []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/generation/text-to-model") {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			sent = append(sent, body)
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	offering, found := product.Get(id)
	if !found {
		t.Fatal("H full text Offering missing")
	}
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL: server.URL + tripov3.Prefix, APIKey: "tripo-key", HTTPClient: server.Client(),
		AllowHTTPAssets: true, OfferingRoutes: []generation.Offering{offering},
		ErrorMapping: errorMapping, StatusMapping: statusMapping,
	})
	if err != nil {
		t.Fatal(err)
	}

	// deliver freezes the input, runs the task and returns the wire body and the
	// texture count of the claimed model GLB.
	deliver := func(t *testing.T, changes map[string]any) (map[string]any, int) {
		t.Helper()
		values := map[string]any{"prompt": "a compass", "authorize_extra_credits": true}
		for key, value := range changes {
			values[key] = value
		}
		encoded, _ := json.Marshal(values)
		frozenOffering, input, err := product.Normalize(schema.TextTo3D, id, encoded)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		sent = nil
		mu.Unlock()
		started, err := adapter.Start(context.Background(), generation.StartRequest{
			AttemptID: "attempt-704", SubmissionKey: "submit-704", Offering: frozenOffering, Input: input,
		})
		if err != nil || started.Job == nil {
			t.Fatalf("the fake refused the request: %+v %v", started, err)
		}
		request := generation.ObserveRequest{
			TaskID: "task-704", AttemptID: "attempt-704", Offering: frozenOffering, Job: *started.Job, Input: input,
		}
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
		mu.Lock()
		defer mu.Unlock()
		if len(sent) != 1 {
			t.Fatalf("text-to-model requests = %d, want 1", len(sent))
		}
		for _, claim := range claims {
			if claim.Role != "model" || claim.MediaType != "model/gltf-binary" {
				continue
			}
			response, err := server.Client().Get(claim.Locator)
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
			return sent[0], len(document.Textures)
		}
		t.Fatalf("no model GLB claim: %+v", claims)
		return nil, 0
	}

	t.Run("texture off", func(t *testing.T) {
		body, textures := deliver(t, map[string]any{"texture": false})
		if body["texture"] != false || body["pbr"] != false {
			t.Fatalf("wire body texture=%v pbr=%v, want false and false", body["texture"], body["pbr"])
		}
		if textures != 0 {
			t.Fatalf("the model GLB has %d textures, want none", textures)
		}
	})
	t.Run("default texture on", func(t *testing.T) {
		body, textures := deliver(t, nil)
		if body["texture"] != true || body["pbr"] != true {
			t.Fatalf("wire body texture=%v pbr=%v, want true and true", body["texture"], body["pbr"])
		}
		if textures == 0 {
			t.Fatal("the default model GLB is untextured")
		}
	})
}
