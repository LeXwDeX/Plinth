package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestBranchSessionHTTP(t *testing.T) {
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture")
	}
	repo := value.(*fixtureExecutionState).repository
	now := time.Now().UTC()
	_, err := repo.InsertMessageIfAbsent(context.Background(), "owner-a", chat.Message{ID: "branch-source", SessionID: sessionID, Role: "user", Content: "hello", CreatedAt: now}, now)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/chat/sessions/" + sessionID + "/branch"
	for _, body := range []string{`{"messageId":"branch-source","unknown":1}`, `{"messageId":"branch-source"} {}`, `{}`} {
		response := grantTestRequest(t, handler, cookie, http.MethodPost, path, body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s: %d %s", body, response.Code, response.Body.String())
		}
	}
	response := grantTestRequest(t, handler, cookie, http.MethodPost, path, `{"messageId":"branch-source"}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("branch: %d %s", response.Code, response.Body.String())
	}
	body := decodeMap(t, response.Body.String())
	if copied, ok := body["canvas_copied"].(bool); !ok || copied {
		t.Fatalf("no canvas: %s", response.Body.String())
	}
	session, ok := body["session"].(map[string]any)
	if !ok || session["parent_session_id"] != sessionID || session["parent_message_id"] != "branch-source" {
		t.Fatalf("branch body: %s", response.Body.String())
	}
	_, err = repo.CreateProject(context.Background(), canvas.Project{ID: "source-canvas", OwnerID: "owner-a", SessionID: sessionID, Name: "canvas", Viewport: "{}", BGStyle: "dots", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	withCanvas := grantTestRequest(t, handler, cookie, http.MethodPost, path, `{"messageId":"branch-source"}`, "")
	if withCanvas.Code != http.StatusCreated || decodeMap(t, withCanvas.Body.String())["canvas_copied"] != true {
		t.Fatalf("with canvas: %d %s", withCanvas.Code, withCanvas.Body.String())
	}
	missing := grantTestRequest(t, handler, cookie, http.MethodPost, path, `{"messageId":"missing"}`, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", missing.Code, missing.Body.String())
	}
	_, err = repo.CreateSession(context.Background(), chat.Session{ID: "foreign-branch-parent", OwnerID: "owner-b", OwnerName: "other", Title: "other", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	foreign := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/chat/sessions/foreign-branch-parent/branch", `{"messageId":"branch-source"}`, "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign: %d %s", foreign.Code, foreign.Body.String())
	}
	_, _, err = repo.StartTurn(context.Background(), "owner-a",
		chat.Message{ID: "active-user", SessionID: sessionID, Role: "user", Content: "next", CreatedAt: now},
		chat.Message{ID: "active-assistant", SessionID: sessionID, Role: "assistant", CreatedAt: now},
		chat.Event{SessionID: sessionID, Data: map[string]any{"type": "message"}, CreatedAt: now},
		chat.Stream{SessionID: sessionID, Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: sessionID, AssistantMessageID: "active-assistant", State: "running", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	active := grantTestRequest(t, handler, cookie, http.MethodPost, path, `{"messageId":"branch-source"}`, "")
	if active.Code != http.StatusConflict || decodeMap(t, active.Body.String())["error"] == "" {
		t.Fatalf("active: %d %s", active.Code, active.Body.String())
	}
	_, authenticatedOnly, _, _ := newHTTPHarnessWithCatalogOverride(t, []string{"user"}, nil, func(c *authentication.Config) { c.Fallback = nil }, nil, nil, nil)
	unauthenticated := grantTestRequest(t, authenticatedOnly, nil, http.MethodPost, path, `{"messageId":"branch-source"}`, "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}
}
