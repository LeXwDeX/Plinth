package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #330: the default Catalog declares the 3D step mode with the product's rules:
// three modes, both entries defaulting to the Agent, only 3D capabilities counted.
func TestDefaultStepPolicyLoadsAndProjectsSafeMembers(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	policy := application.StepPolicy
	if policy == nil {
		t.Fatal("default Catalog has no application.step_policy")
	}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("default step policy invalid: %v", err)
	}
	modes := make([]string, 0, len(policy.Modes))
	for _, mode := range policy.Modes {
		modes = append(modes, mode.Value)
	}
	if !slices.Equal(modes, []string{approvalpolicy.StepModeAgent, approvalpolicy.StepModeUnlimited, approvalpolicy.StepModeCap}) {
		t.Fatalf("modes = %v; the ids must match the approval vocabulary", modes)
	}
	if policy.DefaultMode.List != approvalpolicy.StepModeAgent || policy.DefaultMode.MCP != approvalpolicy.StepModeAgent {
		t.Fatalf("default modes = %+v; both entries default to the Agent", policy.DefaultMode)
	}
	if policy.Cap.Default != 20 || policy.Cap.Minimum != 1 || policy.Cap.Maximum != 200 {
		t.Fatalf("cap = %+v", policy.Cap)
	}
	for _, twoDimensional := range []string{"text-to-image", "image-to-image", "image-to-multiview", "edit-multiview", "background-removal"} {
		for _, counted := range policy.CountedCapabilities {
			if string(counted) == twoDimensional {
				t.Fatalf("2D capability %q is counted", twoDimensional)
			}
		}
	}
	if policy.Audit.UpdateAction == "" || !strings.Contains(policy.Messages.CapReached, "{used}") {
		t.Fatalf("audit/messages = %+v %+v", policy.Audit, policy.Messages)
	}

	public := application.Public().StepPolicy
	if public == nil || len(public.Modes) != 3 || public.Cap != policy.Cap {
		t.Fatalf("public step policy = %+v", public)
	}
	encoded, err := json.Marshal(application.Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, internal := range []string{"counted_capabilities", "cap_reached", "update_action", string(policy.CountedCapabilities[0]) + `"`} {
		var document map[string]any
		if err := json.Unmarshal(encoded, &document); err != nil {
			t.Fatal(err)
		}
		stepPolicy, _ := json.Marshal(document["step_policy"])
		if strings.Contains(string(stepPolicy), internal) {
			t.Fatalf("public step_policy leaks %q: %s", internal, stepPolicy)
		}
	}
}

func TestStepPolicyVocabularyMatchesTheApprovalPackage(t *testing.T) {
	t.Parallel()
	// The catalog package may not import business packages, so its validation
	// keeps its own copy of the mode ids. The default Catalog must offer exactly
	// the ids the approval vocabulary declares, and an id outside it is refused.
	application := defaultApplication(t)
	offered := make([]string, 0, len(application.StepPolicy.Modes))
	for _, mode := range application.StepPolicy.Modes {
		offered = append(offered, mode.Value)
	}
	declared := []string{approvalpolicy.StepModeAgent, approvalpolicy.StepModeUnlimited, approvalpolicy.StepModeCap}
	slices.Sort(offered)
	slices.Sort(declared)
	if !slices.Equal(offered, declared) {
		t.Fatalf("offered modes %v differ from the approval vocabulary %v", offered, declared)
	}
	application.StepPolicy.Modes = append(application.StepPolicy.Modes, catalog.ValueLabel{Value: "turbo", Label: "turbo"})
	if err := catalog.ValidateApplicationSchema(application); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

func TestStepPolicyValidationRejectsIncompleteOrUnknownDeclarations(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"no modes": func(app *catalog.ApplicationSchema) { app.StepPolicy.Modes = nil },
		"duplicate mode": func(app *catalog.ApplicationSchema) {
			app.StepPolicy.Modes = append(app.StepPolicy.Modes, app.StepPolicy.Modes[0])
		},
		"unlabeled mode": func(app *catalog.ApplicationSchema) { app.StepPolicy.Modes[0].Label = "" },
		"list default not offered": func(app *catalog.ApplicationSchema) {
			app.StepPolicy.Modes = app.StepPolicy.Modes[:1]
			app.StepPolicy.DefaultMode.List = "cap"
		},
		"mcp default unknown": func(app *catalog.ApplicationSchema) { app.StepPolicy.DefaultMode.MCP = "turbo" },
		"zero minimum":        func(app *catalog.ApplicationSchema) { app.StepPolicy.Cap.Minimum = 0 },
		"maximum below minimum": func(app *catalog.ApplicationSchema) {
			app.StepPolicy.Cap.Minimum, app.StepPolicy.Cap.Default, app.StepPolicy.Cap.Maximum = 10, 10, 5
		},
		"default above maximum": func(app *catalog.ApplicationSchema) { app.StepPolicy.Cap.Default = app.StepPolicy.Cap.Maximum + 1 },
		"default below minimum": func(app *catalog.ApplicationSchema) {
			app.StepPolicy.Cap.Minimum = 5
			app.StepPolicy.Cap.Default = 4
		},
		"no counted capabilities": func(app *catalog.ApplicationSchema) { app.StepPolicy.CountedCapabilities = nil },
		"unknown capability": func(app *catalog.ApplicationSchema) {
			app.StepPolicy.CountedCapabilities = append(app.StepPolicy.CountedCapabilities, "text-to-hologram")
		},
		"duplicate capability": func(app *catalog.ApplicationSchema) {
			app.StepPolicy.CountedCapabilities = append(app.StepPolicy.CountedCapabilities, app.StepPolicy.CountedCapabilities[0])
		},
		"missing message": func(app *catalog.ApplicationSchema) { app.StepPolicy.Messages.CapReached = "" },
		"message without used": func(app *catalog.ApplicationSchema) {
			app.StepPolicy.Messages.CapReached = "上限 {cap}"
		},
		"message without cap": func(app *catalog.ApplicationSchema) {
			app.StepPolicy.Messages.CapReached = "已用 {used}"
		},
		"missing audit resource type": func(app *catalog.ApplicationSchema) { app.StepPolicy.Audit.ResourceType = "" },
		"undeclared audit action":     func(app *catalog.ApplicationSchema) { app.StepPolicy.Audit.UpdateAction = "set_nothing" },
		"missing route":               func(app *catalog.ApplicationSchema) { app.Runtime.HTTP.Routes.AccountStepPolicy = "" },
		"route with parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.AccountStepPolicy = "/api/v1/account/{sessionID}/step-policy"
		},
		"route not absolute": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.AccountStepPolicy = "account/step-policy"
		},
		"route duplicates another": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.AccountStepPolicy = app.Runtime.HTTP.Routes.Tasks
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			application := defaultApplication(t)
			mutate(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("invalid step policy declaration was accepted")
			}
		})
	}
}

func TestStepPolicyCloneDoesNotShareSlices(t *testing.T) {
	t.Parallel()
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := source.Application()
	first.StepPolicy.Modes[0].Value = "mutated"
	first.StepPolicy.CountedCapabilities[0] = "mutated"
	second := source.Application()
	if second.StepPolicy.Modes[0].Value == "mutated" || second.StepPolicy.CountedCapabilities[0] == "mutated" {
		t.Fatal("Catalog Application() shares step policy slices with a previous copy")
	}
}

// A Catalog written before the section existed loads unchanged: the steps mode
// does not exist, nothing is projected, and a shared en bundle that translates
// the section does not make the document invalid.
func TestHistoricalCatalogWithoutStepPolicyStaysValid(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	application.StepPolicy = nil
	// Access keys and the external MCP service both decide by the step mode.
	application.AccessKeys = nil
	application.MCPPublic = nil
	application.Runtime.HTTP.Routes.AccountAccessKeys = ""
	application.Runtime.HTTP.Routes.AccountAccessKey = ""
	application.Runtime.HTTP.Routes.AccountStepPolicy = ""
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("historical Catalog without step_policy rejected: %v", err)
	}
	if application.Public().StepPolicy != nil {
		t.Fatal("historical Catalog projected a step_policy section")
	}
}

// The English public schema carries the translated mode labels; the section is
// covered by the same bundle as the rest of the Catalog.
func TestStepPolicyLabelsAreLocalized(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	localized, err := catalog.NewLocalizedPublicSchemas(application)
	if err != nil {
		t.Fatal(err)
	}
	english := localized.For("en").Schema.StepPolicy
	if english == nil {
		t.Fatal("english public schema lost step_policy")
	}
	labels := map[string]string{}
	for _, mode := range english.Modes {
		labels[mode.Value] = mode.Label
	}
	if labels["agent"] != "Managed by the Agent" || labels["unlimited"] != "Unlimited steps" || labels["cap"] != "Limit steps" {
		t.Fatalf("english labels = %v", labels)
	}
}
