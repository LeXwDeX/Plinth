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

func notesPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/notes"
}

func decodeNotes(t *testing.T, body string) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("notes body %q: %v", body, err)
	}
	notes, _ := decoded["notes"].(string)
	return notes
}

func TestChatSessionNotesCanBeReadAndEditedByTheOwner(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	read := grantTestRequest(t, handler, cookie, http.MethodGet, notesPath(sessionID), "", "")
	if read.Code != http.StatusOK || decodeNotes(t, read.Body.String()) != "" {
		t.Fatalf("a new session has empty notes: %d %s", read.Code, read.Body.String())
	}
	put := grantTestRequest(t, handler, cookie, http.MethodPut, notesPath(sessionID), `{"notes":"  hero: red scarf\r\nstyle: ink  "}`, "")
	if put.Code != http.StatusOK || decodeNotes(t, put.Body.String()) != "hero: red scarf\nstyle: ink" {
		t.Fatalf("edit: %d %s", put.Code, put.Body.String())
	}
	reread := grantTestRequest(t, handler, cookie, http.MethodGet, notesPath(sessionID), "", "")
	if decodeNotes(t, reread.Body.String()) != "hero: red scarf\nstyle: ink" {
		t.Fatalf("notes not persisted: %s", reread.Body.String())
	}
	cleared := grantTestRequest(t, handler, cookie, http.MethodPut, notesPath(sessionID), `{"notes":""}`, "")
	if cleared.Code != http.StatusOK || decodeNotes(t, cleared.Body.String()) != "" {
		t.Fatalf("clear: %d %s", cleared.Code, cleared.Body.String())
	}
}

func TestChatSessionNotesRejectOverlongAndMalformedBodies(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	if put := grantTestRequest(t, handler, cookie, http.MethodPut, notesPath(sessionID), `{"notes":"keep"}`, ""); put.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", put.Code, put.Body.String())
	}
	for _, body := range []string{`{"notes":"` + strings.Repeat("设", 2001) + `"}`, `{}`, `{"notes":7}`, `not json`} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, notesPath(sessionID), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %.40q status=%d %s", body, response.Code, response.Body.String())
		}
	}
	if put := grantTestRequest(t, handler, cookie, http.MethodPut, notesPath(sessionID), `{"notes":"`+strings.Repeat("设", 2000)+`"}`, ""); put.Code != http.StatusOK {
		t.Fatalf("notes at the bound must be accepted: %d %s", put.Code, put.Body.String())
	}
	grantTestRequest(t, handler, cookie, http.MethodPut, notesPath(sessionID), `{"notes":"keep"}`, "")
	grantTestRequest(t, handler, cookie, http.MethodPut, notesPath(sessionID), `{"notes":"`+strings.Repeat("设", 2001)+`"}`, "")
	read := grantTestRequest(t, handler, cookie, http.MethodGet, notesPath(sessionID), "", "")
	if decodeNotes(t, read.Body.String()) != "keep" {
		t.Fatalf("a rejected edit changed the notes: %.60s", read.Body.String())
	}
}

func TestChatSessionNotesAreOwnerScoped(t *testing.T) {
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
	if _, err := repository.SetSessionNotes(context.Background(), "owner-b", "foreign-session", "secret plan"); err != nil {
		t.Fatal(err)
	}
	get := grantTestRequest(t, handler, cookie, http.MethodGet, notesPath("foreign-session"), "", "")
	put := grantTestRequest(t, handler, cookie, http.MethodPut, notesPath("foreign-session"), `{"notes":"taken"}`, "")
	missing := grantTestRequest(t, handler, cookie, http.MethodPut, notesPath("no-such-session"), `{"notes":"x"}`, "")
	if get.Code != http.StatusNotFound || put.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d/%d missing=%d", get.Code, put.Code, missing.Code)
	}
	if strings.Contains(get.Body.String(), "secret plan") {
		t.Fatalf("a foreign note leaked: %s", get.Body.String())
	}
	if notes, _, _ := repository.SessionNotes(context.Background(), "owner-b", "foreign-session"); notes != "secret plan" {
		t.Fatalf("a foreign write changed the notes: %q", notes)
	}
}

// #240: a chat error the page shows carries the access log's request ID.
func TestChatErrorBodyCarriesTheRequestID(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := grantTestBrowser(t)
	response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions/session_missing/messages", `{"content":"hello"}`, "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	id := response.Header().Get("X-Request-ID")
	if id == "" || body["request_id"] != id {
		t.Fatalf("request_id = %q, header = %q, body = %s", body["request_id"], id, response.Body.String())
	}
}
