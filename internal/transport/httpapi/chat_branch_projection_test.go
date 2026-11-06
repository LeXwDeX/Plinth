package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestChatSessionProjectsBranchLineage(t *testing.T) {
	module, handler, cookie, ordinaryID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	now := time.Now().UTC()
	_, err := repository.CreateSession(context.Background(), chat.Session{
		ID: "branch", OwnerID: "owner-a", OwnerName: "owner", Title: "Branch",
		CreatedAt: now, UpdatedAt: now, ParentSessionID: "parent", ParentMessageID: "message",
		ParentTitle: "Original", ParentDeleted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions/branch", "", "")
	body := decodeMap(t, response.Body.String())
	if response.Code != http.StatusOK || body["parent_session_id"] != "parent" || body["parent_message_id"] != "message" || body["parent_title"] != "Original" || body["parent_deleted"] != true {
		t.Fatalf("branch lineage: %d %s", response.Code, response.Body.String())
	}
	list := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions", "", "")
	var sessions []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &sessions); err != nil || list.Code != http.StatusOK {
		t.Fatalf("session list: %d %s err=%v", list.Code, list.Body.String(), err)
	}
	var branch map[string]any
	for _, session := range sessions {
		if session["id"] == "branch" {
			branch = session
		}
	}
	if branch["parent_title"] != "Original" || branch["parent_deleted"] != true {
		t.Fatalf("branch in list: %+v", branch)
	}
	ordinary := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions/"+ordinaryID, "", "")
	if _, exists := decodeMap(t, ordinary.Body.String())["parent_session_id"]; ordinary.Code != http.StatusOK || exists {
		t.Fatalf("ordinary session: %d %s", ordinary.Code, ordinary.Body.String())
	}
	foreign := grantTestRequest(t, handler, nil, http.MethodGet, "/api/chat/sessions/branch", "", "")
	if foreign.Code == http.StatusOK {
		t.Fatalf("anonymous branch read: %d %s", foreign.Code, foreign.Body.String())
	}
}
