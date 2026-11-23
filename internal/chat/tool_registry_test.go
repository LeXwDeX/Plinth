package chat

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestToolRegistryUsesOnePolicyForDescriptionAndExecution(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// Only 2D offerings exist except text-to-3d: availability, not the
	// workspace in view, decides what the model is offered.
	registry, err := compileToolRegistry(loaded.Application().Chat, func(capability, domain string) bool {
		return (domain == "image" && (capability == "text-to-image" || capability == "image-to-image" || capability == "background-removal")) ||
			(domain == "model-3d" && capability == "text-to-3d")
	})
	if err != nil {
		t.Fatal(err)
	}
	for tool, want := range map[string]bool{
		"refine_image":      true,
		"remove_background": true,
		// Tools of the other domain are available too (the domain in view is
		// only a hint), as long as one of their capabilities can run.
		"create_model_3d":            true,
		"create_model_3d_from_image": true,
		"list_session_models":        true,
		// No retexture/rigging/format offering is available.
		"retexture_model":      false,
		"convert_model_format": false,
		"unregistered_tool":    false,
	} {
		_, resolved := registry.resolve(tool, false)
		if resolved != want {
			t.Errorf("resolve(%q)=%v want %v", tool, resolved, want)
		}
		visible := false
		for _, described := range registry.describe("auto") {
			visible = visible || described.Name == tool
		}
		if visible != want {
			t.Errorf("describe() contains %q=%v want %v", tool, visible, want)
		}
	}
	if _, recovered := registry.resolve("convert_model_format", true); !recovered {
		t.Fatal("an accepted paid task must remain observable after its offering disappears")
	}
}

func TestToolRegistryRejectsDuplicateAndIncompleteDeclarations(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	schema.AgentTools = append(schema.AgentTools, schema.AgentTools[0])
	if _, err := compileToolRegistry(schema, nil); err == nil {
		t.Fatal("duplicate tool name accepted")
	}
	schema = loaded.Application().Chat
	schema.Tools[0].Execution.ImplementationKey = ""
	if _, err := compileToolRegistry(schema, nil); err == nil {
		t.Fatal("tool without implementation accepted")
	}
}

func TestToolRegistryKeepsInputDependentToolWhenOneCapabilityIsAvailable(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := compileToolRegistry(loaded.Application().Chat, func(capability, domain string) bool {
		return domain == "image" && capability == "image-to-image"
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.resolve("refine_image", false); !ok {
		t.Fatal("reference-image branch should remain available")
	}
	if _, ok := registry.resolve("create_model_3d", false); ok {
		t.Fatal("tool with no available model offering must be hidden")
	}
}

func TestToolRegistryOffersModelProcessingToolsWhenAnOfferingCanRun(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := compileToolRegistry(loaded.Application().Chat, func(capability, _ string) bool {
		return capability == "retexture" || capability == "rigging" || capability == "refine" || capability == "text-to-image"
	})
	if err != nil {
		t.Fatal(err)
	}
	for tool, want := range map[string]bool{
		"list_session_models": true,
		"retexture_model":     true,
		"refine_image":        true,
		// One reachable step capability keeps the multi-step tool visible.
		"process_model": true,
		// No available format-conversion offering hides the tool.
		"convert_model_format": false,
	} {
		if _, resolved := registry.resolve(tool, false); resolved != want {
			t.Errorf("resolve(%q)=%v want %v", tool, resolved, want)
		}
	}
	resolved, ok := registry.resolve("process_model", false)
	if !ok || resolved.Processing == nil || resolved.Processing.StepArgument != "step" {
		t.Fatalf("resolved process_model lost its server-owned declaration: %+v", resolved)
	}
}

// #382: the turn plan narrows the table the model is offered: a capability the
// plan froze as having no runnable model takes its tools out, the plan's own
// pinned tools stay, and a narrowing that would empty the table is dropped.
func TestToolRegistryNarrowsTheTableToTheTurnPlan(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := compileToolRegistry(loaded.Application().Chat, func(capability, _ string) bool {
		return capability == "retexture" || capability == "text-to-image"
	})
	if err != nil {
		t.Fatal(err)
	}
	names := func(tools []agents.Tool) map[string]bool {
		result := map[string]bool{}
		for _, tool := range tools {
			result[tool.Name] = true
		}
		return result
	}
	// retexture and refine both run, so a plan that refuses one of them still
	// offers the other; the pinned Harness tool stays whatever the plan says.
	plan := &TurnPlan{
		Tools: []string{"get_task"},
		Domains: []TurnPlanDomain{
			{Capability: "retexture", Conflict: &TurnPlanConflict{Kind: ModelConflictWorkspace}},
			{Capability: "refine", OfferingID: "gpt-image/refine"},
		},
	}
	offered := names(registry.narrowToPlan(registry.describe("auto"), plan))
	if offered["retexture_model"] {
		t.Error("a capability the plan has nothing to run is still offered")
	}
	if !offered["refine_image"] {
		t.Error("a capability the plan resolved lost its tool")
	}
	if !offered["get_task"] {
		t.Error("the plan's pinned tool is missing")
	}
	// A capability the plan only wants confirmed keeps its tool: the call is
	// how that confirmation is asked for.
	confirmed := &TurnPlan{Domains: []TurnPlanDomain{{Capability: "retexture",
		Conflict:   &TurnPlanConflict{Kind: ModelConflictWorkspace},
		Candidates: []string{"tripo/geometry/retexture"}}}}
	if got := names(registry.narrowToPlan(registry.describe("auto"), confirmed)); !got["retexture_model"] {
		t.Error("a capability awaiting the person's confirmation lost its tool")
	}
	// A plan that refuses every generation capability leaves the table alone:
	// a turn must still be able to answer.
	all := map[generation.Capability]bool{}
	for _, entry := range registry.ordered {
		for _, capability := range entry.execution.Capabilities {
			all[capability] = true
		}
	}
	wide := &TurnPlan{}
	for capability := range all {
		wide.Domains = append(wide.Domains, TurnPlanDomain{
			Capability: string(capability),
			Conflict:   &TurnPlanConflict{Kind: ModelConflictWorkspace},
		})
	}
	if got := registry.narrowToPlan(registry.describe("auto"), wide); len(got) == 0 {
		t.Fatal("the narrowing emptied the table")
	}
	// No plan at all changes nothing.
	if got := registry.narrowToPlan(registry.describe("auto"), nil); len(got) != len(registry.describe("auto")) {
		t.Fatalf("a nil plan narrowed the table to %d tools", len(got))
	}
}
