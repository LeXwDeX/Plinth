package tripo

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// forced_when is normalization: the Catalog applies it before an input reaches
// this adapter, so the wire never sees it and a native Offering may declare it
// (#277: a Rig's naming is Tripo whenever the skeleton is not a biped). omit_when
// and wire_template change the wire, which this closed protocol does not build,
// so they stay refused.
func TestNativeProcessingDeclarationAllowsForcedWhenOnly(t *testing.T) {
	rig := shippedOffering(t, "tripo/processing/rigging-v3")
	if err := ValidateOffering(rig); err != nil {
		t.Fatalf("the shipped Rig declaration is refused: %v", err)
	}
	spec, _ := nativeParameter(rig, "spec")
	if len(spec.ForcedWhen) == 0 {
		t.Fatal("the shipped Rig no longer declares forced_when on its naming")
	}
	for name, mutate := range map[string]func(*generation.Parameter){
		"omit_when": func(p *generation.Parameter) {
			p.OmitWhen = []generation.ParameterCondition{{Parameter: "rig_type", Operator: "equals", Value: json.RawMessage(`"biped"`)}}
		},
		"wire_template": func(p *generation.Parameter) { p.WireTemplate = json.RawMessage(`{"spec":"{value}"}`) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := rig
			changed.Parameters = slices.Clone(rig.Parameters)
			for i := range changed.Parameters {
				if changed.Parameters[i].Name == "spec" {
					mutate(&changed.Parameters[i])
				}
			}
			if err := ValidateOffering(changed); err == nil {
				t.Fatalf("a native Offering declaring %s was accepted", name)
			}
		})
	}
}

func nativeParameter(offering generation.Offering, name string) (generation.Parameter, bool) {
	for _, p := range offering.Parameters {
		if p.Name == name {
			return p, true
		}
	}
	return generation.Parameter{}, false
}
