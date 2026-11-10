package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func offeringNoteFixture(t *testing.T, modelID string) (catalog.ChatSchema, catalog.ChatToolSchema, generation.Offering, *generationtool.Executor) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application().Chat
	var tool catalog.ChatToolSchema
	for _, candidate := range app.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	offering, found := product.Get(generation.OfferingID("meshy/" + modelID + "/image-to-3d"))
	if !found {
		offering, found = product.Get(generation.OfferingID("tripo/" + modelID + "/image-to-3d"))
	}
	if !found {
		offering, found = product.Get(generation.OfferingID("hyper3d/" + modelID + "/image-to-3d"))
	}
	if !found {
		t.Fatalf("offering for %q missing", modelID)
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    &fakeGateway{offerings: []generation.Offering{offering}},
		References: &fakeReferences{},
		Chat:       app,
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, tool, offering, executor
}

func TestToolDescriptionUsesOfferingKnowledgeForNoTextAndPromptCraft(t *testing.T) {
	app, tool, offering, executor := offeringNoteFixture(t, "meshy-7.1")
	knowledge := app.Agent.OfferingKnowledge
	model := generationtool.OfferingModelName(offering)
	got := executor.ToolDescriptionNote(tool, chat.TurnOptions{ExplicitModel3D: "meshy-7.1", CreativeDomain: "model-3d"})
	noText := knowledge.RenderNoTextGuidance(model, offering.Knowledge)
	guidance := knowledge.RenderPromptGuidance(model, offering.Knowledge)
	if noText == "" || guidance == "" {
		t.Fatalf("fixture knowledge renders no guidance: %+v", offering.Knowledge)
	}
	if !strings.Contains(got, noText) || !strings.Contains(got, guidance) {
		t.Fatalf("note = %q\nwant no-text %q and guidance %q", got, noText, guidance)
	}
	if strings.Contains(got, "{model}") || strings.Contains(got, "{language}") {
		t.Fatalf("note kept a raw placeholder: %q", got)
	}
}

func TestToolDescriptionOmitsNoTextForAnOfferingThatReadsThePrompt(t *testing.T) {
	app, tool, offering, executor := offeringNoteFixture(t, "rodin-gen-2.5")
	knowledge := app.Agent.OfferingKnowledge
	model := generationtool.OfferingModelName(offering)
	got := executor.ToolDescriptionNote(tool, chat.TurnOptions{ExplicitModel3D: "rodin-gen-2.5", CreativeDomain: "model-3d"})
	noText := knowledge.RenderNoTextGuidance(model, offering.Knowledge)
	if noText != "" || strings.Contains(got, "not sent to this service") {
		t.Fatalf("a prompt-reading Offering rendered no-text: %q", got)
	}
	if guidance := knowledge.RenderPromptGuidance(model, offering.Knowledge); !strings.Contains(got, guidance) {
		t.Fatalf("note = %q, want guidance %q", got, guidance)
	}
}

func TestToolDescriptionKeepsTheHistoricalNoPromptFallback(t *testing.T) {
	app, tool, offering, executor := offeringNoteFixture(t, "meshy-7.1")
	offering.Knowledge = nil
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    &fakeGateway{offerings: []generation.Offering{offering}},
		References: &fakeReferences{},
		Chat:       app,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool.ReferenceCapacityNote = catalog.ChatToolReferenceCapacityNote{
		NoPrompt: "legacy no-prompt statement for {model}",
	}
	got := executor.ToolDescriptionNote(tool, chat.TurnOptions{ExplicitModel3D: "meshy-7.1", CreativeDomain: "model-3d"})
	if got != "legacy no-prompt statement for Meshy 7.1" {
		t.Fatalf("legacy fallback = %q", got)
	}
}
