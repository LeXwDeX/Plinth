package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/rigcheckreport"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #277: rigging takes the skeleton type, the skeleton naming or template and the
// finger bones from the Schema, and the production brief's target engine picks
// the default naming. Parameters, options, defaults and constraints are read
// from the shipped Product Schema; the values follow the official pages read on
// 2026-11-12 (Tripo animations-rig, animations-retarget and animations-rig-check,
// Meshy rigging-and-animation). No real request backs them yet.

func optionValuesOf(t *testing.T, offering schema.Offering, name string) []string {
	t.Helper()
	parameter, ok := offeringParameter(offering.Parameters, name)
	if !ok {
		t.Fatalf("%s has no %s", offering.ID, name)
	}
	values := make([]string, 0, len(parameter.Options))
	for _, option := range parameter.Options {
		values = append(values, option.Value)
	}
	return values
}

func TestTripoRigV3OpensTheSkeletonTypeAndNaming(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rig, _ := c.Get("tripo/processing/rigging-v3")
	wantTypes := []string{"biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic", "others"}
	wantNamings := []string{"tripo", "mixamo", "actorcore", "unity", "unreal", "vrm"}
	if got := optionValuesOf(t, rig, "rig_type"); !slices.Equal(got, wantTypes) {
		t.Fatalf("rig_type options = %v", got)
	}
	if got := optionValuesOf(t, rig, "spec"); !slices.Equal(got, wantNamings) {
		t.Fatalf("spec options = %v", got)
	}
	for name, want := range map[string]string{"rig_type": "biped", "spec": "tripo"} {
		p, _ := offeringParameter(rig.Parameters, name)
		if p.Fixed || string(p.Default) != `"`+want+`"` || p.Presentation == nil || p.Presentation.Surface != schema.SurfaceProvider {
			t.Fatalf("%s must be an open choice defaulting to %s: %+v", name, want, p)
		}
		// Both the person and the Agent may choose them.
		if !rig.UserParameterAllowed(p) || !rig.AgentParameterAllowed(p) {
			t.Fatalf("%s is not a public choice", name)
		}
	}
	// The rig check recommends one of seven types; every one is a choice here and on v2.5.
	legacy, _ := c.Get("tripo/processing/rigging")
	for _, rigType := range []string{"biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic"} {
		if !rigcheckreport.ValidRigType(rigType) || !slices.Contains(wantTypes, rigType) || !slices.Contains(optionValuesOf(t, legacy, "rig_type"), rigType) {
			t.Fatalf("%q, a type the rig check recommends, is not a choice of both Rig Offerings", rigType)
		}
	}
	// others is documented only under v3.0.
	if slices.Contains(optionValuesOf(t, legacy, "rig_type"), "others") {
		t.Fatal("v2.5 must not offer others")
	}
	for _, option := range mustParameter(t, rig, "rig_type").Options {
		if strings.TrimSpace(option.Label) == "" || option.Label == option.Value {
			t.Fatalf("rig_type option %q lacks a Chinese label: %q", option.Value, option.Label)
		}
	}
}

func mustParameter(t *testing.T, offering schema.Offering, name string) schema.Parameter {
	t.Helper()
	parameter, ok := offeringParameter(offering.Parameters, name)
	if !ok {
		t.Fatalf("%s has no %s", offering.ID, name)
	}
	return parameter
}

func TestTripoRigV3ConstrainsTheNamingToABipedSkeleton(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	normalize := func(input string) map[string]any {
		t.Helper()
		_, normalized, err := c.Normalize(schema.Rigging, "tripo/processing/rigging-v3", json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		var values map[string]any
		if err := json.Unmarshal(normalized, &values); err != nil {
			t.Fatal(err)
		}
		return values
	}
	const base = `{"source_task":"source","authorize_extra_credits":true`
	// The defaults are the historical biped / Tripo / GLB.
	if values := normalize(base + `}`); values["rig_type"] != "biped" || values["spec"] != "tripo" || values["out_format"] != "glb" {
		t.Fatalf("defaults = %v", values)
	}
	for _, spec := range []string{"tripo", "mixamo", "actorcore", "unity", "unreal", "vrm"} {
		if values := normalize(base + `,"rig_type":"biped","spec":"` + spec + `"}`); values["spec"] != spec {
			t.Fatalf("biped naming %s = %v", spec, values["spec"])
		}
	}
	// The upstream applies a naming to a biped only: for every other type the
	// Schema fixes it to Tripo instead of recording a choice that has no effect.
	for _, rigType := range []string{"quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic", "others"} {
		if values := normalize(base + `,"rig_type":"` + rigType + `","spec":"unreal"}`); values["rig_type"] != rigType || values["spec"] != "tripo" {
			t.Fatalf("%s with unreal = %v", rigType, values)
		}
	}
	for _, input := range []string{
		base + `,"rig_type":"humanoid"}`,
		base + `,"spec":"godot"}`,
		base + `,"rig_type":"biped","spec":""}`,
		base + `,"out_format":"fbx","rig_type":"biped"}`, // stays frozen: replaced, not an error
	} {
		_, normalized, err := c.Normalize(schema.Rigging, "tripo/processing/rigging-v3", json.RawMessage(input))
		if strings.Contains(input, "out_format") {
			if err != nil || !strings.Contains(string(normalized), `"out_format":"glb"`) {
				t.Fatalf("out_format is not frozen: %s, %v", normalized, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("accepted %s: %s", input, normalized)
		}
	}
	// The naming is greyed out in the form for anything but a biped.
	spec := mustParameter(t, mustOffering(t, c, "tripo/processing/rigging-v3"), "spec")
	if len(spec.Presentation.DisabledWhen) != 1 || spec.Presentation.DisabledWhen[0].Parameter != "rig_type" || spec.Presentation.DisabledWhen[0].Operator != "not-equals" || string(spec.Presentation.DisabledWhen[0].Value) != `"biped"` {
		t.Fatalf("spec is not disabled for a non-biped skeleton: %+v", spec.Presentation)
	}
}

func mustOffering(t *testing.T, c *Catalog, id string) schema.Offering {
	t.Helper()
	offering, ok := c.Get(schema.OfferingID(id))
	if !ok {
		t.Fatalf("missing %s", id)
	}
	return offering
}

func TestTripoRigV25OpensOnlyTheSkeletonType(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rig := mustOffering(t, c, "tripo/processing/rigging")
	if got := optionValuesOf(t, rig, "rig_type"); !slices.Equal(got, []string{"biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic"}) {
		t.Fatalf("rig_type options = %v", got)
	}
	if p := mustParameter(t, rig, "rig_type"); p.Fixed || string(p.Default) != `"biped"` || !rig.AgentParameterAllowed(p) {
		t.Fatalf("v2.5 skeleton type is not an open choice: %+v", p)
	}
	// Engine namings are documented only under v3.0: v2.5 keeps Tripo naming fixed.
	if p := mustParameter(t, rig, "spec"); !p.Fixed || string(p.Default) != `"tripo"` || rig.AgentParameterAllowed(p) {
		t.Fatalf("v2.5 naming is not frozen to Tripo: %+v", p)
	}
	_, normalized, err := c.Normalize(schema.Rigging, "tripo/processing/rigging", json.RawMessage(`{"source_task":"source","authorize_extra_credits":true,"rig_type":"serpentine","spec":"unreal"}`))
	if err != nil || !strings.Contains(string(normalized), `"rig_type":"serpentine"`) || !strings.Contains(string(normalized), `"spec":"tripo"`) {
		t.Fatalf("normalized = %s, %v", normalized, err)
	}
}

func TestTripoAnimationOffersEveryDocumentedBasicPresetByRigKind(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	animation := mustOffering(t, c, "tripo/processing/animation")
	action := mustParameter(t, animation, "animation_action")
	groups := map[string][]string{}
	for _, option := range action.Options {
		if strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Group) == "" {
			t.Fatalf("preset %q needs a label and a group: %+v", option.Value, option)
		}
		groups[option.Group] = append(groups[option.Group], option.Value)
	}
	if len(groups) != 2 || len(groups["双足骨架"]) != 11 || len(groups["其他生物骨架"]) != 5 {
		t.Fatalf("preset groups = %v", groups)
	}
	// Every offered preset is accepted by normalization, and the default stays the walk.
	for _, option := range action.Options {
		_, normalized, err := c.Normalize(schema.Animation, "tripo/processing/animation", json.RawMessage(`{"source_task":"rig","animation_action":"`+option.Value+`"}`))
		if err != nil || !strings.Contains(string(normalized), `"animation_action":"`+option.Value+`"`) {
			t.Fatalf("%s: %s, %v", option.Value, normalized, err)
		}
	}
	_, normalized, err := c.Normalize(schema.Animation, "tripo/processing/animation", json.RawMessage(`{"source_task":"rig"}`))
	if err != nil || !strings.Contains(string(normalized), `"animation_action":"preset:walk"`) {
		t.Fatalf("default preset: %s, %v", normalized, err)
	}
}

func TestMeshyRiggingDeclaresSkeletonTemplateAndFingers(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rig := mustOffering(t, c, "meshy/processing/rigging")
	wantTemplates := []string{"meshyrig", "mixamo", "ue5_mannequin", "unity_humanoid", "max_biped", "maya_human_ik", "blender_rigify_basic_human", "cc5_hd_neutral_base", "daz_genesis9"}
	if got := optionValuesOf(t, rig, "skeleton_template"); !slices.Equal(got, wantTemplates) {
		t.Fatalf("skeleton_template options = %v", got)
	}
	template, fingers := mustParameter(t, rig, "skeleton_template"), mustParameter(t, rig, "include_fingers")
	if string(template.Default) != `"meshyrig"` || template.Fixed || template.Type != "select" || !rig.AgentParameterAllowed(template) || !rig.UserParameterAllowed(template) {
		t.Fatalf("skeleton_template: %+v", template)
	}
	if string(fingers.Default) != `false` || fingers.Fixed || fingers.Type != "boolean" || !rig.AgentParameterAllowed(fingers) || !rig.UserParameterAllowed(fingers) {
		t.Fatalf("include_fingers: %+v", fingers)
	}
	// Defaults are frozen with the request but stay off the wire, so the request of
	// an unchanged Rigging is the historical one.
	_, normalized, err := c.Normalize(schema.Rigging, "meshy/processing/rigging", json.RawMessage(`{"source_task":"source"}`))
	if err != nil {
		t.Fatal(err)
	}
	var frozen map[string]any
	_ = json.Unmarshal(normalized, &frozen)
	if frozen["skeleton_template"] != "meshyrig" || frozen["include_fingers"] != false {
		t.Fatalf("frozen defaults = %v", frozen)
	}
	if !schema.ParameterOmitted(template, frozen) || !schema.ParameterOmitted(fingers, frozen) {
		t.Fatalf("defaults must be omitted from the wire: %v", frozen)
	}
	frozen["skeleton_template"], frozen["include_fingers"] = "ue5_mannequin", true
	if schema.ParameterOmitted(template, frozen) || schema.ParameterOmitted(fingers, frozen) {
		t.Fatalf("a chosen template and fingers must reach the wire: %v", frozen)
	}
	// Finger bones cannot go with a texture image (the Rigging page's 400).
	for _, input := range []string{
		`{"source_task":"source","include_fingers":true,"texture_image_url":"https://example.test/skin.png"}`,
		`{"source_task":"source","skeleton_template":"godot_humanoid"}`,
		`{"source_task":"source","include_fingers":"yes"}`,
	} {
		if _, _, err := c.Normalize(schema.Rigging, "meshy/processing/rigging", json.RawMessage(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	for _, input := range []string{
		`{"source_task":"source","skeleton_template":"unity_humanoid","include_fingers":true,"height_meters":1.8}`,
		`{"source_task":"source","include_fingers":false,"texture_image_url":"https://example.test/skin.png"}`,
	} {
		if _, _, err := c.Normalize(schema.Rigging, "meshy/processing/rigging", json.RawMessage(input)); err != nil {
			t.Fatalf("refused %s: %v", input, err)
		}
	}
}

func TestBriefTargetEngineDeclaresTheSkeletonDefaults(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	defaults := c.Application().Chat.Model3DUsage.Brief.ParameterDefaults
	want := map[string]map[string]map[string]string{
		"unity":  {"tripo/processing/rigging-v3": {"spec": "unity"}, "meshy/processing/rigging": {"skeleton_template": "unity_humanoid"}},
		"unreal": {"tripo/processing/rigging-v3": {"spec": "unreal"}, "meshy/processing/rigging": {"skeleton_template": "ue5_mannequin"}},
	}
	if len(defaults) != len(want) {
		t.Fatalf("brief.parameter_defaults = %+v", defaults)
	}
	for _, entry := range defaults {
		engines, ok := want[entry.Value]
		if !ok || entry.Field != "target_engine" {
			t.Fatalf("unexpected default %+v", entry)
		}
		if len(entry.ParameterOverrides) != len(engines) {
			t.Fatalf("%s overrides = %v", entry.Value, entry.ParameterOverrides)
		}
		for offering, values := range engines {
			for name, value := range values {
				if string(entry.ParameterOverrides[offering][name]) != `"`+value+`"` {
					t.Fatalf("%s %s.%s = %s", entry.Value, offering, name, entry.ParameterOverrides[offering][name])
				}
			}
		}
	}
	// Godot, Web and other engines name no default: the service's own skeleton applies.
	for _, entry := range defaults {
		if slices.Contains([]string{"godot", "web", "other"}, entry.Value) {
			t.Fatalf("%s must keep the service default", entry.Value)
		}
	}
	// The mapping is server-only: the browser reads the brief's fields, not what an option does.
	public, err := json.Marshal(c.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"parameter_defaults", "ue5_mannequin", "unity_humanoid"} {
		if strings.Contains(string(public), private) {
			t.Fatalf("the public application leaks %q", private)
		}
	}
}

func TestBriefParameterDefaultsAreValidatedAtLoad(t *testing.T) {
	defaults := func(document map[string]any) []any {
		brief := document["application"].(map[string]any)["chat"].(map[string]any)["model3d_usage"].(map[string]any)["brief"].(map[string]any)
		return brief["parameter_defaults"].([]any)
	}
	first := func(document map[string]any) map[string]any { return defaults(document)[0].(map[string]any) }
	overrides := func(document map[string]any, offering string) map[string]any {
		return first(document)["parameter_overrides"].(map[string]any)[offering].(map[string]any)
	}
	for name, tc := range map[string]struct {
		mutate func(document map[string]any)
		want   string
	}{
		"a field the brief does not declare": {func(d map[string]any) { first(d)["field"] = "no_such_field" }, "不是简报的 select 字段"},
		"a field that is no select":          {func(d map[string]any) { first(d)["field"] = "style" }, "不是简报的 select 字段"},
		"an option the field does not have":  {func(d map[string]any) { first(d)["value"] = "cryengine" }, "不是字段"},
		"the same option twice": {func(d map[string]any) {
			brief := d["application"].(map[string]any)["chat"].(map[string]any)["model3d_usage"].(map[string]any)["brief"].(map[string]any)
			brief["parameter_defaults"] = append(defaults(d), defaults(d)[0])
		}, "重复"},
		"no overrides": {func(d map[string]any) { first(d)["parameter_overrides"] = map[string]any{} }, "不能为空"},
		"an Offering that does not exist": {func(d map[string]any) {
			first(d)["parameter_overrides"] = map[string]any{"tripo/processing/nope": map[string]any{"spec": "unity"}}
		}, "不存在"},
		"a parameter that is fixed":           {func(d map[string]any) { overrides(d, "tripo/processing/rigging-v3")["out_format"] = "fbx" }, "不是可公开调整的参数"},
		"a parameter the Offering lacks":      {func(d map[string]any) { overrides(d, "tripo/processing/rigging-v3")["nope"] = "x" }, "不属于 Offering"},
		"a value the parameter does not take": {func(d map[string]any) { overrides(d, "tripo/processing/rigging-v3")["spec"] = "godot" }, "spec"},
	} {
		t.Run(name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, tc.mutate)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	// An override the Offering's own rules rewrite is refused: a naming that the
	// Schema forces back to Tripo for the skeleton type given next to it.
	err := mutateDefaultCatalog(t, func(document map[string]any) {
		values := overrides(document, "tripo/processing/rigging-v3")
		values["rig_type"] = "quadruped"
		values["spec"] = "unity"
	})
	if err == nil || !strings.Contains(err.Error(), "改写") {
		t.Fatalf("an override rewritten by forced_when was accepted: %v", err)
	}
}
