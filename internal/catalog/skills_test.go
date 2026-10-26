package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestPackagedSkillCatalogIsEmptyAndValid(t *testing.T) {
	document, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if document.Revision == "" || len(document.Skills) != 0 {
		t.Fatalf("packaged Skill catalog = %#v", document)
	}
	retired, err := catalog.RetiredBuiltInSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 162 {
		t.Fatalf("retirement metadata covers %d of 162 legacy Skills", len(retired))
	}
}

func TestSkillCatalogValidationRejectsDuplicateIDs(t *testing.T) {
	skill := catalog.SkillSchema{
		ID: "custom", Name: "Custom", Category: "anime",
		RecommendedSize: "1536x1024", ThumbnailPath: "/gpt-skills/test.webp",
		RawImageURL: "/gpt-skills/test.webp", Prompt: "style",
	}
	document := catalog.SkillCatalogSchema{
		Revision: "test", Skills: []catalog.SkillSchema{skill, skill},
	}
	if err := catalog.ValidateSkillCatalog(document); err == nil {
		t.Fatal("expected duplicate ID validation failure")
	}
}

func TestSkillCatalogAllowsEmptyCategoryButRejectsUnknownCategory(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	document := catalog.SkillCatalogSchema{Revision: "optional-category", Skills: []catalog.SkillSchema{{
		ID: "personal-test", Name: "Test", RecommendedSize: product.Application().Chat.Skills.Creation.RecommendedSize, Prompt: "style",
	}}}
	validate := func() error {
		return catalog.ValidateSkillCatalogAgainstProductSchema(document, product.Application(), product.List(""))
	}
	if err := validate(); err != nil {
		t.Fatalf("empty category rejected: %v", err)
	}
	document.Skills[0].Category = product.Application().Chat.Skills.Categories[0].ID
	if err := validate(); err != nil {
		t.Fatalf("existing category rejected: %v", err)
	}
	document.Skills[0].Category = "unknown-category"
	if err := validate(); err == nil {
		t.Fatal("unknown nonempty category accepted")
	}
}
