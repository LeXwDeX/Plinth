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

func agentScopePath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/agent-scope"
}

func decodeAgentScope(t *testing.T, body string) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("agent scope body %q: %v", body, err)
	}
	scope, _ := decoded["agentScope"].(string)
	return scope
}

func TestChatAgentScopeDefaultsToAutoAndSwitchesForOwner(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)

	read := grantTestRequest(t, handler, cookie, http.MethodGet, agentScopePath(sessionID), "", "")
	if read.Code != http.StatusOK || decodeAgentScope(t, read.Body.String()) != "auto" {
		t.Fatalf("default scope: %d %s", read.Code, read.Body.String())
	}
	for _, scope := range []string{"2d", "3d", "auto"} {
		put := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath(sessionID), `{"agentScope":"`+scope+`"}`, "")
		if put.Code != http.StatusOK || decodeAgentScope(t, put.Body.String()) != scope {
			t.Fatalf("switch to %s: %d %s", scope, put.Code, put.Body.String())
		}
		reread := grantTestRequest(t, handler, cookie, http.MethodGet, agentScopePath(sessionID), "", "")
		if decodeAgentScope(t, reread.Body.String()) != scope {
			t.Fatalf("scope %s not persisted: %s", scope, reread.Body.String())
		}
		// The session list carries it, so the workspace restores it on reload.
		list := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions", "", "")
		if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"agentScope":"`+scope+`"`) {
			t.Fatalf("session list lacks the scope %s: %d %s", scope, list.Code, list.Body.String())
		}
	}
}

func TestChatAgentScopeRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath(sessionID), `{"agentScope":"3d"}`, "")
	for _, body := range []string{`{"agentScope":"4d"}`, `{"agentScope":""}`, `{"agentScope":"AUTO"}`, `{}`, `not json`} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath(sessionID), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q status=%d %s", body, response.Code, response.Body.String())
		}
	}
	read := grantTestRequest(t, handler, cookie, http.MethodGet, agentScopePath(sessionID), "", "")
	if decodeAgentScope(t, read.Body.String()) != "3d" {
		t.Fatalf("rejected requests changed the scope: %s", read.Body.String())
	}
}

func TestChatAgentScopeIsOwnerScoped(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	now := time.Now().UTC()
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "foreign-session", OwnerID: "owner-b",
		OwnerName: "Other", Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	get := grantTestRequest(t, handler, cookie, http.MethodGet, agentScopePath("foreign-session"), "", "")
	put := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath("foreign-session"), `{"agentScope":"2d"}`, "")
	missing := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath("no-such-session"), `{"agentScope":"2d"}`, "")
	if get.Code != http.StatusNotFound || put.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d/%d missing=%d", get.Code, put.Code, missing.Code)
	}
	scope, found, err := repository.SessionAgentScope(context.Background(), "owner-b", "foreign-session")
	if err != nil || !found || scope != "auto" {
		t.Fatalf("foreign session changed: %q found=%v err=%v", scope, found, err)
	}
}

func TestChatAgentScopeChangeIsAudited(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	for range 2 {
		put := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath(sessionID), `{"agentScope":"2d"}`, "")
		if put.Code != http.StatusOK {
			t.Fatalf("switch: %d %s", put.Code, put.Body.String())
		}
	}
	events, err := repository.ListAdministrationAuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changes := 0
	for _, event := range events {
		if event.Action != "set_agent_scope" {
			continue
		}
		changes++
		if event.ResourceID != sessionID || event.ActorID != "owner-a" || event.Detail["from"] != "auto" || event.Detail["to"] != "2d" {
			t.Fatalf("audit event = %+v", event)
		}
	}
	// Setting the same scope again is not a change and writes no second entry.
	if changes != 1 {
		t.Fatalf("audit entries = %d, want 1", changes)
	}
}
