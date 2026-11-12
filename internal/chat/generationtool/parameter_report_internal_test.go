package generationtool

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #296: the tool result lists the frozen parameters that differ from the
// Offering's default, with who set each one, so the Agent reports what was
// really submitted instead of what it meant to send.

func reportRaw(text string) json.RawMessage { return json.RawMessage(text) }

// reportTestOffering declares the shapes the report distinguishes: advised and
// plain parameters, a number with a conditional range, a forced boolean, a
// parameter without a default (seed), values the report never lists (prompts,
// references, the consent flag, hidden and fixed parameters).
func reportTestOffering() generation.Offering {
	maximum := 2000000.0
	return generation.Offering{
		ID: choiceFullID, ProviderID: "tripo", ModelID: "tripo-v3.1-full", ModelName: "Tripo v3.1 Full",
		Capability: generation.TextTo3D, Domain: generation.DomainModel3D, Enabled: true,
		Features: []string{"skills"},
		Cost:     &generation.CostDeclaration{ConsentParameter: "authorize_extra_credits"},
		Parameters: []generation.Parameter{
			{Name: "prompt", Label: "Prompt", Type: "text", Semantic: generation.SemanticPrompt},
			{Name: "negative_prompt", Label: "Negative prompt", Type: "text", Semantic: generation.SemanticNegativePrompt},
			{Name: "reference_assets", Label: "Reference", Type: "asset-array", Semantic: generation.SemanticReferenceImage},
			{Name: "mesh_mode", Label: "Mesh mode", Type: "select", Default: reportRaw(`"Quad"`), AdjustmentPolicy: "advised",
				Options: []generation.ParameterOption{{Value: "Quad", Label: "Quad"}, {Value: "Raw", Label: "Raw"}}},
			{Name: "face_limit", Label: "Face limit", Type: "number", Integer: true, Semantic: generation.SemanticPolycount,
				Default: reportRaw(`2000000`), AdjustmentPolicy: "advised", Maximum: &maximum,
				MaximumWhen: []schema.ConditionalMaximum{{
					When:    schema.ParameterCondition{Parameter: "mesh_mode", Operator: "equals", Value: reportRaw(`"Raw"`)},
					Maximum: 500000,
				}}},
			{Name: "quality_level", Label: "Quality", Type: "select", Semantic: generation.SemanticQuality, Default: reportRaw(`"standard"`),
				Options: []generation.ParameterOption{{Value: "standard", Label: "Standard"}, {Value: "high", Label: "High"}}},
			{Name: "texture", Label: "Texture", Type: "boolean", Default: reportRaw(`false`),
				ForcedWhen: []schema.ForcedValue{{
					When:  schema.ParameterCondition{Parameter: "mesh_mode", Operator: "equals", Value: reportRaw(`"Raw"`)},
					Value: reportRaw(`true`),
				}}},
			{Name: "tapose", Label: "T/A pose", Type: "boolean", Default: reportRaw(`false`)},
			{Name: "seed", Label: "Seed", Type: "number", Integer: true},
			{Name: "note", Label: "Note", Type: "text"},
			{Name: "formats", Label: "Formats", Type: "string-array"},
			{Name: "model_version", Label: "Version", Type: "select", Fixed: true, Default: reportRaw(`"v3"`)},
			{Name: "authorize_extra_credits", Label: "Extra credits", Type: "boolean", Default: reportRaw(`false`)},
			{Name: "wire_mode", Label: "Wire mode", Type: "select", Default: reportRaw(`"a"`),
				Presentation: &schema.ParameterPresentation{Surface: schema.SurfaceHidden, Control: schema.ControlSelect}},
		},
	}
}

func reportTestTool() catalog.ChatToolSchema {
	tool := choiceTestTool("create_model_3d", generation.TextTo3D)
	tool.ResultFields.Parameters = "parameters"
	tool.ArgumentMappings = map[string]string{"prompt": "prompt", "model_seed": "model_seed"}
	tool.OverrideMappings = map[string]string{"model_seed": "seed"}
	return tool
}

func reportTestExecutor() *Executor {
	executor := choiceTestExecutor(&choiceTestGateway{})
	executor.chat.Agent.ParameterReport.MaximumValueCharacters = 24
	return executor
}

func reportTestCommand() chat.ToolCommand {
	command := choiceTestCommand(reportTestTool())
	command.Call.Arguments = reportRaw(`{"prompt":"a fox"}`)
	return command
}

// entries flattens a report to name -> value / source for comparison.
func reportEntries(t *testing.T, report []reportedParameter) (values map[string]string, sources map[string]string, names []string) {
	t.Helper()
	values, sources = map[string]string{}, map[string]string{}
	for _, entry := range report {
		values[entry.Name] = string(entry.Value)
		sources[entry.Name] = entry.Source
		names = append(names, entry.Name)
	}
	return values, sources, names
}

func TestParameterReportListsNothingWhenEveryParameterIsAtItsDefault(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	offering := reportTestOffering()
	// Everything the report never lists differs from its default here: the
	// prompts, the reference, the consent flag, the hidden parameter. An unset
	// seed is no entry.
	input := reportRaw(`{"prompt":"a fox","negative_prompt":"NEG","reference_assets":["a1"],"mesh_mode":"Quad",` +
		`"face_limit":2000000,"quality_level":"standard","texture":false,"tapose":false,"model_version":"v3",` +
		`"authorize_extra_credits":true,"wire_mode":"b"}`)
	report := executor.parameterReport(command, offering, input, nil)
	if report == nil || len(report) != 0 {
		t.Fatalf("report = %#v, want an empty list (every parameter at its default)", report)
	}
	encoded, _ := json.Marshal(map[string]any{"parameters": report})
	if string(encoded) != `{"parameters":[]}` {
		t.Fatalf("an empty report must read as [], got %s", encoded)
	}
}

func TestParameterReportNamesTheSourceOfEachFrozenValue(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	offering := reportTestOffering()
	// The purpose layer: a game character is capped at 20000 faces.
	executor.chat.Model3DUsage.Profiles[1].ParameterOverrides = map[string]map[string]json.RawMessage{
		choiceFullID: {"face_limit": reportRaw(`20000`)},
	}
	command.Options.ProductionBrief = choiceTestBrief("game_character")
	// The Agent's own call: the seed through the tool's mapped argument.
	command.Call.Arguments = reportRaw(`{"prompt":"a fox","model_seed":7}`)
	// The person's panel for the turn.
	command.Options.Parameters = map[string]json.RawMessage{"tapose": reportRaw(`true`)}
	// The Agent's remembered setting.
	executor.offeringParameterStates = func(owner, session, offeringID string) map[string]RememberedParameter {
		if owner != "owner" || session != "session" || offeringID != choiceFullID {
			t.Fatalf("remembered settings read for %s/%s/%s", owner, session, offeringID)
		}
		return map[string]RememberedParameter{"note": {Value: reportRaw(`"keep the tail"`), Source: "agent"}}
	}
	// A Skill that raises quality.
	executor.chat.Skills = catalog.ChatSkillsSchema{
		Feature: "skills",
		Execution: catalog.ChatSkillExecutionSchema{
			PromptTemplateFields: catalog.ChatSkillPromptTemplateFields{UserInput: "{user_input}", SkillPrompt: "{skill_prompt}"},
			PromptTemplates:      map[schema.Capability]string{generation.TextTo3D: "{user_input} {skill_prompt}"},
			ParameterOverrides:   map[schema.ParameterSemantic]json.RawMessage{generation.SemanticQuality: reportRaw(`"high"`)},
		},
	}
	executor.skills = map[string]catalog.SkillSchema{"style": {ID: "style", Prompt: "painted"}}
	command.Options.SkillID = "style"

	input := reportRaw(`{"prompt":"a fox painted","mesh_mode":"Quad","face_limit":20000,"quality_level":"high",` +
		`"tapose":true,"seed":7,"note":"keep the tail"}`)
	report := executor.parameterReport(command, offering, input, nil)
	values, sources, names := reportEntries(t, report)
	if want := []string{"face_limit", "quality_level", "tapose", "seed", "note"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("listed %v, want %v (declaration order, nothing else)", names, want)
	}
	wantSources := map[string]string{
		"face_limit": "profile:game_character", "quality_level": "skill", "tapose": "user", "seed": "agent", "note": "agent",
	}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Fatalf("sources = %v, want %v", sources, wantSources)
	}
	if values["face_limit"] != "20000" || values["seed"] != "7" || values["tapose"] != "true" || values["note"] != `"keep the tail"` {
		t.Fatalf("values = %v", values)
	}
	if report[0].Label != "Face limit" || report[3].Label != "Seed" {
		t.Fatalf("labels = %q / %q", report[0].Label, report[3].Label)
	}
	rendered, _ := json.Marshal(report[3])
	if string(rendered) != `{"name":"seed","label":"Seed","value":7,"source":"agent"}` {
		t.Fatalf("entry shape = %s", rendered)
	}
}

func TestParameterReportLeavesAnUnsetSeedOut(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	// The Agent said it set a seed, but the call carried none and the frozen
	// input has none: the report cannot list one.
	command.Call.Arguments = reportRaw(`{"prompt":"a fox"}`)
	report := executor.parameterReport(command, reportTestOffering(),
		reportRaw(`{"prompt":"a fox","tapose":true}`), nil)
	_, _, names := reportEntries(t, report)
	if !reflect.DeepEqual(names, []string{"tapose"}) {
		t.Fatalf("listed %v, want only tapose (no seed was frozen)", names)
	}
	// An empty text with no default is unset too.
	report = executor.parameterReport(command, reportTestOffering(), reportRaw(`{"note":"","formats":[]}`), nil)
	if len(report) != 0 {
		t.Fatalf("empty values were listed: %#v", report)
	}
}

// The case that prompted #296: a Rodin task whose frozen input has no seed. The
// Agent said the seed was 1; the report of the shipped Offering, normalized as
// the kernel normalizes it, lists no seed, and lists one only when one is frozen.
func TestParameterReportOfTheShippedRodinOfferingListsASeedOnlyWhenOneIsFrozen(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	executor := &Executor{chat: product.Application().Chat}
	command := reportTestCommand()
	command.Call.Arguments = reportRaw(`{"prompt":"a fox"}`)
	const rodin = "hyper3d/rodin-gen-2.5/text-to-3d"
	freeze := func(input string) (generation.Offering, json.RawMessage) {
		offering, normalized, err := product.Normalize(generation.TextTo3D, rodin, reportRaw(input))
		if err != nil {
			t.Fatal(err)
		}
		return offering, normalized
	}
	// The consent flag is the executor's own (agentConsentInput) on every step.
	offering, normalized := freeze(`{"prompt":"a fox","authorize_extra_credits":true}`)
	if report := executor.parameterReport(command, offering, normalized, nil); report == nil || len(report) != 0 {
		t.Fatalf("a Rodin step at its defaults listed %#v", report)
	}
	executor.offeringParameterStates = func(string, string, string) map[string]RememberedParameter {
		return map[string]RememberedParameter{"seed": {Value: reportRaw(`1`), Source: "agent"}}
	}
	offering, normalized = freeze(`{"prompt":"a fox","seed":1,"authorize_extra_credits":true}`)
	values, sources, names := reportEntries(t, executor.parameterReport(command, offering, normalized, nil))
	if !reflect.DeepEqual(names, []string{"seed"}) || values["seed"] != "1" || sources["seed"] != "agent" {
		t.Fatalf("a frozen seed listed as %v / %v / %v", names, values, sources)
	}
}

func TestParameterReportCreditsTheRememberedPinToThePersonAndOnlyWhileItStillMatches(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	offering := reportTestOffering()
	executor.offeringParameterStates = func(string, string, string) map[string]RememberedParameter {
		return map[string]RememberedParameter{
			"tapose": {Value: reportRaw(`true`), Source: "user"},
			"seed":   {Value: reportRaw(`3`), Source: "agent"},
			// A pin on a parameter the person cannot set is never applied.
			"mesh_mode": {Value: reportRaw(`"Raw"`), Source: "user"},
		}
	}
	report := executor.parameterReport(command, offering, reportRaw(`{"tapose":true,"seed":4,"mesh_mode":"Raw"}`), nil)
	_, sources, _ := reportEntries(t, report)
	// The pin still holds the frozen value: the person. The Agent's setting was
	// changed after the step froze 4: nobody can be credited. An advised
	// parameter takes no pin from the person.
	if sources["tapose"] != "user" || sources["seed"] != "unknown" || sources["mesh_mode"] != "unknown" {
		t.Fatalf("sources = %v", sources)
	}
}

func TestParameterReportPrefersTheFrozenAdjustmentAndNamesForcedAndConvergedValues(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	offering := reportTestOffering()
	// mesh_mode=Raw forces texture on and narrows face_limit to 500000.
	input := reportRaw(`{"mesh_mode":"Raw","texture":true,"face_limit":500000}`)
	adjustments := []generation.ParameterAdjustment{{
		Name: "mesh_mode", Value: reportRaw(`"Raw"`), Source: "profile:game_prop", Profile: "game_prop",
	}}
	_, sources, names := reportEntries(t, executor.parameterReport(command, offering, input, adjustments))
	if !reflect.DeepEqual(names, []string{"mesh_mode", "face_limit", "texture"}) {
		t.Fatalf("listed %v", names)
	}
	want := map[string]string{"mesh_mode": "profile:game_prop", "texture": "forced", "face_limit": "converged"}
	if !reflect.DeepEqual(sources, want) {
		t.Fatalf("sources = %v, want %v", sources, want)
	}
	// An Agent change the kernel froze with its reason stays the Agent's even
	// when no layer of this call holds the value any more: the frozen record is
	// the evidence.
	agentChange := []generation.ParameterAdjustment{{
		Name: "mesh_mode", Value: reportRaw(`"Raw"`), Reason: "the tail must stay raw", Source: "agent",
	}}
	_, sources, _ = reportEntries(t, executor.parameterReport(command, offering, input, agentChange))
	if sources["mesh_mode"] != "agent" {
		t.Fatalf("a frozen Agent adjustment was credited to %q", sources["mesh_mode"])
	}
	// An adjustment for another value is no evidence for this one.
	adjustments[0].Value = reportRaw(`"Quad"`)
	_, sources, _ = reportEntries(t, executor.parameterReport(command, offering, input, adjustments))
	if sources["mesh_mode"] != "unknown" {
		t.Fatalf("stale adjustment credited: %v", sources)
	}
	// A value someone gave is theirs, even when it equals the converged one. An
	// advised parameter is the Agent's to change, never the person's: the
	// person's panel value for it is not applied, so it earns no credit.
	command.Options.Parameters = map[string]json.RawMessage{"face_limit": reportRaw(`500000`)}
	_, sources, _ = reportEntries(t, executor.parameterReport(command, offering, input, nil))
	if sources["face_limit"] != "converged" || sources["texture"] != "forced" {
		t.Fatalf("sources = %v", sources)
	}
	executor.offeringParameterStates = func(string, string, string) map[string]RememberedParameter {
		return map[string]RememberedParameter{"face_limit": {Value: reportRaw(`500000`), Source: "agent"}}
	}
	_, sources, _ = reportEntries(t, executor.parameterReport(command, offering, input, nil))
	if sources["face_limit"] != "agent" || sources["texture"] != "forced" {
		t.Fatalf("sources = %v", sources)
	}
}

func TestParameterReportCreditsTheToolDefaultAndRefusesToGuess(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	offering := reportTestOffering()
	command.Tool.DefaultParameters = map[string]json.RawMessage{"quality_level": reportRaw(`"high"`)}
	_, sources, _ := reportEntries(t, executor.parameterReport(command, offering,
		reportRaw(`{"quality_level":"high","seed":9}`), nil))
	if sources["quality_level"] != "default" || sources["seed"] != "unknown" {
		t.Fatalf("sources = %v", sources)
	}
}

func TestParameterReportCutsLongValues(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	offering := reportTestOffering()
	input := reportRaw(`{"note":"` + strings.Repeat("长", 40) + `","formats":["glb","fbx","obj","usdz","stl"],"seed":12}`)
	report := executor.parameterReport(command, offering, input, nil)
	byName := map[string]reportedParameter{}
	for _, entry := range report {
		byName[entry.Name] = entry
	}
	if got := string(byName["note"].Value); got != `"`+strings.Repeat("长", 24)+`…"` || !byName["note"].ValueTruncated {
		t.Fatalf("long text = %s truncated=%v", got, byName["note"].ValueTruncated)
	}
	var cut string
	if err := json.Unmarshal(byName["formats"].Value, &cut); err != nil || !byName["formats"].ValueTruncated ||
		!strings.HasPrefix(cut, `["glb","fbx","obj"`) || !strings.HasSuffix(cut, "…") {
		t.Fatalf("long list = %s truncated=%v", byName["formats"].Value, byName["formats"].ValueTruncated)
	}
	if string(byName["seed"].Value) != "12" || byName["seed"].ValueTruncated {
		t.Fatalf("short value changed: %#v", byName["seed"])
	}
}

func TestParameterReportNeedsTheDeclaredKeyAndAReadableInput(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	offering := reportTestOffering()
	good := reportRaw(`{"seed":1}`)
	command.Tool.ResultFields.Parameters = ""
	if report := executor.parameterReport(command, offering, good, nil); report != nil {
		t.Fatalf("a tool without the key got a report: %#v", report)
	}
	command.Tool.ResultFields.Parameters = "parameters"
	for _, input := range []json.RawMessage{nil, reportRaw(`not json`), reportRaw(`null`)} {
		if report := executor.parameterReport(command, offering, input, nil); report != nil {
			t.Fatalf("input %q was reported as %#v; an unreadable input must report nothing, not an empty list", input, report)
		}
	}
}

func TestParameterReportOfAProcessingStepReadsOnlyItsParameterArguments(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	offering := reportTestOffering()
	command.Tool.Processing = &catalog.ChatToolProcessingSchema{ParameterArguments: map[string]string{"target": "face_limit"}}
	command.Call.Arguments = reportRaw(`{"target":9000}`)
	// The panel and the remembered settings are not layers of a processing step.
	command.Options.Parameters = map[string]json.RawMessage{"seed": reportRaw(`5`)}
	executor.offeringParameterStates = func(string, string, string) map[string]RememberedParameter {
		return map[string]RememberedParameter{"seed": {Value: reportRaw(`5`), Source: "user"}}
	}
	_, sources, _ := reportEntries(t, executor.parameterReport(command, offering, reportRaw(`{"face_limit":9000,"seed":5}`), nil))
	if sources["face_limit"] != "agent" || sources["seed"] != "unknown" {
		t.Fatalf("sources = %v", sources)
	}
}

func TestParameterReportOfAHarnessStepSeparatesTheFixedValuesFromTheAgentsFills(t *testing.T) {
	executor, command := reportTestExecutor(), reportTestCommand()
	offering := reportTestOffering()
	selection := harness.DefaultSelection("owner", "session")
	selection.OperationID = "op-1"
	selection.FixedParameters = map[string]json.RawMessage{"tapose": reportRaw(`true`)}
	command.Options.Harness = &resolve.Snapshot{Selection: selection}
	command.Call.Arguments = reportRaw(`{"parameters":{"seed":11}}`)
	_, sources, _ := reportEntries(t, executor.parameterReport(command, offering, reportRaw(`{"tapose":true,"seed":11}`), nil))
	if sources["tapose"] != "user" || sources["seed"] != "agent" {
		t.Fatalf("sources = %v", sources)
	}
}
