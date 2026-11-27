package catalog

import (
	"encoding/json"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #704: whether a 3D generation makes a texture is the person's call (a white
// model is a legitimate request). The texture switch of the Tripo and Meshy
// 7.1/t2.5 generation Offerings is an ordinary panel setting, as Meshy-7's
// already was: no advised policy, no reason, surface provider. Turning it off
// still turns PBR off where the Offering declares that, and the Tripo
// settings that only mean something with a texture hide with it.
func TestTextureSwitchIsAPanelSetting_704(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	switches := map[schemacontract.OfferingID]string{
		"meshy/meshy-7.1/image-to-3d":            "should_texture",
		"meshy/meshy-t2.5/image-to-3d":           "should_texture",
		"meshy/meshy-7.1-multiview/image-to-3d":  "should_texture",
		"tripo/tripo-v3.1-full/text-to-3d":       "texture",
		"tripo/tripo-v3.1/text-to-3d":            "texture",
		"tripo/tripo-v3.1-full/image-to-3d":      "texture",
		"tripo/tripo-v3.1-multiview/image-to-3d": "texture",
		"tripo/tripo-v3.1/image-to-3d":           "texture",
		"tripo/tripo-p2/image-to-3d":             "texture",
		"tripo/tripo-p2-multiview/image-to-3d":   "texture",
		"tripo/tripo-p2/text-to-3d":              "texture",
	}
	// PBR follows the switch where the Offering declares it (Meshy's
	// multiview Offering declares no such rule, #704 leaves it as it was).
	pbr := map[schemacontract.OfferingID]string{
		"meshy/meshy-7.1/image-to-3d":  "enable_pbr",
		"meshy/meshy-t2.5/image-to-3d": "enable_pbr",
	}
	textureOnly := []string{"texture_quality", "texture_version", "export_texture_format", "export_texture_size"}
	for id, name := range switches {
		offering, ok := product.Get(id)
		if !ok {
			t.Fatalf("missing %s", id)
		}
		parameters := map[string]schemacontract.Parameter{}
		for _, parameter := range offering.Parameters {
			parameters[parameter.Name] = parameter
		}
		toggle, ok := parameters[name]
		if !ok {
			t.Fatalf("%s declares no %s", id, name)
		}
		if toggle.AdjustmentPolicy != "" || toggle.AdjustmentReason != "" || toggle.Presentation == nil ||
			toggle.Presentation.Surface != schemacontract.SurfaceProvider || !offering.UserParameterAllowed(toggle) ||
			!offering.AgentParameterAllowed(toggle) || offering.AdvisedParameter(name) {
			t.Errorf("%s.%s = policy %q, presentation %+v; want an ordinary provider setting", id, name, toggle.AdjustmentPolicy, toggle.Presentation)
		}
		if name == "texture" {
			pbr[id] = "pbr"
		}
		if follower, ok := pbr[id]; ok {
			values := map[string]any{name: false, follower: true}
			schemacontract.ApplyForcedValues(offering.Parameters, values)
			if values[follower] != false {
				t.Errorf("%s: %s=false leaves %s = %v", id, name, follower, values[follower])
			}
		}
		if name != "texture" {
			continue
		}
		for _, dependent := range textureOnly {
			parameter, declared := parameters[dependent]
			if !declared {
				continue
			}
			shown := false
			for _, condition := range parameter.Presentation.VisibleWhen {
				shown = shown || (condition.Parameter == "texture" && condition.Operator == "truthy")
			}
			if !shown {
				raw, _ := json.Marshal(parameter.Presentation.VisibleWhen)
				t.Errorf("%s.%s visible_when = %s, want it shown only with a texture", id, dependent, raw)
			}
		}
	}
}
