package catalog

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

var nativeTripoCandidates = []struct {
	name, model, endpoint, fidelity string
	capability                      schema.Capability
	credits                         int
}{
	{"texture", "v3.5-20260815", "models/texture", "", schema.Retexture, 30},
	{"rigging", "v2.5-20260210", "animations/rig", "skeleton", schema.Rigging, 25},
	{"animation", "", "animations/retarget", "animation", schema.Animation, 10},
}

func enabledNativeTripoCatalog(t *testing.T) *Catalog {
	t.Helper()
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var offerings []map[string]json.RawMessage
	if err := json.Unmarshal(document["offerings"], &offerings); err != nil {
		t.Fatal(err)
	}
	var application map[string]json.RawMessage
	if err := json.Unmarshal(document["application"], &application); err != nil {
		t.Fatal(err)
	}
	var discovery map[string]json.RawMessage
	if err := json.Unmarshal(application["provider_discovery"], &discovery); err != nil {
		t.Fatal(err)
	}
	var models []ProviderDiscoveryLegacyModel
	if err := json.Unmarshal(discovery["legacy_models"], &models); err != nil {
		t.Fatal(err)
	}
	for _, o := range offerings {
		var id schema.OfferingID
		var model string
		json.Unmarshal(o["id"], &id)
		json.Unmarshal(o["model_id"], &model)
		for _, candidate := range nativeTripoCandidates {
			if id == schema.OfferingID("tripo/processing/"+candidate.name) {
				o["enabled"] = json.RawMessage("true")
				if !slices.ContainsFunc(models, func(registered ProviderDiscoveryLegacyModel) bool { return registered.ModelID == model }) {
					models = append(models, ProviderDiscoveryLegacyModel{ModelID: model, ProviderKey: "tripo"})
				}
			}
		}
	}
	discovery["legacy_models"], _ = json.Marshal(models)
	application["provider_discovery"], _ = json.Marshal(discovery)
	document["application"], _ = json.Marshal(application)
	document["offerings"], _ = json.Marshal(offerings)
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// #186 route B (2026-10-20): Texture and Retarget are enabled with v2.5 Rig,
// pending their own dev-test public acceptance; any failed slice is reverted.
func TestNativeTripoAvailabilityKeepsExactSourcesFullQuotesAndDelivery(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range nativeTripoCandidates {
		t.Run(candidate.name, func(t *testing.T) {
			id := schema.OfferingID("tripo/processing/" + candidate.name)
			o, ok := c.Get(id)
			enabled := true
			revision := map[string]string{"texture": "2026-10-22.1", "rigging": "2026-10-16.3", "animation": "2026-10-20.2"}[candidate.name]
			if !ok || o.Enabled != enabled || o.Revision != revision || o.ModelID != "tripo-"+candidate.name || o.Capability != candidate.capability || o.AdapterKey != "tripo.v3" || !o.CreditsMetered || o.EstimatedCredits != candidate.credits || o.Retry.MaxAttempts != 1 || len(o.Retry.Retryable) != 0 || o.IdempotentSubmission {
				t.Fatalf("unsafe qualification candidate: %+v", o)
			}
			input := map[string]string{
				"texture":   `{"source_task":"source","prompt":"silver","authorize_extra_credits":true}`,
				"rigging":   `{"source_task":"source","authorize_extra_credits":true}`,
				"animation": `{"source_task":"source"}`,
			}[candidate.name]
			frozen, normalized, normalizeErr := c.Normalize(o.Capability, id, json.RawMessage(input))
			if (normalizeErr == nil) != enabled {
				t.Fatalf("candidate enabled=%v normalization error=%v", enabled, normalizeErr)
			}
			if enabled {
				var values struct {
					Formats []string `json:"output_formats"`
				}
				if err := json.Unmarshal(normalized, &values); err != nil {
					t.Fatal(err)
				}
				wantTotal := map[string]int{"texture": 80, "rigging": 35, "animation": 10}[candidate.name]
				wantFormats := map[string][]string{"texture": {"FBX", "OBJ", "STL", "USDZ", "3MF"}, "rigging": {"FBX"}, "animation": nil}[candidate.name]
				if frozen.EstimateTotalCredits(normalized) != wantTotal || !slices.Equal(values.Formats, wantFormats) {
					t.Fatalf("enabled %s lost full frozen cost/formats: total=%d input=%s", candidate.name, frozen.EstimateTotalCredits(normalized), normalized)
				}
			}
			listed := false
			for _, advertised := range c.List(o.Capability) {
				if advertised.ID == id {
					listed = true
				}
			}
			if listed != enabled {
				t.Fatalf("public listing=%v enabled=%v", listed, enabled)
			}
			legacyCount := 0
			for _, legacy := range c.Application().ProviderDiscovery.LegacyModels {
				if legacy.ModelID == o.ModelID {
					legacyCount++
					if legacy.ProviderKey != "tripo" {
						t.Fatalf("wrong native legacy provider: %+v", legacy)
					}
				}
			}
			if legacyCount != map[bool]int{true: 1, false: 0}[enabled] {
				t.Fatalf("legacy registration count=%d enabled=%v", legacyCount, enabled)
			}
			source := o.Parameters[0]
			wantCapabilities := []schema.Capability{schema.TextTo3D, schema.ImageTo3D}
			wantOfferings := []schema.OfferingID{"tripo/tripo-v3.1-full/text-to-3d", "tripo/tripo-v3.1-full/image-to-3d"}
			if candidate.name == "animation" {
				wantCapabilities = []schema.Capability{schema.Rigging}
				wantOfferings = []schema.OfferingID{"tripo/processing/rigging-v3"}
			}
			wantBinding := map[bool]string{true: "provider", false: ""}[candidate.name == "animation"]
			if source.TaskSourceBinding != wantBinding {
				t.Fatalf("source frozen-config binding = %q, want %q", source.TaskSourceBinding, wantBinding)
			}
			if source.Name != "source_task" || source.Type != "task" || source.Semantic != schema.SemanticSourceTask || source.WireName != "input" || !source.Required || !source.SameProvider || source.TaskSourceKind != schema.TaskSourceUpstreamJob || !slices.Equal(source.TaskCapabilities, wantCapabilities) || !slices.Equal(source.TaskOfferings, wantOfferings) {
				t.Fatalf("native source identity changed: %+v", source)
			}
			if len(o.Outputs) != map[string]int{"texture": 7, "rigging": 3, "animation": 2}[candidate.name] {
				t.Fatalf("unexpected output slots: %+v", o.Outputs)
			}
			out, preview := o.Outputs[0], o.Outputs[len(o.Outputs)-1]
			if out.Role != "model" || out.Variant != "glb" || out.Delivery != schema.AssetDeliveryRequired || out.Minimum == nil || *out.Minimum != 1 || out.MaxBytes != 1<<30 || out.Fidelity != candidate.fidelity || !slices.Equal(out.MediaTypes, []string{"model/gltf-binary"}) {
				t.Fatalf("required model delivery changed: %+v", out)
			}
			if preview.Role != "thumbnail" || preview.Variant != "rendered_image" || preview.Delivery != schema.AssetDeliveryBackground || preview.Minimum == nil || *preview.Minimum != 0 {
				t.Fatalf("preview must stay optional: %+v", preview)
			}
			if !slices.Equal(o.AssetHosts, []string{"tripo3d.com", "tripo3d.ai"}) {
				t.Fatalf("unexpected asset hosts: %v", o.AssetHosts)
			}
			var wire struct {
				Endpoint, Model string
				ObserveEndpoint string `json:"observe_endpoint"`
				ConvertEndpoint string `json:"convert_endpoint"`
				UploadEndpoint  string `json:"upload_endpoint"`
				Aliases         []struct {
					Key, Role, Variant string
					Fields             []string
				} `json:"response_aliases"`
			}
			if err := json.Unmarshal(o.ProviderOptions, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Endpoint != candidate.endpoint || wire.Model != candidate.model || wire.ObserveEndpoint != "tasks/{task_id}" || wire.ConvertEndpoint != map[bool]string{true: "", false: "models/convert"}[candidate.name == "animation"] || wire.UploadEndpoint != "" || len(wire.Aliases) != 2 {
				t.Fatalf("unexpected native route: %+v", wire)
			}
			for i, expected := range []struct{ key, role, variant string }{{"model_url", "model", "glb"}, {"rendered_image_url", "thumbnail", "rendered_image"}} {
				alias := wire.Aliases[i]
				if alias.Key != map[bool]string{true: "model", false: expected.key}[i == 0 && candidate.name != "animation"] || alias.Role != expected.role || alias.Variant != expected.variant || !slices.Equal(alias.Fields, []string{expected.key}) {
					t.Fatalf("unexpected alias: %+v", alias)
				}
			}
		})
	}
	public, err := json.Marshal(c.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"provider_options", "models/texture", "animations/retarget", "v2.5-20260210"} {
		if strings.Contains(string(public), private) {
			t.Fatalf("public application leaks private wire %q", private)
		}
	}
}

func TestNativeTripoQualificationFixtureFreezesQualityAndBipedIdentity(t *testing.T) {
	c := enabledNativeTripoCatalog(t)
	cases := []struct {
		name  string
		input string
		fixed map[string]any
	}{
		{"texture", `{"source_task":"source","prompt":"silver","authorize_extra_credits":true,"texture_quality":"fast","pbr":false,"bake":false,"delight":false}`, map[string]any{"texture_quality": "extreme", "pbr": true, "bake": true, "delight": true, "texture_alignment": "geometry", "prompt": "silver"}},
		{"rigging", `{"source_task":"source","authorize_extra_credits":true,"rig_type":"avian","spec":"mixamo","out_format":"fbx"}`, map[string]any{"rig_type": "biped", "spec": "tripo", "out_format": "glb"}},
		{"animation", `{"source_task":"rig","out_format":"fbx","export_with_geometry":false,"animate_in_place":true}`, map[string]any{"animation_action": "preset:walk", "out_format": "glb", "export_with_geometry": true, "animate_in_place": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := schema.OfferingID("tripo/processing/" + tc.name)
			o, _ := c.Get(id)
			_, normalized, err := c.Normalize(o.Capability, id, json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]any
			if err := json.Unmarshal(normalized, &values); err != nil {
				t.Fatal(err)
			}
			for name, want := range tc.fixed {
				if values[name] != want {
					t.Errorf("%s = %v, want %v", name, values[name], want)
				}
			}
			for _, p := range o.Parameters {
				if _, fixed := tc.fixed[p.Name]; fixed && p.Name != "prompt" && p.Name != "texture_alignment" && p.Name != "animation_action" && (!p.Fixed || p.Presentation == nil || p.Presentation.Surface != "readonly") {
					t.Fatalf("fixed parameter lacks readonly contract: %+v", p)
				}
			}
		})
	}
}

func TestNativeTripoCandidatesDeclareEndpointSpecificParameters(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id      schema.OfferingID
		name    string
		options []string
	}{
		{"tripo/processing/texture", "texture_quality", []string{"fast", "standard", "detailed", "extreme"}},
		{"tripo/processing/texture", "texture_alignment", []string{"geometry", "original_image"}},
		{"tripo/processing/rigging", "rig_type", []string{"biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic"}},
		{"tripo/processing/rigging", "spec", []string{"tripo", "mixamo"}},
		{"tripo/processing/rigging", "out_format", []string{"glb", "fbx"}},
		{"tripo/processing/animation", "out_format", []string{"glb", "fbx"}},
	} {
		o, _ := c.Get(tc.id)
		p, ok := offeringParameter(o.Parameters, tc.name)
		if !ok {
			t.Fatalf("missing %s parameter %s", tc.id, tc.name)
		}
		var got []string
		for _, option := range p.Options {
			got = append(got, option.Value)
		}
		if !slices.Equal(got, tc.options) {
			t.Fatalf("%s %s options = %v", tc.id, tc.name, got)
		}
	}
	texture, _ := c.Get("tripo/processing/texture")
	prompt, ok := offeringParameter(texture.Parameters, "prompt")
	if !ok || prompt.Type != "text" || !prompt.Required || prompt.Semantic != schema.SemanticPrompt || prompt.WireName != "texture_prompt.text" {
		t.Fatalf("texture prompt must map to the documented nested text field: %+v", prompt)
	}
	for _, candidate := range nativeTripoCandidates {
		o, _ := c.Get(schema.OfferingID("tripo/processing/" + candidate.name))
		for _, p := range o.Parameters {
			if p.Type == "asset" || p.Type == "asset-array" || p.Semantic == schema.SemanticSourceURL || p.Semantic == schema.SemanticSourceAsset {
				t.Fatalf("candidate widened beyond native task input: %+v", p)
			}
		}
	}
}

func TestNativeTripoQualificationFixtureRejectsUnsupportedInputsAndPresets(t *testing.T) {
	c := enabledNativeTripoCatalog(t)
	for _, tc := range []struct{ name, input string }{
		{"texture", `{"source_task":"source"}`},
		{"texture", `{"source_task":"source","prompt":"silver","source_asset":"asset"}`},
		{"animation", `{"source_task":"rig","animation_action":"preset:biped:walk"}`},
		{"animation", `{"source_task":"rig","animation_action":"preset:quadruped:walk"}`},
		{"animation", `{"source_task":"rig","animation_action":"preset:idle"}`},
		{"animation", `{"source_task":"rig","animation_action":"preset:run"}`},
		// Deprecated upstream and ignored; the qualified wire never sends it.
		{"animation", `{"source_task":"rig","bake_animation":true}`},
	} {
		id := schema.OfferingID("tripo/processing/" + tc.name)
		o, _ := c.Get(id)
		if _, _, err := c.Normalize(o.Capability, id, json.RawMessage(tc.input)); err == nil {
			t.Fatalf("accepted unsupported %s input: %s", tc.name, tc.input)
		}
	}
	animation, _ := c.Get("tripo/processing/animation")
	for _, p := range animation.Parameters {
		if p.Name == "animation_action" {
			// Only the preset qualified on a v3.0 Rig source is exposed.
			want := []string{"preset:walk"}
			var got []string
			for _, option := range p.Options {
				got = append(got, option.Value)
			}
			if p.WireName != "animation" || !p.Required || !slices.Equal(got, want) {
				t.Fatalf("qualified Rig v3 preset contract changed: %+v", p)
			}
		}
	}
}

// Retarget uses the source Rig's own version, so the Schema binds the exact
// v3.0 Offering and its frozen model/skeleton instead of same-provider rigging.
func TestTripoRetargetBindsExactRigV3FrozenConfiguration(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	animation, _ := c.Get("tripo/processing/animation")
	rig, _ := c.Get("tripo/processing/rigging-v3")
	source := animation.Parameters[0]
	if source.Name != "source_task" || source.TaskSourceBinding != "provider" || !slices.Equal(source.TaskOfferings, []schema.OfferingID{rig.ID}) || !slices.Equal(source.TaskCapabilities, []schema.Capability{schema.Rigging}) {
		t.Fatalf("Retarget source is not the exact Rig v3 Offering: %+v", source)
	}
	if _, ok := offeringParameter(animation.Parameters, "bake_animation"); ok {
		t.Fatal("deprecated bake_animation must not be declared")
	}
	var animationWire struct {
		Model     string `json:"model"`
		SourceRig struct {
			Model   string `json:"model"`
			RigType string `json:"rig_type"`
			Spec    string `json:"spec"`
		} `json:"source_rig"`
	}
	var rigWire struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(animation.ProviderOptions, &animationWire); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rig.ProviderOptions, &rigWire); err != nil {
		t.Fatal(err)
	}
	if animationWire.Model != "" || animationWire.SourceRig.Model != rigWire.Model || rigWire.Model != "v3.0-20260909" {
		t.Fatalf("Retarget must not send its own model and must bind Rig %q: %+v", rigWire.Model, animationWire)
	}
	for name, want := range map[string]string{"rig_type": animationWire.SourceRig.RigType, "spec": animationWire.SourceRig.Spec} {
		p, ok := offeringParameter(rig.Parameters, name)
		if !ok || !p.Fixed || string(p.Default) != `"`+want+`"` {
			t.Fatalf("source Rig %s is not frozen to %q: %+v", name, want, p)
		}
	}
	legacy, _ := c.Get("tripo/processing/rigging")
	if slices.Contains(source.TaskOfferings, legacy.ID) {
		t.Fatal("v2.5 Rig must stay rejected as a Retarget source")
	}
}
