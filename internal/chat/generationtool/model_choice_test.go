package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #103: the model of a 3D step is chosen by four layers, highest first: the
// person's explicit pick in the session, the Agent's model argument, the
// production brief's purpose, and the Offering defaults.

const (
	choiceFullID  = "tripo/tripo-v3.1-full/text-to-3d"
	choiceP2ID    = "tripo/tripo-p2/text-to-3d"
	choiceMeshyID = "meshy/meshy-t2.5/text-to-3d"
)

type choiceTestGateway struct {
	offerings   []generation.Offering
	unavailable map[generation.OfferingID]bool
}

func (gateway *choiceTestGateway) Offerings(capability generation.Capability) []generation.Offering {
	var result []generation.Offering
	for _, offering := range gateway.offerings {
		if offering.Capability == capability {
			result = append(result, offering)
		}
	}
	return result
}

func (gateway *choiceTestGateway) OfferingAvailable(id generation.OfferingID) bool {
	return !gateway.unavailable[id]
}

func (*choiceTestGateway) Submit(context.Context, generation.SubmitCommand) (generation.Accepted, error) {
	return generation.Accepted{}, errors.New("choice test gateway does not submit")
}

func (*choiceTestGateway) Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error) {
	return generation.Task{}, errors.New("choice test gateway does not read tasks")
}

func choiceTestOffering(id, modelID string, capability generation.Capability, parameters ...generation.Parameter) generation.Offering {
	provider, _, _ := strings.Cut(id, "/")
	return generation.Offering{
		ID: generation.OfferingID(id), ProviderID: provider, ModelID: modelID, ModelName: modelID,
		Capability: capability, Domain: generation.DomainModel3D, Enabled: true, Parameters: parameters,
	}
}

func choiceTestUsage() catalog.ChatModel3DUsageSchema {
	return catalog.ChatModel3DUsageSchema{
		DefaultProfile: "render",
		Profiles: []catalog.ChatModel3DUsageProfile{
			{
				ID: "render", Label: "展示/渲染", Description: "展示用模型。",
				Reason: "展示用途优先完整细节。",
				RecommendedOfferings: map[string][]string{
					shapeTextTo3D: {choiceFullID},
				},
				Budget: catalog.ChatModel3DUsageBudget{MaxTriangles: 2000000},
			},
			{
				ID: "game_character", Label: "游戏/实时·角色", Description: "游戏角色。",
				Reason: "角色用途需要受控面数与 UV。",
				RecommendedOfferings: map[string][]string{
					shapeTextTo3D: {choiceP2ID, choiceMeshyID},
				},
				ParameterOverrides: map[string]map[string]json.RawMessage{
					choiceP2ID: {"face_limit": json.RawMessage(`20000`)},
				},
				Budget: catalog.ChatModel3DUsageBudget{MaxTriangles: 60000, RequireUV: true},
			},
		},
		Brief: catalog.ChatProductionBriefSchema{Fields: []catalog.ChatProductionBriefField{
			{Name: "purpose", Type: "profile", Label: "用途"},
		}},
	}
}

func choiceTestExecutor(gateway Gateway) *Executor {
	return &Executor{
		gateway: gateway,
		chat: catalog.ChatSchema{
			Model3DUsage: choiceTestUsage(),
			ThreeView: catalog.ChatThreeViewSchema{
				Multiview: catalog.ChatThreeViewMultiviewSchema{Tool: "create_model_3d_from_views"},
			},
			Presentation: catalog.ChatPresentationSchema{Messages: catalog.ChatMessageTexts{
				ModelChoiceConflict:          "模型已锁定为 {locked}，不能改为 {requested}",
				ModelChoiceUnavailable:       "模型 {model} 不可用；可选：{choices}",
				ModelChoiceWorkspaceConflict: "工作区当前选择 {workspace}，用途 {profile} 推荐 {recommended}；请先确认",
			}},
		},
	}
}

func choiceTestTool(name string, capability generation.Capability) catalog.ChatToolSchema {
	return catalog.ChatToolSchema{
		Name:          name,
		Execution:     catalog.ChatToolExecutionSchema{Capabilities: []generation.Capability{capability}},
		ModelArgument: "model",
		ResultFields:  catalog.ChatToolResultFields{ModelChoice: "model_choice"},
	}
}

func choiceTestCommand(tool catalog.ChatToolSchema) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner", SessionID: "session", Tool: tool,
		Call: agents.ToolCall{ID: "call-1", Name: tool.Name},
	}
}

func choiceTestArguments(model string) map[string]json.RawMessage {
	if model == "" {
		return nil
	}
	encoded, _ := json.Marshal(model)
	return map[string]json.RawMessage{"model": encoded}
}

func choiceTestBrief(purpose string) chat.ProductionBrief {
	if purpose == "" {
		return nil
	}
	encoded, _ := json.Marshal(purpose)
	return chat.ProductionBrief{"purpose": encoded}
}

func TestChooseOfferingFollowsTheModelChoiceLayers(t *testing.T) {
	full := choiceTestOffering(choiceFullID, "tripo-v3.1-full", generation.TextTo3D)
	full.Default = true
	p2 := choiceTestOffering(choiceP2ID, "tripo-p2", generation.TextTo3D)
	meshy := choiceTestOffering(choiceMeshyID, "meshy-t2.5", generation.TextTo3D)
	tests := []struct {
		name      string
		locked    string
		purpose   string
		named     string
		workspace string
		wantID    string
		source    string
		profile   string
		refusal   string
	}{
		{name: "the person's explicit pick wins over the purpose", locked: choiceFullID,
			purpose: "game_character", wantID: choiceFullID, source: "user"},
		{name: "the Agent's model argument", named: choiceMeshyID, wantID: choiceMeshyID, source: "agent"},
		{name: "the purpose recommendation", purpose: "game_character", wantID: choiceP2ID,
			source: "profile", profile: "game_character"},
		{name: "the defaults when nothing chose", wantID: choiceFullID, source: "default"},
		{name: "a conflict with the person's pick is refused", locked: choiceP2ID, named: choiceMeshyID,
			refusal: "锁定"},
		{name: "an unavailable Agent model is refused", named: "missing/model", refusal: "不可用"},
		{name: "a purpose step that differs from the workspace model asks first", purpose: "game_character",
			workspace: choiceFullID, refusal: "确认"},
		{name: "the purpose step runs when the workspace model already matches", purpose: "render",
			workspace: choiceFullID, wantID: choiceFullID, source: "profile", profile: "render"},
		{name: "the Agent outranks the workspace picker value", named: choiceMeshyID,
			workspace: choiceFullID, wantID: choiceMeshyID, source: "agent"},
		{name: "without a purpose the workspace picker value still runs", workspace: choiceMeshyID,
			wantID: choiceMeshyID, source: "user"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway := &choiceTestGateway{offerings: []generation.Offering{full, p2, meshy}}
			executor := choiceTestExecutor(gateway)
			command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
			command.Options.ExplicitModel3D = test.locked
			command.Options.AIModel = test.workspace
			command.Options.ProductionBrief = choiceTestBrief(test.purpose)
			choice, err := executor.chooseOffering(command, generation.TextTo3D, choiceTestArguments(test.named), modelInput{})
			if err != nil {
				t.Fatal(err)
			}
			if test.refusal != "" {
				if choice.Offering.ID != "" || !strings.Contains(choice.Refusal, test.refusal) {
					t.Fatalf("refusal = %+v, want a refusal mentioning %q and no Offering", choice, test.refusal)
				}
				// The tool turns a refusal into the Agent's confirmation result;
				// nothing is submitted there.
				result := executor.modelChoiceFailure(command, choice.Refusal)
				if !result.NoSubmission || result.TaskID != "" || result.Error == "" {
					t.Fatalf("refusal result = %+v", result)
				}
				return
			}
			if choice.Refusal != "" || choice.Offering.ID != generation.OfferingID(test.wantID) ||
				choice.Source != test.source || choice.Profile != test.profile {
				t.Fatalf("choice = %+v, want %s from %s (profile %q)", choice, test.wantID, test.source, test.profile)
			}
			if test.source == "profile" {
				wantReason := "角色用途需要受控面数与 UV。"
				if test.profile == "render" {
					wantReason = "展示用途优先完整细节。"
				}
				if choice.Reason != wantReason {
					t.Fatalf("purpose reason = %q", choice.Reason)
				}
			}
		})
	}
}

func TestChooseOfferingDoesNotSilentlyReplaceTheWorkspaceModel(t *testing.T) {
	full := choiceTestOffering(choiceFullID, "tripo-v3.1-full", generation.TextTo3D)
	p2 := choiceTestOffering(choiceP2ID, "tripo-p2", generation.TextTo3D)
	command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
	command.Options.AIModel = choiceFullID
	command.Options.ProductionBrief = choiceTestBrief("game_character")
	choice, err := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{full, p2}}).
		chooseOffering(command, generation.TextTo3D, nil, modelInput{})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Offering.ID != "" || !strings.Contains(choice.Refusal, "确认") ||
		!strings.Contains(choice.Refusal, "tripo-p2") {
		t.Fatalf("choice = %+v, want a refusal naming the recommended model", choice)
	}
}

// A workspace pick that is itself one of the purpose's recommendations runs
// without asking: nothing is replaced, and the purpose still owns the choice
// (#282 made the first 2D recommendation differ from the default picker model).
func TestChooseOfferingRunsAWorkspaceModelThePurposeRecommends(t *testing.T) {
	p2 := choiceTestOffering(choiceP2ID, "tripo-p2", generation.TextTo3D)
	meshy := choiceTestOffering(choiceMeshyID, "meshy-t2.5", generation.TextTo3D)
	command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
	command.Options.AIModel = choiceMeshyID
	command.Options.ProductionBrief = choiceTestBrief("game_character")
	choice, err := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{p2, meshy}}).
		chooseOffering(command, generation.TextTo3D, nil, modelInput{})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Refusal != "" || choice.Offering.ID != meshy.ID || choice.Source != "profile" || choice.Profile != "game_character" {
		t.Fatalf("choice = %+v, want the recommended workspace model without a confirmation", choice)
	}
}

func TestChooseOfferingSkipsAnUnavailableProfileRecommendation(t *testing.T) {
	p2 := choiceTestOffering(choiceP2ID, "tripo-p2", generation.TextTo3D)
	meshy := choiceTestOffering(choiceMeshyID, "meshy-t2.5", generation.TextTo3D)
	gateway := &choiceTestGateway{
		offerings:   []generation.Offering{p2, meshy},
		unavailable: map[generation.OfferingID]bool{p2.ID: true},
	}
	command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
	command.Options.ProductionBrief = choiceTestBrief("game_character")
	choice, err := choiceTestExecutor(gateway).chooseOffering(command, generation.TextTo3D, nil, modelInput{})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Offering.ID != meshy.ID || choice.Source != "profile" || choice.Profile != "game_character" {
		t.Fatalf("choice = %+v", choice)
	}
}

func TestChooseOfferingSkipsAProfileOfferingThatCannotTakeTheInput(t *testing.T) {
	single := choiceTestOffering("tripo/tripo-v2/image-to-3d", "tripo-v2", generation.ImageTo3D,
		generation.Parameter{Name: "reference_image", Type: "asset", Semantic: generation.SemanticReferenceImage})
	multi := choiceTestOffering("tripo/tripo-v3/image-to-3d", "tripo-v3", generation.ImageTo3D,
		generation.Parameter{Name: "reference_images", Type: "asset-array", MaxItems: 4,
			Semantic: generation.SemanticReferenceImage})
	multi.ReferenceSelectionPolicy = "preserve"
	usage := choiceTestUsage()
	usage.Profiles[1].RecommendedOfferings = map[string][]string{
		shapeImageTo3D: {string(single.ID), string(multi.ID)},
	}
	executor := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{single, multi}})
	executor.chat.Model3DUsage = usage
	command := choiceTestCommand(choiceTestTool("create_model_3d_from_image", generation.ImageTo3D))
	command.Options.ProductionBrief = choiceTestBrief("game_character")
	choice, err := executor.chooseOffering(command, generation.ImageTo3D, nil, modelInput{References: 2})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Offering.ID != multi.ID || choice.Source != "profile" {
		t.Fatalf("choice = %+v, want the Offering that accepts two references", choice)
	}
}

func TestChooseOfferingTreatsAStructuredMultiviewModelAsThePersons(t *testing.T) {
	offering := choiceTestOffering("tripo/tripo-v3.1-multiview/multiview-to-3d", "tripo-v3.1-multiview", generation.ImageTo3D)
	command := choiceTestCommand(choiceTestTool("create_model_3d_from_views", generation.ImageTo3D))
	command.Tool.IgnoreUserModel = true
	command.Options.Multiview = &chat.MultiviewRequest{Model: string(offering.ID)}
	choice, err := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{offering}}).
		chooseOffering(command, generation.ImageTo3D, choiceTestArguments(string(offering.ID)), modelInput{})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Offering.ID != offering.ID || choice.Source != "user" {
		t.Fatalf("multiview choice = %+v", choice)
	}
}

// The session's explicit lock applies to the multiview tool too; its
// ignore_user_model only keeps the workspace picker out of the fallback.
func TestTheExplicitModelLockAppliesToTheMultiviewTool(t *testing.T) {
	multiview := choiceTestOffering("tripo/tripo-v3.1-multiview/image-to-3d", "tripo-v3.1-multiview", generation.ImageTo3D)
	p2 := choiceTestOffering("tripo/tripo-p2/image-to-3d", "tripo-p2", generation.ImageTo3D)
	executor := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{multiview, p2}})
	command := choiceTestCommand(choiceTestTool("create_model_3d_from_views", generation.ImageTo3D))
	command.Tool.IgnoreUserModel = true

	command.Options.ExplicitModel3D = string(p2.ID)
	conflict, err := executor.chooseOffering(command, generation.ImageTo3D,
		choiceTestArguments(string(multiview.ID)), modelInput{})
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Offering.ID != "" || !strings.Contains(conflict.Refusal, "锁定") {
		t.Fatalf("a structured multiview model replaced the lock: %+v", conflict)
	}

	command.Options.ExplicitModel3D = string(multiview.ID)
	locked, err := executor.chooseOffering(command, generation.ImageTo3D, nil, modelInput{})
	if err != nil {
		t.Fatal(err)
	}
	if locked.Offering.ID != multiview.ID || locked.Source != "user" {
		t.Fatalf("locked multiview choice = %+v", locked)
	}
}

// A multiview call with no purpose recommendation may only run one of its
// declared models that takes the views. Falling back to the first available
// Offering could submit an image-to-3D model that declares no views (#103).
func TestTheMultiviewFallbackOnlyRunsADeclaredModelThatTakesTheViews(t *testing.T) {
	views := []string{"front", "back", "left"}
	declared := choiceTestOffering("tripo/tripo-v3.1-multiview/image-to-3d", "tripo-v3.1-multiview", generation.ImageTo3D,
		generation.Parameter{Name: "reference_views", Type: "select", Semantic: generation.SemanticReferenceViews,
			Options: []generation.ParameterOption{{Value: "front"}, {Value: "back"}, {Value: "left"}}})
	unfit := choiceTestOffering("meshy/meshy-7.1/image-to-3d", "meshy-7.1", generation.ImageTo3D,
		generation.Parameter{Name: "reference_image", Type: "asset", Semantic: generation.SemanticReferenceImage})
	tool := choiceTestTool("create_model_3d_from_views", generation.ImageTo3D)
	tool.IgnoreUserModel = true
	tool.Parameters = json.RawMessage(`{"type":"object","properties":{"model":{"type":"string","enum":["tripo-v3.1-multiview"]}}}`)

	command := choiceTestCommand(tool)
	chosen, err := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{unfit, declared}}).
		chooseOffering(command, generation.ImageTo3D, nil, modelInput{Views: views})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.Offering.ID != declared.ID || chosen.Source != "default" || chosen.Refusal != "" {
		t.Fatalf("multiview fallback = %+v, want the declared model", chosen)
	}

	refused, err := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{unfit}}).
		chooseOffering(command, generation.ImageTo3D, nil, modelInput{Views: views})
	if err != nil {
		t.Fatal(err)
	}
	if refused.Offering.ID != "" || refused.Refusal == "" {
		t.Fatalf("multiview fallback submitted an Offering that cannot take the views: %+v", refused)
	}
}

func TestModelChoiceOfNamesWhoChoseTheModel(t *testing.T) {
	full := choiceTestOffering(choiceFullID, "tripo-v3.1-full", generation.TextTo3D)
	p2 := choiceTestOffering(choiceP2ID, "tripo-p2", generation.TextTo3D)
	gateway := &choiceTestGateway{offerings: []generation.Offering{full, p2}}
	executor := choiceTestExecutor(gateway)
	command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
	input := json.RawMessage(`{}`)
	if choice := executor.modelChoiceOf(command, full, input); choice == nil || choice.Source != "default" {
		t.Fatalf("default choice = %+v", choice)
	}
	command.Call.Arguments = json.RawMessage(`{"model":"tripo/tripo-p2/text-to-3d"}`)
	if choice := executor.modelChoiceOf(command, p2, input); choice == nil || choice.Source != "agent" {
		t.Fatalf("agent choice = %+v", choice)
	}
	command.Call.Arguments = nil
	command.Options.ProductionBrief = choiceTestBrief("game_character")
	if choice := executor.modelChoiceOf(command, p2, input); choice == nil || choice.Source != "profile" ||
		choice.Profile != "game_character" || choice.Reason != "角色用途需要受控面数与 UV。" {
		t.Fatalf("profile choice = %+v", choice)
	}
	command.Options.ExplicitModel3D = choiceFullID
	if choice := executor.modelChoiceOf(command, full, input); choice == nil || choice.Source != "user" {
		t.Fatalf("locked choice = %+v", choice)
	}
	command.Options.ExplicitModel3D = ""
	command.Options.Multiview = &chat.MultiviewRequest{Model: choiceP2ID}
	if choice := executor.modelChoiceOf(command, p2, input); choice == nil || choice.Source != "user" {
		t.Fatalf("multiview choice = %+v", choice)
	}
	command.Options.Multiview = nil
	// The workspace picker value is below the purpose: with a purpose that
	// recommends p2, a full-model workspace value still reads as profile.
	command.Options.AIModel = choiceFullID
	if choice := executor.modelChoiceOf(command, p2, input); choice == nil || choice.Source != "profile" {
		t.Fatalf("workspace value outranked the purpose = %+v", choice)
	}
	command.Options.ProductionBrief = nil
	if choice := executor.modelChoiceOf(command, full, input); choice == nil || choice.Source != "user" {
		t.Fatalf("workspace fallback choice = %+v", choice)
	}
	command.Options.AIModel = ""
	command.Tool.ResultFields.ModelChoice = ""
	if choice := executor.modelChoiceOf(command, full, input); choice != nil {
		t.Fatalf("a tool without the result key reported %+v", choice)
	}
}

// A mapped panel value and a mapped Agent argument are layers above the
// purpose even when they happen to equal its override.
// A purpose override of a public parameter that is not advised is applied as a
// value, not frozen as an adjustment. Giving it a reason would make
// freezeParameterAdjustments refuse the whole preparation (#103).
func TestPurposeOverrideOfANonAdvisedParameterNeedsNoReason(t *testing.T) {
	faceLimit := generation.Parameter{Name: "face_limit", Type: "number", Integer: true, AdjustmentPolicy: "advised"}
	polycount := generation.Parameter{Name: "target_polycount", Type: "number", Integer: true}
	pose := generation.Parameter{Name: "pose_mode", Type: "select"}
	offering := choiceTestOffering(choiceMeshyID, "meshy-t2.5", generation.TextTo3D, polycount, pose, faceLimit)
	usage := choiceTestUsage()
	usage.Profiles[1].ParameterOverrides[choiceMeshyID] = map[string]json.RawMessage{
		"target_polycount": json.RawMessage(`20000`),
		"pose_mode":        json.RawMessage(`"a-pose"`),
		"face_limit":       json.RawMessage(`20000`),
	}
	executor := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{offering}})
	executor.chat.Model3DUsage = usage
	command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
	command.Options.ProductionBrief = choiceTestBrief("game_character")
	input := json.RawMessage(`{"target_polycount":20000,"pose_mode":"a-pose","face_limit":20000}`)
	reasons, sources, err := executor.parameterReasons(command, offering, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"target_polycount", "pose_mode"} {
		if reasons[name] != "" || sources[name] != "" {
			t.Fatalf("non-advised %s got a reason: %v / %v", name, reasons, sources)
		}
	}
	if reasons["face_limit"] != "角色用途需要受控面数与 UV。" || sources["face_limit"] != "profile:game_character" {
		t.Fatalf("advised face_limit lost its provenance: %v / %v", reasons, sources)
	}
}

func TestMappedPanelAndAgentArgumentsOutrankPurposeProvenance(t *testing.T) {
	pose := generation.Parameter{Name: "pose_mode", Type: "text"}
	faceLimit := generation.Parameter{Name: "face_limit", Type: "number", Integer: true, AdjustmentPolicy: "advised"}
	offering := choiceTestOffering(choiceP2ID, "tripo-p2", generation.TextTo3D, pose, faceLimit)
	usage := choiceTestUsage()
	usage.Profiles[1].ParameterOverrides[choiceP2ID] = map[string]json.RawMessage{
		"pose_mode":  json.RawMessage(`"a-pose"`),
		"face_limit": json.RawMessage(`20000`),
	}
	executor := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{offering}})
	executor.chat.Model3DUsage = usage
	const reason = "角色用途需要受控面数与 UV。"
	input := json.RawMessage(`{"pose_mode":"a-pose","face_limit":20000}`)
	tests := []struct {
		name   string
		mutate func(*chat.ToolCommand)
	}{
		{name: "the person's mapped panel", mutate: func(command *chat.ToolCommand) {
			command.Options.PoseMode = "a-pose"
		}},
		{name: "the Agent's mapped argument", mutate: func(command *chat.ToolCommand) {
			command.Call.Arguments = json.RawMessage(`{"pose_mode":"a-pose"}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
			command.Tool.ArgumentMappings = map[string]string{"pose_mode": "pose_mode"}
			command.Tool.OverrideMappings = map[string]string{"pose_mode": "pose_mode"}
			command.Options.ProductionBrief = choiceTestBrief("game_character")
			test.mutate(&command)
			reasons, sources, err := executor.parameterReasons(command, offering, input)
			if err != nil {
				t.Fatal(err)
			}
			if reasons["pose_mode"] != "" || sources["pose_mode"] != "" {
				t.Fatalf("mapped value credited to the purpose: %v / %v", reasons, sources)
			}
			if reasons["face_limit"] != reason || sources["face_limit"] != "profile:game_character" {
				t.Fatalf("unmapped purpose value lost its provenance: %v / %v", reasons, sources)
			}
		})
	}
}

func TestProfileParameterValuesOnlyApplyPublicParameters(t *testing.T) {
	faceLimit := generation.Parameter{Name: "face_limit", Type: "number", Integer: true}
	fixed := generation.Parameter{Name: "version", Type: "text", Fixed: true}
	offering := choiceTestOffering(choiceP2ID, "tripo-p2", generation.TextTo3D, faceLimit, fixed)
	usage := choiceTestUsage()
	usage.Profiles[1].ParameterOverrides[choiceP2ID]["version"] = json.RawMessage(`"v4"`)
	executor := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{offering}})
	executor.chat.Model3DUsage = usage
	command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
	command.Options.ProductionBrief = choiceTestBrief("game_character")
	profile, values := executor.profileParameterValues(command, offering)
	if profile.ID != "game_character" {
		t.Fatalf("profile = %+v", profile)
	}
	if len(values) != 1 || string(values["face_limit"]) != "20000" {
		t.Fatalf("values = %v, want only the public face_limit", values)
	}
}

func TestPurposeParametersKeepTheirProvenanceOnlyWhenTheySuppliedTheValue(t *testing.T) {
	faceLimit := generation.Parameter{Name: "face_limit", Type: "number", Integer: true, AdjustmentPolicy: "advised"}
	offering := choiceTestOffering(choiceP2ID, "tripo-p2", generation.TextTo3D, faceLimit)
	executor := choiceTestExecutor(&choiceTestGateway{offerings: []generation.Offering{offering}})
	const reason = "角色用途需要受控面数与 UV。"
	baseCommand := func() chat.ToolCommand {
		command := choiceTestCommand(choiceTestTool("create_model_3d", generation.TextTo3D))
		command.Options.ProductionBrief = choiceTestBrief("game_character")
		return command
	}
	input := json.RawMessage(`{"face_limit":20000}`)
	tests := []struct {
		name       string
		mutate     func(*chat.ToolCommand)
		wantReason string
		wantSource string
	}{
		{name: "the purpose supplied the value", mutate: func(*chat.ToolCommand) {},
			wantReason: reason, wantSource: "profile:game_character"},
		{name: "the user's panel supplied the same value", mutate: func(command *chat.ToolCommand) {
			command.Options.Parameters = map[string]json.RawMessage{"face_limit": json.RawMessage(`20000`)}
		}},
		{name: "the Agent's explicit snapshot supplied the same value", mutate: func(command *chat.ToolCommand) {
			command.ExplicitParameters = map[string]json.RawMessage{"face_limit": json.RawMessage(`20000`)}
		}},
		{name: "the Agent gave its own reason", mutate: func(command *chat.ToolCommand) {
			command.Call.Arguments = json.RawMessage(`{"parameter_reasons":{"face_limit":"手调"}}`)
		}, wantReason: "手调"},
		{name: "the value differs", mutate: func(*chat.ToolCommand) {}, wantReason: "", wantSource: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := baseCommand()
			test.mutate(&command)
			current := input
			if test.name == "the value differs" {
				current = json.RawMessage(`{"face_limit":30000}`)
			}
			reasons, sources, err := executor.parameterReasons(command, offering, current)
			if err != nil {
				t.Fatal(err)
			}
			if reasons["face_limit"] != test.wantReason || sources["face_limit"] != test.wantSource {
				t.Fatalf("reasons = %v, sources = %v, want %q / %q", reasons, sources, test.wantReason, test.wantSource)
			}
		})
	}
}

// #239: the same four layers choose a 2D model. The session's explicit 2D
// pick wins, then the Agent's model argument, then the 2D purpose, then the
// workspace pick and the Offering default.
const (
	imageProTextID  = "meshy/nano-banana-pro/text-to-image"
	imageProImageID = "meshy/nano-banana-pro/image-to-image"
	imageGptTextID  = "openai/gpt-image-2/text-to-image"
)

func imageTestOffering(id, capability string) generation.Offering {
	provider, _, _ := strings.Cut(id, "/")
	return generation.Offering{
		ID: generation.OfferingID(id), ProviderID: provider, ModelID: strings.Split(id, "/")[1],
		ModelName: strings.Split(id, "/")[1], Capability: generation.Capability(capability),
		Domain: generation.DomainImage, Enabled: true,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "text", Semantic: generation.SemanticPrompt},
			{Name: "aspect_ratio", Type: "select", Semantic: generation.SemanticOutputSize,
				Options: []generation.ParameterOption{{Value: "1:1"}, {Value: "3:4"}}, Default: json.RawMessage(`"1:1"`)},
			{Name: "remove_background", Type: "boolean", Default: json.RawMessage(`false`)},
		},
	}
}

func imageTestUsage() catalog.ChatImage2DUsageSchema {
	return catalog.ChatImage2DUsageSchema{
		DefaultProfile: "concept_art",
		Profiles: []catalog.ChatImage2DUsageProfile{
			{
				ID: "concept_art", Label: "概念设定图", Description: "概念图。", Reason: "概念图优先构图。",
				RecommendedOfferings: map[string][]string{
					shapeTextToImage: {imageProTextID},
				},
				ParameterOverrides: map[string]map[string]json.RawMessage{
					imageProTextID: {"aspect_ratio": json.RawMessage(`"1:1"`)},
				},
			},
			{
				ID: "illustration", Label: "角色立绘", Description: "立绘。", Reason: "立绘需要竖画幅和稳定身份。",
				RecommendedOfferings: map[string][]string{
					shapeTextToImage: {imageProTextID},
				},
				ParameterOverrides: map[string]map[string]json.RawMessage{
					imageProTextID: {"aspect_ratio": json.RawMessage(`"3:4"`), "remove_background": json.RawMessage(`true`)},
				},
			},
		},
	}
}

func imageTestExecutor(gateway Gateway) *Executor {
	return &Executor{
		gateway: gateway,
		chat: catalog.ChatSchema{
			Model3DUsage: catalog.ChatModel3DUsageSchema{
				Brief: catalog.ChatProductionBriefSchema{Fields: []catalog.ChatProductionBriefField{
					{Name: "purpose", Type: "profile", Label: "用途"},
					{Name: "image_use", Type: "image_profile", Label: "2D 用途"},
					{Name: "style", Type: "text", Label: "风格", MaximumCharacters: 200},
				}},
			},
			Image2DUsage: imageTestUsage(),
			Presentation: catalog.ChatPresentationSchema{Messages: catalog.ChatMessageTexts{
				ModelChoiceConflict:          "模型已锁定为 {locked}，不能改为 {requested}",
				ModelChoiceUnavailable:       "模型 {model} 不可用；可选：{choices}",
				ModelChoiceWorkspaceConflict: "工作区当前选择 {workspace}，用途 {profile} 推荐 {recommended}；请先确认",
			}},
		},
	}
}

func imageTestTool() catalog.ChatToolSchema {
	return catalog.ChatToolSchema{
		Name: "refine_image",
		Execution: catalog.ChatToolExecutionSchema{
			Capabilities: []generation.Capability{generation.ImageToImage, generation.TextToImage},
			Domains:      []string{"image"},
		},
		ModelArgument: "model",
		ResultFields:  catalog.ChatToolResultFields{ModelChoice: "model_choice"},
	}
}

func imageTestCommand(brief chat.ProductionBrief, workspaceModel, explicitImage, named string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner", SessionID: "session", Tool: imageTestTool(),
		Options: chat.TurnOptions{
			CreativeDomain:     "image",
			AIModel:            workspaceModel,
			ProductionBrief:    brief,
			ExplicitModelImage: explicitImage,
		},
		Call: agents.ToolCall{ID: "call-1", Name: "refine_image"},
	}
}

func imageTestBrief(imageUse string) chat.ProductionBrief {
	encoded, _ := json.Marshal(imageUse)
	return chat.ProductionBrief{"image_use": encoded}
}

func TestChooseOfferingFollowsThe2DModelChoiceLayers(t *testing.T) {
	gateway := &choiceTestGateway{offerings: []generation.Offering{
		imageTestOffering(imageProTextID, string(generation.TextToImage)),
		imageTestOffering(imageProImageID, string(generation.ImageToImage)),
		imageTestOffering(imageGptTextID, string(generation.TextToImage)),
	}}
	executor := imageTestExecutor(gateway)
	// The purpose recommends the Pro model; the workspace pick is the same, so
	// the purpose chooses and carries its reason.
	choice, err := executor.chooseOffering(
		imageTestCommand(imageTestBrief("illustration"), "nano-banana-pro", "", ""),
		generation.TextToImage, nil, modelInput{})
	if err != nil || choice.Refusal != "" || choice.Offering.ID != imageProTextID ||
		choice.Source != "profile" || choice.Profile != "illustration" || choice.Reason == "" {
		t.Fatalf("purpose choice = %+v %v", choice, err)
	}
	// A different visible workspace pick is confirmed, not silently replaced.
	choice, err = executor.chooseOffering(
		imageTestCommand(imageTestBrief("illustration"), "gpt-image-2", "", ""),
		generation.TextToImage, nil, modelInput{})
	if err != nil || choice.Refusal == "" {
		t.Fatalf("workspace conflict = %+v %v", choice, err)
	}
	// The Agent's model argument sits above the purpose.
	arguments := imageTestArguments("gpt-image-2")
	choice, err = executor.chooseOffering(
		imageTestCommand(imageTestBrief("illustration"), "", "", ""),
		generation.TextToImage, arguments, modelInput{})
	if err != nil || choice.Refusal != "" || choice.Offering.ID != imageGptTextID || choice.Source != "agent" {
		t.Fatalf("agent choice = %+v %v", choice, err)
	}
	// The explicit 2D pick wins over the Agent and the purpose.
	choice, err = executor.chooseOffering(
		imageTestCommand(imageTestBrief("illustration"), "", "nano-banana-pro", "gpt-image-2"),
		generation.TextToImage, arguments, modelInput{})
	if err != nil || choice.Refusal == "" {
		t.Fatalf("explicit conflict = %+v %v", choice, err)
	}
	choice, err = executor.chooseOffering(
		imageTestCommand(imageTestBrief("illustration"), "", "nano-banana-pro", ""),
		generation.TextToImage, nil, modelInput{})
	if err != nil || choice.Refusal != "" || choice.Offering.ID != imageProTextID || choice.Source != "user" {
		t.Fatalf("locked choice = %+v %v", choice, err)
	}
}

func imageTestArguments(model string) map[string]json.RawMessage {
	encoded, _ := json.Marshal(model)
	return map[string]json.RawMessage{"model": encoded}
}

// #239 review: the image_use description and the system prompt promise that an
// omitted or empty image_use lets the default 2D purpose apply. The default
// purpose must therefore choose the model and freeze its overrides even when
// the brief is empty or carries only other fields.
func TestChooseOfferingFallsBackToTheDefault2DPurpose(t *testing.T) {
	gateway := &choiceTestGateway{offerings: []generation.Offering{
		imageTestOffering(imageProTextID, string(generation.TextToImage)),
		imageTestOffering(imageGptTextID, string(generation.TextToImage)),
	}}
	executor := imageTestExecutor(gateway)
	briefs := map[string]chat.ProductionBrief{
		"no brief":                {},
		"brief without image_use": {"style": json.RawMessage(`"写实"`)},
		"stale image_use":         imageTestBrief("undeclared_purpose"),
	}
	for name, brief := range briefs {
		t.Run(name, func(t *testing.T) {
			command := imageTestCommand(brief, "", "", "")
			choice, err := executor.chooseOffering(command, generation.TextToImage, nil, modelInput{})
			if err != nil || choice.Refusal != "" || choice.Offering.ID != imageProTextID ||
				choice.Source != "profile" || choice.Profile != "concept_art" || choice.Reason == "" {
				t.Fatalf("default purpose choice = %+v %v", choice, err)
			}
			profile, values := executor.purposeParameterValues(command, choice.Offering, nil)
			if profile.ID != "concept_art" || string(values["aspect_ratio"]) != `"1:1"` {
				t.Fatalf("default purpose values = %+v %+v", profile, values)
			}
			input := json.RawMessage(`{"prompt":"x","aspect_ratio":"1:1"}`)
			if named := executor.modelChoiceOf(command, choice.Offering, input); named == nil ||
				named.Source != "profile" || named.Profile != "concept_art" || named.Reason == "" {
				t.Fatalf("default purpose model choice = %+v", named)
			}
		})
	}
	// A declared image_use still wins over the default.
	choice, err := executor.chooseOffering(
		imageTestCommand(imageTestBrief("illustration"), "", "", ""),
		generation.TextToImage, nil, modelInput{})
	if err != nil || choice.Refusal != "" || choice.Profile != "illustration" || choice.Source != "profile" {
		t.Fatalf("declared purpose choice = %+v %v", choice, err)
	}
}

func TestImagePurposeOverridesFreezeIntoTheInput(t *testing.T) {
	executor := imageTestExecutor(&choiceTestGateway{})
	command := imageTestCommand(imageTestBrief("illustration"), "", "", "")
	offering := imageTestOffering(imageProTextID, string(generation.TextToImage))
	profile, values := executor.purposeParameterValues(command, offering, nil)
	if profile.ID != "illustration" || string(values["aspect_ratio"]) != `"3:4"` || string(values["remove_background"]) != `true` {
		t.Fatalf("purpose values = %+v %+v", profile, values)
	}
	// A 2D purpose never contributes a triangle budget.
	if _, exists := values["face_limit"]; exists {
		t.Fatalf("2D purpose contributed a 3D budget: %+v", values)
	}
}

func TestModelChoiceOfNamesTheImageSource(t *testing.T) {
	executor := imageTestExecutor(&choiceTestGateway{offerings: []generation.Offering{
		imageTestOffering(imageProTextID, string(generation.TextToImage)),
	}})
	input := json.RawMessage(`{"prompt":"x","aspect_ratio":"3:4","remove_background":true}`)
	offering := imageTestOffering(imageProTextID, string(generation.TextToImage))
	command := imageTestCommand(imageTestBrief("illustration"), "", "", "")
	choice := executor.modelChoiceOf(command, offering, input)
	if choice == nil || choice.Source != "profile" || choice.Profile != "illustration" || choice.Reason == "" {
		t.Fatalf("profile model choice = %+v", choice)
	}
	command.Options.ExplicitModelImage = "nano-banana-pro"
	if choice := executor.modelChoiceOf(command, offering, input); choice == nil || choice.Source != "user" {
		t.Fatalf("locked model choice = %+v", choice)
	}
}
