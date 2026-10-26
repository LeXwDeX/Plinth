package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func nativeDeliveryHTTPTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "catalog", "default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	discovery := doc["application"].(map[string]any)["provider_discovery"].(map[string]any)
	for _, item := range doc["offerings"].([]any) {
		o := item.(map[string]any)
		if o["id"] != "tripo/processing/rigging" && o["id"] != "tripo/processing/texture" {
			continue
		}
		o["enabled"] = true
		registered := false
		for _, model := range discovery["legacy_models"].([]any) {
			if model.(map[string]any)["model_id"] == o["model_id"] {
				registered = true
			}
		}
		if !registered {
			discovery["legacy_models"] = append(discovery["legacy_models"].([]any), map[string]any{"model_id": o["model_id"], "provider_key": "tripo"})
		}
	}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHarnessPublicEstimateIncludesMissingFixedNativeFormatsWithoutChangingSelection(t *testing.T) {
	t.Parallel()
	defaultCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		operation, id string
		credits       int
	}{
		{"rigging", "tripo/processing/rigging", 35},
		{"rigging", "tripo/processing/rigging-v3", 35},
		{"retexture", "tripo/processing/texture", 80},
		{"rigging", "meshy/processing/rigging", 5},
	} {
		t.Run(tc.id, func(t *testing.T) {
			c := defaultCatalog
			o, _ := c.Get(schema.OfferingID(tc.id))
			_, handler, cookie, session := grantTestBrowserWithCatalog(t, nil, []string{"user"}, c)
			before := harnessWorkSet(t, handler, cookie, session)
			selected := harness.Selection{OperationID: tc.operation, OfferingID: tc.id, OfferingRevision: o.Revision, EnabledOperations: []string{tc.operation}, FixedParameters: map[string]json.RawMessage{}, Sources: []harness.Source{}, FillableParameters: []string{}}
			body, _ := json.Marshal(map[string]any{"expected_revision": 0, "selection": selected})
			put := grantTestRequest(t, handler, cookie, http.MethodPut, harnessPath(session), string(body), "")
			if put.Code != http.StatusOK {
				t.Fatalf("PUT=%d %s", put.Code, put.Body.String())
			}
			response := grantTestRequest(t, handler, cookie, http.MethodGet, harnessPath(session), "", "")
			if response.Code != http.StatusOK {
				t.Fatalf("GET=%d %s", response.Code, response.Body.String())
			}
			var view struct {
				Credits   int               `json:"estimated_credits"`
				Selection harness.Selection `json:"selection"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			if view.Credits != tc.credits {
				t.Fatalf("public estimate=%d want=%d: %s", view.Credits, tc.credits, response.Body.String())
			}
			if len(view.Selection.FixedParameters) != 0 {
				t.Fatalf("estimate mutated selection: %+v", view.Selection)
			}
			if after := harnessWorkSet(t, handler, cookie, session); after != before {
				t.Fatal("estimate created work")
			}
		})
	}
}

func TestNativePublicDiscoveryListsEnabledTripoProcessingAndKeepsPrivateProtocolServerSide(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		var c *catalog.Catalog
		if enabled {
			c = nativeDeliveryHTTPTestCatalog(t)
		}
		_, handler, _, _ := newHTTPHarnessWithCatalogOverride(t, []string{"admin"}, nil, nil, nil, nil, c)
		for _, path := range []string{"/api/v1/schema", "/api/v1/offerings", "/api/providers?capability=rigging&projection=extended", "/api/providers?capability=retexture&projection=extended", "/api/providers?capability=animation&projection=extended"} {
			payload := fetchSurface(t, handler, path)
			assertNoCostSentinels(t, path, payload)
			for _, private := range []string{`"provider_options"`, `"native_conversion"`, `"source_alias"`, `"response_aliases"`, `models/convert`, `animations/rig`} {
				if bytes.Contains(payload, []byte(private)) {
					t.Fatalf("%s leaked private declaration %s", path, private)
				}
			}
		}
		for _, tc := range []struct {
			capability, model string
			credits           int
		}{{"rigging", "tripo-rigging", 35}, {"rigging", "tripo-rigging-v3", 35}, {"retexture", "tripo-texture", 80}, {"animation", "tripo-animation", 0}} {
			// maximum_estimated_credits is 0 when no billed option changes the
			// price: Retarget has a flat 10-credit estimate and no cost declaration.
			// #186 route B (2026-10-20): all four are enabled in the default Schema.
			wantEnabled := true
			for _, projection := range []string{"", "&projection=extended"} {
				var discovered struct {
					Providers []struct {
						ID string `json:"id"`
					} `json:"providers"`
				}
				if err := json.Unmarshal(fetchSurface(t, handler, "/api/providers?capability="+tc.capability+projection), &discovered); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, provider := range discovered.Providers {
					if provider.ID == tc.model {
						found = true
					}
				}
				if found != wantEnabled {
					t.Fatalf("model %s projection=%q advertised=%v enabled=%v", tc.model, projection, found, wantEnabled)
				}
			}
			found := false
			var offerings struct {
				Offerings []struct {
					Model   string `json:"model_id"`
					Maximum int    `json:"maximum_estimated_credits"`
				} `json:"offerings"`
			}
			if err := json.Unmarshal(fetchSurface(t, handler, "/api/v1/offerings?capability="+tc.capability), &offerings); err != nil {
				t.Fatal(err)
			}
			for _, offering := range offerings.Offerings {
				if offering.Model == tc.model {
					found = true
					if offering.Maximum != tc.credits {
						t.Fatalf("maximum %s=%d", tc.model, offering.Maximum)
					}
				}
			}
			if found != wantEnabled {
				t.Fatalf("model %s publicly listed=%v enabled=%v", tc.model, found, wantEnabled)
			}
		}
	}
}
