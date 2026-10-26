package skillrun

import (
	"errors"
	"testing"
)

func TestRecipeRejectsMissingExecutionDefinitionAndForwardDependency(t *testing.T) {
	base := Recipe{ID: "three-view", Revision: "r1", Steps: []StepSpec{{
		Key: "front", Label: "Front", Capability: "image-to-image", OfferingID: "offering",
		OfferingRevision: "r2", PromptTemplate: "front portrait", OutputRole: "image",
	}}}
	if err := ValidateRecipe(base); err != nil {
		t.Fatal(err)
	}
	bad := base
	bad.Steps = append([]StepSpec(nil), base.Steps...)
	bad.Steps[0].SourceStepKey = "side"
	if err := ValidateRecipe(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forward dependency accepted: %v", err)
	}
	bad = base
	bad.Steps = append([]StepSpec(nil), base.Steps...)
	bad.Steps[0].PromptTemplate = ""
	if err := ValidateRecipe(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("skill card without executable template accepted: %v", err)
	}
	op1, key1, plan1 := StepIdentity("run-1", "front")
	op2, key2, plan2 := StepIdentity("run-1", "front")
	if op1 != op2 || key1 != key2 || plan1 != plan2 {
		t.Fatal("step identity is unstable")
	}
	op0, key0, plan0 := StepIdentityAt("run-1", "front", 0)
	if op0 != op1 || key0 != key1 || plan0 != plan1 {
		t.Fatal("attempt 0 must keep the identity a step was created with")
	}
	op4, key4, plan4 := StepIdentityAt("run-1", "front", 1)
	op5, _, _ := StepIdentityAt("run-1", "front", 2)
	if op4 == op1 || key4 == key1 || plan4 == plan1 || op4 == op5 {
		t.Fatal("a later attempt must not reuse an earlier paid identity")
	}
	op3, _, _ := StepIdentity("run-1", "side")
	if op3 == op1 {
		t.Fatal("different steps share a paid identity")
	}
}
