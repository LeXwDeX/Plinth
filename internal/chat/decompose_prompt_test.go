package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

const decomposeBlockHead = "【场景图拆解】"

// decomposePromptCase is one session shape rendered through a whole turn.
type decomposePromptCase struct {
	// project assigns the session to a project; an asset list lives in one.
	project bool
	// picture adds one uploaded picture to the user message.
	picture bool
	// assetList wires the list workflow. The shipped Schema declares
	// application.asset_list.decompose.
	assetList bool
	// automatic turns application.asset_list.execution on; the shipped
	// Schema keeps it off (#494).
	automatic bool
}

// decomposePromptSystem runs one turn with the shipped Chat Schema and
// returns the system context the model read on its first request.
func decomposePromptSystem(t *testing.T, testCase decomposePromptCase) string {
	t.Helper()
	schema := configuredChatSchema(t)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, nil)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if testCase.project {
		stored, found, err := repository.Session(ctx, "owner-a", session.ID)
		if err != nil || !found {
			t.Fatalf("session = %+v found=%v err=%v", stored, found, err)
		}
		stored.ProjectID = "prj_1"
		repository.sessions[session.ID] = stored
	}
	if testCase.assetList {
		listSchema := defaultAssetListSchema(t)
		if testCase.automatic {
			listSchema = automaticAssetListSchema(t, listSchema)
		}
		listService, err := assetlist.New(assetlist.Config{
			Repository:      newAssetListRepo(),
			Schema:          listSchema,
			ProjectMaxItems: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		service.SetAssetLists(chat.AssetListPolicy{Service: listService, Schema: listSchema})
	}
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: &scopeToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	options := chat.TurnOptions{CreativeDomain: "model-3d"}
	start := chat.StartTurn{
		OwnerID: "owner-a", SessionID: session.ID,
		Content: "把图里的东西都做出来", Options: options,
	}
	if testCase.picture {
		start.ImageURLs = []string{"data:image/png;base64,QQ=="}
	}
	assistant, err := service.StartTurn(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID,
		AssistantMessageID: assistant.ID, Options: turn.Options,
	}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) == 0 {
		t.Fatal("the Agent was never called")
	}
	return agent.requests[0].Messages[0].Content
}

// #331: a project turn with an uploaded picture and the list workflow carries
// the decomposition guidance, with the per-image bound filled in.
func TestDecomposePromptReachesAPictureTurnInAProject(t *testing.T) {
	system := decomposePromptSystem(t, decomposePromptCase{project: true, picture: true, assetList: true})
	for _, want := range []string{
		decomposeBlockHead,
		"inspect_image",
		"crop_image_regions",
		"propose_asset_list",
		"reference_input_asset_ids",
		"同一张图最多拆 24 项",
		"不画概念图，也不先问数量",
		"不得让裁图直接作为 3D 输入",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context omits %q", want)
		}
	}
	if strings.Count(system, decomposeBlockHead) != 1 {
		t.Errorf("the decompose block appears %d times", strings.Count(system, decomposeBlockHead))
	}
}

// #331: the guidance is only where it can act: a session outside a project, a
// text-only turn and a deployment without the list workflow all leave it out.
func TestDecomposePromptIsOmittedWhereItCannotAct(t *testing.T) {
	for name, testCase := range map[string]decomposePromptCase{
		"session outside a project": {picture: true, assetList: true},
		"text-only turn":            {project: true, assetList: true},
		"no list workflow":          {project: true, picture: true},
	} {
		system := decomposePromptSystem(t, testCase)
		if strings.Contains(system, decomposeBlockHead) {
			t.Errorf("%s: the decompose block is present", name)
		}
	}
}

// #508 asset-list-01: with automatic production off (the shipped default), a
// confirmed list starts nothing. The decompose guidance then ends with the
// Schema's manual-production note, so the Agent makes the items itself after
// the confirmation instead of waiting for a run that never starts. With
// production on, the note is absent.
func TestDecomposePromptSaysHowAConfirmedListIsProduced508(t *testing.T) {
	manual := defaultAssetListSchema(t).Messages.ManualProduction
	if manual == "" {
		t.Fatal("the shipped Schema declares no manual-production note")
	}
	system := decomposePromptSystem(t, decomposePromptCase{project: true, picture: true, assetList: true})
	if !strings.Contains(system, manual) {
		t.Errorf("the decompose block omits the manual-production note")
	}
	system = decomposePromptSystem(t, decomposePromptCase{
		project: true, picture: true, assetList: true, automatic: true,
	})
	if !strings.Contains(system, decomposeBlockHead) || strings.Contains(system, manual) {
		t.Errorf("automatic production: decompose block present=%v, manual note present=%v",
			strings.Contains(system, decomposeBlockHead), strings.Contains(system, manual))
	}
}

// automaticAssetListSchema turns automatic production on in a copy of the
// shipped Schema.
func automaticAssetListSchema(t *testing.T, schema catalog.AssetListSchema) catalog.AssetListSchema {
	t.Helper()
	if schema.Execution == nil {
		t.Fatal("the shipped Schema declares no execution section")
	}
	execution := *schema.Execution
	execution.Enabled = true
	schema.Execution = &execution
	return schema
}
