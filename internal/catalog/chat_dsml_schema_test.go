package catalog_test

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #275: the Schema declares every accepted DSML spelling — the official
// "<｜DSML｜function_calls>", the doubled marker some relays emit, the "calls"
// alias and whitespace between marker and element name.
func TestDefaultCatalogDeclaresDSMLSpellingVariants(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := taskCatalog.Application().Chat
	for _, marker := range []string{"｜DSML｜", "｜｜DSML｜｜"} {
		if !slices.Contains(chat.DSML.Markers, marker) {
			t.Fatalf("markers = %q, missing %q", chat.DSML.Markers, marker)
		}
	}
	for _, element := range []string{"function_calls", "tool_calls", "calls"} {
		if !slices.Contains(chat.DSML.ToolCallsElements, element) {
			t.Fatalf("tool_calls_elements = %q, missing %q", chat.DSML.ToolCallsElements, element)
		}
	}
	if !chat.DSML.MarkerWhitespace {
		t.Fatal("marker_whitespace must accept the spaced variant")
	}
	messages := chat.Presentation.Messages
	if messages.ToolRoundLimitPending == "" ||
		!strings.Contains(messages.ToolRoundLimitPendingItem, "{intent}") ||
		!strings.Contains(messages.ToolRoundLimitPendingTool, "{tool}") {
		t.Fatalf("pending texts = %#v", messages)
	}
}

func TestDSMLSpellingsAndPendingTextsAreValidated(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	markers := `"markers": [
          "｜DSML｜",
          "｜｜DSML｜｜",
          "|DSML|",
          "||DSML||"
        ]`
	elements := `"tool_calls_elements": [
          "function_calls",
          "tool_calls",
          "calls"
        ]`
	for name, test := range map[string]struct{ old, new, detail string }{
		"no markers":         {markers, `"markers": []`, "dsml.markers"},
		"blank marker":       {markers, `"markers": [""]`, "dsml.markers"},
		"spaced marker":      {markers, `"markers": ["｜DSML｜ "]`, "dsml.markers"},
		"duplicate marker":   {markers, `"markers": ["｜DSML｜", "｜DSML｜"]`, "重复"},
		"no elements":        {elements, `"tool_calls_elements": []`, "dsml.tool_calls_elements"},
		"tag in element":     {elements, `"tool_calls_elements": ["calls>"]`, "dsml.tool_calls_elements"},
		"duplicate element":  {elements, `"tool_calls_elements": ["calls", "calls"]`, "重复"},
		"no pending heading": {`"tool_round_limit_pending": "以下操作本轮未执行，确认后请再发一条消息继续："`, `"tool_round_limit_pending": ""`, "tool_round_limit_pending"},
		"item without intent": {`"tool_round_limit_pending_item": "- {tool}：{intent}"`,
			`"tool_round_limit_pending_item": "- {tool}"`, "tool_round_limit_pending"},
		"tool without name": {`"tool_round_limit_pending_tool": "- {tool}"`,
			`"tool_round_limit_pending_tool": "-"`, "tool_round_limit_pending"},
	} {
		changed := bytes.Replace(source, []byte(test.old), []byte(test.new), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, test.old)
		}
		_, err := catalog.Load(bytes.NewReader(changed))
		if err == nil || !strings.Contains(err.Error(), test.detail) {
			t.Errorf("%s: error = %v, want %q", name, err, test.detail)
		}
	}
}

// #275: the tool budget counts executed calls per Agent reply. The Schema
// declares 20 with a bounded runtime override (AGENT_MAX_TOOL_CALLS_PER_REPLY).
func TestDefaultCatalogDeclaresPerReplyToolCallBudget(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	agent := application.Chat.Agent
	if agent.MaxToolCallsPerReply != 20 || agent.MaxToolCallsPerReplyMaximum < 20 {
		t.Fatalf("budget = %d of %d", agent.MaxToolCallsPerReply, agent.MaxToolCallsPerReplyMaximum)
	}
	setting, exists := application.Runtime.Configuration.Settings[agent.MaxToolCallsPerReplySetting]
	if !exists || len(setting.Environment) != 1 ||
		setting.Environment[0].Name != "AGENT_MAX_TOOL_CALLS_PER_REPLY" || setting.Default != "" {
		t.Fatalf("override setting %q = %#v", agent.MaxToolCallsPerReplySetting, setting)
	}
	if !strings.Contains(application.Chat.Presentation.Messages.ToolRoundLimit, "{limit}") {
		t.Fatalf("tool_round_limit = %q", application.Chat.Presentation.Messages.ToolRoundLimit)
	}
}

func TestPerReplyToolCallBudgetIsValidated(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	budget := `"max_tool_calls_per_reply": 20,`
	setting := `"max_tool_calls_per_reply_setting": "agent_max_tool_calls_per_reply",`
	override := `"name": "AGENT_MAX_TOOL_CALLS_PER_REPLY"
              }
            ],
            "default": ""`
	limitText := `"tool_round_limit": "本次回复的工具调用次数已用完（上限 {limit} 次），如需继续请再发一条消息"`
	for name, test := range map[string]struct{ old, new, detail string }{
		"zero budget":     {budget, `"max_tool_calls_per_reply": 0,`, "max_tool_calls_per_reply"},
		"over maximum":    {budget, `"max_tool_calls_per_reply": 101,`, "max_tool_calls_per_reply"},
		"unknown setting": {setting, `"max_tool_calls_per_reply_setting": "missing_setting",`, "missing_setting"},
		"non-numeric default": {override,
			strings.Replace(override, `"default": ""`, `"default": "many"`, 1), "agent_max_tool_calls_per_reply"},
		"default over maximum": {override,
			strings.Replace(override, `"default": ""`, `"default": "500"`, 1), "agent_max_tool_calls_per_reply"},
		"limit text without count": {limitText, `"tool_round_limit": "工具调用次数已用完"`, "tool_round_limit"},
	} {
		changed := bytes.Replace(source, []byte(test.old), []byte(test.new), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, test.old)
		}
		_, err := catalog.Load(bytes.NewReader(changed))
		if err == nil || !strings.Contains(err.Error(), test.detail) {
			t.Errorf("%s: error = %v, want %q", name, err, test.detail)
		}
	}
}

func TestParseToolCallBudget(t *testing.T) {
	for value, want := range map[string]int{"": 0, " 7 ": 7, "1": 1, "100": 100} {
		if got, err := catalog.ParseToolCallBudget(value, 100); err != nil || got != want {
			t.Fatalf("ParseToolCallBudget(%q) = %d, %v", value, got, err)
		}
	}
	for _, value := range []string{"0", "-1", "101", "2.5", "twenty"} {
		if _, err := catalog.ParseToolCallBudget(value, 100); err == nil {
			t.Fatalf("ParseToolCallBudget(%q) accepted", value)
		}
	}
}
