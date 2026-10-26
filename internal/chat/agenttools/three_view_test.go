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
