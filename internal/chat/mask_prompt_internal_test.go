package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agentscope"
)

// #244: the selection block names the frozen mask's target, box, coverage and
// transparency meaning, and the newest user message carries the
// server-composed selection preview with its Schema label.
func TestSelectionMaskPromptAndPreview(t *testing.T) {
	orchestrator, schema := canvasPromptOrchestrator(t)
	contextSchema := schema.Context
	snapshot := Attachment{Handle: "@img5", Kind: AttachmentKindImage, Role: AttachmentRoleCanvas,
		AssetID: "snapshot", URL: "/api/v1/assets/snapshot",
		Canvas: &CanvasLayout{Width: 64, Height: 48}}
	options := TurnOptions{
		InputSource: contextSchema.CanvasInputSource,
		Selection:   []Attachment{snapshot},
		SelectionMask: &SelectionMask{MaskAssetID: "mask-asset", Width: 64, Height: 48,
			BBox: [4]int{1, 2, 30, 40}, Coverage: 0.25},
	}
	prompt := orchestrator.selectionPrompt(options)
	for _, want := range []string{"@img5", "1,2-30,40", "25.0%", contextSchema.SelectionMaskNote} {
		if want == "" || !strings.Contains(prompt, want) {
			t.Fatalf("selection mask prompt lacks %q:\n%s", want, prompt)
		}
	}
	// Without a mask the block keeps its old shape: no mask words.
	plain := orchestrator.selectionPrompt(TurnOptions{InputSource: contextSchema.CanvasInputSource,
		Selection: []Attachment{snapshot}})
	if strings.Contains(plain, contextSchema.SelectionMaskNote) ||
		strings.Contains(plain, strings.ReplaceAll(contextSchema.SelectionMaskItemTemplate, "{handle}", "@img5")) {
		t.Fatalf("plain canvas prompt names a mask:\n%s", plain)
	}

	registry, err := compileToolRegistry(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator.registry = registry
	orchestrator.agentScope = agentscope.FromSchema(schema.AgentScope)
	orchestrator.selectionPreview = func(_ context.Context, ownerID, pictureURL, maskAssetID string) (string, bool) {
		if ownerID == "owner" && pictureURL == "/api/v1/assets/snapshot" && maskAssetID == "mask-asset" {
			return "data:image/png;base64,c2VsZWN0aW9u", true
		}
		return "", false
	}
	history := []Message{{ID: "user-1", Role: schema.Roles.User, Content: "把这里改成蓝色"}}
	request := orchestrator.agentRequest(context.Background(), history, options, "owner", "")
	last := request.Messages[len(request.Messages)-1]
	foundPicture, foundLabel := false, false
	for _, part := range last.Parts {
		if part.URL == "data:image/png;base64,c2VsZWN0aW9u" {
			foundPicture = true
		}
		if strings.Contains(part.Text, contextSchema.SelectionMaskPreviewLabel) {
			foundLabel = true
		}
	}
	if !foundPicture || !foundLabel {
		t.Fatalf("selection preview part/label missing: %+v", last)
	}

	// A composition failure keeps the mask text and does not add a picture.
	orchestrator.selectionPreview = func(context.Context, string, string, string) (string, bool) { return "", false }
	request = orchestrator.agentRequest(context.Background(), history, options, "owner", "")
	last = request.Messages[len(request.Messages)-1]
	for _, part := range last.Parts {
		if strings.HasPrefix(part.URL, "data:image/png") {
			t.Fatalf("failed preview produced a picture part: %+v", part)
		}
	}
	if !strings.Contains(request.Messages[0].Content, contextSchema.SelectionMaskNote) {
		t.Fatal("system prompt lost the mask description when the preview failed")
	}
}
