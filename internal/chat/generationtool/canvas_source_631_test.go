package generationtool_test

import (
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// boardOf is the artboard snapshot of a canvas turn that flattens the given
// layers; sources are the verified Tasks of its generated picture layers.
func boardOf(rig *purposeRig, layers int, sources ...string) chat.TurnOptions {
	return chat.TurnOptions{
		InputSource: rig.schema.Context.CanvasInputSource,
		Selection: []chat.Attachment{{Handle: "@img9", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCanvas,
			AssetID: "board", URL: "/api/v1/input-assets/board",
			Canvas: &chat.CanvasLayout{Width: 2048, Height: 2048, Layers: layers, Sources: sources}}},
	}
}

// #631: the person clicked a version on the strip whose layer is the only
// thing on the artboard, so the turn went out as a canvas turn; the Agent
// named that version (@img3) explicitly. The board then shows nothing but the
// named picture: it is not sent a second time ahead of it. The frozen
// references, the purpose numbering and the Agent's reference_handles name the
// same single picture, and the lineage of the result is the version itself.
func TestCanvasTurnBoardOfOnlyTheNamedPictureIsNotSentAgain(t *testing.T) {
	rig := newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{Options: boardOf(rig, 1, "task-pose")},
		`{"prompt":"add a brass buckle","reference_handles":["@img3"],"reference_purposes":{"@img3":"the original pouch"}}`)
	if got := rig.resolver.urls; !slices.Equal(got, []string{"/api/v1/tasks/task-pose/assets/pose"}) {
		t.Fatalf("frozen references = %v; want only the named version", got)
	}
	if got := rig.submittedPrompt(t); got != rig.expectedPrompt(rig.item("1", "the original pouch"), "add a brass buckle") {
		t.Fatalf("prompt = %q; the named version must be image 1", got)
	}
}

// #266 is unchanged wherever the board holds more than the named picture: a
// second layer, a layer of another picture, or a layout whose layers the server
// could not verify. The snapshot leads and the named picture follows it.
func TestCanvasTurnBoardStillLeadsWhenItHoldsMoreThanTheNamedPicture(t *testing.T) {
	for name, options := range map[string]func(*purposeRig) chat.TurnOptions{
		"two layers":          func(rig *purposeRig) chat.TurnOptions { return boardOf(rig, 2, "task-pose") },
		"another picture":     func(rig *purposeRig) chat.TurnOptions { return boardOf(rig, 1, "task-other") },
		"unverified layers":   func(rig *purposeRig) chat.TurnOptions { return boardOf(rig, 1) },
		"unknown layer count": func(rig *purposeRig) chat.TurnOptions { return boardOf(rig, 0, "task-pose") },
	} {
		t.Run(name, func(t *testing.T) {
			rig := newPurposeRig(t, 4)
			rig.run(t, chat.ToolCommand{Options: options(rig)},
				`{"prompt":"p","reference_handles":["@img3"],"reference_purposes":{"@img3":"the original pouch"}}`)
			if got := rig.resolver.urls; !slices.Equal(got, []string{"/api/v1/input-assets/board", "/api/v1/tasks/task-pose/assets/pose"}) {
				t.Fatalf("frozen references = %v; want the board first", got)
			}
			if got := rig.submittedPrompt(t); got != rig.expectedPrompt(rig.item("2", "the original pouch"), "p") {
				t.Fatalf("prompt = %q", got)
			}
		})
	}
	// The Agent naming nothing, or another picture only, keeps the board too.
	rig := newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{Options: boardOf(rig, 1, "task-pose")}, `{"prompt":"p","reference_handles":["@img7"]}`)
	if got := rig.resolver.urls; !slices.Equal(got, []string{"/api/v1/input-assets/board", "/api/v1/input-assets/outfit"}) {
		t.Fatalf("frozen references = %v; want the board first", got)
	}
	rig = newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{Options: boardOf(rig, 1, "task-pose")}, `{"prompt":"p"}`)
	if got := rig.resolver.urls; !slices.Equal(got, []string{"/api/v1/input-assets/board"}) {
		t.Fatalf("frozen references = %v; want the board alone", got)
	}
}
