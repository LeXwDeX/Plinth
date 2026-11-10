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

func promptReportFixture(t *testing.T, maximumLength int, skills catalog.SkillCatalogSchema, tweak func(*catalog.ChatSchema, *catalog.ChatToolSchema, *generation.Offering)) (chat.ToolCommand, *generationtool.Executor, *fakeGateway) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	app := application.Chat
	var tool catalog.ChatToolSchema
	for _, candidate := range app.Tools {
		if candidate.Name == "refine_image" {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatal("refine_image is not declared")
	}
	offering := generation.Offering{
		ID: "configured/offering", ModelID: "configured-model", ModelName: "Configured Model",
		Capability: tool.FallbackCapability, Enabled: true,
		Features: []string{app.Skills.Feature}, Timeout: time.Second,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true, MaxLength: maximumLength},
			{Name: "negative_prompt", Type: "string", Semantic: generation.SemanticNegativePrompt},
		},
	}
	tool.DefaultParameters = map[string]json.RawMessage{"negative_prompt": json.RawMessage(`"NEG"`)}
	if tweak != nil {
		tweak(&app, &tool, &offering)
	}
	gateway := &fakeGateway{
		offerings: []generation.Offering{offering},
		tasks: []generation.Task{{
			ID: "configured-task", State: generation.TaskSucceeded, Progress: 100, Offering: offering,
		}},
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: app, TaskStates: application.TaskStates, Skills: skills,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments, _ := json.Marshal(map[string]any{"prompt": "original prompt text"})
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: arguments},
		Tool: tool, Options: chat.TurnOptions{AIModel: "configured-model", CreativeDomain: "image"},
	}
	return command, executor, gateway
}

func TestPromptOverLimitRefusesBeforeSubmission(t *testing.T) {
	command, executor, gateway := promptReportFixture(t, 10, catalog.SkillCatalogSchema{}, func(app *catalog.ChatSchema, _ *catalog.ChatToolSchema, _ *generation.Offering) {
		app.Agent.PromptReport.OverLimit = "refuse"
	})
	command.Call.Arguments = []byte(`{"prompt":"this prompt is far too long"}`)
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || result.TaskID != "" || gateway.submitted.OfferingID != "" {
		t.Fatalf("over-limit prompt was submitted: %#v / %#v", result, gateway.submitted)
	}
	if !strings.Contains(result.Error, "over the limit of 10 characters") || !strings.Contains(result.Error, "by 17 characters") {
		t.Fatalf("refusal = %q", result.Error)
	}
}

func TestPromptOverLimitTruncatesForHistoricalCatalog(t *testing.T) {
	command, executor, gateway := promptReportFixture(t, 10, catalog.SkillCatalogSchema{}, func(app *catalog.ChatSchema, _ *catalog.ChatToolSchema, _ *generation.Offering) {
		app.Agent.PromptReport.OverLimit = "truncate"
	})
	command.Call.Arguments = []byte(`{"prompt":"this prompt is far too long"}`)
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatal(err)
	}
	var submitted map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted["prompt"] != "this promp" {
		t.Fatalf("submitted prompt = %q", submitted["prompt"])
	}
}

func TestPromptReportCarriesActualPromptAndAppliedLayers(t *testing.T) {
	skills := catalog.SkillCatalogSchema{
		Revision: "configured",
		Skills: []catalog.SkillSchema{{
			ID: "configured-skill", Prompt: "SKILL STYLE",
		}},
	}
	command, executor, gateway := promptReportFixture(t, 0, skills, func(app *catalog.ChatSchema, _ *catalog.ChatToolSchema, _ *generation.Offering) {
		fields := app.Skills.Execution.PromptTemplateFields
		app.Skills.Execution.PromptTemplates[generation.ImageToImage] =
			fields.UserInput + " || " + fields.SkillPrompt
	})
	command.Options.SkillID = "configured-skill"
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	var submitted map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &submitted); err != nil {
		t.Fatal(err)
	}
	finalPrompt, _ := submitted["prompt"].(string)
	if finalPrompt == "" || !strings.Contains(finalPrompt, "SKILL STYLE") {
		t.Fatalf("submitted prompt = %q", finalPrompt)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	if content["final_prompt"] != finalPrompt {
		t.Fatalf("final_prompt = %#v, submitted = %q", content["final_prompt"], finalPrompt)
	}
	note, _ := content["prompt_note"].(map[string]any)
	layers, _ := note["layers"].([]any)
	if len(layers) < 2 {
		t.Fatalf("prompt_note layers = %#v", note)
	}
	rendered, _ := json.Marshal(layers)
	if !strings.Contains(string(rendered), "Skill prompt template") || !strings.Contains(string(rendered), "default negative prompt") {
		t.Fatalf("prompt_note layers = %s", rendered)
	}
	if !strings.Contains(string(rendered), "{skill_prompt}") {
		t.Fatalf("the Skill layer does not name the applied template: %s", rendered)
	}
	if note["final_length"] != float64(len([]rune(finalPrompt))) {
		t.Fatalf("final_length = %#v", note["final_length"])
	}
}

func TestPromptReportOmitsAnOverlongFinalPrompt(t *testing.T) {
	skills := catalog.SkillCatalogSchema{Revision: "configured", Skills: []catalog.SkillSchema{{
		ID: "configured-skill", Prompt: "a long skill prompt",
	}}}
	command, executor, _ := promptReportFixture(t, 0, skills, func(app *catalog.ChatSchema, _ *catalog.ChatToolSchema, _ *generation.Offering) {
		fields := app.Skills.Execution.PromptTemplateFields
		app.Skills.Execution.PromptTemplates[generation.ImageToImage] =
			fields.UserInput + " || " + fields.SkillPrompt
		app.Agent.PromptReport.MaximumReportedCharacters = 5
	})
	command.Options.SkillID = "configured-skill"
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	if _, present := content["final_prompt"]; present {
		t.Fatalf("an overlong final prompt was reported: %#v", content["final_prompt"])
	}
	note, _ := content["prompt_note"].(map[string]any)
	if note["final_prompt_omitted"] != true || note["final_length"] == nil {
		t.Fatalf("prompt_note = %#v", note)
	}
}
