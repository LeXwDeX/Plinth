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

// #406: a new session is created in the Schema's default approval mode (auto)
// and the session object, the approval route and the public Schema agree on it.
// A session stored before the default changed keeps its own mode.

// newDefaultModeSession406 creates a session over HTTP and leaves its approval
// mode alone: it is in whatever the Schema says a new session starts in. (The
// session grantTestBrowser creates is pinned to a mode for the grant tests.)
func newDefaultModeSession406(t *testing.T, handler http.Handler, cookie *http.Cookie) string {
	t.Helper()
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions", `{"title":"default mode"}`, "")
	var session struct {
		ID string `json:"id"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &session) != nil || session.ID == "" {
		t.Fatalf("create session: %d %s", created.Code, created.Body.String())
	}
	return session.ID
}

func publicApprovalDefault406(t *testing.T, handler http.Handler, cookie *http.Cookie) string {
	t.Helper()
	response := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/schema", "", "")
	var public struct {
		Schema struct {
			Chat struct {
				ApprovalPolicy struct {
					DefaultMode string   `json:"default_mode"`
					Modes       []string `json:"modes"`
				} `json:"approval_policy"`
			} `json:"chat"`
		} `json:"schema"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &public) != nil {
		t.Fatalf("public schema: %d %s", response.Code, response.Body.String())
	}
	policy := public.Schema.Chat.ApprovalPolicy
	found := false
	for _, mode := range policy.Modes {
		found = found || mode == policy.DefaultMode
	}
	if !found {
		t.Fatalf("the public default %q is not an offered mode %v", policy.DefaultMode, policy.Modes)
	}
	return policy.DefaultMode
}

func TestNewSessionIsCreatedInTheSchemaDefaultApprovalMode_406(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := grantTestBrowser(t)

	schemaDefault := publicApprovalDefault406(t, handler, cookie)
	if schemaDefault != "auto" {
		t.Fatalf("the public Schema's default approval mode = %q, want auto", schemaDefault)
	}

	// The session object a client gets when it creates a session.
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions", `{"title":"another"}`, "")
	var session map[string]any
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &session) != nil ||
		session["approvalMode"] != schemaDefault {
		t.Fatalf("created session: %d %s, want approvalMode %q", created.Code, created.Body.String(), schemaDefault)
	}
	sessionID, _ := session["id"].(string)
	// The route the top bar reads.
	read := grantTestRequest(t, handler, cookie, http.MethodGet, approvalPath(sessionID), "", "")
	if mode, _ := decodeApproval(t, read.Body.String()); read.Code != http.StatusOK || mode != schemaDefault {
		t.Fatalf("approval route: %d %s, want %q", read.Code, read.Body.String(), schemaDefault)
	}
	// And the session list: exactly one session (the helper's, pinned to budget
	// for the grant tests) is not in the default.
	list := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions", "", "")
	if list.Code != http.StatusOK || strings.Count(list.Body.String(), `"approvalMode":"`+schemaDefault+`"`) != 1 {
		t.Fatalf("session list: %d %s", list.Code, list.Body.String())
	}
}

func TestStoredSessionsKeepTheirApprovalModeUnderTheNewDefault_406(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	now := time.Now().UTC()
	stored := map[string]string{
		"legacy-budget": "budget", "legacy-manual": "manual", "legacy-auto": "auto",
		// The repository stores a missing value as the strict mode, like the column default.
		"legacy-blank": "",
	}
	for id, mode := range stored {
		if _, err := repository.CreateSession(context.Background(), chat.Session{ID: id, OwnerID: "owner-a",
			OwnerName: "Owner", Title: id, CreatedAt: now, UpdatedAt: now, ApprovalMode: mode}); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{"legacy-budget": "budget", "legacy-manual": "manual", "legacy-auto": "auto", "legacy-blank": "manual"}
	list := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions", "", "")
	var listed []map[string]any
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &listed) != nil {
		t.Fatalf("session list: %d %s", list.Code, list.Body.String())
	}
	projected := map[string]any{}
	for _, session := range listed {
		projected[session["id"].(string)] = session["approvalMode"]
	}
	for id, mode := range want {
		read := grantTestRequest(t, handler, cookie, http.MethodGet, approvalPath(id), "", "")
		if got, _ := decodeApproval(t, read.Body.String()); read.Code != http.StatusOK || got != mode {
			t.Errorf("%s: approval route = %d %s, want %q", id, read.Code, read.Body.String(), mode)
		}
		if projected[id] != mode {
			t.Errorf("%s: session list = %v, want %q", id, projected[id], mode)
		}
	}
}
