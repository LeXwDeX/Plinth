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

// #103 review F1: the scope picker is gone, and the public write only accepts
// auto. A stale client cannot lock a session out of the other workspace.
func TestChatAgentScopeStaysOnAutoForOwner(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)

	read := grantTestRequest(t, handler, cookie, http.MethodGet, agentScopePath(sessionID), "", "")
	if read.Code != http.StatusOK || decodeAgentScope(t, read.Body.String()) != "auto" {
		t.Fatalf("default scope: %d %s", read.Code, read.Body.String())
	}
	put := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath(sessionID), `{"agentScope":"auto"}`, "")
	if put.Code != http.StatusOK || decodeAgentScope(t, put.Body.String()) != "auto" {
		t.Fatalf("write auto: %d %s", put.Code, put.Body.String())
	}
	reread := grantTestRequest(t, handler, cookie, http.MethodGet, agentScopePath(sessionID), "", "")
	if decodeAgentScope(t, reread.Body.String()) != "auto" {
		t.Fatalf("auto not persisted: %s", reread.Body.String())
	}
	list := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions", "", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"agentScope":"auto"`) {
		t.Fatalf("session list lacks the scope: %d %s", list.Code, list.Body.String())
	}
	for _, scope := range []string{"2d", "3d"} {
		refused := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath(sessionID), `{"agentScope":"`+scope+`"}`, "")
		if refused.Code != http.StatusBadRequest {
			t.Fatalf("write %s status=%d %s", scope, refused.Code, refused.Body.String())
		}
	}
	reread = grantTestRequest(t, handler, cookie, http.MethodGet, agentScopePath(sessionID), "", "")
	if decodeAgentScope(t, reread.Body.String()) != "auto" {
		t.Fatalf("a refused write changed the scope: %s", reread.Body.String())
	}
}

func TestChatAgentScopeRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	for _, body := range []string{
		`{"agentScope":"2d"}`, `{"agentScope":"3d"}`, `{"agentScope":"4d"}`,
		`{"agentScope":""}`, `{"agentScope":"AUTO"}`, `{}`, `not json`,
	} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath(sessionID), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q status=%d %s", body, response.Code, response.Body.String())
		}
	}
	read := grantTestRequest(t, handler, cookie, http.MethodGet, agentScopePath(sessionID), "", "")
	if decodeAgentScope(t, read.Body.String()) != "auto" {
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
	put := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath("foreign-session"), `{"agentScope":"auto"}`, "")
	missing := grantTestRequest(t, handler, cookie, http.MethodPut, agentScopePath("no-such-session"), `{"agentScope":"auto"}`, "")
	if get.Code != http.StatusNotFound || put.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d/%d missing=%d", get.Code, put.Code, missing.Code)
	}
	scope, found, err := repository.SessionAgentScope(context.Background(), "owner-b", "foreign-session")
	if err != nil || !found || scope != "auto" {
		t.Fatalf("foreign session changed: %q found=%v err=%v", scope, found, err)
	}
}
