package catalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// loadAdjustedDefault loads the built-in Catalog after adjust changed its
// generic document.
func loadAdjustedDefault(t *testing.T, adjust func(chat map[string]any)) (*Catalog, error) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(DefaultDocument(), &document); err != nil {
		t.Fatal(err)
	}
	adjust(document["application"].(map[string]any)["chat"].(map[string]any))
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return Load(bytes.NewReader(encoded))
}

func namedTool(t *testing.T, chat map[string]any, section, name string) map[string]any {
	t.Helper()
	for _, entry := range chat[section].([]any) {
		if tool := entry.(map[string]any); tool["name"] == name {
			return tool
		}
	}
	t.Fatalf("%s %s is not declared", section, name)
	return nil
}

// #665 (wave 2): a Catalog written before #665 still loads. The three-view
// recovery texts it lacks borrow the nearest declared texts, create_three_view
// may lack include_right, and a generation tool's keyword outside the
// toolschema subset (never enforced there) does not stop the load.
func TestCatalogWithoutThe665KeysStillLoads(t *testing.T) {
	added := []string{"turn_stopped", "model_unavailable", "image_unavailable", "arguments_invalid", "denied", "duplicate_run"}
	loaded, err := loadAdjustedDefault(t, func(chat map[string]any) {
		messages := chat["three_view"].(map[string]any)["messages"].(map[string]any)
		for _, key := range added {
			delete(messages, key)
		}
		threeView := namedTool(t, chat, "agent_tools", "create_three_view")
		delete(threeView["parameters"].(map[string]any)["properties"].(map[string]any), "include_right")
		for _, entry := range chat["tools"].([]any) {
			properties, _ := entry.(map[string]any)["parameters"].(map[string]any)["properties"].(map[string]any)
			for _, property := range properties {
				if declared := property.(map[string]any); declared["type"] == "string" {
					declared["format"] = "uri"
					return
				}
			}
		}
		t.Fatal("no generation tool declares a string parameter")
	})
	if err != nil {
		t.Fatalf("a pre-#665 Catalog does not load: %v", err)
	}
	chat := loaded.Application().Chat
	messages, presentation := chat.ThreeView.Messages, chat.Presentation.Messages
	builtIn := builtInThreeViewMessages()
	for name, pair := range map[string][2]string{
		// The same meaning, declared by the Catalog itself.
		"turn_stopped":      {messages.TurnStopped, presentation.TurnStopped},
		"arguments_invalid": {messages.ArgumentsInvalid, presentation.ToolArgumentsInvalid},
		"image_unavailable": {messages.ImageUnavailable, messages.ReferenceRequired},
		// No text of another meaning is borrowed: the release's own.
		"model_unavailable": {messages.ModelUnavailable, builtIn.ModelUnavailable},
		"denied":            {messages.Denied, builtIn.Denied},
		"duplicate_run":     {messages.DuplicateRun, builtIn.DuplicateRun},
	} {
		if pair[0] == "" || pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
	}
	if messages.DuplicateRun == messages.AdmissionFull || messages.Denied == chat.PendingApprovals.Messages.Denied ||
		messages.ModelUnavailable == presentation.MultiviewModelUnavailable || !strings.Contains(messages.DuplicateRun, "{run}") {
		t.Errorf("a text of another meaning was borrowed: %+v", messages)
	}
	// The shipped Catalog keeps its own texts.
	shipped, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if own := shipped.Application().Chat.ThreeView.Messages; own.DuplicateRun == own.AdmissionFull || own.Denied == "" {
		t.Errorf("shipped texts were replaced: %+v", own)
	}
}

// #665 (wave 2): an Agent tool keyword outside the subset is refused with the
// tool, the parameter and the keyword named; include_right, when declared,
// stays a boolean.
func TestAgentToolKeywordOutsideTheSubsetIsNamed(t *testing.T) {
	_, err := loadAdjustedDefault(t, func(chat map[string]any) {
		tool := namedTool(t, chat, "agent_tools", "create_three_view")
		purpose := tool["parameters"].(map[string]any)["properties"].(map[string]any)["purpose"].(map[string]any)
		purpose["pattern"] = "^[a-z_]+$"
	})
	if err == nil || !strings.Contains(err.Error(), `"create_three_view"`) ||
		!strings.Contains(err.Error(), "purpose") || !strings.Contains(err.Error(), `"pattern"`) {
		t.Fatalf("err = %v", err)
	}
	_, err = loadAdjustedDefault(t, func(chat map[string]any) {
		tool := namedTool(t, chat, "agent_tools", "create_three_view")
		tool["parameters"].(map[string]any)["properties"].(map[string]any)["include_right"] =
			map[string]any{"type": "string"}
	})
	if err == nil || !strings.Contains(err.Error(), "include_right") {
		t.Fatalf("string include_right: err = %v", err)
	}
}

// #665 (wave 2): the processing service's Agent retry bound comes from the
// plan tool's declaration.
func TestProcessingAgentStepRetriesReadsThePlanTool(t *testing.T) {
	shipped, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if got := ProcessingAgentStepRetries(shipped.Application().Chat.Tools); got != 2 {
		t.Fatalf("declared retries = %d, want the shipped 2", got)
	}
	if got := ProcessingAgentStepRetries(nil); got != 0 {
		t.Fatalf("no plan tool = %d, want 0", got)
	}
}

// #665 (wave 2): wording the review found inaccurate.
func TestWave2AgentWording(t *testing.T) {
	shipped, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := shipped.Application().Chat
	messages := chat.ThreeView.Messages
	// A denied grant may be a refusal, a stopped turn or an expired card.
	if strings.Contains(messages.Denied, "点了「不批准」") || !strings.Contains(messages.Denied, "过期") ||
		!strings.Contains(messages.Denied, "停止") {
		t.Errorf("denied = %q", messages.Denied)
	}
	if !strings.Contains(messages.DuplicateRun, "还在进行中，或在卡片上等待") || !strings.Contains(messages.DuplicateRun, "{run}") {
		t.Errorf("duplicate_run = %q", messages.DuplicateRun)
	}
	if truncated := chat.Presentation.Messages.AgentReplyTruncated; !strings.Contains(truncated, "如果截断时正在写工具调用") {
		t.Errorf("agent_reply_truncated = %q", truncated)
	}
	for _, prompt := range chat.Agent.CraftPrompts {
		if strings.Contains(prompt.Text, "不提供它时用已有三视图组") {
			t.Errorf("craft prompt %s keeps the unclear parenthetical", prompt.ID)
		}
	}
	for _, skill := range chat.Skills.SystemSkills {
		if strings.Contains(skill.Prompt, "几个视角放在同一轮里一起调用") {
			t.Errorf("skill %s still reads as one create_three_view call per view", skill.ID)
		}
		if skill.Tool == "create_three_view" && !strings.Contains(skill.Prompt, "create_three_view 一次调用就建好整组视角") {
			t.Errorf("skill %s does not say one call makes the group", skill.ID)
		}
	}
	// Tool descriptions name the approval modes as the top bar does.
	for _, description := range append(toolDescriptions(chat.Tools), agentToolDescriptions(chat.AgentTools)...) {
		for _, stale := range []string{"manual approval mode", "manual rules", "auto and manual", "in budget mode", "In budget mode", "strict mode", "auto mode"} {
			if strings.Contains(description, stale) {
				t.Errorf("tool description keeps %q", stale)
			}
		}
	}
}

func toolDescriptions(tools []ChatToolSchema) []string {
	var out []string
	for _, tool := range tools {
		out = append(out, tool.Description)
	}
	return out
}

func agentToolDescriptions(tools []ChatAgentToolSchema) []string {
	var out []string
	for _, tool := range tools {
		out = append(out, tool.Description)
	}
	return out
}

// #665 (wave 3): the built-in Catalog document itself declares every
// three-view text an older Catalog may lack, since a missing one is filled
// from it; the fill must never hide a missing declaration in the release.
func TestDefaultCatalogDeclaresThe665ThreeViewTexts(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(DefaultDocument(), &document); err != nil {
		t.Fatal(err)
	}
	messages := document["application"].(map[string]any)["chat"].(map[string]any)["three_view"].(map[string]any)["messages"].(map[string]any)
	for _, key := range []string{"turn_stopped", "model_unavailable", "image_unavailable", "arguments_invalid", "denied", "duplicate_run",
		// #689: the checkpoint pause of a continuation and the refusal after it.
		"paused_run", "checkpoint_paused", "checkpoint_note"} {
		if text, _ := messages[key].(string); strings.TrimSpace(text) == "" {
			t.Errorf("default_catalog.json three_view.messages.%s is not declared", key)
		}
	}
	for key, fields := range map[string][]string{"paused_run": {"{run}"},
		"checkpoint_paused": {"{run}", "{view}"}, "checkpoint_note": {"{count}"}} {
		for _, field := range fields {
			if text, _ := messages[key].(string); !strings.Contains(text, field) {
				t.Errorf("three_view.messages.%s lacks %s", key, field)
			}
		}
	}
}
