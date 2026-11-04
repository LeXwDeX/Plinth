package catalog_test

// Tripo P2 contract fixture, constructed from the official P pages read
// 2026-11-04:
// https://developers.tripo3d.ai/en/docs/generation-image-to-model/p
// https://developers.tripo3d.ai/en/docs/generation-multiview-to-model/p
// https://developers.tripo3d.ai/en/docs/generation-text-to-model/p
// https://developers.tripo3d.ai/en/pricing
// The offering stays disabled until a real paid sample and the P2 price are
// verified; these tests lock the declared contract, not a live response.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestTripoP2OfferingsDeclareConstructedLowPolyContract(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id         string
		capability schema.Capability
		endpoint   string
	}{
		{"tripo/tripo-p2/image-to-3d", schema.ImageTo3D, "generation/image-to-model"},
		{"tripo/tripo-p2-multiview/image-to-3d", schema.ImageTo3D, "generation/multiview-to-model"},
		{"tripo/tripo-p2/text-to-3d", schema.TextTo3D, "generation/text-to-model"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			o, ok := c.Get(schema.OfferingID(tc.id))
			if !ok {
				t.Fatal("missing Offering")
			}
			if o.Enabled {
				t.Fatal("P2 must stay disabled until a real sample and the price are verified")
			}
			if o.Capability != tc.capability {
				t.Fatalf("capability = %q, want %q", o.Capability, tc.capability)
			}
			if o.NativePricing != nil || o.Cost != nil || o.EstimatedCredits != 0 {
				t.Fatalf("pending price must not publish a quote: native=%v cost=%v estimated=%d",
					o.NativePricing != nil, o.Cost != nil, o.EstimatedCredits)
			}
			var options struct {
				Model             string `json:"model"`
				Endpoint          string `json:"endpoint"`
				PricingStatus     string `json:"pricing_status"`
				ContractScope     string `json:"contract_scope"`
				ContractSource    string `json:"contract_source"`
				ContractCheckedAt string `json:"contract_checked_at"`
			}
			if err := json.Unmarshal(o.ProviderOptions, &options); err != nil {
				t.Fatal(err)
			}
			if options.Model != "P2-20260801" || options.Endpoint != tc.endpoint {
				t.Fatalf("P2 wire identity missing: %+v", options)
			}
			if options.PricingStatus != "pending-verification" || options.ContractScope != "constructed-from-docs" ||
				!strings.Contains(options.ContractSource, "developers.tripo3d.ai") || options.ContractCheckedAt != "2026-11-04" {
				t.Fatalf("P2 source/pricing marker missing: %+v", options)
			}
			defaults := map[string]any{}
			var faceLimit *schema.Parameter
			for index := range o.Parameters {
				parameter := &o.Parameters[index]
				if parameter.Name == "face_limit" {
					faceLimit = parameter
				}
				if len(parameter.Default) == 0 {
					continue
				}
				var value any
				if err := json.Unmarshal(parameter.Default, &value); err != nil {
					t.Fatal(err)
				}
				defaults[parameter.Name] = value
			}
			if defaults["texture_quality"] != "extreme" || defaults["texture_version"] != "v3.5-20260815" ||
				defaults["pbr"] != true || defaults["texture"] != true || defaults["export_uv"] != true || defaults["delight"] != true {
				t.Fatalf("highest-quality defaults missing: %+v", defaults)
			}
			if faceLimit == nil || faceLimit.Minimum == nil || *faceLimit.Minimum != 48 ||
				faceLimit.Maximum == nil || *faceLimit.Maximum != 50000 {
				t.Fatalf("face_limit contract missing: %+v", faceLimit)
			}
			if len(faceLimit.Default) != 0 {
				t.Fatalf("face_limit must stay adaptive (no default), got %s", faceLimit.Default)
			}
			if len(faceLimit.MaximumWhen) != 1 || faceLimit.MaximumWhen[0].Maximum != 25000 ||
				faceLimit.MaximumWhen[0].When.Parameter != "quad" || faceLimit.MaximumWhen[0].When.Operator != "truthy" {
				t.Fatalf("quad face_limit bound missing: %+v", faceLimit.MaximumWhen)
			}
			if len(o.Outputs) != 2 || o.Outputs[0].Role != "model" || o.Outputs[0].Minimum == nil || *o.Outputs[0].Minimum != 1 ||
				o.Outputs[1].Role != "thumbnail" || o.Outputs[1].Delivery != "background" {
				t.Fatalf("required previewable model contract missing: %+v", o.Outputs)
			}
			if tc.capability == schema.ImageTo3D {
				var references *schema.Parameter
				for index := range o.Parameters {
					if o.Parameters[index].Name == "reference_assets" {
						references = &o.Parameters[index]
					}
				}
				if references == nil || !slices.Contains(references.MediaTypes, "image/webp") {
					t.Fatalf("P pages accept PNG/JPEG/WebP inputs: %+v", references)
				}
			}
		})
	}
}

func TestTripoP2FaceLimitRequiresAnAdjustmentReason(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get(schema.OfferingID("tripo/tripo-p2/image-to-3d"))
	if !ok {
		t.Fatal("missing Offering")
	}
	values := map[string]json.RawMessage{"face_limit": json.RawMessage(`150`)}
	if _, err := catalog.ValidateParameterPatch(o, values, true, nil); err == nil || !strings.Contains(err.Error(), "adjustment reason") {
		t.Fatalf("face_limit change without a reason accepted: %v", err)
	}
	if _, err := catalog.ValidateParameterPatch(o, values, true, map[string]string{"face_limit": "game budget caps the mesh at 150 faces"}); err != nil {
		t.Fatalf("face_limit change with a reason refused: %v", err)
	}
	if _, err := catalog.ValidateParameterPatch(o, map[string]json.RawMessage{"texture_quality": json.RawMessage(`"extreme"`)}, true, nil); err != nil {
		t.Fatalf("recommended default still demands a reason: %v", err)
	}
}

func TestMaximumWhenDeclarationValidation(t *testing.T) {
	cases := []struct {
		name      string
		parameter string
		mutate    func(map[string]any)
		wantErr   string
	}{
		{
			name:      "non-number target",
			parameter: "quad",
			mutate: func(p map[string]any) {
				p["maximum_when"] = []any{map[string]any{
					"when":    map[string]any{"parameter": "texture", "operator": "truthy"},
					"maximum": float64(10),
				}}
			},
			wantErr: "只有 number 类型可以声明 maximum_when",
		},
		{
			name:      "above the static maximum",
			parameter: "face_limit",
			mutate: func(p map[string]any) {
				p["maximum_when"] = []any{map[string]any{
					"when":    map[string]any{"parameter": "quad", "operator": "truthy"},
					"maximum": float64(60000),
				}}
			},
			wantErr: "不能大于 maximum",
		},
		{
			name:      "below the static minimum",
			parameter: "face_limit",
			mutate: func(p map[string]any) {
				p["maximum_when"] = []any{map[string]any{
					"when":    map[string]any{"parameter": "quad", "operator": "truthy"},
					"maximum": float64(10),
				}}
			},
			wantErr: "不能小于 minimum",
		},
		{
			name:      "unknown referenced parameter",
			parameter: "face_limit",
			mutate: func(p map[string]any) {
				p["maximum_when"] = []any{map[string]any{
					"when":    map[string]any{"parameter": "missing", "operator": "truthy"},
					"maximum": float64(20000),
				}}
			},
			wantErr: "引用了未知参数",
		},
		{
			name:      "self reference",
			parameter: "face_limit",
			mutate: func(p map[string]any) {
				p["maximum_when"] = []any{map[string]any{
					"when":    map[string]any{"parameter": "face_limit", "operator": "truthy"},
					"maximum": float64(20000),
				}}
			},
			wantErr: "不能引用自身",
		},
		{
			name:      "condition value type mismatch",
			parameter: "face_limit",
			mutate: func(p map[string]any) {
				p["maximum_when"] = []any{map[string]any{
					"when":    map[string]any{"parameter": "quad", "operator": "equals", "value": float64(5)},
					"maximum": float64(20000),
				}}
			},
			wantErr: "value 不符合参数",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadCatalogWithOfferingMutation(t, "tripo/tripo-p2/image-to-3d", func(o map[string]any) {
				parameters, _ := o["parameters"].([]any)
				for _, item := range parameters {
					parameter, _ := item.(map[string]any)
					if parameter["name"] == tc.parameter {
						tc.mutate(parameter)
						return
					}
				}
				t.Fatalf("parameter %q missing", tc.parameter)
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestAdvisedRequiredParameterStillNeedsADefault(t *testing.T) {
	err := loadCatalogWithOfferingMutation(t, "tripo/tripo-p2/image-to-3d", func(o map[string]any) {
		parameters, _ := o["parameters"].([]any)
		for _, item := range parameters {
			parameter, _ := item.(map[string]any)
			if parameter["name"] != "texture" {
				continue
			}
			parameter["required"] = true
			delete(parameter, "default")
			return
		}
		t.Fatal("texture parameter missing")
	})
	if err == nil || !strings.Contains(err.Error(), "invalid advised policy") {
		t.Fatalf("required advised parameter without a default accepted: %v", err)
	}
}
