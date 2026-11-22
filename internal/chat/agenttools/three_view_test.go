package agenttools

import (
	"context"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

func TestThreeViewUsesOneExplicitImageAndWorkspaceSelectionWins(t *testing.T) {
	command := chat.ToolCommand{History: []chat.Message{
		{Role: "user", ImageURLs: []string{"old"}},
		{Role: "user", ImageURLs: []string{"current"}},
	}}
	if got := selectedThreeViewImages(command, nil); !reflect.DeepEqual(got, []string{"current"}) {
		t.Fatalf("implicit current image = %v", got)
	}
	if got := selectedThreeViewImages(command, []int{1}); !reflect.DeepEqual(got, []string{"old"}) {
		t.Fatalf("explicit historical image = %v", got)
	}
	command.Options.ReferenceImageIndices = []int{2}
	if got := selectedThreeViewImages(command, []int{1}); !reflect.DeepEqual(got, []string{"current"}) {
		t.Fatalf("workspace selection = %v", got)
	}
	command.Options.ReferenceImageIndices = []int{3}
	if got := selectedThreeViewImages(command, nil); got != nil {
		t.Fatalf("out of range image should fail closed: %v", got)
	}
}

type noExistingThreeViewRun struct{ skillrun.Store }

func (noExistingThreeViewRun) GetSkillRunByIdempotency(context.Context, generation.OwnerID, string) (skillrun.Run, error) {
	return skillrun.Run{}, skillrun.ErrMissing
}

type unusedThreeViewRecipes struct{ skillrun.RecipeResolver }
type unusedThreeViewReferences struct{ SkillReferenceResolver }

type referenceSelectionOfferings struct{}

func (referenceSelectionOfferings) Offerings(generation.Capability) []generation.Offering {
	return []generation.Offering{{ID: "selected-image-offering", ModelID: "selected-image-model", Enabled: true}}
}
func (referenceSelectionOfferings) OfferingAvailable(generation.OfferingID) bool { return true }

func TestThreeViewReferenceCardinalityUsesSchemaCopyBeforePreparingAnything(t *testing.T) {
	runs, err := skillrun.NewService(noExistingThreeViewRun{}, unusedThreeViewRecipes{})
	if err != nil {
		t.Fatal(err)
	}
	for _, images := range [][]string{nil, {"/object-one.png", "/object-two.png"}} {
		executor := &Executor{
			skillRuntime: &skillrun.Runtime{Runs: runs}, skillReferences: unusedThreeViewReferences{},
			skillOfferings:   referenceSelectionOfferings{},
			skillTurnStopped: func(context.Context, string, string, string) (bool, error) { return false, nil },
		}
		executor.SetThreeView(catalog.ChatThreeViewSchema{Messages: catalog.ChatThreeViewMessages{
			ReferenceRequired: "configured: select exactly one image of your subject",
		}})
		command := chat.ToolCommand{
			OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn",
			Call:    agents.ToolCall{ID: "call", Name: ToolCreateThreeView, Arguments: []byte(`{}`)},
			Options: chat.TurnOptions{CreativeDomain: "image", AIModel: "selected-image-model"},
			History: []chat.Message{{Role: "user", ImageURLs: images}},
		}
		result, err := executor.createThreeView(context.Background(), command, nil)
		if err != nil || !result.NoSubmission || result.Suspend || result.TaskID != "" ||
			result.AgentContent != executor.threeView.Messages.ReferenceRequired {
			t.Fatalf("images=%v: result=%+v error=%v", images, result, err)
		}
		// Reference/recipe/Generation/Grant ports are intentionally absent:
		// ambiguous selection must return before any of them is invoked.
	}
}

func TestThreeViewSubjectTypeUsesDeclaredTemplates(t *testing.T) {
	executor := &Executor{}
	executor.SetThreeView(catalog.ChatThreeViewSchema{
		DefaultSubjectType: "generic", SubjectArgument: "subject_type",
		SubjectTypes: []catalog.ChatThreeViewSubjectType{
			{ID: "generic", Label: "Generic"}, {ID: "humanoid", Label: "Humanoid"}},
		Messages: catalog.ChatThreeViewMessages{SubjectInvalid: "unknown {subject}; choose {choices}"},
	})
	if got, refusal := executor.threeViewSubjectType(""); got != "generic" || refusal != "" {
		t.Fatalf("default subject = %q, %q", got, refusal)
	}
	if got, refusal := executor.threeViewSubjectType("humanoid"); got != "humanoid" || refusal != "" {
		t.Fatalf("declared subject = %q, %q", got, refusal)
	}
	if got, refusal := executor.threeViewSubjectType("robot"); got != "" ||
		refusal != "unknown robot; choose generic, humanoid" {
		t.Fatalf("unknown subject = %q, %q", got, refusal)
	}
}

// A new group takes the recipe of the requested use; an absent use takes the
// declared default, so a call that predates the design sheet keeps the
// historical recipe (#409).
func TestThreeViewPurposeChoosesTheRecipeAndDefaultsToModelInput(t *testing.T) {
	executor := &Executor{}
	executor.SetThreeView(catalog.ChatThreeViewSchema{
		RecipeID: threeViewRecipeID, DefaultPurpose: "model_input",
		Purposes: []catalog.ChatThreeViewPurpose{
			{ID: "model_input", Label: "3D input", RecipeID: threeViewRecipeID},
			{ID: "design_sheet", Label: "Design sheet", RecipeID: "image-design-sheet"},
		},
		Messages: catalog.ChatThreeViewMessages{PurposeInvalid: "unknown {purpose}; choose {choices}"},
	})
	purpose, ok := executor.threeViewPurpose("")
	if !ok || purpose.RecipeID != threeViewRecipeID {
		t.Fatalf("absent purpose = %+v, %v", purpose, ok)
	}
	purpose, ok = executor.threeViewPurpose("design_sheet")
	if !ok || purpose.RecipeID != "image-design-sheet" {
		t.Fatalf("design sheet = %+v, %v", purpose, ok)
	}
	if _, ok := executor.threeViewPurpose("no_such_use"); ok {
		t.Fatal("an unknown use must not fall back to a recipe")
	}
	if got := executor.threeViewPurposeRefusal("no_such_use"); got != "unknown no_such_use; choose model_input, design_sheet" {
		t.Fatalf("purpose refusal = %q", got)
	}
	for _, recipe := range []string{threeViewRecipeID, "image-design-sheet"} {
		if !executor.threeViewRecipeKnown(recipe) {
			t.Fatalf("declared recipe %q is not readable", recipe)
		}
	}
	if executor.threeViewRecipeKnown("image-other") || executor.threeViewRecipeKnown("") {
		t.Fatal("a recipe no use declares passed the checkpoint")
	}
}

// A run made before purposes existed keeps its recipe after the upgrade: the
// checkpoint's equality test became membership in the declared uses (#409).
func TestThreeViewOldRunStaysReadableAfterThePurposeSetGrew(t *testing.T) {
	ctx := context.Background()
	executor, _, _, command, _ := threeViewHarnessFixture(t)
	executor.SetThreeView(catalog.ChatThreeViewSchema{
		RecipeID: threeViewRecipeID, DefaultPurpose: "model_input",
		Purposes: []catalog.ChatThreeViewPurpose{
			{ID: "model_input", RecipeID: threeViewRecipeID},
			{ID: "design_sheet", RecipeID: "image-design-sheet"},
		},
		Messages: catalog.ChatThreeViewMessages{ReferenceRequired: "one image",
			AgentStarted: "{run}:{task}:{view}", AdmissionFull: "admission full, not submitted",
			PurposeInvalid: "unknown {purpose}; choose {choices}"},
	})
	result, err := executor.Execute(ctx, command, nil)
	if err != nil || !result.Suspend {
		t.Fatalf("prepare %+v %v", result, err)
	}
	run, _, _, err := executor.existingThreeView(ctx, command)
	if err != nil || run.RecipeID != threeViewRecipeID {
		t.Fatalf("the old run after the upgrade = %+v %v", run, err)
	}
}

func TestThreeViewHandleWithoutAPictureIsRefusedWithSchemaWording(t *testing.T) {
	executor := &Executor{context: catalog.ChatContextSchema{
		HandlePrefixes:        map[string]string{"image": "@img", "model": "@model"},
		SelectionAlias:        "@selected",
		UnknownHandleTemplate: "configured-unknown {handle}",
	}}
	command := chat.ToolCommand{
		History: []chat.Message{{Role: "user", ImageURLs: []string{"/pic-1"},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, ImageSlot: 1}}}},
		Options: chat.TurnOptions{Selection: []chat.Attachment{
			// A selected object that verified without a picture to send.
			{Handle: "@img2", Kind: chat.AttachmentKindImage},
		}},
	}
	urls, refusal := executor.threeViewImages(command, nil, []string{"@img1"})
	if refusal != "" || !reflect.DeepEqual(urls, []string{"/pic-1"}) {
		t.Fatalf("handle with a picture = %v, %q", urls, refusal)
	}
	urls, refusal = executor.threeViewImages(command, nil, []string{"@img2"})
	if urls != nil || refusal != "configured-unknown @img2" {
		t.Fatalf("handle without a picture must be refused, not sent empty: %v, %q", urls, refusal)
	}
	urls, refusal = executor.threeViewImages(command, nil, []string{"@selected"})
	if urls != nil || refusal != "configured-unknown @selected" {
		t.Fatalf("selection alias without a picture = %v, %q", urls, refusal)
	}
}
