package chat_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #699: when no style picture shows a standing full body, the Agent asks the
// proportions on a next_step card whose options each name one refine_image.
// In the reported session the Agent then sent two paid restyles in one turn
// (a compact one and a geometric fix of the long one). Answering the card on
// the card runs exactly one generation; the second is refused with the
// Schema's words.
func TestRestyleProportionCardAnswerRunsOneGeneration_699(t *testing.T) {
	compact := []byte(`{"prompt":"redraw the character picture in the style picture's rendering, about four heads tall",` +
		`"reference_handles":["@img1","@img3"]}`)
	rig := newOneStepRig(t, nil,
		agents.Completion{ToolCalls: []agents.ToolCall{{ID: "call-compact", Name: "refine_image", Arguments: compact}}},
		agents.Completion{ToolCalls: []agents.ToolCall{{ID: "call-geometry", Name: "refine_image",
			Arguments: []byte(`{"prompt":"make the legs 25% shorter","reference_handles":["@img9"]}`)}}},
		agents.Completion{Content: "已按约 4 头身重绘。"},
	)
	step := &chat.ChoiceStep{Tool: "refine_image", Capability: "image-to-image"}
	rig.domain = "image"
	rig.options = []chat.ChoiceOption{
		{ID: "character", Label: "沿用角色图的比例", Step: step, Arguments: json.RawMessage(`{}`)},
		{ID: "four_heads", Label: "约 4 头身", Step: step, Arguments: json.RawMessage(`{}`)},
		{ID: "style_head", Label: "只取风格图的头部与线条", Step: step, Arguments: json.RawMessage(`{}`)},
	}
	rig.run(t, "auto", "选择：约 4 头身", "four_heads", step)
	if !slices.Equal(rig.tools.calls, []string{"refine_image"}) {
		t.Fatalf("ran %v, want one refine_image", rig.tools.calls)
	}
	results := rig.toolResults()
	if len(results) != 2 || results[0] != "ran refine_image" ||
		!strings.Contains(results[1], "约 4 头身") || !strings.Contains(results[1], "refine_image") {
		t.Fatalf("tool results = %q", results)
	}
}
