package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestProviderDiscoveryProjectsUploadedModelConstraints(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	profile := defaultUploadedModelSnapshot(t)

	cases := []struct {
		capability string
		want       map[string]any
	}{
		{"remesh", map[string]any{"profile_id": "static-glb-v1"}},
		{"retexture", map[string]any{"profile_id": "static-glb-v1"}},
		{
			"rigging",
			map[string]any{
				"profile_id":       "static-glb-v1",
				"require_textures": true,
				"requirements":     "需要带贴图、面向 +Z 的人形角色。文件结构通过校验不代表满足人形或朝向条件；不符合条件时请先处理模型。",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.capability, func(t *testing.T) {
			tc.want["profile_revision"] = profile["profile_revision"]
			tc.want["profile_limits"] = profile["profile_limits"]
			provider := fetchDiscoveryProvider(t, handler,
				"/api/providers?capability="+tc.capability+"&projection=extended",
				"meshy-processing",
			)
			inputs := discoveryInputSchemas(t, provider, tc.capability)
			input := discoveryInputByName(t, inputs, "source_asset")
			if got := input["uploaded_model"]; !jsonValueEqual(got, tc.want) {
				t.Fatalf("%s source_asset uploaded_model = %#v, want %#v", tc.capability, got, tc.want)
			}
		})
	}

	provider := fetchDiscoveryProvider(t, handler,
		"/api/providers?capability=animation&projection=extended",
		"meshy-processing",
	)
	if schemas, ok := provider["inputSchemas"].(map[string]any); ok {
		if inputs, ok := schemas["animation"].([]any); ok {
			for _, raw := range inputs {
				input, _ := raw.(map[string]any)
				if _, exists := input["uploaded_model"]; exists {
					t.Fatalf("animation input unexpectedly has uploaded_model: %#v", input)
				}
			}
		}
	}
}

func TestProviderDiscoveryProjectsUploadedModelFieldFromSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.ProviderDiscovery.API.InputFields.UploadedModel = "source_model_constraints"
		},
	)
	provider := fetchDiscoveryProvider(t, handler,
		"/api/providers?capability=remesh&projection=extended",
		"meshy-processing",
	)
	input := discoveryInputByName(t, discoveryInputSchemas(t, provider, "remesh"), "source_asset")
	if _, exists := input["uploaded_model"]; exists {
		t.Fatalf("default uploaded_model field leaked after schema rename: %#v", input)
	}
	if got := input["source_model_constraints"]; !jsonValueEqual(got, defaultUploadedModelSnapshot(t)) {
		t.Fatalf("schema-configured uploaded model field = %#v", got)
	}
}

func defaultUploadedModelSnapshot(t *testing.T) map[string]any {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	profile := product.Application().Runtime.InputAssets.ModelProfile
	if profile == nil {
		t.Fatal("missing uploaded-model profile")
	}
	encoded, err := json.Marshal(profile.Limits)
	if err != nil {
		t.Fatal(err)
	}
	var limits map[string]any
	if err := json.Unmarshal(encoded, &limits); err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"profile_id": profile.ID, "profile_revision": profile.Revision, "profile_limits": limits,
	}
}

func TestNativeOfferingsExposeUploadedModelConstraints(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	for _, capability := range []string{"remesh", "retexture", "rigging"} {
		offering := fetchOfferingForCapability(t, handler, capability)
		parameter := offeringParameterByName(t, offering, "source_asset")
		if parameter["uploaded_model"] == nil {
			t.Fatalf("%s source_asset lacks uploaded_model: %#v", capability, parameter)
		}
	}
	offering := fetchOfferingForCapability(t, handler, "animation")
	for _, raw := range offering["parameters"].([]any) {
		parameter, _ := raw.(map[string]any)
		if _, exists := parameter["uploaded_model"]; exists {
			t.Fatalf("animation parameter unexpectedly has uploaded_model: %#v", parameter)
		}
	}
}

func fetchDiscoveryProvider(
	t *testing.T,
	handler http.Handler,
	target string,
	providerID string,
) map[string]any {
	t.Helper()
	response := serveLegacyRequest(handler, http.MethodGet, target, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("%s status = %d, body = %s", target, response.Code, response.Body.String())
	}
	var payload struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return legacyProviderByID(t, payload.Providers, providerID)
}

func discoveryInputSchemas(t *testing.T, provider map[string]any, capability string) []any {
	t.Helper()
	schemas, ok := provider["inputSchemas"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchemas = %#v", provider["inputSchemas"])
	}
	inputs, ok := schemas[capability].([]any)
	if !ok {
		t.Fatalf("inputSchemas[%q] = %#v", capability, schemas[capability])
	}
	return inputs
}

func discoveryInputByName(t *testing.T, inputs []any, name string) map[string]any {
	t.Helper()
	for _, raw := range inputs {
		input, ok := raw.(map[string]any)
		if ok && input["name"] == name {
			return input
		}
	}
	t.Fatalf("input %q not found in %#v", name, inputs)
	return nil
}

func fetchOfferingForCapability(t *testing.T, handler http.Handler, capability string) map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/offerings?capability="+capability, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("offerings %s status = %d, body = %s", capability, response.Code, response.Body.String())
	}
	var payload struct {
		Offerings []map[string]any `json:"offerings"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	for _, offering := range payload.Offerings {
		if offering["capability"] == capability {
			return offering
		}
	}
	t.Fatalf("offering %q not found: %#v", capability, payload.Offerings)
	return nil
}

func offeringParameterByName(t *testing.T, offering map[string]any, name string) map[string]any {
	t.Helper()
	parameters, ok := offering["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters = %#v", offering["parameters"])
	}
	for _, raw := range parameters {
		parameter, ok := raw.(map[string]any)
		if ok && parameter["name"] == name {
			return parameter
		}
	}
	t.Fatalf("parameter %q not found in %#v", name, parameters)
	return nil
}

func jsonValueEqual(got any, want any) bool {
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	return gotErr == nil && wantErr == nil && string(gotJSON) == string(wantJSON)
}
