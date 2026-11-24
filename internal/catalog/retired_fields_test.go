package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// toolLimitDeclarations are what a Catalog written before #553 declared for the
// per-reply tool call limit, each inserted after an anchor of the current
// default Catalog.
var toolLimitDeclarations = []struct {
	anchor, insert string
	before         bool
}{
	{`"max_listed_models": 20,`, `
        "max_tool_calls_per_reply": 100,
        "max_tool_calls_per_reply_setting": "agent_max_tool_calls_per_reply",
        "max_tool_calls_per_reply_maximum": 100,`, false},
	{`"llm_context_window_tokens": {`, `"agent_max_tool_calls_per_reply": {
            "type": "string",
            "environment": [{"name": "AGENT_MAX_TOOL_CALLS_PER_REPLY"}],
            "default": ""
          },
          `, true},
	{`"turn_stopped": "已按要求停止本轮生成",`, `
          "tool_round_limit": "本次回复的工具调用次数已用完（上限 {limit} 次），如需继续请再发一条消息",
          "tool_round_limit_pending": "以下操作本轮未执行，确认后请再发一条消息继续：",
          "tool_round_limit_pending_item": "- {tool}：{intent}",
          "tool_round_limit_pending_tool": "- {tool}",`, false},
	{`"chat.presentation.messages.turn_stopped": "Generation for this turn was stopped as requested",`, `
            "chat.presentation.messages.tool_round_limit": "The tool call limit for this reply has been reached (limit: {limit}).",
            "chat.presentation.messages.tool_round_limit_pending": "The following actions were not run this turn:",
            "chat.presentation.messages.tool_round_limit_pending_item": "- {tool}: {intent}",`, false},
	{`"awaiting_approval": "awaiting_approval",`, `
          "tool_limit": "tool_limit",`, false},
	{`"ui.studio.context.compact_busy": "回复进行中，结束后再压缩",`, `
        "ui.studio.tool_calls.label": "工具调用次数",
        "ui.studio.tool_calls.hint": "本次回复已执行的工具调用；到上限后 Agent 会总结并请你确认。",`, false},
	{`"ui.studio.context.compact_busy": "A reply is running; compact after it ends",`, `
            "ui.studio.tool_calls.label": "Tool calls",
            "ui.studio.tool_calls.hint": "Tool calls in this reply; at the limit the Agent summarizes and asks you to continue.",`, false},
}

func withToolLimitDeclarations(t *testing.T, source []byte) []byte {
	t.Helper()
	for _, declaration := range toolLimitDeclarations {
		if bytes.Count(source, []byte(declaration.anchor)) != 1 {
			t.Fatalf("anchor %q is not unique in the default Catalog", declaration.anchor)
		}
		replacement := declaration.anchor + declaration.insert
		if declaration.before {
			replacement = declaration.insert + declaration.anchor
		}
		source = bytes.Replace(source, []byte(declaration.anchor), []byte(replacement), 1)
	}
	return source
}

// #553: the default Catalog declares no tool call limit, neither for the
// server nor in the public Schema.
func TestDefaultCatalogDeclaresNoToolCallLimit(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{
		"max_tool_calls_per_reply", "AGENT_MAX_TOOL_CALLS_PER_REPLY", "tool_round_limit",
		`"tool_limit"`, "ui.studio.tool_calls.label", "ui.studio.tool_calls.hint",
	} {
		if bytes.Contains(source, []byte(retired)) {
			t.Errorf("default Catalog still declares %s", retired)
		}
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(taskCatalog.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(public, []byte("agent_limits")) || bytes.Contains(public, []byte("max_tool_calls")) {
		t.Fatal("the public Schema still carries a tool call limit")
	}
	messages := taskCatalog.Application().Content.Messages
	english := taskCatalog.Application().Localization.Bundles["en"].Messages
	for _, text := range []string{messages["ui.studio.tool_calls.turn"], english["ui.studio.tool_calls.turn"]} {
		if !strings.Contains(text, "{count}") || strings.Contains(text, "/") {
			t.Fatalf("ui.studio.tool_calls.turn = %q", text)
		}
	}
}

// #553: a custom CATALOG_FILE written before the limit went still loads; the
// retired declarations are dropped, not read. Any other unknown key is still
// refused by the strict decode.
func TestCatalogWithRetiredToolCallLimitStillLoads(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	legacy := withToolLimitDeclarations(t, source)
	loaded, err := catalog.Load(bytes.NewReader(legacy))
	if err != nil {
		t.Fatalf("a Catalog with the retired tool call limit is refused: %v", err)
	}
	application := loaded.Application()
	if _, exists := application.Runtime.Configuration.Settings["agent_max_tool_calls_per_reply"]; exists {
		t.Fatal("the retired runtime setting was kept")
	}
	if _, exists := application.Content.Messages["ui.studio.tool_calls.label"]; exists {
		t.Fatal("the retired counter text was kept")
	}
	if _, exists := application.Localization.Bundles["en"].Messages["ui.studio.tool_calls.hint"]; exists {
		t.Fatal("the retired English counter text was kept")
	}
	if application.Chat.State.TurnEndReasons.Completed != "completed" {
		t.Fatalf("turn end reasons = %#v", application.Chat.State.TurnEndReasons)
	}

	unknown := bytes.Replace(source, []byte(`"max_listed_models": 20,`),
		[]byte(`"max_listed_models": 20, "max_tool_calls_per_turn": 5,`), 1)
	if bytes.Equal(unknown, source) {
		t.Fatal("fixture not found")
	}
	if _, err := catalog.Load(bytes.NewReader(unknown)); err == nil || !strings.Contains(err.Error(), "max_tool_calls_per_turn") {
		t.Fatalf("an unknown key is not refused: %v", err)
	}
}
