package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #699: a restyle (draw this character in that picture's style) was handled
// as an image edit. The shipped wording names it a redraw at the call point,
// splits what a style picture and a character picture give, asks for the
// proportions on a card when no style picture shows them, and keeps the model
// that made the result being corrected.
func TestShippedRefineImageWordsARestyleAsARedraw_699(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat
	var refine catalog.ChatToolSchema
	for _, tool := range chat.Tools {
		if tool.Name == "refine_image" {
			refine = tool
		}
	}
	if refine.Name == "" {
		t.Fatal("refine_image is not declared")
	}
	for _, want := range []string{
		"is a redraw, not an edit",
		"never from a failed result",
		"body proportions, line work, shading, palette",
		"never its character, pose, background or text",
		"identity, outfit, hair, expression and gestures",
		"no style picture shows a standing full body",
		"ask_choice kind next_step",
		"submit no generation in that reply",
		"never by number",
		"never name a look to avoid",
		"never describe the character picture with style words",
		"change only the proportion sentence",
		"one call at a time",
	} {
		if !strings.Contains(refine.Description, want) {
			t.Errorf("the refine_image description lacks %q", want)
		}
	}
	properties := string(refine.Parameters)
	for _, want := range []string{
		"take only the art style: body proportions",
		"a style purpose never includes the background",
		"pass the model that made it",
		"say so before switching models",
	} {
		if !strings.Contains(properties, want) {
			t.Errorf("the refine_image parameters lack %q", want)
		}
	}

	// The fallback templates hold for one picture and for several, and no
	// longer tell the model to keep the style (a restyle without purposes
	// would otherwise ask for the opposite of the request).
	for name, template := range map[string]string{
		"reference": refine.PromptWithReferenceTemplate,
		"previous":  refine.PromptWithPreviousTemplate,
	} {
		if !strings.Contains(template, "reference image(s)") || !strings.Contains(template, "does not ask to change") {
			t.Errorf("the %s template is not neutral: %q", name, template)
		}
		for _, stale := range []string{"Edit the reference image", "Keep the original composition, style", "Edit: "} {
			if strings.Contains(template, stale) {
				t.Errorf("the %s template still says %q", name, stale)
			}
		}
	}

	var production string
	for _, prompt := range chat.Agent.CraftPrompts {
		if prompt.ID == "image2d_production" {
			production = prompt.Text
		}
	}
	if !strings.Contains(production, "change only ...; keep everything else the same") ||
		!strings.Contains(production, "换画风是重绘，不套此条") {
		t.Error("the 2D craft brief does not exempt a restyle from the change-only rule")
	}

	// The card the restyle opens takes options only; its wording says a typed
	// answer works too.
	for _, kind := range chat.Choice.Kinds {
		if kind.ID == "next_step" && !strings.Contains(kind.Message, "也可以直接在对话里写") {
			t.Errorf("the next_step card does not say a typed answer works: %q", kind.Message)
		}
	}
}
