package catalog_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #493: the zero-credit report steps and the paid processing steps are two
// tools, and every step a tool declares has an enabled Offering behind it.
//
// The split is the point of the test: a 0-credit step that costs nothing and
// only reads the model must not sit in the same step enum as the steps that
// spend credits, and a step whose Offering is disabled must not be declared at
// all (Repair Printability was declared with `enabled: false` while the tool
// still offered it as a step).
func TestAnalysisStepsAreSplitFromThePaidProcessingTool(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	tools := map[string]catalog.ChatToolSchema{}
	for _, tool := range app.Chat.Tools {
		tools[tool.Name] = tool
	}
	analyze, ok := tools["analyze_model"]
	if !ok {
		t.Fatal("analyze_model is not declared")
	}
	process, ok := tools["process_model"]
	if !ok {
		t.Fatal("process_model is not declared")
	}
	report := []schemacontract.Capability{"analyze-printability", "rig-check"}
	if !slices.Equal(analyze.Execution.Capabilities, report) {
		t.Fatalf("analyze_model capabilities = %v, want %v", analyze.Execution.Capabilities, report)
	}
	for _, step := range report {
		if analyze.Processing.Steps[string(step)] != step {
			t.Fatalf("analyze_model does not bind %s: %v", step, analyze.Processing.Steps)
		}
		if slices.Contains(process.Execution.Capabilities, step) || process.Processing.Steps[string(step)] != "" {
			t.Fatalf("process_model still declares the report step %s", step)
		}
	}
	// The LLM reads the step enum, so it must name exactly the declared steps.
	assertStepEnumMatchesProcessingSteps(t, analyze)
	assertStepEnumMatchesProcessingSteps(t, process)
	// Every step any processing tool declares has at least one enabled Offering
	// behind it, so a step whose service is disabled cannot be offered at all.
	// The split itself is by what a step delivers, not by its price alone: a
	// local split or merge is also 0-credit, but it delivers a model, so it
	// stays on the processing tool.
	for name, tool := range tools {
		if tool.Processing == nil {
			continue
		}
		for step := range tool.Processing.Steps {
			if len(c.List(schemacontract.Capability(step))) == 0 {
				t.Errorf("%s step %q has no enabled Offering", name, step)
			}
		}
	}
	for _, step := range report {
		for _, offering := range c.List(step) {
			if offering.EstimatedCredits != 0 {
				t.Fatalf("analyze_model step %q has a billable service %s", step, offering.ID)
			}
		}
	}
	for _, capability := range app.Runtime.Frontend.ModelProcessingCapabilities {
		if capability == "repair-printability" {
			t.Error("the 3D panel still lists the disabled repair-printability")
		}
	}
	for _, op := range app.Chat.Harness.Operations {
		if op.ID == "repair-printability" && op.ToolName != "" {
			t.Errorf("the disabled repair-printability operation still names tool %q", op.ToolName)
		}
	}
}

func assertStepEnumMatchesProcessingSteps(t *testing.T, tool catalog.ChatToolSchema) {
	t.Helper()
	var parameters struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	enum := append([]string{}, parameters.Properties["step"].Enum...)
	want := make([]string, 0, len(tool.Processing.Steps))
	for step := range tool.Processing.Steps {
		want = append(want, step)
	}
	slices.Sort(enum)
	slices.Sort(want)
	if !slices.Equal(enum, want) {
		t.Fatalf("%s step enum = %v, want %v", tool.Name, enum, want)
	}
}
