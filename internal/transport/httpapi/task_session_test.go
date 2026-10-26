package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestNativeTaskListPagesOnlyOwnedSessionVersions(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	createSession := func() string {
		t.Helper()
		response := serveLegacyRequest(handler, http.MethodPost, "/api/chat/sessions", strings.NewReader(`{"title":"same name"}`))
		if response.Code != http.StatusCreated {
			t.Fatalf("create session: status=%d body=%s", response.Code, response.Body.String())
		}
		var session struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || session.ID == "" {
			t.Fatalf("decode session: %+v %v", session, err)
		}
		return session.ID
	}
	first := createSession()
	second := createSession()
	if first == second {
		t.Fatal("distinct sessions share an ID")
	}
	for index, sessionID := range []string{first, second, first} {
		if _, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
			OwnerID: "owner-a", SessionID: sessionID,
			Capability: generation.TextTo3D,
			OfferingID: "meshy/meshy-7.1/text-to-3d",
			Input:      json.RawMessage(fmt.Sprintf(`{"prompt":"model %d"}`, index)),
		}); err != nil {
			t.Fatalf("submit %d: %v", index, err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for range 2 {
		endpoint := "/api/v1/tasks?session_id=" + url.QueryEscape(first) + "&limit=1"
		if cursor != "" {
			endpoint += "&cursor=" + url.QueryEscape(cursor)
		}
		response := serveLegacyRequest(handler, http.MethodGet, endpoint, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("list: status=%d body=%s", response.Code, response.Body.String())
		}
		var page struct {
			Tasks []struct {
				ID        string `json:"id"`
				SessionID string `json:"session_id"`
			} `json:"tasks"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Tasks) != 1 || page.Tasks[0].SessionID != first || seen[page.Tasks[0].ID] {
			t.Fatalf("wrong session page: %+v", page)
		}
		seen[page.Tasks[0].ID] = true
		cursor = page.NextCursor
	}
	if len(seen) != 2 || cursor != "" {
		t.Fatalf("pagination: seen=%v cursor=%q", seen, cursor)
	}
	response := serveLegacyRequest(handler, http.MethodGet, "/api/v1/tasks?session_id=missing", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown session status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNativeSubmitTaskKeepsOwnedSessionAndRejectsUnknownSession(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	sessionResponse := serveLegacyRequest(handler, http.MethodPost, "/api/chat/sessions", strings.NewReader(`{"title":"processing"}`))
	if sessionResponse.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", sessionResponse.Code, sessionResponse.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(sessionResponse.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatalf("decode session: %+v %v", session, err)
	}
	requestBody := func(sessionID string) *strings.Reader {
		encoded, err := json.Marshal(map[string]any{
			"session_id":  sessionID,
			"capability":  "text-to-3d",
			"offering_id": "meshy/meshy-7.1/text-to-3d",
			"input":       map[string]any{"prompt": "model"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return strings.NewReader(string(encoded))
	}
	unknown := serveLegacyRequest(handler, http.MethodPost, "/api/v1/tasks/prepare", requestBody("missing"))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown session: %d %s", unknown.Code, unknown.Body.String())
	}
	bare := serveLegacyRequest(handler, http.MethodPost, "/api/v1/tasks", requestBody(session.ID))
	if bare.Code != http.StatusForbidden {
		t.Fatalf("bare submit: %d %s", bare.Code, bare.Body.String())
	}
	if _, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", SessionID: session.ID,
		Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input: json.RawMessage(`{"prompt":"model"}`),
	}); err != nil {
		t.Fatal(err)
	}
	listed := serveLegacyRequest(handler, http.MethodGet, "/api/v1/tasks?session_id="+url.QueryEscape(session.ID), nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"session_id":"`+session.ID+`"`) {
		t.Fatalf("session versions: %d %s", listed.Code, listed.Body.String())
	}
}

func TestChatSessionHistoryPagesWithoutChangingLegacyListShape(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	for range 3 {
		response := serveLegacyRequest(handler, http.MethodPost, "/api/chat/sessions", strings.NewReader(`{"title":"same name"}`))
		if response.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", response.Code, response.Body.String())
		}
	}
	legacy := serveLegacyRequest(handler, http.MethodGet, "/api/chat/sessions", nil)
	var legacySessions []json.RawMessage
	if err := json.Unmarshal(legacy.Body.Bytes(), &legacySessions); err != nil || len(legacySessions) != 3 {
		t.Fatalf("legacy shape: %d %s %v", legacy.Code, legacy.Body.String(), err)
	}
	seen := map[string]bool{}
	cursor := ""
	for range 3 {
		endpoint := "/api/chat/sessions?search=same&limit=1"
		if cursor != "" {
			endpoint += "&cursor=" + url.QueryEscape(cursor)
		}
		response := serveLegacyRequest(handler, http.MethodGet, endpoint, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("page: %d %s", response.Code, response.Body.String())
		}
		var page struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Sessions) != 1 || seen[page.Sessions[0].ID] {
			t.Fatalf("page: %+v %v", page, err)
		}
		seen[page.Sessions[0].ID] = true
		cursor = page.NextCursor
	}
	if len(seen) != 3 || cursor != "" {
		t.Fatalf("pagination: %v cursor=%q", seen, cursor)
	}
	invalid := serveLegacyRequest(handler, http.MethodGet, "/api/chat/sessions?limit=0", nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit: %d", invalid.Code)
	}
}
