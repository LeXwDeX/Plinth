package agents_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// Every reasoning item names the source it came from, and only the same source
// gets it back: the same connection, model and wire, and a thinking kind both
// sides declare (#377). An item that names no kind at all — a message of a
// checkpoint written before the kind existed — matches nothing, because no
// request can tell where it came from.
func TestReasoningItemMatchesOnlyItsOwnSource(t *testing.T) {
	source := agents.Source{Model: "deepseek", Wire: "chat_completions", Kind: agents.ReasoningPlaintext}
	item := agents.ReasoningItem{
		Model: "deepseek", Wire: "chat_completions", Kind: agents.ReasoningPlaintext,
		Content: "private plan",
	}
	if !item.Matches(source) {
		t.Fatalf("the item of the same source did not match: %#v", item)
	}
	for name, change := range map[string]func(*agents.ReasoningItem){
		"another model":      func(item *agents.ReasoningItem) { item.Model = "qwen-max" },
		"another wire":       func(item *agents.ReasoningItem) { item.Wire = "responses" },
		"another kind":       func(item *agents.ReasoningItem) { item.Kind = agents.ReasoningEncrypted },
		"no kind":            func(item *agents.ReasoningItem) { item.Kind = "" },
		"the kind none":      func(item *agents.ReasoningItem) { item.Kind = agents.ReasoningNone },
		"another connection": func(item *agents.ReasoningItem) { item.Connection = "llm-2" },
	} {
		foreign := item
		change(&foreign)
		if foreign.Matches(source) {
			t.Errorf("%s: the item matched: %#v", name, foreign)
		}
	}
	// The connection of the default one is empty on both sides.
	local := item
	local.Connection = "llm-2"
	if !local.Matches(agents.Source{
		Connection: "llm-2", Model: "deepseek", Wire: "chat_completions", Kind: agents.ReasoningPlaintext,
	}) {
		t.Error("the item of the named connection did not match")
	}
}

// A request's source is the connection and model it goes to plus the thinking
// contract it runs under; a request without a frozen contract has no kind, so
// nothing matches it.
func TestRequestSourceFollowsTheFrozenThinking(t *testing.T) {
	request := agents.Request{Model: "deepseek"}
	if source := agents.ForRequest(request); source.Kind != "" || source.Wire != "" {
		t.Fatalf("source without a contract = %#v", source)
	}
	request.Thinking = &agents.Thinking{Wire: "chat_completions", Kind: agents.ReasoningPlaintext, Passback: true}
	source := agents.ForRequest(request)
	if source.Model != "deepseek" || source.Wire != "chat_completions" || source.Kind != agents.ReasoningPlaintext {
		t.Fatalf("source = %#v", source)
	}
}

// An Adapter reads back the reasoning of its own kind only, in order, and none
// when the message carries no item of that kind.
func TestReasoningTextKeepsOnlyTheAdaptersOwnKind(t *testing.T) {
	message := agents.Message{Role: "assistant", Reasoning: []agents.ReasoningItem{
		{Kind: agents.ReasoningPlaintext, Content: "plan one"},
		{Kind: agents.ReasoningEncrypted, Content: `{"type":"reasoning"}`},
		{Kind: agents.ReasoningPlaintext, Content: " and two"},
	}}
	if got := message.ReasoningText(agents.ReasoningPlaintext); got != "plan one and two" {
		t.Fatalf("plaintext = %q", got)
	}
	if got := message.ReasoningText(agents.ReasoningEncrypted); got != `{"type":"reasoning"}` {
		t.Fatalf("encrypted = %q", got)
	}
	if got := message.ReasoningText(""); got != "" {
		t.Fatalf("no kind = %q", got)
	}
	if got := (agents.Message{Role: "assistant"}).ReasoningText(agents.ReasoningPlaintext); got != "" {
		t.Fatalf("a message without reasoning = %q", got)
	}
}

// A message written before the reasoning items existed carries one string under
// ReasoningContent. It still reads as one item with no source: the text is
// there, and no request can be traced back to it (#377).
func TestAMessageOfAnOlderCheckpointStillReadsItsReasoning(t *testing.T) {
	var message agents.Message
	legacy := `[{"Role":"assistant","Content":"","ReasoningContent":"earlier plan"}]`
	if err := json.Unmarshal([]byte(legacy), &message); err != nil {
		t.Fatal(err)
	}
	if len(message.Reasoning) != 1 || message.Reasoning[0].Content != "earlier plan" {
		t.Fatalf("legacy message = %#v", message)
	}
	if message.Reasoning[0].Kind != "" || message.Reasoning[0].Model != "" {
		t.Fatalf("legacy item names a source it never had: %#v", message.Reasoning[0])
	}
	if message.ReasoningText(agents.ReasoningPlaintext) != "" {
		t.Fatal("a legacy item is not sent back: its source is unknown")
	}
	// The messages of today carry a list, and the string is no longer written.
	encoded, err := json.Marshal(agents.Message{Role: "assistant",
		Reasoning: []agents.ReasoningItem{{Model: "deepseek", Wire: "chat_completions", Kind: agents.ReasoningPlaintext, Content: "plan"}}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded agents.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Reasoning) != 1 || decoded.Reasoning[0].Content != "plan" {
		t.Fatalf("round trip = %#v", decoded)
	}
	if text := string(encoded); !json.Valid(encoded) || len(decoded.Reasoning) != 1 {
		t.Fatalf("encoded = %s", text)
	}
}

// What a turn keeps after it has ended: the reasoning of the round's completion
// is dropped, the completion itself stays (#382, #377).
func TestACompletionDropsItsReasoningForTheEndedTurn(t *testing.T) {
	completion := agents.Completion{Content: "done", ReasoningContent: "private plan", ToolCalls: []agents.ToolCall{{ID: "c0"}}}
	cleared := completion.WithoutReasoning()
	if cleared.ReasoningContent != "" || cleared.Content != "done" || len(cleared.ToolCalls) != 1 {
		t.Fatalf("cleared completion = %#v", cleared)
	}
	if completion.ReasoningContent != "private plan" {
		t.Fatalf("the original completion was changed: %#v", completion)
	}
}
