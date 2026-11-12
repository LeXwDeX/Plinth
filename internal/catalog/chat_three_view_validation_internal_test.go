package catalog

import "testing"

// A recipe published before the subject templates (#243) keeps the views it
// had; the mirror pairs and the review notes of the current Schema name views
// it may lack and are simply never evaluated for it. A current recipe is held
// to its own steps.
func TestAHistoricalRecipeIsNotHeldToTheViewsOfTheCurrentOne(t *testing.T) {
	legacy := ChatSkillRecipeSchema{ID: "image-three-view", Steps: []ChatSkillRecipeStepSchema{
		{Key: "front", PromptTemplate: "front"}, {Key: "side", PromptTemplate: "side"}, {Key: "back", PromptTemplate: "back"}}}
	current := ChatSkillRecipeSchema{ID: "image-three-view", Steps: []ChatSkillRecipeStepSchema{
		{Key: "front", PromptTemplate: "{subject_front_constraints}"},
		{Key: "left", PromptTemplate: "{subject_view_constraints}"},
		{Key: "back", PromptTemplate: "{subject_view_constraints}"}}} // no right view
	appearance := ChatThreeViewAppearanceSchema{SubjectThreshold: 32, Mirror: ChatThreeViewMirrorSchema{
		NormalizedSide: 64, Pairs: []ChatThreeViewMirrorPairSchema{
			{Views: []string{"left", "right"}, MinOverlap: 0.6, MaxOrientationMargin: 0.1}}}}
	if err := validateThreeViewAppearance(appearance, legacy); err != nil {
		t.Fatalf("a historical recipe was refused: %v", err)
	}
	if err := validateThreeViewAppearance(appearance, current); err == nil {
		t.Fatal("a current recipe may not name a view it lacks")
	}
	blank := appearance
	blank.Mirror.Pairs = []ChatThreeViewMirrorPairSchema{{Views: []string{"", "right"}, MinOverlap: 0.6}}
	if err := validateThreeViewAppearance(blank, legacy); err == nil {
		t.Fatal("a pair without a view key was accepted")
	}

	schema := ChatThreeViewSchema{
		Messages: ChatThreeViewMessages{AgentCompleted: "done {checklist}"},
		Review: ChatThreeViewReviewSchema{
			ChecklistTemplate: "{views} {items} {view_notes}", ItemTemplate: "- {text}",
			Items:            []ChatThreeViewReviewItem{{ID: "colours", Text: "colours"}},
			ViewNoteTemplate: "{view}: {note}", ViewNotes: map[string]string{"right": "from the right"},
		},
	}
	if err := validateThreeViewReview(schema, legacy); err != nil {
		t.Fatalf("a historical recipe was refused: %v", err)
	}
	if err := validateThreeViewReview(schema, current); err == nil {
		t.Fatal("a current recipe may not annotate a view it lacks")
	}
}
