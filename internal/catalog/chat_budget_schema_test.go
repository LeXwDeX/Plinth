package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The shipped Schema declares the budget mode, its plan tool and the session
// projection the client reads the plan from (#103).
func TestDefaultCatalogDeclaresTheBudgetModeAndItsPlanTool(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	policy := app.Chat.ApprovalPolicy
	if policy.DefaultMode != "budget" {
		t.Fatalf("default approval mode = %q", policy.DefaultMode)
	}
	offer := map[string]bool{}
	for _, mode := range policy.Modes {
		offer[mode] = true
	}
	if !offer["budget"] || !offer["manual"] || !offer["auto"] {
		t.Fatalf("approval modes = %v", policy.Modes)
	}
	budget := policy.Budget
	if budget.MaxCredits <= 0 || budget.MaxSteps <= 0 || budget.MaxTitleCharacters <= 0 || budget.MaxPurposeCharacters <= 0 {
		t.Fatalf("budget bounds = %+v", budget)
	}
	if len(budget.States) != 5 || budget.Messages.Approved == "" || budget.Messages.Denied == "" ||
		budget.Messages.Expired == "" || budget.Messages.Exhausted == "" {
		t.Fatalf("budget states or messages = %+v", budget)
	}
	if app.Chat.API.ResponseFields.Budget != "budget" || app.Runtime.HTTP.Routes.ChatSessionBudget == "" {
		t.Fatalf("session projection = field %q route %q",
			app.Chat.API.ResponseFields.Budget, app.Runtime.HTTP.Routes.ChatSessionBudget)
	}
	var planTool *catalog.ChatAgentToolSchema
	for index := range app.Chat.AgentTools {
		if app.Chat.AgentTools[index].Name == "propose_production_plan" {
			planTool = &app.Chat.AgentTools[index]
		}
	}
	if planTool == nil || planTool.Execution.ImplementationKey != "plan.propose" ||
		planTool.Execution.Scope != "session" || planTool.Execution.AgentScope != "utility" ||
		!slicesContain(planTool.Execution.Effects, "SESSION_WRITE") {
		t.Fatalf("plan tool = %+v", planTool)
	}
	var parameters struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(planTool.Parameters, &parameters); err != nil {
		t.Fatalf("plan tool parameters: %v", err)
	}
	for _, name := range []string{"title", "steps", "budget_credits"} {
		if parameters.Properties[name] == nil {
			t.Fatalf("plan tool parameter %q is not declared", name)
		}
	}
	for _, name := range []string{"title", "steps", "budget_credits"} {
		if !slicesContain(parameters.Required, name) {
			t.Fatalf("plan tool parameter %q is not required", name)
		}
	}

	public := app.Public()
	if public.Chat.API.ResponseFields.Budget != "budget" ||
		public.Runtime.HTTP.Routes.ChatSessionBudget == "" ||
		public.Chat.ApprovalPolicy.DefaultMode != "budget" ||
		!slicesContain(public.Chat.ApprovalPolicy.Modes, "budget") {
		t.Fatalf("public budget schema = %+v / %+v", public.Chat.API.ResponseFields, public.Runtime.HTTP.Routes)
	}
	// #120: the browser acts on each state's declared role, never on its ID.
	roles := map[string]string{}
	for _, state := range public.Chat.ApprovalPolicy.BudgetStates {
		roles[state.ID] = state.Role
	}
	want := map[string]string{
		"pending": catalog.ChatApprovalBudgetRolePending, "active": catalog.ChatApprovalBudgetRoleActive,
		"exhausted": catalog.ChatApprovalBudgetRoleEnded, "closed": catalog.ChatApprovalBudgetRoleEnded,
		"denied": catalog.ChatApprovalBudgetRoleDenied,
	}
	if len(roles) != len(want) {
		t.Fatalf("public budget states = %+v", public.Chat.ApprovalPolicy.BudgetStates)
	}
	for id, role := range want {
		if roles[id] != role {
			t.Fatalf("public budget state %s role = %q, want %q", id, roles[id], role)
		}
	}
}

// A Catalog that offers budget mode must carry a usable plan budget; a
// historical Catalog that does not offer it stays readable.
func TestBudgetModeRequiresItsPlanBoundsAndTexts(t *testing.T) {
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"no maximum credits": func(a *catalog.ApplicationSchema) { a.Chat.ApprovalPolicy.Budget.MaxCredits = 0 },
		"no maximum steps":   func(a *catalog.ApplicationSchema) { a.Chat.ApprovalPolicy.Budget.MaxSteps = 0 },
		"no title bound":     func(a *catalog.ApplicationSchema) { a.Chat.ApprovalPolicy.Budget.MaxTitleCharacters = 0 },
		"no purpose bound":   func(a *catalog.ApplicationSchema) { a.Chat.ApprovalPolicy.Budget.MaxPurposeCharacters = 0 },
		"empty context":      func(a *catalog.ApplicationSchema) { a.Chat.ApprovalPolicy.Budget.ContextTemplate = "" },
		"context without remaining": func(a *catalog.ApplicationSchema) {
			a.Chat.ApprovalPolicy.Budget.ContextTemplate = "{state} {limit} {spent} {reserved} {plan}"
		},
		"missing state": func(a *catalog.ApplicationSchema) {
			a.Chat.ApprovalPolicy.Budget.States = a.Chat.ApprovalPolicy.Budget.States[:4]
		},
		"unknown state": func(a *catalog.ApplicationSchema) {
			a.Chat.ApprovalPolicy.Budget.States[0].ID = "paused"
		},
		"state without a role": func(a *catalog.ApplicationSchema) {
			a.Chat.ApprovalPolicy.Budget.States[0].Role = ""
		},
		"role contradicting the state": func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.ApprovalPolicy.Budget.States {
				if a.Chat.ApprovalPolicy.Budget.States[index].ID == "active" {
					a.Chat.ApprovalPolicy.Budget.States[index].Role = catalog.ChatApprovalBudgetRoleDenied
				}
			}
		},
		"empty approved message": func(a *catalog.ApplicationSchema) {
			a.Chat.ApprovalPolicy.Budget.Messages.Approved = ""
		},
		"approved message without remaining": func(a *catalog.ApplicationSchema) {
			a.Chat.ApprovalPolicy.Budget.Messages.Approved = "{title} {limit}"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("ValidateApplicationSchema accepted an unusable budget declaration")
			} else if !strings.Contains(err.Error(), "budget") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	t.Run("historical catalog without budget mode", func(t *testing.T) {
		c, err := catalog.LoadDefault()
		if err != nil {
			t.Fatal(err)
		}
		app := c.Application()
		app.Chat.ApprovalPolicy.Modes = []string{"auto", "manual"}
		app.Chat.ApprovalPolicy.DefaultMode = "manual"
		for index := range app.Chat.ApprovalPolicy.Prompt.Modes {
			if app.Chat.ApprovalPolicy.Prompt.Modes[index].ID == "budget" {
				app.Chat.ApprovalPolicy.Prompt.Modes = append(
					app.Chat.ApprovalPolicy.Prompt.Modes[:index],
					app.Chat.ApprovalPolicy.Prompt.Modes[index+1:]...)
				break
			}
		}
		app.Chat.ApprovalPolicy.Budget = catalog.ChatApprovalBudgetSchema{}
		if err := catalog.ValidateApplicationSchema(app); err != nil {
			t.Fatalf("historical catalog lost readability: %v", err)
		}
	})
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
