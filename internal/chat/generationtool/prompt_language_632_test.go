package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// localizedPromptExecutor is promptReportFixture with the default Catalog's
// text localizer, so the turn's locale picks the copy.
func localizedPromptExecutor(t *testing.T, maximumLength int, skills catalog.SkillCatalogSchema) (chat.ToolCommand, *generationtool.Executor) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "refine_image" {
			tool = candidate
		}
	}
	offering := generation.Offering{
		ID: "configured/offering", ModelID: "configured-model", ModelName: "Configured Model",
		Capability: tool.FallbackCapability, Enabled: true,
		Features: []string{application.Chat.Skills.Feature}, Timeout: time.Second,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true, MaxLength: maximumLength},
			{Name: "negative_prompt", Type: "string", Semantic: generation.SemanticNegativePrompt},
		},
	}
	tool.DefaultParameters = map[string]json.RawMessage{"negative_prompt": json.RawMessage(`"NEG"`)}
	gateway := &fakeGateway{
		offerings: []generation.Offering{offering},
		tasks:     []generation.Task{{ID: "configured-task", State: generation.TaskSucceeded, Progress: 100, Offering: offering}},
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat, Texts: texts,
		TaskStates: application.TaskStates, Skills: skills,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(map[string]any{"prompt": "原始提示词"})
	return chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: arguments},
		Tool: tool, Options: chat.TurnOptions{AIModel: "configured-model", CreativeDomain: "image"},
	}, executor
}

// #632: the prompt report a generation tool hands back is the server's own
// explanation, so in a Chinese turn its summary, layer names and length
// refusal carry no English sentence. final_prompt and each layer's text are
// what the image model received (English templates included) and stay as
// they were: the summary marks them as a record not to copy into the reply.
// An English turn still reads the en bundle.
func TestPromptReportSpeaksTheTurnLocale(t *testing.T) {
	skills := catalog.SkillCatalogSchema{Revision: "configured",
		Skills: []catalog.SkillSchema{{ID: "configured-skill", Prompt: "SKILL STYLE"}}}
	run := func(locale string) (chat.ToolResult, chat.ToolResult) {
		command, executor := localizedPromptExecutor(t, 0, skills)
		command.Options.Locale = locale
		command.Options.SkillID = "configured-skill"
		changed, err := executor.Execute(context.Background(), command, nil)
		if err != nil {
			t.Fatal(err)
		}
		command, executor = localizedPromptExecutor(t, 4, catalog.SkillCatalogSchema{})
		command.Options.Locale = locale
		refused, err := executor.Execute(context.Background(), command, nil)
		if err != nil {
			t.Fatal(err)
		}
		return changed, refused
	}
	explanation := func(t *testing.T, result chat.ToolResult) (string, []string) {
		t.Helper()
		var content map[string]any
		if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
			t.Fatalf("tool result is not JSON: %v\n%s", err, result.AgentContent)
		}
		note, _ := content["prompt_note"].(map[string]any)
		summary, _ := note["summary"].(string)
		var labels []string
		layers, _ := note["layers"].([]any)
		for _, layer := range layers {
			entry, _ := layer.(map[string]any)
			label, _ := entry["layer"].(string)
			labels = append(labels, label)
		}
		if summary == "" || len(labels) == 0 {
			t.Fatalf("no prompt report: %s", result.AgentContent)
		}
		return summary, labels
	}

	changed, refused := run("zh-CN")
	summary, labels := explanation(t, changed)
	for _, text := range append([]string{summary, refused.Error}, labels...) {
		if match := englishSentenceIn(text); match != "" {
			t.Errorf("zh-CN prompt report carries the English sentence %q in %q", match, text)
		}
	}
	if !strings.Contains(summary, "不要照抄") || !strings.Contains(refused.Error, "个字符") {
		t.Errorf("zh-CN report = %q / %q", summary, refused.Error)
	}

	changed, refused = run("en")
	summary, labels = explanation(t, changed)
	if englishSentenceIn(summary) == "" || englishSentenceIn(refused.Error) == "" ||
		!strings.Contains(refused.Error, "characters") || !strings.Contains(strings.Join(labels, "|"), "Skill prompt template") {
		t.Errorf("en report lost its English copy: %q / %q / %v", summary, refused.Error, labels)
	}
}
