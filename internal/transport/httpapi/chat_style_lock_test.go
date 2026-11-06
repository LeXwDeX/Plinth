package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func styleLockPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/style-lock"
}

func decodeStyleLock(t *testing.T, body string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("style lock body %q: %v", body, err)
	}
	value, ok := decoded["styleLock"].(map[string]any)
	if !ok {
		t.Fatalf("styleLock missing from %s", body)
	}
	return value
}

func TestChatSessionStyleLockCanBeReadAndReplacedByTheOwner(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	read := grantTestRequest(t, handler, cookie, http.MethodGet, styleLockPath(sessionID), "", "")
	lock := decodeStyleLock(t, read.Body.String())
	references, _ := lock["references"].([]any)
	if read.Code != http.StatusOK || lock["enabled"] != false || lock["text"] != "" || len(references) != 0 {
		t.Fatalf("default style lock: %d %s", read.Code, read.Body.String())
	}
	put := grantTestRequest(t, handler, cookie, http.MethodPut, styleLockPath(sessionID), `{"enabled":true,"text":"  ink  ","references":[]}`, "")
	if put.Code != http.StatusOK || decodeStyleLock(t, put.Body.String())["text"] != "ink" {
		t.Fatalf("set style lock: %d %s", put.Code, put.Body.String())
	}
	replaced := grantTestRequest(t, handler, cookie, http.MethodPut, styleLockPath(sessionID), `{"enabled":false,"text":"","references":[]}`, "")
	if replaced.Code != http.StatusOK || decodeStyleLock(t, replaced.Body.String())["enabled"] != false {
		t.Fatalf("replace style lock: %d %s", replaced.Code, replaced.Body.String())
	}
}

func TestChatSessionStyleLockRejectsUnknownTrailingAndOverlongBodies(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	for _, body := range []string{
		`{"enabled":true,"text":"ok","references":[],"extra":true}`,
		`{"enabled":true,"text":"ok","references":[]} {"enabled":false}`,
		`{"enabled":true,"text":"ok"}`,
		`{"enabled":true,"text":7,"references":[]}`,
		`{"enabled":true,"text":"` + strings.Repeat("设", 501) + `","references":[]}`,
	} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, styleLockPath(sessionID), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %.50q status=%d %s", body, response.Code, response.Body.String())
		}
	}
}

func TestChatSessionStyleLockIsOwnerScopedAndRequiresLogin(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "foreign-style-lock", OwnerID: "owner-b", OwnerName: "Other", Title: "other"}); err != nil {
		t.Fatal(err)
	}
	get := grantTestRequest(t, handler, cookie, http.MethodGet, styleLockPath("foreign-style-lock"), "", "")
	put := grantTestRequest(t, handler, cookie, http.MethodPut, styleLockPath("foreign-style-lock"), `{"enabled":true,"text":"ok","references":[]}`, "")
	if get.Code != http.StatusNotFound || put.Code != http.StatusNotFound {
		t.Fatalf("foreign access = %d/%d", get.Code, put.Code)
	}
	unauthenticated := grantTestRequest(t, handler, &http.Cookie{Name: "meshyai_session", Value: "invalid"}, http.MethodGet, styleLockPath(sessionID), "", "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}
}
