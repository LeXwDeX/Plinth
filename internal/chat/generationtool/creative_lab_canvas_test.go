package generationtool_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #189 (dev-test note 98826): a design preview is placed on the artboard when
// it finishes, so every later turn of the session is a canvas turn whose
// selection is the artboard snapshot. Building the preview must not depend on
// clearing the artboard first.
func canvasBuildCase(t *testing.T, source string, sources ...string) creativeLabCase {
	t.Helper()
	c := newCreativeLabCase(t, "auto", "build_creative_lab_item", `{"source_task":`+jsonString(source)+`}`)
	c.command.History = []chat.Message{
		{ID: "user-1", Role: "user", Content: "做个手办预览", Attachments: []chat.Attachment{
			{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "asset-photo", ImageSlot: 1}},
			ImageURLs: []string{"/api/v1/assets/asset-photo"}},
		{ID: "assistant-1", Role: "assistant", Content: "预览好了", Attachments: []chat.Attachment{
			{Handle: "@img2", Kind: chat.AttachmentKindImage, TaskID: "task-figure", AssetID: "asset-preview", ImageSlot: 1}},
			ImageURLs: []string{"/api/v1/tasks/task-figure/assets/asset-preview"}},
	}
	c.command.Options.InputSource = c.executorCanvasSource(t)
	c.command.Options.Selection = []chat.Attachment{{Handle: "@img3", Kind: chat.AttachmentKindImage,
		Role: chat.AttachmentRoleCanvas, AssetID: "asset-snapshot", URL: "/api/v1/assets/asset-snapshot",
		Canvas: &chat.CanvasLayout{Width: 1024, Height: 1024, Layers: 2, Sources: sources}}}
	return c
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (c creativeLabCase) executorCanvasSource(t *testing.T) string {
	t.Helper()
	source := c.canvasInputSource
	if source == "" {
		t.Fatal("default Schema declares no canvas input source")
	}
	return source
}

func assertBuiltFromFigure(t *testing.T, c creativeLabCase, result chat.ToolResult) {
	t.Helper()
	if len(c.gateway.prepared) != 1 {
		t.Fatalf("not prepared: result=%+v", result)
	}
	prepared := c.gateway.prepared[0]
	var input map[string]any
	if err := json.Unmarshal(prepared.Input, &input); err != nil || input["source_task"] != "task-figure" ||
		prepared.OfferingID != "meshy/creative-lab/figure/build" {
		t.Fatalf("prepared %s %s (%v)", prepared.OfferingID, prepared.Input, err)
	}
}

func TestCreativeLabBuildOnACanvasTurnTakesThePreviewHandleOrTask(t *testing.T) {
	for _, source := range []string{"@img2", "task-figure"} {
		t.Run(source, func(t *testing.T) {
			c := canvasBuildCase(t, source)
			assertBuiltFromFigure(t, c, c.run(t))
		})
	}
}

// The snapshot (by its handle or @selected) resolves to the preview it shows:
// the server verified the Tasks of the picture layers it flattens.
func TestCreativeLabBuildResolvesTheCanvasSnapshotToItsPreviewLayer(t *testing.T) {
	for _, source := range []string{"@selected", "@img3"} {
		t.Run(source, func(t *testing.T) {
			c := canvasBuildCase(t, source, "task-figure")
			assertBuiltFromFigure(t, c, c.run(t))
		})
	}
	// An upload layer next to the preview has no Task and does not compete.
	t.Run("preview and a pasted picture", func(t *testing.T) {
		c := canvasBuildCase(t, "@selected", "task-figure", "task-missing")
		assertBuiltFromFigure(t, c, c.run(t))
	})
}

func TestCreativeLabBuildRefusesACanvasSnapshotWithoutOneOwnPreview(t *testing.T) {
	for name, sources := range map[string][]string{
		"no preview layer":       nil,
		"two previews":           {"task-figure", "task-figure-2"},
		"another session's task": {"task-figure-other-session"},
		"another owner's task":   {"task-figure-other-owner"},
	} {
		t.Run(name, func(t *testing.T) {
			c := canvasBuildCase(t, "@selected", sources...)
			c.gateway.sources["task-figure-2"] = c.gateway.sources["task-figure"]
			second := c.gateway.sources["task-figure-2"]
			second.ID = "task-figure-2"
			c.gateway.sources["task-figure-2"] = second
			result := c.run(t)
			if !result.NoSubmission || len(c.gateway.prepared) != 0 || c.gateway.submitCount != 0 {
				t.Fatalf("result=%+v prepared=%d", result, len(c.gateway.prepared))
			}
			// The refusal says the artboard snapshot is not a preview and names
			// the previews of this session that can be built instead.
			refusal := result.AgentContent + result.Error
			if !strings.Contains(refusal, c.canvasMessage) || !strings.Contains(refusal, "@img2") {
				t.Fatalf("refusal must explain the snapshot and name @img2: %q", refusal)
			}
		})
	}
}

// A real mismatch outside the canvas still names the previews to use.
func TestCreativeLabBuildRefusalNamesTheSessionPreviews(t *testing.T) {
	c := canvasBuildCase(t, "task-figure-other-session")
	c.command.Options.InputSource, c.command.Options.Selection = "", nil
	result := c.run(t)
	if !result.NoSubmission || len(c.gateway.prepared) != 0 {
		t.Fatalf("result=%+v", result)
	}
	if refusal := result.AgentContent + result.Error; !strings.Contains(refusal, "@img2") {
		t.Fatalf("refusal must name @img2: %q", refusal)
	}
}

var _ = generation.TaskSucceeded
