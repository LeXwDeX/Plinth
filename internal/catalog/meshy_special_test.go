package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var meshySpecial = []struct {
	id, capability, endpoint string
	credits                  int
	roles                    []string
}{
	{"meshy/processing/uv-unwrap", "uv-unwrap", "uv-unwrap", 5, []string{"model", "thumbnail"}},
	{"meshy/processing/resize", "resize", "resize", 1, []string{"model"}},
	{"meshy/processing/analyze-printability", "analyze-printability", "print/analyze", 0, []string{"report"}},
	{"meshy/processing/repair-printability", "repair-printability", "print/repair", 10, []string{"model", "report"}},
	{"meshy/processing/multi-color-print", "multi-color-print", "print/multi-color", 10, []string{"print-archive"}},
	{"meshy/prime/text-to-motion", "text-to-motion", "text-to-motion", 10, []string{"clip"}},
}

func specialParameter(t *testing.T, o schema.Offering, name string) schema.Parameter {
	t.Helper()
	for _, p := range o.Parameters {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("%s missing %s", o.ID, name)
	return schema.Parameter{}
}
func TestMeshySpecialQualifiedAvailabilityWithExactDelivery(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range meshySpecial {
		t.Run(tc.capability, func(t *testing.T) {
			o, ok := c.Get(schema.OfferingID(tc.id))
			if !ok {
				t.Fatalf("missing candidate %s", tc.id)
			}
			if o.Enabled != (tc.capability != "repair-printability") || o.IdempotentSubmission || o.Retry.MaxAttempts != 1 || len(o.Retry.Retryable) != 0 || !o.CreditsMetered || o.EstimatedCredits != tc.credits {
				t.Fatalf("unsafe qualification contract: %+v", o)
			}
			if tc.capability == "repair-printability" {
				if _, _, err := c.Normalize(o.Capability, o.ID, json.RawMessage(`{}`)); err == nil {
					t.Fatal("unqualified Repair became selectable")
				}
				if !strings.Contains(o.ModelName, "待实测") {
					t.Fatal("Repair must retain pending qualification label")
				}
			} else if strings.Contains(o.ModelName, "候选") {
				t.Fatal("qualified capability still labeled candidate")
			}
			if err := meshy.ValidateOffering(o); err != nil {
				t.Fatalf("invalid real Meshy contract: %v", err)
			}
			var wire struct {
				Endpoint   string `json:"endpoint"`
				APIVersion string `json:"api_version"`
				Inline     *struct {
					MaxBytes  int64  `json:"max_bytes"`
					MediaType string `json:"media_type"`
				} `json:"inline_model_input"`
				Profile *schema.ModelInputProfileOptions `json:"model_input_profile"`
				Assets  []struct {
					Role      string `json:"role"`
					MediaType string `json:"media_type"`
					Variant   string `json:"variant"`
				} `json:"result_assets"`
			}
			if err := json.Unmarshal(o.ProviderOptions, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Endpoint != tc.endpoint || wire.APIVersion != "v1" {
				t.Fatalf("wrong endpoint: %+v", wire)
			}
			if tc.capability == "text-to-motion" || tc.capability == "multi-color-print" {
				if len(wire.Assets) != 1 || wire.Assets[0].MediaType != o.Outputs[0].MediaTypes[0] || wire.Assets[0].Role != o.Outputs[0].Role || wire.Assets[0].Variant != o.Outputs[0].Variant {
					t.Fatal("scalar MIME must bind the exact declared output")
				}
			}
			var roles []string
			for _, out := range o.Outputs {
				if out.Minimum == nil || *out.Minimum != 1 || out.Delivery != "required" || out.MaxBytes <= 0 || len(out.MediaTypes) != 1 {
					t.Fatalf("unbounded/nonrequired output: %+v", out)
				}
				roles = append(roles, out.Role)
				switch out.Role {
				case "report":
					if out.MediaTypes[0] != "application/vnd.art-ai.print-report+json" || out.MaxBytes != 32768 {
						t.Fatal("wrong normalized report")
					}
				case "print-archive":
					if out.Variant != "3mf" || out.MediaTypes[0] != "model/3mf" {
						t.Fatal("wrong archive")
					}
				case "clip":
					if out.Variant != "fbx" || out.MediaTypes[0] != "model/fbx" {
						t.Fatal("wrong motion")
					}
				case "model":
					if out.Variant != "glb" || out.MediaTypes[0] != "model/gltf-binary" {
						t.Fatal("wrong model")
					}
				}
			}
			if !slices.Equal(roles, tc.roles) {
				t.Fatalf("roles=%v", roles)
			}
			if tc.capability == "text-to-motion" {
				if wire.Inline != nil || wire.Profile != nil {
					t.Fatal("motion must not have model input")
				}
				return
			}
			p := specialParameter(t, o, "source_asset")
			if !p.Required || p.Type != "asset" || p.Semantic != schema.SemanticSourceAsset || p.WireName != "model_url" || p.SameProvider || p.MaxBytes != 10485760 || !slices.Equal(p.Extensions, []string{".glb"}) || p.UploadedModel == nil || p.UploadedModel.NativeOnly || p.UploadedModel.ProfileRevision != "1" || p.UploadedModel.ProfileLimits == nil {
				t.Fatalf("wrong frozen cross-provider source: %+v", p)
			}
			for _, p := range o.Parameters {
				if p.Type == "task" || p.Semantic == schema.SemanticSourceURL {
					t.Fatal("unqualified upstream source path")
				}
			}
			wantMime := "model/gltf-binary"
			if tc.capability == "uv-unwrap" || tc.capability == "resize" {
				wantMime = "application/octet-stream"
			}
			if wire.Inline == nil || wire.Inline.MaxBytes != p.MaxBytes || wire.Inline.MediaType != wantMime || wire.Profile == nil || wire.Profile.Parameter != p.Name || wire.Profile.ProfileLimits == nil {
				t.Fatal("missing exact profile/inline binding")
			}
			if tc.capability == "uv-unwrap" && wire.Profile.MaxInstancedTriangles != 40000 {
				t.Fatal("UV instance triangle cap missing")
			}
			if tc.capability == "multi-color-print" && (!p.UploadedModel.RequireTextures || !p.UploadedModel.RequireSingleBaseColorTexture || p.UploadedModel.Requirements == "") {
				t.Fatal("single base color atlas must be enforced")
			}
		})
	}
	legacy, _ := c.Get("meshy/processing/remesh")
	if !legacy.Enabled || legacy.Retry.MaxAttempts != 2 || specialParameter(t, legacy, "source_task").WireName != "input_task_id" {
		t.Fatal("legacy remesh contract changed")
	}
}

func TestMeshySpecialCandidateInputBoundaries(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	get := func(id string) schema.Offering {
		o, ok := c.Get(schema.OfferingID(id))
		if !ok {
			t.Fatalf("missing %s", id)
		}
		return o
	}
	resize := get("meshy/processing/resize")
	for _, raw := range []string{`{"source_asset":"owned"}`, `{"source_asset":"owned","resize_height":0}`, `{"source_asset":"owned","resize_height":-1}`, `{"source_asset":"owned","resize_height":0.1,"resize_longest_side":0.2}`, `{"source_asset":"owned","auto_size":true}`, `{"source_asset":"owned","resize_height":1,"origin_at":"other"}`} {
		if _, err := normalizeInput(resize, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`{"source_asset":"owned","resize_height":0.001}`, `{"source_asset":"owned","resize_longest_side":0.2,"origin_at":"center"}`} {
		if _, err := normalizeInput(resize, json.RawMessage(raw)); err != nil {
			t.Fatalf("rejected %s: %v", raw, err)
		}
	}
	motion := get("meshy/prime/text-to-motion")
	for _, v := range []string{"2", "2.5", "10"} {
		raw, _ := json.Marshal(map[string]any{"prompt": "a character waving", "duration": v, "mode": "swift"})
		normalized, err := normalizeInput(motion, raw)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(normalized, &m)
		if m["mode"] != "prime" {
			t.Fatal("quality override")
		}
		p := specialParameter(t, motion, "duration")
		if string(p.WireValues[v]) != v {
			t.Fatalf("duration wire must be numeric: %s", p.WireValues[v])
		}
	}
	for _, v := range []any{"1.5", "2.25", "10.5", 2.5} {
		raw, _ := json.Marshal(map[string]any{"prompt": "wave", "duration": v})
		if _, err := normalizeInput(motion, raw); err == nil {
			t.Fatalf("accepted invalid duration %v", v)
		}
	}
	if _, err := normalizeInput(motion, json.RawMessage(`{"prompt":"`+strings.Repeat("x", 401)+`","duration":"3"}`)); err == nil {
		t.Fatal("long prompt")
	}
	multicolor := get("meshy/processing/multi-color-print")
	normalized, err := normalizeInput(multicolor, json.RawMessage(`{"source_asset":"owned","style":"cartoon","max_depth":3}`))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(normalized, &m)
	if m["style"] != "realistic" || m["max_depth"] != float64(6) || m["max_colors"] != float64(16) {
		t.Fatalf("highest fidelity defaults: %s", normalized)
	}
}

func TestMeshySpecialToolsAndHarnessAreSchemaMapped(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	a := c.Application()
	var motion *ChatToolSchema
	for i := range a.Chat.Tools {
		tool := &a.Chat.Tools[i]
		if tool.Name == "create_motion_clip" {
			motion = tool
		}
		if tool.Name == "process_model" || tool.Name == "plan_model_processing" {
			for _, tc := range meshySpecial[:5] {
				if !slices.Contains(tool.Execution.Capabilities, schema.Capability(tc.capability)) || tool.Processing.Steps[tc.capability] != schema.Capability(tc.capability) {
					t.Fatalf("%s lacks %s", tool.Name, tc.capability)
				}
			}
		}
	}
	if motion == nil || !motion.IgnoreUserModel || motion.RequireUserModel || motion.Processing != nil || len(motion.ResultAssetRoles) != 0 || motion.ResultAttachmentKind != "none" || motion.Capability != "text-to-motion" {
		t.Fatalf("motion must return a real Task handle without model attachment: %+v", motion)
	}
	for _, tc := range meshySpecial[:5] {
		found := false
		for _, op := range a.Chat.Harness.Operations {
			if op.ID == tc.capability {
				found = true
				if op.Group != "common" || len(op.ModelIDs) != 0 || op.ToolName != "process_model" || len(op.Prerequisites) != 0 {
					t.Fatalf("invalid common operation: %+v", op)
				}
				if tc.capability != "repair-printability" && op.UnavailableReason != "" {
					t.Fatalf("qualified Harness operation carries a static disable marker: %s", op.UnavailableReason)
				}
				if tc.capability == "repair-printability" && op.UnavailableReason == "" {
					t.Fatal("unqualified Repair lost its disable reason")
				}
			}
		}
		if !found {
			t.Fatalf("missing Harness %s", tc.capability)
		}
	}
}

func TestMeshyQualifiedPromotionAvailabilityAndPublicContract(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	publicJSON, err := json.Marshal(c.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), "provider_options") || strings.Contains(string(publicJSON), "provider_connections") {
		t.Fatal("private execution configuration leaked")
	}
	published := map[string]bool{}
	for _, model := range c.Application().ProviderDiscovery.LegacyModels {
		published[model.ModelID] = true
	}
	inputs := map[string]json.RawMessage{
		"uv-unwrap":            json.RawMessage(`{"source_asset":"owned"}`),
		"resize":               json.RawMessage(`{"source_asset":"owned","resize_height":0.2}`),
		"analyze-printability": json.RawMessage(`{"source_asset":"owned"}`),
		"multi-color-print":    json.RawMessage(`{"source_asset":"owned"}`),
		"text-to-motion":       json.RawMessage(`{"prompt":"a character waving","duration":"3"}`),
	}
	for _, tc := range meshySpecial {
		o, ok := c.Get(schema.OfferingID(tc.id))
		if !ok {
			t.Fatal("missing offering")
		}
		if tc.capability == "repair-printability" {
			if o.Enabled || published[o.ModelID] || o.Revision != "2026-10-15.1" {
				t.Fatal("unqualified Repair became executable")
			}
			continue
		}
		if !o.Enabled || o.Revision != "2026-10-15.2" || !published[o.ModelID] || strings.Contains(o.ModelName, "候选") {
			t.Errorf("qualified %s missing exact promoted availability", tc.id)
		}
		if _, _, err := c.Normalize(o.Capability, o.ID, inputs[tc.capability]); err != nil {
			t.Errorf("qualified %s not selectable: %v", tc.id, err)
		}
	}
	o, _ := c.Get("meshy/processing/multi-color-print")
	brand := specialParameter(t, o, "printer_brand")
	if !brand.Required || !brand.Fixed || string(brand.Default) != `"bambu"` || len(brand.Options) != 1 || brand.Options[0].Value != "bambu" || brand.Presentation == nil || brand.Presentation.Surface != schema.SurfaceReadOnly {
		t.Fatal("public printer brand is not fixed to the qualified Bambu scope")
	}
	if _, err := validateValue(brand, "prusa"); err == nil {
		t.Fatal("non-Bambu parameter value was accepted")
	}
	// Fixed parameters retain the established normalization contract: callers may
	// echo another value, but only the frozen default can reach the wire.
	_, normalized, err := c.Normalize(o.Capability, o.ID, json.RawMessage(`{"source_asset":"owned","printer_brand":"prusa"}`))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if json.Unmarshal(normalized, &values) != nil || values["printer_brand"] != "bambu" {
		t.Fatal("unqualified brand could reach Provider wire")
	}
	colors := specialParameter(t, o, "max_colors")
	if colors.Label != "最多打印颜色数" || !strings.Contains(colors.Description, "上限") || !strings.Contains(colors.Description, "不保证") {
		t.Fatal("color count overstates actual output")
	}
	motion, _ := c.Get("meshy/prime/text-to-motion")
	duration := specialParameter(t, motion, "duration")
	if duration.Label != "目标动作时长（秒）" || !strings.Contains(duration.Description, "末帧采样跨度可能短一帧") {
		t.Fatal("target duration must not promise continuous byte duration")
	}
}

func TestMeshyQualifiedHarnessPublicOperationsAreSelectable(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{"uv-unwrap": false, "resize": false, "analyze-printability": false, "multi-color-print": false}
	for _, op := range c.Application().Public().Chat.Harness.Operations {
		if _, ok := selected[op.ID]; ok {
			selected[op.ID] = true
			if op.UnavailableReason != "" {
				t.Errorf("qualified operation %s carries static public disable marker", op.ID)
			}
		}
		if op.ID == "repair-printability" && op.UnavailableReason == "" {
			t.Error("unqualified Repair lost disable reason")
		}
	}
	for op, seen := range selected {
		if !seen {
			t.Errorf("missing qualified operation %s", op)
		}
	}
}
