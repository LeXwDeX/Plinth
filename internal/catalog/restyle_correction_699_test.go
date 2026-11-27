package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #699 second round: a real-model run that corrected proportions with "3.5–4,
// compact, big head" fell back to about two heads tall, and the style
// purpose's exclusion was written in only two of five runs. The shipped
// wording fixes the correction to one sentence with a head count and a normal
// head size, and every style purpose example carries the exclusion.
func TestShippedRefineImageFixesTheProportionCorrection_699(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var refine catalog.ChatToolSchema
	for _, tool := range product.Application().Chat.Tools {
		if tool.Name == "refine_image" {
			refine = tool
		}
	}
	if refine.Name == "" {
		t.Fatal("refine_image is not declared")
	}
	for _, want := range []string{
		"keep the rest of the prompt as it was",
		"the target head count with a normal head size",
		"never big head, compact or chibi",
	} {
		if !strings.Contains(refine.Description, want) {
			t.Errorf("the refine_image description lacks %q", want)
		}
	}
	properties := string(refine.Parameters)
	const exclusion = "not its character, background, pose or text"
	if strings.Count(properties, "take only the art style") != strings.Count(properties, "take only the art style: body proportions, line work, shading and palette; "+exclusion) {
		t.Errorf("a style purpose example lacks %q", exclusion)
	}
	if !strings.Contains(properties, "end every style purpose with "+exclusion) {
		t.Error("reference_purposes does not ask every style purpose to carry the exclusion")
	}
}
