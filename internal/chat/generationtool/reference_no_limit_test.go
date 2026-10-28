package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

func TestNoPromptNoteWithoutKnownReferenceLimit(t *testing.T) {
	product := qualifiedMultiviewCatalog(t)
	app := product.Application()
	tool, _ := toolDescriptionNoteFixture(t)
	// This test pins the no_prompt sentence; the settings sentence is covered
	// by TestToolDescriptionListsOnlyDeclaredSettings.
	tool.ReferenceCapacityNote.Settings = ""
	scalar, ok := product.Get("tripo/geometry/smartsegment-image")
	if !ok || !scalar.Enabled || scalar.ModelID != "tripo-smartsegment-image" {
		t.Fatal("missing enabled scalar-reference offering")
	}
	var reference generation.Parameter
	for _, p := range scalar.Parameters {
		if p.Semantic == generation.SemanticPrompt {
			t.Fatal("scalar offering unexpectedly declares a prompt")
		}
		if p.Semantic == generation.SemanticReferenceImage {
			reference = p
		}
	}
	if reference.Type != "asset" {
		t.Fatalf("reference parameter = %+v, want scalar asset", reference)
	}
	for _, tc := range []struct {
		name       string
		parameters []generation.Parameter
		old        bool
		wantNote   bool
	}{
		{"actual scalar", scalar.Parameters, false, true},
		{"array without max", []generation.Parameter{{Name: "image", Semantic: generation.SemanticReferenceImage, Type: "asset-array"}}, false, true},
		{"scalar with prompt", []generation.Parameter{reference, {Name: "description", Semantic: generation.SemanticPrompt, Type: "text"}}, false, false},
		{"array without max with prompt", []generation.Parameter{{Name: "image", Semantic: generation.SemanticReferenceImage, Type: "asset-array"}, {Name: "description", Semantic: generation.SemanticPrompt, Type: "text"}}, false, false},
		{"old schema scalar", scalar.Parameters, true, false},
		{"old schema array without max", []generation.Parameter{{Name: "image", Semantic: generation.SemanticReferenceImage, Type: "asset-array"}}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offering := scalar
			offering.Parameters = tc.parameters
			gateway := &fakeGateway{offerings: []generation.Offering{offering}}
			executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{}, Chat: app.Chat, TaskStates: app.TaskStates})
			if err != nil {
				t.Fatal(err)
			}
			chosenTool := tool
			if tc.old {
				chosenTool.ReferenceCapacityNote.NoPrompt = ""
			}
			want := ""
			if tc.wantNote {
				want = strings.ReplaceAll(tool.ReferenceCapacityNote.NoPrompt, "{model}", generationtool.OfferingModelName(offering))
				if want == "" {
					t.Fatal("missing Schema no_prompt text")
				}
			}
			for _, active := range []bool{false, true} {
				options := chat.TurnOptions{AIModel: offering.ModelID, CreativeDomain: "model-3d"}
				if active {
					options.AIModel = "other"
					options.Harness = &resolve.Snapshot{Selection: harness.Selection{OperationID: "selected", OfferingID: string(offering.ID), OfferingRevision: offering.Revision}, ToolName: tool.Name, Capability: tool.Capability, OfferingRevision: offering.Revision}
				}
				if got := executor.ToolDescriptionNote(chosenTool, options); got != want {
					t.Errorf("active harness %v: note = %q, want %q", active, got, want)
				}
			}
		})
	}
}
