package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #321: the processing tools that pick a service by capability ask the person
// on an in-chat choice card when several compatible Offerings exist.
func TestDefaultSchemaDeclaresOfferingChoiceForServiceTools(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	for _, name := range []string{"retexture_model", "process_model", "plan_model_processing"} {
		tool := processingTool(t, &app, name)
		choice := tool.Processing.Choice
		if choice == nil {
			t.Fatalf("%s declares no offering choice", name)
		}
		var parameters struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			t.Fatal(err)
		}
		if parameters.Properties[choice.DefaultArgument].Type != "boolean" {
			t.Errorf("%s default argument %q must be a declared boolean", name, choice.DefaultArgument)
		}
		if tool.Processing.Plan == nil && parameters.Properties[choice.OfferingArgument].Type != "string" {
			t.Errorf("%s offering argument %q must be a declared string", name, choice.OfferingArgument)
		}
		// #321: the refusal of an unknown service never echoes the ID back
		// into text the Agent repeats.
		if strings.Contains(choice.UnknownOffering, "{offering}") || strings.TrimSpace(choice.UnknownOffering) == "" ||
			strings.TrimSpace(choice.Content) == "" {
			t.Errorf("%s choice texts are incomplete: %+v", name, choice)
		}
		if tool.Processing.Messages.Service == nil {
			t.Errorf("%s declares no texts for a service the person named (#321 G)", name)
		}
	}
	// Format conversion routes by the requested format (the format coverage order), not by provider.
	if processingTool(t, &app, "convert_model_format").Processing.Choice != nil {
		t.Error("convert_model_format must keep its format-ordered route")
	}
	if !strings.Contains(app.Chat.Choice.AnswerTemplate, "{continuation}") || !strings.Contains(app.Chat.Choice.PendingTemplate, "{continuation}") {
		t.Errorf("chat.choice templates must carry the continuation: %+v", app.Chat.Choice)
	}
	if app.Runtime.HTTP.Routes.ChatSessionChoices == "" || app.Chat.API.Fields.ChoiceAnswer == "" {
		t.Error("the choice route and request field must be declared")
	}
	public := app.Public()
	encoded, _ := json.Marshal(public)
	if strings.Contains(string(encoded), app.Chat.Choice.PendingTemplate) {
		t.Error("the Agent-facing choice texts must stay server side")
	}
	for _, key := range []string{"ui.chat.choice.offering_question", "ui.chat.choice.confirm", "ui.chat.choice.use_default",
		"ui.chat.choice.answered", "ui.chat.choice.answer_text"} {
		if app.Content.Messages[key] == "" || app.Localization.Bundles["en"].Messages[key] == "" {
			t.Errorf("choice card text %s needs zh and en copies", key)
		}
	}
}

// #321 D: every paid processing tool asks before running on another model
// than the selected one.
func TestDefaultSchemaDeclaresSourceConfirmation(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	for _, name := range []string{"process_model", "retexture_model", "plan_model_processing", "convert_model_format"} {
		if processingTool(t, &app, name).Processing.Messages.SourceConfirmation == "" {
			t.Errorf("%s does not ask before switching models", name)
		}
	}
}

func TestOfferingChoiceValidationRejectsInconsistentDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"default argument must be declared", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "process_model").Processing.Choice.DefaultArgument = "missing"
		}},
		{"single-step tool needs an offering argument", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "retexture_model").Processing.Choice.OfferingArgument = ""
		}},
		{"plan tool carries offering_id per step", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "plan_model_processing").Processing.Choice.OfferingArgument = "source_task_id"
		}},
		{"choice arguments cannot reuse another argument", func(a *catalog.ApplicationSchema) {
			choice := processingTool(t, a, "process_model").Processing.Choice
			choice.OfferingArgument = "source_task_id"
		}},
		{"choice texts are required", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "retexture_model").Processing.Choice.Content = " "
		}},
		{"format routing cannot ask", func(a *catalog.ApplicationSchema) {
			tool := processingTool(t, a, "convert_model_format")
			tool.Processing.Choice = &catalog.ChatToolOfferingChoiceSchema{OfferingArgument: "format", DefaultArgument: "texture_format", Content: "x", UnknownOffering: "x"}
		}},
		{"answer template carries the continuation", func(a *catalog.ApplicationSchema) { a.Chat.Choice.AnswerTemplate = "answered" }},
		{"pending template carries the continuation", func(a *catalog.ApplicationSchema) { a.Chat.Choice.PendingTemplate = "" }},
		{"a card needs two options", func(a *catalog.ApplicationSchema) { a.Chat.Choice.MaxOptions = 1 }},
		{"listing bound is required", func(a *catalog.ApplicationSchema) { a.Chat.Choice.ListLimit = 0 }},
		{"source confirmation names both models", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "process_model").Processing.Messages.SourceConfirmation = "ask about {source}{reason}"
		}},
		{"source confirmation carries the reason", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "retexture_model").Processing.Messages.SelectedSourceIncompatible = ""
		}},
		{"the unknown-service text never echoes the ID", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "process_model").Processing.Choice.UnknownOffering = "The offering {offering} is unknown"
		}},
		{"a tool that asks about services declares the named-service texts", func(a *catalog.ApplicationSchema) {
			processingTool(t, a, "retexture_model").Processing.Messages.Service = nil
		}},
		{"the named-service refusal carries its reasons", func(a *catalog.ApplicationSchema) {
			service := *processingTool(t, a, "process_model").Processing.Messages.Service
			service.Refused = "{requested} cannot run {options}"
			processingTool(t, a, "process_model").Processing.Messages.Service = &service
		}},
		{"the size reason carries the size and the limit", func(a *catalog.ApplicationSchema) {
			service := *processingTool(t, a, "plan_model_processing").Processing.Messages.Service
			service.TooLarge = "{service} is too large"
			processingTool(t, a, "plan_model_processing").Processing.Messages.Service = &service
		}},
		{"mock summary is required", func(a *catalog.ApplicationSchema) { a.Chat.Choice.MockSummary = "" }},
		{"request field is required", func(a *catalog.ApplicationSchema) { a.Chat.API.Fields.ChoiceAnswer = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			tt.mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("ValidateApplicationSchema accepted an inconsistent choice declaration")
			}
		})
	}
}
