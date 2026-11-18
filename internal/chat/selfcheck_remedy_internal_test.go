package chat

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func shippedRemedyInputs(t *testing.T) ([]catalog.ChatModelInspectFixSchema, []schemacontract.Capability) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	if application.StepPolicy == nil {
		t.Fatal("the shipped Catalog declares no step policy")
	}
	return application.Chat.ModelReview.Inspect.Production.Fixes, application.StepPolicy.CountedCapabilities
}

func TestSelfCheckRemediesNameOnlyFixableCountedChecks(t *testing.T) {
	fixes, counted := shippedRemedyInputs(t)
	remedies := SelfCheckRemedies(fixes, []string{"triangles", "uv", "debris", "y_up", "skin", "unknown"}, counted)
	want := []SelfCheckRemedy{
		{Check: "triangles", Capabilities: []string{"remesh"}, Steps: 1},
		{Check: "debris", Capabilities: []string{"model-split", "model-merge"}, Steps: 1},
		{Check: "skin", Capabilities: []string{"rigging"}, Steps: 1},
	}
	if len(remedies) != len(want) {
		t.Fatalf("remedies = %+v, want %+v", remedies, want)
	}
	for index := range want {
		got := remedies[index]
		if got.Check != want[index].Check || got.Steps != want[index].Steps ||
			len(got.Capabilities) != len(want[index].Capabilities) {
			t.Fatalf("remedy[%d] = %+v, want %+v", index, got, want[index])
		}
		for capabilityIndex := range want[index].Capabilities {
			if got.Capabilities[capabilityIndex] != want[index].Capabilities[capabilityIndex] {
				t.Fatalf("remedy[%d].capabilities = %v, want %v", index, got.Capabilities, want[index].Capabilities)
			}
		}
	}
}

func TestSelfCheckRemediesSkipLocalFixesAndDuplicates(t *testing.T) {
	fixes, counted := shippedRemedyInputs(t)
	// A local processing fix (UV unwrap) is not a 3D step, and a fix with no
	// option (regenerate) has no executable route. A repeated check is one
	// remedy.
	remedies := SelfCheckRemedies(fixes, []string{"uv", "watertight", "y_up", "triangles", "triangles"}, counted)
	if len(remedies) != 1 || remedies[0].Check != "triangles" || remedies[0].Steps != 1 {
		t.Fatalf("remedies = %+v, want only triangles", remedies)
	}
}

func TestSelfCheckRemediesNeedACountedCapability(t *testing.T) {
	fixes, _ := shippedRemedyInputs(t)
	if remedies := SelfCheckRemedies(fixes, []string{"triangles", "skin"}, nil); len(remedies) != 0 {
		t.Fatalf("remedies without counted capabilities = %+v", remedies)
	}
	// A check the Schema does not map is not offered.
	remedies := SelfCheckRemedies([]catalog.ChatModelInspectFixSchema{{
		Check: "triangles",
		Options: []catalog.ChatModelInspectFixOptionSchema{{
			Capabilities: []catalog.ChatModelInspectFixCapabilitySchema{{Capability: "format-conversion"}},
		}},
	}}, []string{"triangles"}, []schemacontract.Capability{schemacontract.Remesh})
	if len(remedies) != 0 {
		t.Fatalf("a local format conversion was counted as a 3D step: %+v", remedies)
	}
	if remedies := SelfCheckRemedies(nil, []string{"triangles"}, nil); len(remedies) != 0 {
		t.Fatalf("a check without a fix was remedied: %+v", remedies)
	}
}
