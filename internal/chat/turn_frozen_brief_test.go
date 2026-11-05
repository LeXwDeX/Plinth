package chat_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #103: the production brief and the explicit 3D model pick are frozen with
// the turn when it starts. The first run of the turn is scheduled with the
// request's options, which carry neither; the tools must still see the frozen
// values, or an Agent-named model replaces the person's locked pick.
func TestFirstRunOfATurnUsesTheBriefAndModelLockFrozenWithIt(t *testing.T) {
	service, rig := newReviewOrchestrator(t, true)
	tools := &reviewPictureTools{}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: rig.schema, Model: "m", Agent: rig.agent, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetExplicitModel3D(ctx, "owner-a", session.ID, "rodin-gen-2.5"); err != nil {
		t.Fatal(err)
	}
	brief := chat.ProductionBrief{"purpose": json.RawMessage(`"game_prop"`)}
	if _, _, err := rig.repository.ReplaceSessionBrief(ctx, "owner-a", session.ID, nil, brief); err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "make a stool"})
	if err != nil {
		t.Fatal(err)
	}
	// A pick changed after the turn started belongs to the next turn.
	if _, err := service.SetExplicitModel3D(ctx, "owner-a", session.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
		Options: chat.TurnOptions{},
	}); err != nil {
		t.Fatal(err)
	}
	if len(tools.commands) == 0 {
		t.Fatal("no tool ran")
	}
	options := tools.commands[0].Options
	if options.ExplicitModel3D != "rodin-gen-2.5" {
		t.Fatalf("tool saw explicit 3D model %q, want the one frozen with the turn", options.ExplicitModel3D)
	}
	if !reflect.DeepEqual(options.ProductionBrief, brief) {
		t.Fatalf("tool saw brief %s, want the one frozen with the turn", options.ProductionBrief)
	}
}
