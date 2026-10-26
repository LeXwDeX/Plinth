package generationtool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func taskAssetURL(schema catalog.ChatSchema, task, asset string) string {
	return strings.NewReplacer("{task_id}", task, "{asset_id}", asset).Replace(schema.Context.AssetURLTemplate)
}

func refineCall(name, taskID, prompt string) chat.ToolCall {
	arguments, _ := json.Marshal(map[string]string{"prompt": prompt})
	return chat.ToolCall{Name: name, TaskID: taskID, Arguments: arguments}
}

// #272: the prompt of an earlier call is carried into the new one only when
// the picture being edited is that call's own result. A new input (the
// artboard snapshot, an upload, another tool's picture) is edited with the
// reference template, so old instructions never leak into it.
func TestPreviousPromptIsChainedOnlyForThatCallsOwnResult(t *testing.T) {
	executor, schema := handleExecutor(t)
	refine := schema.Tools[0]
	if refine.Name != "refine_image" || !strings.Contains(refine.PromptWithPreviousTemplate, "{previous_prompt}") {
		t.Fatalf("default refine tool: %+v", refine.Name)
	}
	outpaint := taskAssetURL(schema, "task-outpaint", "asset-4")
	older := taskAssetURL(schema, "task-old", "asset-2")
	history := []chat.Message{
		{Role: schema.Roles.User, Content: "a cat"},
		{Role: schema.Roles.Assistant, ImageURLs: []string{older, "/legacy/generated/old.png"},
			ToolCalls: []chat.ToolCall{refineCall(refine.Name, "task-old", "a ginger cat on a sofa")},
			Attachments: []chat.Attachment{
				{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-old", AssetID: "asset-2", ImageSlot: 1},
				{Handle: "@img3", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-old", ImageSlot: 2}}},
		{Role: schema.Roles.User, Content: "extend it"},
		{Role: schema.Roles.Assistant, ImageURLs: []string{outpaint, taskAssetURL(schema, "task-views", "asset-v")},
			ToolCalls: []chat.ToolCall{
				refineCall(refine.Name, "task-outpaint", "Outpaint and extend the room, keep exactly"),
				refineCall("create_three_view", "task-views", "three views of the cat")}},
		{Role: schema.Roles.User, Content: "把两张猫猫图按照结构摆放合并为一张照片"},
	}
	edit := "Merge the two cats as laid out"
	withPrevious := func(previous string) string {
		return strings.NewReplacer("{previous_prompt}", previous, "{prompt}", edit).Replace(refine.PromptWithPreviousTemplate)
	}
	withReference := strings.ReplaceAll(refine.PromptWithReferenceTemplate, "{prompt}", edit)
	for _, test := range []struct {
		name       string
		references []string
		want       string
	}{
		{"artboard snapshot first, earlier result after it", []string{"/api/v1/input-assets/snapshot", outpaint}, withReference},
		{"a fresh upload", []string{"/api/v1/input-assets/upload"}, withReference},
		{"the last call's own result", []string{outpaint}, withPrevious("Outpaint and extend the room, keep exactly")},
		{"an older call's own result", []string{older}, withPrevious("a ginger cat on a sofa")},
		{"an older result known only by its attachment", []string{"/legacy/generated/old.png"}, withPrevious("a ginger cat on a sofa")},
		{"another tool's result", []string{taskAssetURL(schema, "task-views", "asset-v")}, withReference},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := chat.ToolCommand{Tool: refine, History: history}
			if got := executor.synthesizePrompt(command, edit, test.references); got != test.want {
				t.Fatalf("prompt = %q\nwant     %q", got, test.want)
			}
		})
	}
}

func canvasCommand(schema catalog.ChatSchema, width, height int) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner", SessionID: "session", Tool: schema.Tools[0],
		Call: agents.ToolCall{ID: "call", Name: schema.Tools[0].Name},
		Options: chat.TurnOptions{InputSource: schema.Context.CanvasInputSource, Selection: []chat.Attachment{{
			Handle: "@img5", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCanvas, AssetID: "snapshot",
			URL: "/api/v1/input-assets/snapshot", Canvas: &chat.CanvasLayout{Width: width, Height: height, Layers: 2}}}},
	}
}

func pixelSizeOffering() generation.Offering {
	return generation.Offering{ID: "openai/gpt-image-2/image-to-image", Capability: generation.ImageToImage, Parameters: []generation.Parameter{
		{Name: "prompt", Type: "text", Semantic: generation.SemanticPrompt},
		{Name: "size", Type: "text", Semantic: generation.SemanticOutputSize, Default: json.RawMessage(`"2048x2048"`),
			Options:   []generation.ParameterOption{{Value: "1024x1024"}, {Value: "1536x1024"}, {Value: "1536x1152"}, {Value: "2048x2048"}},
			ImageSize: &generation.ImageSizeConstraint{MinPixels: 655360, MaxPixels: 8294400, MaxEdge: 3840, Multiple: 16, MaxRatio: 3}},
	}}
}

func ratioOffering() generation.Offering {
	return generation.Offering{ID: "meshy/nano-banana-pro/image-to-image", Capability: generation.ImageToImage, Parameters: []generation.Parameter{
		{Name: "prompt", Type: "text", Semantic: generation.SemanticPrompt},
		{Name: "aspect_ratio", Type: "select", Semantic: generation.SemanticOutputSize, Default: json.RawMessage(`"1:1"`),
			Options: []generation.ParameterOption{{Value: "1:1"}, {Value: "4:3"}, {Value: "3:4"}, {Value: "16:9"}, {Value: "9:16"}}},
	}}
}

// #272: what the artboard shows is what comes out. On a canvas turn the output
// size follows the artboard over a size the Agent set earlier in the session;
// a model that cannot produce it gets the nearest legal size, and the Agent is
// told which.
func TestCanvasTurnOutputSizeFollowsTheArtboard(t *testing.T) {
	_, schema := handleExecutor(t)
	agentSize := func(_, _, _ string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"size": json.RawMessage(`"1536x1152"`), "aspect_ratio": json.RawMessage(`"16:9"`)}
	}
	executor := Executor{chat: schema, offeringParameters: agentSize}
	build := func(command chat.ToolCommand, offering generation.Offering) map[string]any {
		t.Helper()
		raw, err := executor.buildInput(context.Background(), command, offering, map[string]json.RawMessage{}, "merge", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var input map[string]any
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatal(err)
		}
		return input
	}
	note := func(command chat.ToolCommand, offering generation.Offering, input map[string]any) string {
		raw, _ := json.Marshal(input)
		return executor.canvasSizeNote(command, offering, raw)
	}
	followed := func(canvas, size string) string {
		return strings.NewReplacer("{canvas}", canvas, "{size}", size).Replace(schema.Context.CanvasOutputSizeTemplate)
	}
	nearest := func(canvas, size string) string {
		return strings.NewReplacer("{canvas}", canvas, "{size}", size).Replace(schema.Context.CanvasOutputSizeNearestTemplate)
	}

	for _, test := range []struct {
		name          string
		width, height int
		offering      generation.Offering
		parameter     string
		want          string
		note          string
	}{
		{"pixel model takes the artboard size", 2048, 2048, pixelSizeOffering(), "size", "2048x2048", followed("2048×2048", "2048x2048")},
		{"pixel model takes a custom artboard size", 1920, 1088, pixelSizeOffering(), "size", "1920x1088", followed("1920×1088", "1920x1088")},
		{"pixel model snaps to its grid", 1000, 700, pixelSizeOffering(), "size", "1008x704", nearest("1000×700", "1008x704")},
		{"ratio model takes the artboard ratio", 2048, 1536, ratioOffering(), "aspect_ratio", "4:3", followed("2048×1536", "4:3")},
		{"ratio model takes the nearest ratio", 2048, 1000, ratioOffering(), "aspect_ratio", "16:9", nearest("2048×1000", "16:9")},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := canvasCommand(schema, test.width, test.height)
			input := build(command, test.offering)
			if input[test.parameter] != test.want {
				t.Fatalf("%s = %v; want %s", test.parameter, input[test.parameter], test.want)
			}
			if got := note(command, test.offering, input); got != test.note {
				t.Fatalf("note = %q; want %q", got, test.note)
			}
		})
	}

	// The note reports what was really submitted, after the Offering's own
	// rules (a forced larger size of the same ratio still follows the canvas).
	command := canvasCommand(schema, 2048, 2048)
	if got := note(command, pixelSizeOffering(), map[string]any{"size": "4096x4096"}); got != followed("2048×2048", "4096x4096") {
		t.Fatalf("forced same-ratio size note = %q", got)
	}

	// Outside a canvas turn, for a tool that does not bind the snapshot, or for
	// a fixed size, the earlier settings apply and nothing is said.
	plain := canvasCommand(schema, 2048, 2048)
	plain.Options.InputSource = ""
	ignoring := canvasCommand(schema, 2048, 2048)
	ignoring.Tool.CanvasSource = canvasSourceRefuse
	fixed := pixelSizeOffering()
	fixed.Parameters[1].Fixed = true
	for name, run := range map[string]struct {
		command  chat.ToolCommand
		offering generation.Offering
		want     any
	}{
		"not a canvas turn":            {plain, pixelSizeOffering(), "1536x1152"},
		"tool does not bind the board": {ignoring, pixelSizeOffering(), "1536x1152"},
		"fixed size":                   {canvasCommand(schema, 2048, 2048), fixed, nil},
	} {
		input := build(run.command, run.offering)
		if input["size"] != run.want {
			t.Fatalf("%s: size = %v; want %v", name, input["size"], run.want)
		}
		if name != "fixed size" {
			if got := note(run.command, run.offering, input); got != "" {
				t.Fatalf("%s: note = %q", name, got)
			}
		}
	}
}
