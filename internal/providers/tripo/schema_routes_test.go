package tripo_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestAllTripoRequestsUseOfferingRoutes(t *testing.T) {
	for _, capability := range []generation.Capability{generation.TextTo3D, generation.ImageTo3D} {
		t.Run(string(capability), func(t *testing.T) {
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			offering, ok := product.Get(generation.OfferingID("tripo/tripo-v3.1/" + string(capability)))
			if !ok {
				t.Fatal("missing default Tripo offering")
			}
			// Route recovery also applies to a Task frozen before native pricing.
			offering.Revision = "2026-10-16.213.1"
			offering.NativePricing = nil
			offering.EstimatedCredits = 20
			offering.Cost = &generation.CostDeclaration{ExtraCredits: 5, SelectionParameter: "output_formats", ConsentParameter: "authorize_extra_credits"}
			offering.CostPolicy = &generation.CostPolicy{MaxAuthorizedExtraCredits: 30}
			var options map[string]json.RawMessage
			if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
				t.Fatal(err)
			}
			setOption := func(key, value string) {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				options[key] = encoded
			}
			setOption("endpoint", "configured/create")
			setOption("observe_endpoint", "configured/status/{task_id}")
			setOption("upload_endpoint", "configured/upload")
			offering.ProviderOptions, _ = json.Marshal(options)
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "job-1", "file_token": "file-1", "status": "running", "progress": 5}})
			}))
			defer server.Close()
			adapter := newAdapterWithImage(t, server, []byte("image-data"), offering)
			observation, err := adapter.Start(t.Context(), generation.StartRequest{
				Offering: offering, Input: []byte(`{"prompt":"cat"}`),
				InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "input", MediaType: "image/png"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Observe(t.Context(), generation.ObserveRequest{Offering: offering, Job: *observation.Job}); err != nil {
				t.Fatal(err)
			}
			want := []string{"/configured/create", "/configured/status/job-1"}
			if capability == generation.ImageTo3D {
				want = append([]string{"/configured/upload"}, want...)
			}
			if !reflect.DeepEqual(paths, want) {
				t.Fatalf("paths=%v want=%v", paths, want)
			}
			// An already-persisted task has the original model and no route keys.
			paths = nil
			pinnedModel, _ := json.Marshal(map[string]json.RawMessage{"model": options["model"]})
			offering.ProviderOptions = pinnedModel
			observation, err = adapter.Start(t.Context(), generation.StartRequest{
				Offering: offering, Input: []byte(`{"prompt":"cat"}`),
				InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "input", MediaType: "image/png"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Observe(t.Context(), generation.ObserveRequest{Offering: offering, Job: *observation.Job}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(paths, want) {
				t.Fatalf("old snapshot paths=%v want=%v", paths, want)
			}
		})
	}
}
