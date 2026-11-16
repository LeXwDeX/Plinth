package chat_test

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #306: the 加工 panel shows the value a parameter that follows the session
// purpose will take before the person confirms the step. The value is the one
// the plan freezes for the parameter the panel left out (#292): the resolver's
// FollowedValues and ApplyToPlanStep read the same Values, so the preview cannot
// disagree with the plan. A session whose purpose gives the parameter nothing
// shows the Offering's default, which is what the step takes then.

// followedDefault is the value the Offering gives the parameter on its own.
func followedDefault(t *testing.T, offering generation.Offering, name string) string {
	t.Helper()
	for _, parameter := range offering.Parameters {
		if parameter.Name == name {
			var value string
			if err := json.Unmarshal(parameter.Default, &value); err != nil {
				t.Fatalf("%s default %s: %v", offering.ID, parameter.Default, err)
			}
			return value
		}
	}
	t.Fatalf("%s declares no %s", offering.ID, name)
	return ""
}

func TestFollowedValuesAreTheValueThePlanStepFreezes(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	for name, test := range map[string]struct {
		brief map[string]any
		want  string
	}{
		"game character":                     {map[string]any{"purpose": "game_character"}, "2k"},
		"game character on a phone":          {map[string]any{"purpose": "game_character", "target_platform": "mobile"}, "2k"},
		"game character on a PC":             {map[string]any{"purpose": "game_character", "target_platform": "pc_console"}, "4k"},
		"game prop":                          {map[string]any{"purpose": "game_prop"}, "2k"},
		"web and AR":                         {map[string]any{"purpose": "web_ar"}, "2k"},
		"print":                              {map[string]any{"purpose": "print"}, "4k"},
		"display bounds nothing below 8K":    {map[string]any{"purpose": "render"}, "8k"},
		"a brief without a purpose":          {map[string]any{"style": "写实"}, "8k"},
		"a purpose the Schema no longer has": {map[string]any{"purpose": "retired_purpose"}, "8k"},
		"no brief":                           {nil, "8k"},
	} {
		t.Run(name, func(t *testing.T) {
			brief := purposeParamsBrief(t, test.brief)
			for _, id := range tripoExportOfferings {
				offering, ok := product.Get(id)
				if !ok {
					t.Fatalf("missing %s", id)
				}
				got := resolver.FollowedValues(brief, offering)
				if len(got) != 1 || string(got["export_texture_size"]) != `"`+test.want+`"` {
					t.Fatalf("%s: followed values = %v, want only export_texture_size %q", id, got, test.want)
				}
				// The plan the panel creates freezes the same value: the one the
				// purpose gave, or the Offering's default when it gave none.
				step, err := resolver.ApplyToPlanStep(brief, offering, json.RawMessage(planStepRequest))
				if err != nil {
					t.Fatal(err)
				}
				frozen, filled := purposeParamsDecode(t, step)["export_texture_size"].(string)
				if !filled {
					frozen = followedDefault(t, offering, "export_texture_size")
				}
				if frozen != test.want {
					t.Fatalf("%s: the plan step takes %q, the preview says %q", id, frozen, test.want)
				}
			}
		})
	}
}

// Only the parameters the Offering declares as following the purpose are
// resolved; the other values the purpose gives an Offering (the Agent and the
// direct task API apply them all, here a face limit) are not part of what the
// plan fills.
func TestFollowedValuesAreOnlyTheFollowingParameters(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	offering, ok := product.Get("tripo/geometry/decimate-triangle")
	if !ok {
		t.Fatal("missing the shipped decimation Offering")
	}
	usage := resolver.Model3D
	profiles := append([]catalog.ChatModel3DUsageProfile(nil), usage.Profiles...)
	for index := range profiles {
		if profiles[index].ID != "game_character" {
			continue
		}
		overrides := map[string]map[string]json.RawMessage{}
		for id, values := range profiles[index].ParameterOverrides {
			overrides[id] = values
		}
		overrides[string(offering.ID)] = map[string]json.RawMessage{
			"face_limit": json.RawMessage(`20000`), "export_texture_size": json.RawMessage(`"2k"`),
		}
		profiles[index].ParameterOverrides = overrides
	}
	usage.Profiles = profiles
	resolver.Model3D = usage

	brief := purposeParamsBrief(t, map[string]any{"purpose": "game_character"})
	got := resolver.FollowedValues(brief, offering)
	names := []string{}
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) != 1 || names[0] != "export_texture_size" || string(got["export_texture_size"]) != `"2k"` {
		t.Fatalf("followed values = %v, want only the export size the purpose sets", got)
	}
}

// An Offering with nothing that follows the purpose has nothing to show,
// whatever the session's purpose gives it.
func TestFollowedValuesOfAnOfferingWithNothingToFollowAreEmpty(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	offering, ok := product.Get("meshy/processing/remesh")
	if !ok {
		t.Fatal("missing the shipped Meshy remesh Offering")
	}
	brief := purposeParamsBrief(t, map[string]any{"purpose": "game_character", "target_platform": "mobile"})
	if got := resolver.FollowedValues(brief, offering); len(got) != 0 {
		t.Fatalf("followed values = %v, want none", got)
	}
}
