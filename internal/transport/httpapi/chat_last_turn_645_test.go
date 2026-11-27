package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// seedChoiceReply stores the assistant message a test's card belongs to: a
// card is read only while the reply that asked it exists (#645).
func seedChoiceReply(t *testing.T, repository *sqlite.Repository, sessionID, id string) {
	t.Helper()
	seedChatMessage(t, repository, sessionID, id, "assistant", time.Now().UTC())
}

func seedChatMessage(t *testing.T, repository *sqlite.Repository, sessionID, id, role string, at time.Time) {
	t.Helper()
	if _, err := repository.InsertMessageIfAbsent(context.Background(), "owner-a",
		chat.Message{ID: id, SessionID: sessionID, Role: role, Content: id, CreatedAt: at}, at); err != nil {
		t.Fatal(err)
	}
}

func lastTurnDeletePath(sessionID, messageID string, lastTurn ...string) string {
	query := url.Values{}
	for _, id := range lastTurn {
		query.Add("lastTurn", id)
	}
	return "/api/chat/sessions/" + sessionID + "/messages/" + messageID + "?" + query.Encode()
}

// #645 A: "regenerate" on a page that missed a newer turn names an older turn.
// The server deletes a last-turn message only when the named messages are
// exactly the session's last turn now; otherwise it answers 409 with the
// Schema's words and deletes nothing. B: the deleted reply's card goes with it.
func TestReplacingTheLastTurnRefusesAnOutOfDatePage_645(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	start := time.Now().UTC().Add(-time.Hour)
	for index, message := range [][2]string{
		{"user-645-1", "user"}, {"reply-645-1", "assistant"}, {"user-645-2", "user"}, {"reply-645-2", "assistant"},
	} {
		seedChatMessage(t, repository, sessionID, message[0], message[1], start.Add(time.Duration(index)*time.Minute))
	}
	if _, err := repository.CreateChoice(context.Background(), chat.Choice{OwnerID: "owner-a", SessionID: sessionID,
		AssistantMessageID: "reply-645-2", CallID: "call-645", Kind: "next_step", Capability: "next_step", StepIndex: -1,
		Options: []chat.ChoiceOption{{ID: "reupload", Label: "换一张图重新上传", Arguments: json.RawMessage(`{}`)},
			{ID: "split", Label: "拆成散图", Arguments: json.RawMessage(`{}`)}}, CreatedAt: start.Add(4 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	stale := grantTestRequest(t, handler, cookie, http.MethodDelete,
		lastTurnDeletePath(sessionID, "reply-645-1", "reply-645-1", "user-645-1"), "", "")
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "对话已有更新，请刷新后再试") {
		t.Fatalf("stale replacement = %d %s", stale.Code, stale.Body.String())
	}
	messages, err := repository.ListMessages(context.Background(), "owner-a", sessionID)
	if err != nil || len(messages) != 4 {
		t.Fatalf("a refused replacement deleted messages: %d %v", len(messages), err)
	}

	for _, step := range [][]string{{"reply-645-2", "reply-645-2", "user-645-2"}, {"user-645-2", "user-645-2"}} {
		response := grantTestRequest(t, handler, cookie, http.MethodDelete, lastTurnDeletePath(sessionID, step[0], step[1:]...), "", "")
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete %s = %d %s", step[0], response.Code, response.Body.String())
		}
	}
	messages, _ = repository.ListMessages(context.Background(), "owner-a", sessionID)
	if len(messages) != 2 || messages[0].ID != "user-645-1" || messages[1].ID != "reply-645-1" {
		t.Fatalf("after replacing the last turn = %+v", messages)
	}
	read := grantTestRequest(t, handler, cookie, http.MethodGet, choicesPath(sessionID), "", "")
	if read.Code != http.StatusOK || strings.Contains(read.Body.String(), "call-645") || strings.Contains(read.Body.String(), "reupload") {
		t.Fatalf("the deleted reply's card is still listed: %d %s", read.Code, read.Body.String())
	}
	// A plain deletion (no lastTurn) keeps its old behaviour.
	if plain := grantTestRequest(t, handler, cookie, http.MethodDelete,
		"/api/chat/sessions/"+sessionID+"/messages/user-645-1", "", ""); plain.Code != http.StatusNoContent {
		t.Fatalf("plain delete = %d %s", plain.Code, plain.Body.String())
	}
}
