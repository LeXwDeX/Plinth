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

func promptReplayExecutor() *Executor {
	return &Executor{chat: catalog.ChatSchema{
		Context: catalog.ChatContextSchema{
			ToolPromptArgument: "prompt", ToolReferenceArgument: "reference_image_indices",
		},
		StyleLock: catalog.ChatStyleLockSchema{PromptTemplate: "\n\nStyle lock: {style}"},
		Agent: catalog.ChatAgentSchema{PromptReport: catalog.ChatPromptReportSchema{
			ChangedTemplate:              "changed",
			ChangedWithoutPromptTemplate: "changed without the original",
			UnchangedTemplate:            "unchanged",
			TruncationTemplate:           "cut {excess} of {length} to {maximum} for {model}",
			TooLongTemplate:              "refused {excess} of {length} over {maximum} for {model}",
			Layers: map[string]string{
				promptLayerReferenceTemplate: "reference template",
				promptLayerStyleLock:         "style lock",
				promptLayerNegativePrompt:    "negative prompt",
				promptLayerTruncation:        "truncation",
				promptLayerPreparedInput:     "prepared input",
			},
		}},
	}}
}

func promptReplayOffering(maxLength int) generation.Offering {
	return generation.Offering{
		ID: "fake/image", ModelID: "fake-model", ModelName: "Fake Model",
		Capability: generation.ImageToImage, Domain: generation.DomainImage,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, MaxLength: maxLength},
			{Name: "reference_assets", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 4},
			{Name: "negative_prompt", Type: "string", Semantic: generation.SemanticNegativePrompt},
		},
	}
}

func promptReplayCommand() chat.ToolCommand {
	return chat.ToolCommand{
		History: []chat.Message{{Role: "user", ImageURLs: []string{"https://example.test/one.png"}}},
		Call:    agents.ToolCall{Arguments: []byte(`{"prompt":"draw a cube","reference_image_indices":[1]}`)},
		Tool: catalog.ChatToolSchema{
			Name: "refine_image", Capability: generation.ImageToImage,
			FallbackCapability:          generation.ImageToImage,
			PromptWithReferenceTemplate: "Edit the reference image: {prompt}",
			PromptWithPreviousTemplate:  "Previous: {previous_prompt}. Now: {prompt}",
			DefaultParameters:           map[string]json.RawMessage{"negative_prompt": json.RawMessage(`"NEG"`)},
			ResultFields:                catalog.ChatToolResultFields{FinalPrompt: "final_prompt", PromptNote: "prompt_note"},
		},
		Options: chat.TurnOptions{StyleLock: chat.StyleLock{Enabled: true, Text: "LOCK"}},
	}
}

// #249: an approval or resume has no in-memory layer trace, but the prepared
// input still carries the exact prompt that was approved. The rebuilt report
// must name that prompt and every layer the first submission applied.
func TestPromptTraceFromPreparedRebuildsTheAppliedLayers(t *testing.T) {
	executor := promptReplayExecutor()
	offering := promptReplayOffering(0)
	command := promptReplayCommand()
	final := "Edit the reference image: draw a cube\n\nStyle lock: LOCK"
	input, _ := json.Marshal(map[string]any{
		"prompt": final, "reference_assets": []string{"asset-1"}, "negative_prompt": "NEG",
	})
	trace := executor.promptTraceFromPrepared(context.Background(), command, offering, input)
	if trace == nil || trace.Final != final || trace.Original != "draw a cube" {
		t.Fatalf("trace = %#v", trace)
	}
	var ids []string
	for _, layer := range trace.Layers {
		ids = append(ids, layer.ID)
		if layer.Text == "" {
			t.Fatalf("layer %q has no text", layer.ID)
		}
	}
	want := []string{promptLayerReferenceTemplate, promptLayerStyleLock, promptLayerNegativePrompt}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("layers = %v, want %v", ids, want)
	}
	finalPrompt, note := executor.promptReport(command, command.Tool, offering, trace)
	if finalPrompt != final {
		t.Fatalf("final_prompt = %q, want %q", finalPrompt, final)
	}
	rendered, _ := json.Marshal(note)
	if !strings.Contains(string(rendered), `"summary":"changed"`) ||
		!strings.Contains(string(rendered), "reference template") ||
		!strings.Contains(string(rendered), "style lock") ||
		!strings.Contains(string(rendered), "negative prompt") {
		t.Fatalf("prompt_note = %s", rendered)
	}
}

// A resume that can no longer reconstruct the reference list says so instead
// of inventing layers: the frozen prompt is still named, under prepared_input,
// and the deterministic default negative prompt stays reported.
func TestPromptTraceFromPreparedFallsBackWhenReferencesChanged(t *testing.T) {
	executor := promptReplayExecutor()
	offering := promptReplayOffering(0)
	command := promptReplayCommand()
	command.History = []chat.Message{{Role: "user", ImageURLs: []string{"https://example.test/other.png"}}}
	input, _ := json.Marshal(map[string]any{
		"prompt": "a frozen prompt", "reference_assets": []string{"asset-1"},
	})
	trace := executor.promptTraceFromPrepared(context.Background(), command, offering, input)
	if trace == nil || trace.Final != "a frozen prompt" || trace.Original != "draw a cube" {
		t.Fatalf("trace = %#v", trace)
	}
	if len(trace.Layers) != 2 || trace.Layers[0].ID != promptLayerNegativePrompt ||
		trace.Layers[1].ID != promptLayerPreparedInput {
		t.Fatalf("layers = %#v", trace.Layers)
	}
}

// #249: a prompt the server cut is reported with the truncation copy, which
// says the step was submitted, never with the too_long refusal copy.
func TestPromptTraceFromPreparedReportsTruncationWithItsOwnCopy(t *testing.T) {
	executor := promptReplayExecutor()
	offering := promptReplayOffering(20)
	command := promptReplayCommand()
	// Replaying the chain cuts the first 20 runes of the frozen prompt.
	final := []rune("Edit the reference image: draw a cube\n\nStyle lock: LOCK")[:20]
	input, _ := json.Marshal(map[string]any{
		"prompt": string(final), "reference_assets": []string{"asset-1"}, "negative_prompt": "NEG",
	})
	trace := executor.promptTraceFromPrepared(context.Background(), command, offering, input)
	if trace == nil || trace.Final != string(final) || trace.Truncated == 0 {
		t.Fatalf("trace = %#v", trace)
	}
	finalPrompt, note := executor.promptReport(command, command.Tool, offering, trace)
	if finalPrompt != string(final) {
		t.Fatalf("final_prompt = %q", finalPrompt)
	}
	rendered, _ := json.Marshal(note)
	if !strings.Contains(string(rendered), "cut ") {
		t.Fatalf("prompt_note does not use truncation_template: %s", rendered)
	}
	if strings.Contains(string(rendered), "Nothing was submitted") || strings.Contains(string(rendered), "refused ") {
		t.Fatalf("the refusal copy leaked into a submitted step: %s", rendered)
	}
}

// A Catalog without truncation_template gets a neutral truncation statement,
// again never the refusal statement.
func TestPromptReportTruncationFallsBackToNeutralCopy(t *testing.T) {
	executor := promptReplayExecutor()
	executor.chat.Agent.PromptReport.TruncationTemplate = ""
	offering := promptReplayOffering(10)
	command := promptReplayCommand()
	trace := &promptTrace{Original: "this prompt is far too long", Final: "this promp",
		Truncated: 20, MaxLength: 10}
	_, note := executor.promptReport(command, command.Tool, offering, trace)
	rendered, _ := json.Marshal(note)
	if !strings.Contains(string(rendered), "submitted it after cutting the last 20 characters") {
		t.Fatalf("neutral truncation copy missing: %s", rendered)
	}
	if strings.Contains(string(rendered), "Nothing was submitted") {
		t.Fatalf("refusal copy used for a submitted cut: %s", rendered)
	}
}

// The unchanged and changed-without-original templates are rendered, not just
// declared: the Agent learns why a prompt note has no layer list.
func TestPromptReportUsesTheUnchangedAndChangedWithoutPromptTemplates(t *testing.T) {
	executor := promptReplayExecutor()
	offering := promptReplayOffering(0)
	command := promptReplayCommand()
	unchanged := &promptTrace{Original: "as written", Final: "as written"}
	finalPrompt, note := executor.promptReport(command, command.Tool, offering, unchanged)
	rendered, _ := json.Marshal(note)
	if finalPrompt != "" || !strings.Contains(string(rendered), `"summary":"unchanged"`) {
		t.Fatalf("unchanged report = %q %s", finalPrompt, rendered)
	}
	withoutOriginal := &promptTrace{Final: "rewritten", Layers: []promptLayer{{ID: promptLayerPreparedInput}}}
	_, note = executor.promptReport(command, command.Tool, offering, withoutOriginal)
	rendered, _ = json.Marshal(note)
	if !strings.Contains(string(rendered), `"summary":"changed without the original"`) {
		t.Fatalf("changed report = %s", rendered)
	}
}

// An unchanged prompt still reports nothing when the Catalog has no unchanged
// template (historical Catalogs).
func TestPromptReportWithoutTemplatesKeepsTheHistoricalResult(t *testing.T) {
	executor := promptReplayExecutor()
	executor.chat.Agent.PromptReport.UnchangedTemplate = ""
	offering := promptReplayOffering(0)
	command := promptReplayCommand()
	finalPrompt, note := executor.promptReport(command, command.Tool, offering,
		&promptTrace{Original: "same", Final: "same"})
	if finalPrompt != "" || note != nil {
		t.Fatalf("report = %q %#v", finalPrompt, note)
	}
}
