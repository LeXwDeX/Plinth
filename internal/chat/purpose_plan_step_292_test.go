package chat_test

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #292: a processing plan a person starts in the 加工 panel follows the session
// purpose for the parameters an Offering declares as following it
// (presentation.follows_purpose): the export texture size of the Tripo processing
// steps. The panel leaves such a parameter out until the person picks a value; the
// resolver the Agent's tools and the direct task API share (#295) gives it the
// purpose's value, and only that parameter. A value the request sets is the
// request's own, even when it equals the Offering's default.

// A step of a plan carries no source parameter: the plan injects it.
const planStepRequest = `{"authorize_extra_credits":true}`

func TestPlanStepTakesThePurposeTextureBudgetForItsFollowingParameter(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	for name, test := range map[string]struct {
		brief map[string]any
		want  string // the size the purpose gives; "" leaves the request as it was
	}{
		"game character":                  {map[string]any{"purpose": "game_character"}, "2k"},
		"game character on a phone":       {map[string]any{"purpose": "game_character", "target_platform": "mobile"}, "2k"},
		"game character on a PC":          {map[string]any{"purpose": "game_character", "target_platform": "pc_console"}, "4k"},
		"game prop":                       {map[string]any{"purpose": "game_prop"}, "2k"},
		"print":                           {map[string]any{"purpose": "print"}, "4k"},
		"display bounds nothing below 8K": {map[string]any{"purpose": "render"}, ""},
		"a brief without a purpose":       {map[string]any{"style": "写实"}, ""},
		"no brief":                        {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			brief := purposeParamsBrief(t, test.brief)
			for _, id := range tripoExportOfferings {
				offering, ok := product.Get(id)
				if !ok {
					t.Fatalf("missing %s", id)
				}
				got, err := resolver.ApplyToPlanStep(brief, offering, json.RawMessage(planStepRequest))
				if err != nil {
					t.Fatal(err)
				}
				if test.want == "" {
					if !bytes.Equal(got, []byte(planStepRequest)) {
						t.Fatalf("%s: input = %s, want the request's own bytes", id, got)
					}
					continue
				}
				values := purposeParamsDecode(t, got)
				if values["export_texture_size"] != test.want || values["authorize_extra_credits"] != true || len(values) != 2 {
					t.Fatalf("%s: input = %s, want the request and an export size of %s", id, got, test.want)
				}
			}
		})
	}
}

// What the person set is the person's: a size equal to the default is a choice
// too, and a game purpose does not take it back.
func TestPlanStepKeepsASizeTheRequestSet(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	offering, ok := product.Get("tripo/processing/rigging-v3")
	if !ok {
		t.Fatal("missing the shipped rigging Offering")
	}
	brief := purposeParamsBrief(t, map[string]any{"purpose": "game_character", "target_platform": "mobile"})
	for _, size := range []string{"8k", "4k", "2k"} {
		request := `{"authorize_extra_credits":true,"export_texture_size":"` + size + `"}`
		got, err := resolver.ApplyToPlanStep(brief, offering, json.RawMessage(request))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte(request)) {
			t.Fatalf("input = %s, want the request's %s kept byte for byte", got, size)
		}
	}
}

// Only a parameter the Offering says follows the purpose is filled. The purpose
// may give the Offering other values (the direct task API and the Agent apply them
// all); the plan a person assembles in the panel is not changed by them, so what
// the panel sent, the price it quoted and the chain checks it passed stay true.
func TestPlanStepFillsNothingButTheFollowingParameters(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	offering, ok := product.Get("tripo/geometry/decimate-triangle")
	if !ok {
		t.Fatal("missing the shipped decimation Offering")
	}
	// A game purpose that also asks this decimation for a face limit.
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
	direct, err := resolver.ApplyToDirectInput(brief, offering, json.RawMessage(planStepRequest), nil)
	if err != nil {
		t.Fatal(err)
	}
	if values := purposeParamsDecode(t, direct.Input); values["face_limit"] != float64(20000) || values["export_texture_size"] != "2k" {
		t.Fatalf("the direct path = %s, want both values the purpose gives", direct.Input)
	}
	step, err := resolver.ApplyToPlanStep(brief, offering, json.RawMessage(planStepRequest))
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for name := range purposeParamsDecode(t, step) {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "authorize_extra_credits" || keys[1] != "export_texture_size" {
		t.Fatalf("the plan step = %s, want the request and the export size only", step)
	}
}

// A request that is not a JSON object is left to the plan's own validation.
func TestPlanStepLeavesAnInputThatIsNotAnObject(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	offering, _ := product.Get("tripo/processing/rigging-v3")
	brief := purposeParamsBrief(t, map[string]any{"purpose": "game_character", "target_platform": "mobile"})
	for _, input := range []string{``, `null`, `[]`, `"text"`, `{`} {
		got, err := resolver.ApplyToPlanStep(brief, offering, json.RawMessage(input))
		if err != nil || !bytes.Equal(got, []byte(input)) {
			t.Fatalf("input %q -> %q, %v; want it unchanged", input, got, err)
		}
	}
}
