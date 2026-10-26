package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// recordTurn stores one finished turn with the given options, as a send
// would, so the session read can project its models.
func recordTurn(t *testing.T, repository *sqlite.Repository, owner, session, id string, at time.Time, options chat.TurnOptions) {
	t.Helper()
	ctx := context.Background()
	user := chat.Message{ID: id + "-user", SessionID: session, Role: "user", Content: "draw", CreatedAt: at}
	assistant := chat.Message{ID: id, SessionID: session, Role: "assistant", Content: "__GENERATING__", CreatedAt: at}
	if _, _, err := repository.StartTurn(ctx, owner, user, assistant,
		chat.Event{SessionID: session, Data: map[string]any{"type": "start"}, CreatedAt: at},
		chat.Stream{AssistantMessageID: id, SessionID: session, Status: "active"},
		chat.Turn{OwnerID: owner, SessionID: session, AssistantMessageID: id, Options: options, State: "running", CreatedAt: at, UpdatedAt: at},
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateTurnState(ctx, id, "done", at); err != nil {
		t.Fatal(err)
	}
	if err := repository.FinishStream(ctx, session, id, "done", at); err != nil {
		t.Fatal(err)
	}
}

func sessionModels(t *testing.T, handler http.Handler, cookie *http.Cookie, session string) (int, map[string]any, bool) {
	t.Helper()
	response := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions/"+session, "", "")
	var body map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	models, present := body["generationModels"].(map[string]any)
	return response.Code, models, present
}

// #318: a session read returns the model last sent in each workspace domain,
// taken from the frozen options of its recent turns, so a new browser can
// restore the picker the person used in that session.
func TestChatSessionReadRestoresTheModelsLastSentPerDomain(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	if code, _, present := sessionModels(t, handler, cookie, sessionID); code != http.StatusOK || present {
		t.Fatalf("a session without turns: %d present=%v", code, present)
	}
	base := time.Now().UTC().Add(-time.Hour)
	// Turn 1: 2D in view, a 3D model picked too.
	recordTurn(t, repository, "owner-a", sessionID, "turn-1", base, chat.TurnOptions{
		CreativeDomain: "image", AIModel: "image-model-old", DomainModels: map[string]string{"model-3d": "model-3d-a"},
	})
	// Turn 2: 3D in view with a newer 3D model; no 2D model this time.
	recordTurn(t, repository, "owner-a", sessionID, "turn-2", base.Add(time.Minute), chat.TurnOptions{
		CreativeDomain: "model-3d", AIModel: "model-3d-b",
	})
	// Turn 3: 2D in view with a newer 2D model.
	recordTurn(t, repository, "owner-a", sessionID, "turn-3", base.Add(2*time.Minute), chat.TurnOptions{
		CreativeDomain: "image", AIModel: "image-model-new",
	})
	code, models, present := sessionModels(t, handler, cookie, sessionID)
	if code != http.StatusOK || !present || len(models) != 2 || models["image"] != "image-model-new" || models["model-3d"] != "model-3d-b" {
		t.Fatalf("session models = %d %v", code, models)
	}
	// Another owner's session stays a 404 and leaks nothing.
	now := time.Now().UTC()
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "foreign-models", OwnerID: "owner-b",
		OwnerName: "Other", Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	recordTurn(t, repository, "owner-b", "foreign-models", "turn-b", now, chat.TurnOptions{CreativeDomain: "image", AIModel: "secret-model"})
	if code, models, _ := sessionModels(t, handler, cookie, "foreign-models"); code != http.StatusNotFound || models != nil {
		t.Fatalf("foreign session = %d %v", code, models)
	}
}
