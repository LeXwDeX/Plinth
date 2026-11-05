package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #137: the shipped Schema declares which single generation steps may keep
// running in the background, never a Processing Plan or a SkillRun tool.
func TestShippedBackgroundWaitCoversOnlySingleGenerationSteps(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	policy := app.Chat.Agent.BackgroundWait
	if policy == nil || app.Runtime.HTTP.LegacyRoutes.ChatTurnBackground == "" {
		t.Fatal("the shipped Schema declares the background wait and its route")
	}
	for _, name := range []string{"refine_image", "create_model_3d"} {
		if !policy.Allows(name) {
			t.Fatalf("%s is a single generation step and must be eligible", name)
		}
	}
	for _, name := range []string{"plan_model_processing", "create_three_view", "get_task"} {
		if policy.Allows(name) {
			t.Fatalf("%s must never continue in the background", name)
		}
	}
	if (*catalog.ChatBackgroundWaitSchema)(nil).Allows("refine_image") {
		t.Fatal("a missing policy allows nothing")
	}
	public := app.Public()
	if public.Runtime.HTTP.LegacyRoutes.ChatTurnBackground == "" || public.Chat.API.Fields.TurnBackgrounded == "" ||
		public.Chat.API.Fields.TurnBackgroundAvailable == "" || public.Chat.Frontend.BackgroundWaitPollMilliseconds <= 0 {
		t.Fatal("the composer needs the route, its members and the poll interval")
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "\"background_wait\"") || strings.Contains(string(encoded), policy.TaskLineTemplate) {
		t.Fatal("the background wait policy and its Agent texts must stay on the server")
	}
}

func TestBackgroundWaitDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"route without policy", func(a *catalog.ApplicationSchema) { a.Chat.Agent.BackgroundWait = nil }},
		{"no tools", func(a *catalog.ApplicationSchema) { a.Chat.Agent.BackgroundWait.Tools = nil }},
		{"unknown tool", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.BackgroundWait.Tools = append(a.Chat.Agent.BackgroundWait.Tools, "missing_tool")
		}},
		{"skill run tool", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.BackgroundWait.Tools = append(a.Chat.Agent.BackgroundWait.Tools, "create_three_view")
		}},
		{"processing plan tool", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.BackgroundWait.Tools = append(a.Chat.Agent.BackgroundWait.Tools, "plan_model_processing")
		}},
		{"duplicate tool", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.BackgroundWait.Tools = append(a.Chat.Agent.BackgroundWait.Tools, a.Chat.Agent.BackgroundWait.Tools[0])
		}},
		{"threshold", func(a *catalog.ApplicationSchema) { a.Chat.Agent.BackgroundWait.AfterSeconds = 0 }},
		{"listed bound", func(a *catalog.ApplicationSchema) { a.Chat.Agent.BackgroundWait.MaxListedTasks = 0 }},
		{"turn content", func(a *catalog.ApplicationSchema) { a.Chat.Agent.BackgroundWait.TurnContent = " " }},
		{"context placeholder", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.BackgroundWait.ContextTemplate = "tasks are running"
		}},
		{"task line placeholder", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.BackgroundWait.TaskLineTemplate = "{task_id} {capability} {model}"
		}},
		{"response members", func(a *catalog.ApplicationSchema) { a.Chat.API.Fields.TurnBackgrounded = "" }},
		{"distinct members", func(a *catalog.ApplicationSchema) {
			a.Chat.API.Fields.TurnBackgroundAvailable = a.Chat.API.Fields.TurnBackgrounded
		}},
		{"poll interval", func(a *catalog.ApplicationSchema) { a.Chat.Frontend.BackgroundWaitPollMilliseconds = 0 }},
		{"route equals stop", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.LegacyRoutes.ChatTurnBackground = a.Runtime.HTTP.LegacyRoutes.ChatTurnCancel
		}},
		{"route without session", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.LegacyRoutes.ChatTurnBackground = "/api/chat/turn/background"
		}},
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
				t.Fatal("ValidateApplicationSchema accepted an invalid declaration")
			}
		})
	}
	// Without the route the policy alone is valid: the Agent context still
	// lists earlier background Tasks, and nothing offers the choice.
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	app.Runtime.HTTP.LegacyRoutes.ChatTurnBackground = ""
	app.Chat.Frontend.BackgroundWaitPollMilliseconds = 0
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("policy without route rejected: %v", err)
	}
}
