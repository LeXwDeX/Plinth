package catalog

import (
	"testing"
)

func TestThreeViewRecipeRejectsForwardDependency(t *testing.T) {
	catalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := catalog.Application()
	application.Chat.Skills.Recipes[0].Steps[0].SourceStepKey = "side"
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("forward step dependency accepted")
	}
}
