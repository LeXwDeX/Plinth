package httpapi_test

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"net/http"
	"testing"
)

// 回归：Offering 层默认值统一提升为最高质量档后，两个投影必须分叉：
// 扩展投影（Go 前端）取得最高档默认值；旧站兼容投影按 legacy_models
// 兼容规则保持旧默认值/无默认值，逐 JSON 不变。
func TestHighestQualityDefaultsStayOutOfLegacyProjection(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)

	cases := []struct {
		capability      string
		providerID      string
		parameter       string
		legacyDefault   any // nil 表示旧投影不得出现 default 字段
		extendedDefault any // nil 表示扩展投影不得出现 default（不自动注入）
	}{
		// Tripo 质量档、Meshy 生成面数与重贴图分辨率已改为后端固定（hidden），
		// 不再出现在任何投影中，见 TestHiddenParametersStayOutOfProviderDiscovery。
		{"remesh", "meshy-processing", "target_polycount", float64(10000), float64(300000)},
	}
	for _, tc := range cases {
		legacyValue, legacyExists := discoveryParameterDefault(
			t,
			handler,
			"/api/providers?capability="+tc.capability,
			tc.providerID,
			tc.capability,
			tc.parameter,
		)
		if tc.legacyDefault == nil {
			if legacyExists {
				t.Fatalf(
					"%s/%s.%s legacy default leaked: %#v",
					tc.providerID, tc.capability, tc.parameter, legacyValue,
				)
			}
		} else if !legacyExists || legacyValue != tc.legacyDefault {
			t.Fatalf(
				"%s/%s.%s legacy default = %#v, want %#v",
				tc.providerID, tc.capability, tc.parameter,
				legacyValue, tc.legacyDefault,
			)
		}

		extendedValue, extendedExists := discoveryParameterDefault(
			t,
			handler,
			"/api/providers?capability="+tc.capability+"&projection=extended",
			tc.providerID,
			tc.capability,
			tc.parameter,
		)
		if tc.extendedDefault == nil {
			if extendedExists {
				t.Fatalf(
					"%s/%s.%s extended default leaked: %#v",
					tc.providerID, tc.capability, tc.parameter, extendedValue,
				)
			}
		} else if !extendedExists || extendedValue != tc.extendedDefault {
			t.Fatalf(
				"%s/%s.%s extended default = %#v, want %#v",
				tc.providerID, tc.capability, tc.parameter,
				extendedValue, tc.extendedDefault,
			)
		}
	}
}

// Hidden/fixed parameters stay out of discovery. Public advised parameters
// appear only in extended projection, carrying the actual recommendation.
func TestHiddenParametersStayOutOfProviderDiscovery(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	hidden := []struct {
		capability, providerID string
		names                  []string
	}{
		{"text-to-3d", "tripo-v3.1-full", []string{"face_limit", "texture", "pbr", "texture_quality", "geometry_quality", "quad", "smart_low_poly", "export_uv"}},
		{"image-to-3d", "tripo-v3.1-full", []string{"face_limit", "enable_image_autofix", "texture_quality", "geometry_quality"}},
		{"text-to-3d", "meshy-7.1", []string{"geometry_resolution", "texture_resolution", "should_remesh", "topology", "enable_pbr"}},
		{"image-to-3d", "meshy-7.1", []string{"geometry_resolution", "texture_resolution", "should_remesh", "topology", "target_polycount", "should_texture", "enable_pbr", "image_enhancement"}},
		{"retexture", "meshy-processing", []string{"texture_resolution", "enable_pbr"}},
	}
	for _, tc := range hidden {
		for _, projection := range []string{"", "&projection=extended"} {
			target := "/api/providers?capability=" + tc.capability + projection
			response := serveLegacyRequest(handler, http.MethodGet, target, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("%s status = %d", target, response.Code)
			}
			var payload struct {
				Providers []map[string]any `json:"providers"`
			}
			if err := json.NewDecoder(
				response.Body,
			).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			provider := legacyProviderByID(t, payload.Providers, tc.providerID)
			parameters := legacyProviderParameters(t, provider, tc.capability)
			for _, raw := range parameters {
				parameter, _ := raw.(map[string]any)
				for _, name := range tc.names {
					if parameter["name"] == name {
						if projection != "" {
							advised := false
							for _, o := range product.List(generation.Capability(tc.capability)) {
								if o.ModelID == tc.providerID {
									for _, p := range o.Parameters {
										if p.Name == name && p.AdjustmentPolicy == "advised" {
											advised = true
											var expected any
											_ = json.Unmarshal(p.Default, &expected)
											if parameter["adjustment_policy"] != "advised" || parameter["readonly"] != true || parameter["adjustment_reason"] != p.AdjustmentReason || parameter["default"] != expected {
												t.Fatalf("advice mismatch: %+v", parameter)
											}
										}
									}
								}
							}
							if advised {
								continue
							}
						}
						t.Fatalf(
							"hidden %s/%s.%s leaked into %q: %#v",
							tc.providerID, tc.capability, name, target, parameter,
						)
					}
				}
			}
		}
	}
}

func discoveryParameterDefault(
	t *testing.T,
	handler http.Handler,
	target string,
	providerID string,
	capability string,
	parameter string,
) (any, bool) {
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
	provider := legacyProviderByID(t, payload.Providers, providerID)
	parameters := legacyProviderParameters(t, provider, capability)
	schema := legacyMapByStringField(t, parameters, "name", parameter)
	value, exists := schema["default"]
	return value, exists
}
