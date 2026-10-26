package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestPickCandidateIsVerifiedAndOwnerScoped(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{}}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"task-1": true, "asset-1": true, "capture-1": true}
	candidate := chat.AttachmentRef{TaskID: "task-1", AssetID: "asset-1", URL: "/api/v1/tasks/task-1/assets/asset-1"}

	if pick, ok, err := service.SessionPick(ctx, "owner-a", session.ID); err != nil || ok {
		t.Fatalf("a new session has no pick: %+v %v %v", pick, ok, err)
	}
	picked, err := service.PickCandidate(ctx, "owner-a", session.ID, candidate)
	if err != nil || picked.TaskID != "task-1" || picked.AssetID != "asset-1" || picked.Kind != chat.AttachmentKindImage {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	if stored, ok, err := service.SessionPick(ctx, "owner-a", session.ID); err != nil || !ok || stored.AssetID != "asset-1" {
		t.Fatalf("stored pick = %+v %v %v", stored, ok, err)
	}
	// Everything that is not a generated picture of this owner's session is refused.
	for name, ref := range map[string]chat.AttachmentRef{
		"foreign task":   {TaskID: "task-9", AssetID: "asset-9"},
		"an upload":      {URL: "data:image/png;base64,QQ=="},
		"a capture":      {CaptureID: "capture-1"},
		"no asset given": {TaskID: "task-1"},
	} {
		if _, err := service.PickCandidate(ctx, "owner-a", session.ID, ref); !errors.Is(err, chat.ErrPickRejected) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	// A refused claim does not replace the existing pick.
	if stored, _, _ := service.SessionPick(ctx, "owner-a", session.ID); stored.AssetID != "asset-1" {
		t.Fatalf("a refused pick changed the stored one: %+v", stored)
	}
	// Another owner cannot read, write or clear it, and cannot tell it exists.
	if _, _, err := service.SessionPick(ctx, "owner-b", session.ID); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign read err = %v", err)
	}
	if _, err := service.PickCandidate(ctx, "owner-b", session.ID, candidate); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign pick err = %v", err)
	}
	if err := service.ClearSessionPick(ctx, "owner-b", session.ID); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign clear err = %v", err)
	}
	if err := service.ClearSessionPick(ctx, "owner-a", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := service.SessionPick(ctx, "owner-a", session.ID); ok {
		t.Fatal("clearing did not forget the pick")
	}
}

func TestPickCandidateRefusesThePicturesOfA3DModelAndSessionPickDropsAStoredOne(t *testing.T) {
	// task-3d is a text-to-3D result: its render and the picture the provider
	// drew before modelling both preview the model. task-2d is a 2D result.
	verifier := &recordingVerifier{
		held:   map[string]map[string]bool{},
		models: map[string]bool{"task-3d": true},
	}
	service, repository, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{
		"task-2d": true, "asset-2d": true, "task-3d": true, "asset-render": true, "asset-drawn": true,
	}
	render := chat.AttachmentRef{TaskID: "task-3d", AssetID: "asset-render", URL: "/api/v1/tasks/task-3d/assets/asset-render"}
	drawn := chat.AttachmentRef{TaskID: "task-3d", AssetID: "asset-drawn"}
	flat := chat.AttachmentRef{TaskID: "task-2d", AssetID: "asset-2d"}

	// A model preview is not a 2D candidate, whichever of its pictures is claimed.
	for name, ref := range map[string]chat.AttachmentRef{"render": render, "drawn picture": drawn} {
		if _, err := service.PickCandidate(ctx, "owner-a", session.ID, ref); !errors.Is(err, chat.ErrPickRejected) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if pick, ok, err := service.SessionPick(ctx, "owner-a", session.ID); err != nil || ok {
		t.Fatalf("a refused pick was stored: %+v %v %v", pick, ok, err)
	}

	// A normal 2D picture is still picked, and a later refused claim keeps it.
	if picked, err := service.PickCandidate(ctx, "owner-a", session.ID, flat); err != nil || picked.TaskID != "task-2d" {
		t.Fatalf("2D pick = %+v, %v", picked, err)
	}
	if _, err := service.PickCandidate(ctx, "owner-a", session.ID, render); !errors.Is(err, chat.ErrPickRejected) {
		t.Fatalf("render over a 2D pick: err = %v", err)
	}
	if stored, ok, err := service.SessionPick(ctx, "owner-a", session.ID); err != nil || !ok || stored.TaskID != "task-2d" {
		t.Fatalf("a refused pick changed the stored one: %+v %v %v", stored, ok, err)
	}

	// A pick stored before such pictures were refused is ignored, never trusted,
	// and does not become the selection of the next turn.
	if found, err := repository.SetSessionPick(ctx, "owner-a", session.ID, chat.Attachment{
		Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-3d", AssetID: "asset-render",
	}); err != nil || !found {
		t.Fatalf("seed stored pick: %v %v", found, err)
	}
	if pick, ok, err := service.SessionPick(ctx, "owner-a", session.ID); err != nil || ok || pick.TaskID != "" {
		t.Fatalf("a stored model preview pick was returned: %+v %v %v", pick, ok, err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "go on"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Options.Selection) != 0 || turn.Options.SelectionPicked {
		t.Fatalf("a stored model preview pick was used as the selection: %+v", turn.Options.Selection)
	}
}

func TestPickedCandidateIsTheSelectionOfLaterTurnsUntilTheClientSelectsSomething(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{}}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateSession(ctx, "owner-a", "Owner", "other")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"task-1": true, "asset-1": true, "task-2": true, "asset-2": true}
	verifier.held["owner-a/"+other.ID] = map[string]bool{"task-1": true, "asset-1": true}
	if _, err := service.PickCandidate(ctx, "owner-a", session.ID, chat.AttachmentRef{TaskID: "task-1", AssetID: "asset-1"}); err != nil {
		t.Fatal(err)
	}

	finish := func(sessionID string, messageID string) {
		t.Helper()
		if err := service.FinishTurn(ctx, messageID, false); err != nil {
			t.Fatal(err)
		}
		if err := service.FinishStream(ctx, sessionID, messageID, false); err != nil {
			t.Fatal(err)
		}
	}
	// No selection sent: the pick is the current object and gets a handle.
	first, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "make it bigger"})
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := service.GetTurn(ctx, "owner-a", session.ID, first.ID)
	if len(turn.Options.Selection) != 1 || turn.Options.Selection[0].AssetID != "asset-1" ||
		turn.Options.Selection[0].Handle == "" || !turn.Options.SelectionPicked {
		t.Fatalf("selection = %+v picked=%v", turn.Options.Selection, turn.Options.SelectionPicked)
	}
	handle := turn.Options.Selection[0].Handle
	finish(session.ID, first.ID)

	// An explicit selection wins for that turn, and is not marked as picked.
	second, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "this one",
		Selection: []chat.AttachmentRef{{Kind: chat.AttachmentKindImage, TaskID: "task-2", AssetID: "asset-2", URL: "/api/v1/tasks/task-2/assets/asset-2"}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, _ = service.GetTurn(ctx, "owner-a", session.ID, second.ID)
	if len(turn.Options.Selection) != 1 || turn.Options.Selection[0].AssetID != "asset-2" || turn.Options.SelectionPicked {
		t.Fatalf("explicit selection = %+v picked=%v", turn.Options.Selection, turn.Options.SelectionPicked)
	}
	finish(session.ID, second.ID)

	// The pick persists: with nothing selected the next turn uses it again, with the same handle.
	third, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "again"})
	if err != nil {
		t.Fatal(err)
	}
	turn, _ = service.GetTurn(ctx, "owner-a", session.ID, third.ID)
	if len(turn.Options.Selection) != 1 || turn.Options.Selection[0].Handle != handle || !turn.Options.SelectionPicked {
		t.Fatalf("pick did not persist: %+v", turn.Options.Selection)
	}
	finish(session.ID, third.ID)

	// The pick belongs to its session: another session of the same owner is unaffected.
	fourth, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: other.ID, Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	turn, _ = service.GetTurn(ctx, "owner-a", other.ID, fourth.ID)
	if len(turn.Options.Selection) != 0 || turn.Options.SelectionPicked {
		t.Fatalf("pick leaked into another session: %+v", turn.Options.Selection)
	}

	// A pick that no longer verifies is ignored instead of being trusted.
	delete(verifier.held["owner-a/"+session.ID], "task-1")
	fifth, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	turn, _ = service.GetTurn(ctx, "owner-a", session.ID, fifth.ID)
	if len(turn.Options.Selection) != 0 || turn.Options.SelectionPicked {
		t.Fatalf("a stale pick was used: %+v", turn.Options.Selection)
	}
}

func TestSelectionPromptTellsTheAgentWhenTheSelectionIsThePickedCandidate(t *testing.T) {
	policy, schema := handlePolicyForTest(t)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) { config.Handles = policy })
	schema.Stream.EventFields.Type = "configured-type"
	schema.Stream.EventFields.Content = "configured-content"
	schema.Stream.EventTypes.Text = "configured-text"
	schema.Stream.EventTypes.Done = "configured-done"
	schema.Roles.System, schema.Roles.User, schema.Roles.Assistant, schema.Roles.Tool =
		"configured-system", "configured-user", "configured-assistant", "configured-tool"
	schema.State.PendingContent = "configured-pending"
	schema.Defaults.Title = "configured default title"
	schema.Agent.SystemPrompt = "base prompt"
	schema.Agent.DomainContext = map[string]string{}
	schema.Context.SelectionPickedTemplate = "\n[picked-by-user]"
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}, {Content: "ok"}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	for index, picked := range []bool{true, false} {
		assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "go"})
		if err != nil {
			t.Fatal(err)
		}
		repository.mu.Lock()
		turn := repository.turns[assistant.ID]
		turn.Options.Selection = []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-1"}}
		turn.Options.SelectionPicked = picked
		repository.turns[assistant.ID] = turn
		repository.mu.Unlock()
		if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
			t.Fatal(err)
		}
		system := agent.requests[index].Messages[0].Content
		if got := strings.Contains(system, "[picked-by-user]"); got != picked {
			t.Fatalf("turn %d picked=%v but prompt contains note=%v: %q", index, picked, got, system)
		}
		if !strings.Contains(system, "@img1") {
			t.Fatalf("the selection block must list the object: %q", system)
		}
	}
}
