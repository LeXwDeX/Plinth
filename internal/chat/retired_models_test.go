package chat_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

var meshyRetired = []catalog.RetiredModelSuccessor{{
	ProviderID: "meshy", ModelID: "meshy-7", ModelName: "Meshy 7",
	SuccessorModelID: "meshy-7.1", SuccessorModelName: "Meshy 7.1",
}}

// #329 follow-up C: a turn that names a retired model (an old tab, an API
// client) is moved to the successor on the server, for the domain in view and
// for the other domains, and the turn records the switch so its generation
// step can say so. The session then remembers the successor.
func TestStartTurnMovesRetiredModelsToTheirSuccessor(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.RetiredModels = meshyRetired
	})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "Retired")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello",
		Options: chat.TurnOptions{CreativeDomain: "model-3d", AIModel: "meshy-7",
			DomainModels: map[string]string{"model-3d": "meshy-7", "image": "image-model"}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Options.AIModel != "meshy-7.1" || turn.Options.DomainModels["model-3d"] != "meshy-7.1" || turn.Options.DomainModels["image"] != "image-model" {
		t.Fatalf("stored models = %q %v", turn.Options.AIModel, turn.Options.DomainModels)
	}
	want := []chat.RetiredModelSwitch{{Domain: "model-3d", From: "meshy-7", FromName: "Meshy 7", To: "meshy-7.1", ToName: "Meshy 7.1"}}
	if !reflect.DeepEqual(turn.Options.RetiredModels, want) {
		t.Fatalf("recorded switches = %+v", turn.Options.RetiredModels)
	}
	models, retired, err := service.SessionModels(ctx, "owner-a", session.ID)
	if err != nil || models["model-3d"] != "meshy-7.1" || models["image"] != "image-model" || len(retired) != 1 || retired["model-3d"] != "meshy-7" {
		t.Fatalf("session models = %v, retired %v, %v", models, retired, err)
	}
}

// A turn stored before the model was retired keeps its frozen options, but
// the session's remembered pick is read as the successor.
func TestSessionModelsReadRetiredPicksAsTheirSuccessor(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.RetiredModels = meshyRetired
	})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "Old")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	repository.turns["old"] = chat.Turn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: "old",
		Options:   chat.TurnOptions{CreativeDomain: "image", AIModel: "image-model", DomainModels: map[string]string{"model-3d": "meshy-7"}},
		CreatedAt: at, UpdatedAt: at}
	models, retired, err := service.SessionModels(ctx, "owner-a", session.ID)
	if err != nil || models["model-3d"] != "meshy-7.1" || models["image"] != "image-model" || len(retired) != 1 || retired["model-3d"] != "meshy-7" {
		t.Fatalf("session models = %v, retired %v, %v", models, retired, err)
	}
	if repository.turns["old"].Options.DomainModels["model-3d"] != "meshy-7" {
		t.Fatal("the frozen turn was rewritten")
	}
}
