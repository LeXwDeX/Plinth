package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func choicesPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/choices"
}

// #321: the session's choice cards are read by their owner only, carry no
// next-call arguments, and a turn answers one exactly once with an option the
// card offers.
func TestChatChoiceCardsAreOwnerScopedAndAnsweredOnce(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	ctx := context.Background()
	now := time.Now().UTC()
	options := []chat.ChoiceOption{
		{ID: "hyper3d/bang/local-model", ProviderName: "Hyper3D", ModelName: "Bang", Arguments: json.RawMessage(`{"offering_id":"hyper3d/bang/local-model","secret_next":"x"}`)},
		{ID: "hyper3d/bang/rodin-task", ProviderName: "Hyper3D", ModelName: "Bang Rodin", Arguments: json.RawMessage(`{"offering_id":"hyper3d/bang/rodin-task"}`)},
	}
	card, err := repository.CreateChoice(ctx, chat.Choice{OwnerID: "owner-a", SessionID: sessionID, AssistantMessageID: "turn-1",
		CallID: "call-1", ToolName: "process_model", Capability: "mesh-segmentation", StepIndex: -1, Options: options,
		DefaultOptionID: options[0].ID, AllowDefault: true, DefaultArguments: json.RawMessage(`{"use_default_offering":true}`), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "foreign-session", OwnerID: "owner-b", OwnerName: "B", Title: "b", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	read := grantTestRequest(t, handler, cookie, http.MethodGet, choicesPath(sessionID), "", "")
	if read.Code != http.StatusOK || strings.Contains(read.Body.String(), "arguments") || strings.Contains(read.Body.String(), "secret_next") {
		t.Fatalf("list = %d %s", read.Code, read.Body.String())
	}
	var listed struct {
		Choices []struct {
			ID              string `json:"id"`
			State           string `json:"state"`
			DefaultOptionID string `json:"default_option_id"`
			AllowDefault    bool   `json:"allow_default"`
			Options         []struct {
				ID           string `json:"id"`
				ProviderName string `json:"provider_name"`
			} `json:"options"`
			Answer *struct {
				OptionID string `json:"option_id"`
				Source   string `json:"source"`
			} `json:"answer"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(read.Body.Bytes(), &listed)
	if len(listed.Choices) != 1 || listed.Choices[0].ID != card.ID || listed.Choices[0].State != "pending" ||
		len(listed.Choices[0].Options) != 2 || !listed.Choices[0].AllowDefault || listed.Choices[0].Answer != nil {
		t.Fatalf("listed = %+v", listed)
	}
	if foreign := grantTestRequest(t, handler, cookie, http.MethodGet, choicesPath("foreign-session"), "", ""); foreign.Code != http.StatusNotFound {
		t.Fatalf("a foreign session's cards = %d %s", foreign.Code, foreign.Body.String())
	}

	send := func(answer string) int {
		body := `{"content":"选择","choiceAnswer":` + answer + `}`
		return grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions/"+sessionID+"/messages", body, "").Code
	}
	for _, malformed := range []string{`{"choice_id":"` + card.ID + `"}`, `{"choice_id":"` + card.ID + `","option_id":"a","use_default":true}`, `"x"`} {
		if code := send(malformed); code != http.StatusBadRequest {
			t.Fatalf("malformed answer %s = %d", malformed, code)
		}
	}
	if code := send(`{"choice_id":"` + card.ID + `","option_id":"tripo/not-offered"}`); code != http.StatusBadRequest {
		t.Fatalf("an option the card does not offer = %d", code)
	}
	if code := send(`{"choice_id":"choice_unknown","option_id":"hyper3d/bang/rodin-task"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown card = %d", code)
	}
	if code := send(`{"choice_id":"` + card.ID + `","option_id":"hyper3d/bang/rodin-task"}`); code != http.StatusAccepted {
		t.Fatalf("a valid answer = %d", code)
	}
	stored, err := repository.GetChoice(ctx, "owner-a", sessionID, card.ID)
	if err != nil || stored.State != chat.ChoiceAnswered || stored.AnswerOptionID != "hyper3d/bang/rodin-task" ||
		stored.AnswerSource != chat.ChoiceAnsweredOnCard || stored.AnswerMessageID == "" {
		t.Fatalf("stored = %+v %v", stored, err)
	}
	reread := grantTestRequest(t, handler, cookie, http.MethodGet, choicesPath(sessionID), "", "")
	_ = json.Unmarshal(reread.Body.Bytes(), &listed)
	if listed.Choices[0].State != "answered" || listed.Choices[0].Answer == nil || listed.Choices[0].Answer.OptionID != "hyper3d/bang/rodin-task" ||
		listed.Choices[0].Answer.Source != "card" {
		t.Fatalf("answered card = %s", reread.Body.String())
	}
}
