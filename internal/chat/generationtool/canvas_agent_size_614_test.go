package generationtool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #614: an output size the Agent chose for the Offering (set_params) is
// carried out on a canvas turn instead of being replaced by the artboard
// size; the artboard keeps setting the size over the person's panel choice
// and a size nobody chose (#653: the highest quality at the canvas ratio, here
// GPT Image's largest legal square). The size note speaks only when the
// canvas set the size.
func TestCanvasTurnKeepsTheOutputSizeTheAgentChose(t *testing.T) {
	_, schema := handleExecutor(t)
	remembered := func(source string) *Executor {
		return &Executor{
			chat: schema,
			offeringParameters: func(_, _, _ string) map[string]json.RawMessage {
				return map[string]json.RawMessage{"size": json.RawMessage(`"3840x2160"`)}
			},
			offeringParameterStates: func(_, _, _ string) map[string]RememberedParameter {
				return map[string]RememberedParameter{"size": {Value: json.RawMessage(`"3840x2160"`), Source: source}}
			},
		}
	}
	build := func(executor *Executor, command chat.ToolCommand, offering generation.Offering) (map[string]any, string) {
		t.Helper()
		raw, _, err := executor.buildInput(context.Background(), command, offering, map[string]json.RawMessage{}, "edit", nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var input map[string]any
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatal(err)
		}
		return input, executor.canvasSizeNote(command, offering, raw)
	}

	// The Agent set 3840x2160 for the Offering; the artboard is 1024x1024.
	input, note := build(remembered("agent"), canvasCommand(schema, 1024, 1024), pixelSizeOffering())
	if input["size"] != "3840x2160" {
		t.Fatalf("size = %v; want the Agent's 3840x2160", input["size"])
	}
	if note != "" {
		t.Fatalf("note = %q; the canvas did not set the size", note)
	}

	// A size the person pinned in the panel: the artboard sets the size.
	input, _ = build(remembered("user"), canvasCommand(schema, 1024, 1024), pixelSizeOffering())
	if input["size"] != "2880x2880" {
		t.Fatalf("person's remembered size: size = %v; want the artboard's 2880x2880", input["size"])
	}

	// The person's panel of this turn replaces the Agent's value: the canvas
	// sets the size again.
	panel := canvasCommand(schema, 1024, 1024)
	panel.ExplicitParameters = map[string]json.RawMessage{"size": json.RawMessage(`"1536x1152"`)}
	panel.Options.Parameters = panel.ExplicitParameters
	input, _ = build(remembered("agent"), panel, pixelSizeOffering())
	if input["size"] != "2880x2880" {
		t.Fatalf("panel over the Agent's value: size = %v; want the artboard's 2880x2880", input["size"])
	}

	// The Agent restored the size to its default ("null"): nobody chose it.
	restored := &Executor{
		chat: schema,
		offeringParameters: func(_, _, _ string) map[string]json.RawMessage {
			return map[string]json.RawMessage{"size": json.RawMessage(`null`)}
		},
		offeringParameterStates: func(_, _, _ string) map[string]RememberedParameter {
			return map[string]RememberedParameter{"size": {Value: json.RawMessage(`null`), Source: "agent"}}
		},
	}
	input, _ = build(restored, canvasCommand(schema, 1024, 1024), pixelSizeOffering())
	if input["size"] != "2880x2880" {
		t.Fatalf("restored default: size = %v; want the artboard's 2880x2880", input["size"])
	}

	// A ratio model keeps the Agent's ratio as well.
	ratio := remembered("agent")
	ratio.offeringParameters = func(_, _, _ string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"aspect_ratio": json.RawMessage(`"16:9"`)}
	}
	ratio.offeringParameterStates = func(_, _, _ string) map[string]RememberedParameter {
		return map[string]RememberedParameter{"aspect_ratio": {Value: json.RawMessage(`"16:9"`), Source: "agent"}}
	}
	input, note = build(ratio, canvasCommand(schema, 1024, 1024), ratioOffering())
	if input["aspect_ratio"] != "16:9" || note != "" {
		t.Fatalf("ratio model: aspect_ratio = %v, note = %q; want the Agent's 16:9 and no note", input["aspect_ratio"], note)
	}
}
