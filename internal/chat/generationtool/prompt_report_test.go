package generationtool_test

import (
	"context"
	"encoding/json"
	"reflect"
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

// #249: a step that resumes after its grant was prepared must report the same
// final prompt and layer note as the first submission. The first call keeps
// its in-memory trace; the resume rebuilds the same layers from the frozen
// PreparedInput, the call's arguments and the frozen Offering.
func TestResumedAgentOperationReportsTheSamePromptAsTheFirstSubmission(t *testing.T) {
	executor, command, gateway, _ := policyHarness(t, sessionMode{mode: "auto"}, "refine_image", "text-to-image")
	offering := gateway.offerings[0]
	offering.Capability = generation.ImageToImage
	offering.Parameters = []generation.Parameter{
		{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt},
		{Name: "reference_assets", Type: "asset-array", Semantic: generation.SemanticReferenceImage},
		{Name: "negative_prompt", Type: "string", Semantic: generation.SemanticNegativePrompt},
	}
	gateway.offerings[0] = offering
	command.Call.Arguments = []byte(`{"prompt":"draw a cube"}`)
	command.Tool.DefaultParameters = map[string]json.RawMessage{"negative_prompt": json.RawMessage(`"NEG"`)}
	command.History = []chat.Message{{Role: "user", ImageURLs: []string{"https://example.test/one.png"}}}
	command.Options.StyleLock = chat.StyleLock{Enabled: true, Text: "LOCK"}

	first, err := executor.Execute(context.Background(), command, nil)
	if err != nil || first.TaskID == "" {
		t.Fatalf("first submission: %+v %v", first, err)
	}
	// The stored operation and its approved grant make the second call take
	// the resume path; it must not prepare a new input.
	second, err := executor.Execute(context.Background(), command, nil)
	if err != nil || second.TaskID == "" {
		t.Fatalf("resumed submission: %+v %v", second, err)
	}
	if gateway.preparedCount != 1 || gateway.submitCount != 2 {
		t.Fatalf("resume prepared=%d submitted=%d", gateway.preparedCount, gateway.submitCount)
	}
	var firstContent, secondContent map[string]any
	if err := json.Unmarshal([]byte(first.AgentContent), &firstContent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(second.AgentContent), &secondContent); err != nil {
		t.Fatal(err)
	}
	if firstContent["final_prompt"] == nil || firstContent["prompt_note"] == nil {
		t.Fatalf("first result has no prompt report: %#v", firstContent)
	}
	if !reflect.DeepEqual(firstContent["final_prompt"], secondContent["final_prompt"]) {
		t.Fatalf("final_prompt changed on resume: %q != %q",
			firstContent["final_prompt"], secondContent["final_prompt"])
	}
	if !reflect.DeepEqual(firstContent["prompt_note"], secondContent["prompt_note"]) {
		t.Fatalf("prompt_note changed on resume:\n%#v\n%#v",
			firstContent["prompt_note"], secondContent["prompt_note"])
	}
	rendered, _ := json.Marshal(firstContent["prompt_note"])
	for _, layer := range []string{"reference-image template", "style lock", "default negative prompt"} {
		if !strings.Contains(string(rendered), layer) {
			t.Fatalf("prompt_note lacks %q: %s", layer, rendered)
		}
	}
	if summary, _ := firstContent["prompt_note"].(map[string]any)["summary"].(string); summary == "" {
		t.Fatalf("prompt_note has no changed summary: %s", rendered)
	}
}
