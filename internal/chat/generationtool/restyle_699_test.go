package generationtool_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #699: a restyle sent without reference purposes fell back to "Edit the
// reference image ... Keep the original composition, style", the opposite of
// the request. The shipped fallbacks are neutral for one picture and for
// several; these tests pin the shipped wording, not the rig's copy of it.

const (
	restyleStale    = "Keep the original composition, style"
	restyleNeutral  = "Use the attached reference image(s) for this request: %s. Keep whatever the request does not ask to change."
	restylePrevious = "The first reference image was made from this earlier request: %s. " +
		"Use the attached reference image(s) for this new request: %s. Keep whatever the new request does not ask to change."
)

func restyleExpect(template string, values ...string) string {
	for _, value := range values {
		template = strings.Replace(template, "%s", value, 1)
	}
	return template
}

// The scripted call of the reported case: a style picture and a character
// picture with their purposes. The wrapper numbers the pictures in the order
// they are frozen, whatever order the purposes were written in.
func TestRestyleWithPurposesNumbersThePicturesInFrozenOrder_699(t *testing.T) {
	rig := newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{}, `{"prompt":"redraw the character picture in the style picture's rendering",
		"reference_handles":["@img7","@img3"],
		"reference_purposes":{"@img3":"keep this character: identity, outfit, hair, expression and gestures",
			"@img7":"take only the art style: body proportions, line work, shading and palette"}}`)
	if got := rig.resolver.urls; !slices.Equal(got, []string{"/api/v1/input-assets/outfit", "/api/v1/tasks/task-pose/assets/pose"}) {
		t.Fatalf("frozen reference order = %v", got)
	}
	want := rig.expectedPrompt(
		rig.item("1", "take only the art style: body proportions, line work, shading and palette")+
			rig.item("2", "keep this character: identity, outfit, hair, expression and gestures"),
		"redraw the character picture in the style picture's rendering")
	if got := rig.submittedPrompt(t); got != want || strings.Contains(got, restyleStale) {
		t.Fatalf("prompt = %q\nwant     %q", got, want)
	}
}

// Two pictures and no purposes: the neutral template, never "keep the style".
func TestRestyleWithoutPurposesUsesTheNeutralTemplate_699(t *testing.T) {
	rig := newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{}, `{"prompt":"p","reference_handles":["@img7","@img3"]}`)
	if got := rig.submittedPrompt(t); got != restyleExpect(restyleNeutral, "p") {
		t.Fatalf("prompt = %q", got)
	}
}

func TestSingleReferenceUsesTheNeutralTemplate_699(t *testing.T) {
	rig := newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{}, `{"prompt":"make it a watercolor","reference_handles":["@img7"]}`)
	if got := rig.submittedPrompt(t); got != restyleExpect(restyleNeutral, "make it a watercolor") {
		t.Fatalf("prompt = %q", got)
	}
}

// Editing this tool's own result still carries the earlier request (#272), but
// as a description of how the picture was made, not as an instruction to edit.
func TestPreviousResultUsesTheNeutralPreviousTemplate_699(t *testing.T) {
	rig := newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{}, `{"prompt":"p","reference_handles":["@img3"]}`)
	if got := rig.submittedPrompt(t); got != restyleExpect(restylePrevious, "white-clad gunman, new pose", "p") {
		t.Fatalf("prompt = %q", got)
	}
}

// The Agent's own purposes never reorder the pictures; only a purpose the
// person picked on the page does. A purpose worded like the page's "input"
// keeps the order the Agent gave.
func TestAgentPurposesDoNotReorderWithoutTurnPurposes_699(t *testing.T) {
	rig := newPurposeRig(t, 4)
	input := rig.schema.AttachmentPurposes["input"].AgentText
	if input == "" {
		t.Fatal("the shipped input purpose has no Agent wording")
	}
	rig.run(t, chat.ToolCommand{}, `{"prompt":"p","reference_handles":["@img3","@img7"],
		"reference_purposes":{"@img3":"take only the art style","@img7":"`+input+`"}}`)
	if got := rig.resolver.urls; !slices.Equal(got, []string{"/api/v1/tasks/task-pose/assets/pose", "/api/v1/input-assets/outfit"}) {
		t.Fatalf("frozen reference order = %v", got)
	}
}
