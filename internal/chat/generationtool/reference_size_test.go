package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// measuredReferences resolves each URL to the asset of the same name and
// measures it, like references.Resolver for an owned input asset.
type measuredReferences map[string][2]int

func (sizes measuredReferences) Resolve(_ context.Context, _ generation.OwnerID, urls []string) ([]generation.AssetID, error) {
	assets := make([]generation.AssetID, 0, len(urls))
	for _, url := range urls {
		assets = append(assets, generation.AssetID(url))
	}
	return assets, nil
}

func (sizes measuredReferences) ImageDimensions(_ context.Context, _, asset string) (int, int, error) {
	size, ok := sizes[asset]
	if !ok {
		return 0, 0, errors.New("no such picture")
	}
	return size[0], size[1], nil
}

// unmeasuredReferences resolves pictures but cannot read their size.
type unmeasuredReferences struct{}

func (unmeasuredReferences) Resolve(_ context.Context, _ generation.OwnerID, urls []string) ([]generation.AssetID, error) {
	assets := make([]generation.AssetID, 0, len(urls))
	for _, url := range urls {
		assets = append(assets, generation.AssetID(url))
	}
	return assets, nil
}

func referencedPixelOffering() generation.Offering {
	offering := pixelSizeOffering()
	offering.Parameters = append(offering.Parameters, generation.Parameter{
		Name: "image", Type: "asset-array", Semantic: generation.SemanticReferenceImage,
	})
	return offering
}

// #296: editing one uploaded picture in chat keeps its shape. When nobody
// chose a size, the output size is the legal value closest to the one
// reference; any chosen size (the turn, the 参数 panel, the Agent's
// set_params), several references or a canvas turn leave the existing rules
// in force. The Agent is told when the reference set the size.
func TestOneReferenceSetsTheOutputSizeNobodyChose(t *testing.T) {
	_, schema := handleExecutor(t)
	refine := schema.Tools[0]
	if refine.Name != "refine_image" || refine.ReferenceSize != referenceSizeSingle || refine.ResultFields.SizeNote == "" {
		t.Fatalf("default refine tool must follow a single reference and report it: %+v", refine.ReferenceSize)
	}
	pictures := measuredReferences{"upload-4x3": {800, 600}, "upload-square": {2048, 2048}, "upload-wide": {1920, 1080}, "snapshot": {2048, 2048}}
	followed := func(reference, size string) string {
		return strings.NewReplacer("{reference}", reference, "{size}", size).Replace(schema.Context.ReferenceOutputSizeTemplate)
	}
	type run struct {
		command    chat.ToolCommand
		remembered map[string]json.RawMessage
		references []string
		resolver   ReferenceResolver
		tool       func(*chat.ToolCommand)
	}
	plain := func() chat.ToolCommand {
		return chat.ToolCommand{OwnerID: "owner", SessionID: "session", Tool: refine}
	}
	build := func(t *testing.T, test run) (map[string]any, string) {
		t.Helper()
		resolver := test.resolver
		if resolver == nil {
			resolver = pictures
		}
		executor := Executor{chat: schema, references: resolver, offeringParameters: func(_, _, _ string) map[string]json.RawMessage {
			return test.remembered
		}}
		command := test.command
		if test.tool != nil {
			test.tool(&command)
		}
		offering := referencedPixelOffering()
		raw, err := executor.buildInput(context.Background(), command, offering, map[string]json.RawMessage{}, "make the background white", test.references, nil)
		if err != nil {
			t.Fatal(err)
		}
		var input map[string]any
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatal(err)
		}
		return input, executor.referenceSizeNote(context.Background(), command, offering, raw)
	}
	turnSize := plain()
	turnSize.Options.Size = "1536x1024"
	canvas := canvasCommand(schema, 2048, 2048)
	for _, test := range []struct {
		name string
		run  run
		size any
		note string
	}{
		{"a 4:3 upload keeps its shape (the #296 report)", run{command: plain(), references: []string{"upload-4x3"}},
			"1536x1152", followed("800×600", "1536x1152")},
		{"a square upload stays square", run{command: plain(), references: []string{"upload-square"}},
			"2048x2048", followed("2048×2048", "2048x2048")},
		// This test Offering has no 16:9 option: the photo snaps to the grid.
		{"a photo off the grid snaps to it", run{command: plain(), references: []string{"upload-wide"}},
			"1920x1088", strings.NewReplacer("{reference}", "1920×1080", "{size}", "1920x1088").Replace(schema.Context.ReferenceOutputSizeNearestTemplate)},
		{"a size restored to its default is not a choice", run{command: plain(), references: []string{"upload-4x3"},
			remembered: map[string]json.RawMessage{"size": json.RawMessage(`null`)}},
			"1536x1152", followed("800×600", "1536x1152")},
		{"the size remembered for the session wins", run{command: plain(), references: []string{"upload-4x3"},
			remembered: map[string]json.RawMessage{"size": json.RawMessage(`"1024x1024"`)}},
			"1024x1024", ""},
		{"a size the turn names wins", run{command: turnSize, references: []string{"upload-4x3"}},
			"1536x1024", ""},
		{"several references keep the default", run{command: plain(), references: []string{"upload-4x3", "upload-square"}},
			nil, ""},
		{"no reference keeps the default", run{command: plain()},
			nil, ""},
		{"a picture whose size cannot be read keeps the default", run{command: plain(), references: []string{"upload-4x3"}, resolver: unmeasuredReferences{}},
			nil, ""},
		{"a tool that does not declare it keeps the default", run{command: plain(), references: []string{"upload-4x3"},
			tool: func(command *chat.ToolCommand) { command.Tool.ReferenceSize = "" }},
			nil, ""},
		{"on a canvas turn the artboard sets the size", run{command: canvas, references: []string{"upload-4x3"}},
			"2048x2048", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, note := build(t, test.run)
			if input["size"] != test.size {
				t.Fatalf("size = %v; want %v (input %v)", input["size"], test.size, input)
			}
			if note != test.note {
				t.Fatalf("note = %q; want %q", note, test.note)
			}
		})
	}
}

// The note is read from the input really submitted: a size of the reference's
// shape follows it, another shape is the nearest legal size, and a submitted
// size the rule would not pick says nothing.
func TestReferenceSizeNoteReadsTheSubmittedSize(t *testing.T) {
	_, schema := handleExecutor(t)
	executor := Executor{chat: schema}
	offering := referencedPixelOffering()
	nearest := strings.NewReplacer("{reference}", "1000×700", "{size}", "1008x704").Replace(schema.Context.ReferenceOutputSizeNearestTemplate)
	if got := executor.referenceSizeNoteFor(offering, map[string]any{"size": "1008x704"}, 1000, 700, nil); got != nearest {
		t.Fatalf("nearest note = %q; want %q", got, nearest)
	}
	if got := executor.referenceSizeNoteFor(offering, map[string]any{"size": "2048x2048"}, 1000, 700, nil); got != "" {
		t.Fatalf("a size the rule does not pick must not be reported: %q", got)
	}
	if got := submittedReferences(offering, map[string]any{"image": []any{"a", "b"}}); len(got) != 2 {
		t.Fatalf("submitted references = %v", got)
	}
}
