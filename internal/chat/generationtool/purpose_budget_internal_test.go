package generationtool

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #129: a value the purpose budget gives a bounded parameter belongs to the
// purpose's layer, so it follows the same provenance rules as an override: it
// is credited to the purpose only when the purpose supplied the final value.
func TestBudgetValuesKeepThePurposeProvenanceOnlyWhenTheySuppliedTheValue(t *testing.T) {
	maximum := 2000000.0
	faceLimit := generation.Parameter{Name: "face_limit", Type: "number", Integer: true, AdjustmentPolicy: "advised",
		Semantic: generation.SemanticPolycount, Default: json.RawMessage(`2000000`), Maximum: &maximum}
	offering := choiceTestOffering(choiceFullID, "tripo-v3.1-full", generation.TextTo3D, faceLimit)
	executor := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{offering}})
	executor.chat.Model3DUsage.BudgetLimits = []catalog.ChatModel3DUsageBudgetLimit{
		{Semantic: generation.SemanticPolycount, Bound: catalog.BudgetBoundMaxTriangles, ItemTemplate: "{model} {parameter} {value} {maximum}"},
	}
	const reason = "角色用途需要受控面数与 UV。"
	baseCommand := func() chat.ToolCommand {
		command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
		command.Options.ProductionBrief = choiceTestBrief("game_character")
		return command
	}
	if _, values := executor.profileParameterValues(baseCommand(), offering); string(values["face_limit"]) != "60000" {
		t.Fatalf("budget values = %v, want face_limit 60000", values)
	}
	input := json.RawMessage(`{"face_limit":60000}`)
	tests := []struct {
		name       string
		mutate     func(*chat.ToolCommand)
		input      json.RawMessage
		wantReason string
		wantSource string
	}{
		{name: "the budget supplied the value", mutate: func(*chat.ToolCommand) {}, input: input,
			wantReason: reason, wantSource: "profile:game_character"},
		{name: "the Agent's explicit snapshot supplied the same value", mutate: func(command *chat.ToolCommand) {
			command.ExplicitParameters = map[string]json.RawMessage{"face_limit": json.RawMessage(`60000`)}
		}, input: input},
		{name: "the value differs", mutate: func(*chat.ToolCommand) {}, input: json.RawMessage(`{"face_limit":2000000}`)},
		{name: "no purpose", mutate: func(command *chat.ToolCommand) { command.Options.ProductionBrief = nil }, input: input},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := baseCommand()
			test.mutate(&command)
			reasons, sources, err := executor.parameterReasons(command, offering, test.input)
			if err != nil {
				t.Fatal(err)
			}
			if reasons["face_limit"] != test.wantReason || sources["face_limit"] != test.wantSource {
				t.Fatalf("reasons = %v, sources = %v, want %q / %q", reasons, sources, test.wantReason, test.wantSource)
			}
		})
	}
}

// The note names only the values over the budget, in the Schema's words.
func TestPurposeBudgetNoteNamesOnlyTheValuesOverTheBudget(t *testing.T) {
	maximum := 2000000.0
	faceLimit := generation.Parameter{Name: "face_limit", Label: "面数上限", Type: "number", Semantic: generation.SemanticPolycount,
		Default: json.RawMessage(`2000000`), Maximum: &maximum}
	texture := generation.Parameter{Name: "texture_resolution", Label: "纹理分辨率", Type: "select", Semantic: generation.SemanticTextureSize,
		Default: json.RawMessage(`"8k"`), Options: []generation.ParameterOption{{Value: "2k", Label: "2K"}, {Value: "8k", Label: "8K"}}}
	offering := choiceTestOffering(choiceFullID, "tripo-v3.1-full", generation.TextTo3D, faceLimit, texture)
	executor := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{offering}})
	size := 2048
	executor.chat.Model3DUsage.Profiles[1].Budget.MaxTextureSize = &size
	executor.chat.Model3DUsage.BudgetLimits = []catalog.ChatModel3DUsageBudgetLimit{
		{Semantic: generation.SemanticPolycount, Bound: catalog.BudgetBoundMaxTriangles, ItemTemplate: "{model}:{parameter}={value}>{maximum}"},
		{Semantic: generation.SemanticTextureSize, Bound: catalog.BudgetBoundMaxTextureSize,
			OptionValues: map[string]float64{"2k": 2048, "8k": 8192}, ItemTemplate: "{model}:{parameter}={value}({amount})>{maximum}"},
	}
	executor.chat.Model3DUsage.BudgetNote = catalog.ChatModel3DUsageBudgetNote{Template: "[{purpose}] {items}", Separator: " | "}
	command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
	command.Tool.ResultFields.PurposeBudgetNote = "purpose_budget_note"
	command.Options.ProductionBrief = choiceTestBrief("game_character")
	if note := executor.purposeBudgetNote(command, offering, json.RawMessage(`{"face_limit":60000,"texture_resolution":"2k"}`)); note != "" {
		t.Fatalf("values within the budget were reported: %q", note)
	}
	note := executor.purposeBudgetNote(command, offering, json.RawMessage(`{"face_limit":70000}`))
	want := "[游戏/实时·角色] tripo-v3.1-full:面数上限=70000>60000 | tripo-v3.1-full:纹理分辨率=8K(8192)>2048"
	if note != want {
		t.Fatalf("note = %q, want %q", note, want)
	}
	command.Tool.ResultFields.PurposeBudgetNote = ""
	if note := executor.purposeBudgetNote(command, offering, json.RawMessage(`{"face_limit":70000}`)); note != "" {
		t.Fatalf("a tool without the result key got a note: %q", note)
	}
}
