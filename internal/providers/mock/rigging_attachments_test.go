package mock

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #326: like the real Meshy Rigging result, the mock lists the six basic
// animation files before the rigged character, so local checks see the shape
// that made the stage show a skeleton.
func TestMeshyRiggingDeliversItsAttachmentsFirst(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/processing/rigging")
	if !ok {
		t.Fatal("meshy rigging missing")
	}
	plans := planClaims(offering, map[string]any{})
	var variants []string
	for _, plan := range plans {
		if plan.role == "model" {
			variants = append(variants, plan.variant)
		}
	}
	want := []string{
		"basic_animations.running_armature_glb_url", "basic_animations.running_glb_url", "basic_animations.walking_armature_glb_url",
		"basic_animations.walking_glb_url", "basic_animations.running_fbx_url", "basic_animations.walking_fbx_url",
		"rigged_character_glb_url", "rigged_character_fbx_url",
	}
	if len(variants) != len(want) {
		t.Fatalf("variants = %v", variants)
	}
	for index := range want {
		if variants[index] != want[index] {
			t.Fatalf("variants = %v; want %v", variants, want)
		}
	}
	for _, plan := range plans {
		if plan.attachment != (plan.variant[:len(meshyBasicAnimations)] == meshyBasicAnimations) {
			t.Fatalf("plan %s attachment = %v", plan.variant, plan.attachment)
		}
	}
}
