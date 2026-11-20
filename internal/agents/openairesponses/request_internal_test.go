package openairesponses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

func marshalItems(t *testing.T, items []any) string {
	t.Helper()
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func reasoningItem(id string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","type":"reasoning","summary":[],"encrypted_content":"enc-` + id + `"}`)
}

// The mapping of the Port's messages to the input items, role by role.
func TestConvertMessagesMapsEveryRole(t *testing.T) {
	picture := agents.ContentPart{Kind: "image_url", URL: "data:image/png;base64,AAAA", Detail: "low"}
	instructions, input, err := convertMessages([]agents.Message{
		{Role: "system", Content: "first rule"},
		{Role: "system", Content: ""},
		{Role: "system", Content: "second rule"},
		{Role: "user", Content: "hello"},
		{Role: "user", Content: ""},
		{Role: "system", Content: "a later reminder"},
		{Role: "assistant", Content: "calling", ToolCalls: []agents.ToolCall{
			{ID: "t1", Name: "a", Arguments: json.RawMessage(`{"x":1}`)},
			{ID: "t2", Name: "b"},
			{ID: "t3", Name: "c", Arguments: json.RawMessage("  ")},
		}},
		{Role: "tool", ToolCallID: "t1", Content: "r1"},
		{Role: "tool", ToolCallID: "t2", Parts: []agents.ContentPart{{Kind: "text", Text: "r2 a"}, {Kind: "text", Text: "r2 b"}, picture}},
		{Role: "tool", ToolCallID: "t3"},
		{Role: "user", Parts: []agents.ContentPart{{Kind: "text", Text: "pictures"}, picture}, PicturesOnly: true},
		{Role: "assistant"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if instructions != "first rule\n\nsecond rule" {
		t.Fatalf("instructions = %q", instructions)
	}
	want := []string{
		`{"role":"user","content":"hello"}`,
		`{"role":"system","content":"a later reminder"}`,
		`{"role":"assistant","content":"calling"}`,
		`{"type":"function_call","call_id":"t1","name":"a","arguments":"{\"x\":1}"}`,
		`{"type":"function_call","call_id":"t2","name":"b","arguments":"{}"}`,
		`{"type":"function_call","call_id":"t3","name":"c","arguments":"{}"}`,
		`{"type":"function_call_output","call_id":"t1","output":"r1"}`,
		`{"type":"function_call_output","call_id":"t2","output":"r2 a\nr2 b"}`,
		`{"type":"function_call_output","call_id":"t3","output":""}`,
		`{"role":"user","content":[{"type":"input_text","text":"pictures"},{"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":"low"}]}`,
	}
	if got := marshalItems(t, input); got != "["+strings.Join(want, ",")+"]" {
		t.Fatalf("input:\n got %s\nwant [%s]", got, strings.Join(want, ","))
	}
	if _, _, err := convertMessages([]agents.Message{{Role: "developer", Content: "x"}}); err == nil {
		t.Fatal("an unknown role was accepted")
	}
}

// A reasoning item goes back where the upstream returned it: ahead of the
// text and of the first call, and between two calls when it came between them.
// One that nothing follows is dropped, and the API never sees a reasoning item
// that leads nowhere.
func TestAssistantItemsPlaceReasoningWhereItCameFrom(t *testing.T) {
	calls := []agents.ToolCall{{ID: "c0", Name: "a", Arguments: json.RawMessage(`{}`)}, {ID: "c1", Name: "b", Arguments: json.RawMessage(`{}`)}}
	carried := encodeReasoning([]carriedReasoning{
		{Before: 0, Item: reasoningItem("r0")},
		{Before: 1, Item: reasoningItem("r1")},
		{Before: 2, Item: reasoningItem("r2")}, // after the last call: nothing follows
	})
	call := func(id, name string) string {
		return `{"type":"function_call","call_id":"` + id + `","name":"` + name + `","arguments":"{}"}`
	}
	r0, r1 := string(reasoningItem("r0")), string(reasoningItem("r1"))
	for name, test := range map[string]struct {
		message agents.Message
		want    []string
	}{
		"two calls": {agents.Message{Role: "assistant", ToolCalls: calls, ReasoningContent: carried},
			[]string{r0, call("c0", "a"), r1, call("c1", "b")}},
		"text and calls": {agents.Message{Role: "assistant", Content: "preamble", ToolCalls: calls, ReasoningContent: carried},
			[]string{r0, `{"role":"assistant","content":"preamble"}`, call("c0", "a"), r1, call("c1", "b")}},
		// With no call to lead to, only what came before any call is placed.
		"text only":    {agents.Message{Role: "assistant", Content: "answer", ReasoningContent: carried}, []string{r0, `{"role":"assistant","content":"answer"}`}},
		"nothing else": {agents.Message{Role: "assistant", ReasoningContent: carried}, nil},
		"no reasoning": {agents.Message{Role: "assistant", ToolCalls: calls}, []string{call("c0", "a"), call("c1", "b")}},
	} {
		if got, want := marshalItems(t, assistantItems(test.message)), "["+strings.Join(test.want, ",")+"]"; got != want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, want)
		}
	}
}

// ReasoningContent that is not this Adapter's list, or lists something that is
// not a reasoning item, is ignored.
func TestDecodeReasoningKeepsOnlyReasoningItems(t *testing.T) {
	for _, content := range []string{"", "plain text", "{}", "[1,2]", `[{"before":0,"item":"x"}]`, `[{"before":-1,"item":{"type":"reasoning"}}]`,
		`[{"before":0,"item":{"type":"function_call"}}]`} {
		if got := decodeReasoning(content); len(got) != 0 {
			t.Errorf("%q decoded to %v", content, got)
		}
	}
	mixed := `[{"before":0,"item":{"type":"function_call"}},{"before":1,"item":{"type":"reasoning","id":"r"}}]`
	if got := decodeReasoning(mixed); len(got) != 1 || got[0].Before != 1 {
		t.Errorf("mixed = %v", got)
	}
	items := []carriedReasoning{{Before: 0, Item: reasoningItem("a")}, {Before: 3, Item: reasoningItem("b")}}
	if got := decodeReasoning(encodeReasoning(items)); len(got) != 2 || got[1].Before != 3 || string(got[1].Item) != string(reasoningItem("b")) {
		t.Errorf("round trip = %v", got)
	}
	if encodeReasoning(nil) != "" {
		t.Error("no items must encode to the empty string")
	}
}

// A reasoning item goes back in the form the probe of 2026-11-20 replayed and
// the gateway accepted: type, summary and encrypted_content, without the id and
// the content the item came with. A summary that has parts is kept; a null or
// absent one is an empty list; an item with no encrypted content is not kept.
func TestReplayReasoningKeepsTheAcceptedMembersOnly(t *testing.T) {
	for name, test := range map[string]struct {
		item itemBody
		want string // "" is not kept
	}{
		"as the gateway returned it": {itemBody{Type: "reasoning", Content: json.RawMessage(`[]`), Summary: json.RawMessage(`[]`), EncryptedContent: "gAAAAAB-token"},
			`{"type":"reasoning","summary":[],"encrypted_content":"gAAAAAB-token"}`},
		"a summary with a part": {itemBody{Type: "reasoning", Summary: json.RawMessage(`[{"type":"summary_text","text":"plan"}]`), EncryptedContent: "t"},
			`{"type":"reasoning","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"t"}`},
		"a null summary":   {itemBody{Type: "reasoning", Summary: json.RawMessage(`null`), EncryptedContent: "t"}, `{"type":"reasoning","summary":[],"encrypted_content":"t"}`},
		"no summary":       {itemBody{Type: "reasoning", EncryptedContent: "t"}, `{"type":"reasoning","summary":[],"encrypted_content":"t"}`},
		"no encrypted one": {itemBody{Type: "reasoning", Summary: json.RawMessage(`[]`)}, ""},
	} {
		got := replayReasoning(test.item)
		if string(got) != test.want {
			t.Errorf("%s: %s, want %s", name, got, test.want)
		}
	}
}

// The done item closes an item and the output of the terminal event only fills
// the gaps: a reasoning item that no done event closed is taken from the
// output, one that a done event closed keeps its own token.
func TestTheTerminalOutputOnlyFillsTheGapsOfDoneItems(t *testing.T) {
	outputs := newOutputs()
	done := json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[],"content":[],"encrypted_content":"from-the-done-event"}`)
	if err := outputs.settle(0, done); err != nil {
		t.Fatal(err)
	}
	if !outputs.isSettled(0) || outputs.isSettled(1) {
		t.Fatalf("settled: %t %t", outputs.isSettled(0), outputs.isSettled(1))
	}
	completion := outputs.completion(false)
	carried := decodeReasoning(completion.ReasoningContent)
	if len(carried) != 1 || !strings.Contains(string(carried[0].Item), "from-the-done-event") || strings.Contains(string(carried[0].Item), "rs_1") {
		t.Fatalf("carried = %s", completion.ReasoningContent)
	}
}
