package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #137/#189: the shipped Schema declares which paid-task tools may keep
// running in the background. A generation step and the three-view SkillRun
// submitter qualify; a Processing Plan or a read-only agent tool never does.
func TestShippedBackgroundWaitCoversGenerationAndSkillRunSteps(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	policy := app.Chat.Agent.BackgroundWait
	if policy == nil || app.Runtime.HTTP.LegacyRoutes.ChatTurnBackground == "" {
		t.Fatal("the shipped Schema declares the background wait and its route")
	}
	for _, name := range []string{"refine_image", "create_model_3d", "create_three_view"} {
		if !policy.Allows(name) {
			t.Fatalf("%s is a paid-task step and must be eligible", name)
		}
	}
	for _, name := range []string{"plan_model_processing", "get_task"} {
		if policy.Allows(name) {
			t.Fatalf("%s must never continue in the background", name)
		}
	}
	if (*catalog.ChatBackgroundWaitSchema)(nil).Allows("refine_image") {
		t.Fatal("a missing policy allows nothing")
	}
	// The audit log names the choice with a label, like the other chat actions.
	labeled := false
	for _, action := range app.AuditActions {
		labeled = labeled || action.Value == app.Chat.Audit.TurnBackgroundAction && action.Label != ""
	}
	if app.Chat.Audit.TurnBackgroundAction == "" || !labeled {
		t.Fatalf("audit action %q has no audit_actions label", app.Chat.Audit.TurnBackgroundAction)
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

// An agent tool qualifies only as a paid SkillRun submitter: it must prepare a
// Skill (skill.prepare) and declare the PAID_TASK effect.
func TestBackgroundWaitRequiresPaidSkillRunAgentTools(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(*catalog.ChatAgentToolSchema)
		want   string
	}{
		"not a skill prepare tool": {func(tool *catalog.ChatAgentToolSchema) { tool.Execution.ImplementationKey = "task.read" }, "skill.prepare"},
		"not a paid task":          {func(tool *catalog.ChatAgentToolSchema) { tool.Execution.Effects = []string{"READ"} }, "不是付费任务工具"},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			mutated := false
			for index := range app.Chat.AgentTools {
				if app.Chat.AgentTools[index].Name == "create_three_view" {
					test.mutate(&app.Chat.AgentTools[index])
					mutated = true
				}
			}
			if !mutated {
				t.Fatal("create_three_view is not declared")
			}
			if err := catalog.ValidateApplicationSchema(app); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateApplicationSchema err = %v, want %q", err, test.want)
			}
		})
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
		{"read-only agent tool", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.BackgroundWait.Tools = append(a.Chat.Agent.BackgroundWait.Tools, "get_task")
		}},
		{"processing plan tool", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.BackgroundWait.Tools = append(a.Chat.Agent.BackgroundWait.Tools, "plan_model_processing")
		}},
		{"cross-list duplicate name", func(a *catalog.ApplicationSchema) {
			a.Chat.AgentTools[0].Name = a.Chat.Tools[0].Name
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
		{"audit action", func(a *catalog.ApplicationSchema) { a.Chat.Audit.TurnBackgroundAction = " " }},
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
