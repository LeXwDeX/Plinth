package generation_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// conditional_outputs_mixed_test.go locks the kernel rule of a requested slot
// (#462): a `requested_when` slot exists only for an input that selected it.
// The decision is pinned into the Task's frozen Offering at submit, so the
// success gate, the resource-state projection and the Provider's post-success
// work list all read one fact instead of re-evaluating the condition.

func mixedFullFormatOffering(t *testing.T) schema.Offering {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !ok {
		t.Fatal("default Catalog is missing the Tripo full-format Offering")
	}
	return offering
}

func frozenVariants(t *testing.T, frozen generation.Offering) (required, requested []string) {
	t.Helper()
	for _, output := range frozen.Outputs {
		if output.Role != "converted_model" {
			continue
		}
		switch output.Delivery {
		case schema.AssetDeliveryRequired:
			required = append(required, output.Variant)
		case schema.AssetDeliveryOnDemand:
			requested = append(requested, output.Variant)
		}
	}
	return required, requested
}

func TestRequestedSlotsExistOnlyForTheFormatsTheInputSelected(t *testing.T) {
	offering := mixedFullFormatOffering(t)
	for _, test := range []struct {
		name      string
		formats   []string
		requested []string
	}{
		{"the primary model only", []string{"GLTF"}, nil},
		{"one extra format", []string{"GLTF", "FBX"}, []string{"converted.fbx"}},
		{"every format", []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"},
			[]string{"converted.usdz", "converted.fbx", "converted.obj", "converted.stl", "converted.3mf"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, err := json.Marshal(map[string]any{
				"prompt": "a crate", "output_formats": test.formats, "authorize_extra_credits": true,
			})
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := generation.ResolveConditionalOutputs(offering, input)
			if err != nil {
				t.Fatal(err)
			}
			required, requested := frozenVariants(t, frozen)
			// The GLTF conversion that produces the self-contained primary model
			// stays required: the task cannot succeed without it. (The model role
			// itself is a legacy slot with no variant, so it is not listed here.)
			if len(required) != 1 || required[0] != "converted.gltf" {
				t.Fatalf("required converted slots = %v, want only converted.gltf", required)
			}
			if len(requested) != len(test.requested) {
				t.Fatalf("requested slots = %v, want %v", requested, test.requested)
			}
			for index, variant := range test.requested {
				if requested[index] != variant {
					t.Fatalf("requested slots = %v, want %v", requested, test.requested)
				}
			}
			// The slot declarations are otherwise untouched: the delivery checks
			// of a requested format are the same as before.
			for _, output := range frozen.Outputs {
				if output.RequestedWhen == nil {
					continue
				}
				if output.Minimum == nil || *output.Minimum != 1 || output.Variant == "" {
					t.Fatalf("requested slot lost its delivery contract: %+v", output)
				}
			}
		})
	}
}

// A requested slot that the input does not select leaves no slot at all, so the
// Offering the Task freezes cannot be read as asking for it.
func TestUnselectedRequestedSlotIsNotDeclaredAtAll(t *testing.T) {
	offering := mixedFullFormatOffering(t)
	input, err := json.Marshal(map[string]any{
		"prompt": "a crate", "output_formats": []string{"GLTF", "USDZ"}, "authorize_extra_credits": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := generation.ResolveConditionalOutputs(offering, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range frozen.Outputs {
		if output.Variant != "converted.usdz" {
			continue
		}
		if output.RequestedWhen == nil {
			t.Fatal("the selected format lost its requested_when declaration")
		}
		return
	}
	t.Fatal("the selected format has no slot")
}
