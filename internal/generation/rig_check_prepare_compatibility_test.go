package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// The optional validator is a Go interface on the shared Tripo adapter. Exercise
// the existing operations through real Prepare/Grant/SubmitPrepared (without
// reconciliation), so adding Rig Check cannot silently break their admission.
func TestRigCheckValidatorPreservesExistingTripoPrepareAndReplay(t *testing.T) {
	for _, kind := range []string{"text", "image", "uploaded-glb-to-stl"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				t.Errorf("Prepare/Submit unexpectedly sent HTTP %s %s", r.Method, r.URL.Path)
				w.WriteHeader(500)
			}))
			defer server.Close()
			h := newConfiguredHarness(t, rigReportSourcePort{}, nil, func(deps *generation.Dependencies) {
				adapter, err := tripo.New(tripo.Config{BaseURL: server.URL + "/v3", APIKey: "test", HTTPClient: server.Client(), InputAssets: deps.Inputs})
				if err != nil {
					t.Fatal(err)
				}
				registry := providers.NewRegistry()
				if err := registry.Register("tripo.v3", adapter); err != nil {
					t.Fatal(err)
				}
				deps.Providers = registry
			})
			ctx := context.Background()
			owner := generation.OwnerID("compat-owner")
			offeringID := generation.OfferingID("tripo/tripo-v3.1-full/text-to-3d")
			capability := generation.TextTo3D
			values := map[string]any{"prompt": "native compatibility", "output_formats": []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"}, "authorize_extra_credits": true}
			var expectedAsset generation.InputAsset
			if kind == "image" {
				var imageBytes bytes.Buffer
				if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
					t.Fatal(err)
				}
				var err error
				expectedAsset, err = h.inputs.Upload(ctx, owner, bytes.NewReader(imageBytes.Bytes()), "image/png", 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				offeringID = "tripo/tripo-v3.1-full/image-to-3d"
				capability = generation.ImageTo3D
				delete(values, "prompt")
				values["reference_assets"] = []string{string(expectedAsset.ID)}
			} else if kind == "uploaded-glb-to-stl" {
				var err error
				expectedAsset, err = h.inputs.Upload(ctx, owner, bytes.NewReader(modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Interleaved: true, Textured: true})), "model/gltf-binary", 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				offeringID = "tripo/format-conversion/uploaded-glb-to-stl"
				capability = generation.FormatConversion
				values = map[string]any{"source_asset": string(expectedAsset.ID)}
			}
			h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, string(owner), "compat-session")
			raw, _ := json.Marshal(values)
			command := generation.PrepareInputCommand{OwnerID: owner, SessionID: "compat-session", Capability: capability, OfferingID: offeringID, Input: raw}
			first, err := h.module.PrepareInput(ctx, command)
			if err != nil {
				t.Fatal(err)
			}
			second, err := h.module.PrepareInput(ctx, command)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first.NormalizedInput, second.NormalizedInput) || first.InputHash != second.InputHash || first.OfferingRevision == "" {
				t.Fatalf("prepare is unstable: %+v %+v", first, second)
			}
			if expectedAsset.ID != "" && (len(first.Assets) != 1 || first.Assets[0].ID != expectedAsset.ID || first.Assets[0].SHA256 != expectedAsset.SHA256) {
				t.Fatalf("input identity changed: %+v", first.Assets)
			}
			if kind == "uploaded-glb-to-stl" {
				var normalized map[string]any
				json.Unmarshal(first.NormalizedInput, &normalized)
				if normalized["format"] != "stl" {
					t.Fatal("fixed STL default missing")
				}
			}
			if err := h.module.ValidateOfferingInput(ctx, owner, offeringID, first.OfferingRevision, raw); err != nil {
				t.Fatalf("read-only validation no longer compatible: %v", err)
			}
			approved := authorizePrepared(t, ctx, h.repository, first, "compatibility-replay")
			accepted, err := h.module.SubmitPrepared(ctx, approved)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := h.module.SubmitPrepared(ctx, approved)
			if err != nil || !replayed.Replayed || accepted.TaskID != replayed.TaskID {
				t.Fatalf("prepared replay changed: %+v %v", replayed, err)
			}
			if requests.Load() != 0 {
				t.Fatal("admission dispatched upstream")
			}
		})
	}
}
