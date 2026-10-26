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

func pickPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/pick"
}

func decodePick(t *testing.T, body string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("pick body %q: %v", body, err)
	}
	pick, present := decoded["pick"]
	if !present {
		t.Fatalf("pick member missing: %s", body)
	}
	if pick == nil {
		return nil
	}
	return pick.(map[string]any)
}

func TestChatSessionPickCanBeReadSetAndClearedByTheOwner(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	read := grantTestRequest(t, handler, cookie, http.MethodGet, pickPath(sessionID), "", "")
	if read.Code != http.StatusOK || decodePick(t, read.Body.String()) != nil {
		t.Fatalf("a new session has no pick: %d %s", read.Code, read.Body.String())
	}
	put := grantTestRequest(t, handler, cookie, http.MethodPut, pickPath(sessionID),
		`{"kind":"image","taskId":"own-task","assetId":"own-asset","url":"/api/v1/tasks/own-task/assets/own-asset"}`, "")
	if put.Code != http.StatusOK {
		t.Fatalf("pick: %d %s", put.Code, put.Body.String())
	}
	picked := decodePick(t, put.Body.String())
	if picked["taskId"] != "own-task" || picked["assetId"] != "own-asset" || picked["kind"] != "image" {
		t.Fatalf("picked = %#v", picked)
	}
	reread := grantTestRequest(t, handler, cookie, http.MethodGet, pickPath(sessionID), "", "")
	if got := decodePick(t, reread.Body.String()); got == nil || got["assetId"] != "own-asset" {
		t.Fatalf("pick not persisted: %s", reread.Body.String())
	}
	cleared := grantTestRequest(t, handler, cookie, http.MethodDelete, pickPath(sessionID), "", "")
	if cleared.Code != http.StatusOK || decodePick(t, cleared.Body.String()) != nil {
		t.Fatalf("clear: %d %s", cleared.Code, cleared.Body.String())
	}
	if got := decodePick(t, grantTestRequest(t, handler, cookie, http.MethodGet, pickPath(sessionID), "", "").Body.String()); got != nil {
		t.Fatalf("pick survived clearing: %#v", got)
	}
}

func TestChatSessionPickRefusesUnverifiableCandidatesAndKeepsTheOldPick(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	if put := grantTestRequest(t, handler, cookie, http.MethodPut, pickPath(sessionID),
		`{"kind":"image","taskId":"own-task","assetId":"own-asset"}`, ""); put.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", put.Code, put.Body.String())
	}
	for _, body := range []string{
		`{"kind":"image","taskId":"someone-elses-task","assetId":"someone-elses-asset"}`,
		`{"kind":"image","taskId":"own-task"}`, // no asset: not a candidate
		`{"kind":"image","url":"data:image/png;base64,QQ=="}`,
		`{}`, `not json`, `[]`,
	} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, pickPath(sessionID), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %.50q status=%d %s", body, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "someone-elses") {
			t.Fatalf("a foreign ID was echoed: %s", response.Body.String())
		}
	}
	read := grantTestRequest(t, handler, cookie, http.MethodGet, pickPath(sessionID), "", "")
	if got := decodePick(t, read.Body.String()); got == nil || got["assetId"] != "own-asset" {
		t.Fatalf("a refused pick changed the stored one: %s", read.Body.String())
	}
}

func TestChatSessionPickRefusesThePicturesOfA3DModel(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	if put := grantTestRequest(t, handler, cookie, http.MethodPut, pickPath(sessionID),
		`{"kind":"image","taskId":"own-task","assetId":"own-asset"}`, ""); put.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", put.Code, put.Body.String())
	}
	// own-model is a 3D model Task: its render and drawn picture preview the model.
	for _, body := range []string{
		`{"kind":"image","taskId":"own-model","assetId":"own-render","url":"/api/v1/tasks/own-model/assets/own-render"}`,
		`{"kind":"image","taskId":"own-model","assetId":"own-drawn"}`,
	} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, pickPath(sessionID), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %.60q status=%d %s", body, response.Code, response.Body.String())
		}
	}
	read := grantTestRequest(t, handler, cookie, http.MethodGet, pickPath(sessionID), "", "")
	if got := decodePick(t, read.Body.String()); got == nil || got["assetId"] != "own-asset" {
		t.Fatalf("a refused model preview changed the stored pick: %s", read.Body.String())
	}
}

func TestChatSessionPickIsOwnerScoped(t *testing.T) {
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
	if _, err := repository.SetSessionPick(context.Background(), "owner-b", "foreign-session",
		chat.Attachment{Kind: "image", AssetID: "own-secret-asset", TaskID: "own-secret-task"}); err != nil {
		t.Fatal(err)
	}
	get := grantTestRequest(t, handler, cookie, http.MethodGet, pickPath("foreign-session"), "", "")
	put := grantTestRequest(t, handler, cookie, http.MethodPut, pickPath("foreign-session"), `{"kind":"image","taskId":"own-task","assetId":"own-asset"}`, "")
	del := grantTestRequest(t, handler, cookie, http.MethodDelete, pickPath("foreign-session"), "", "")
	missing := grantTestRequest(t, handler, cookie, http.MethodPut, pickPath("no-such-session"), `{"kind":"image","taskId":"own-task","assetId":"own-asset"}`, "")
	if get.Code != http.StatusNotFound || put.Code != http.StatusNotFound || del.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d/%d/%d missing=%d", get.Code, put.Code, del.Code, missing.Code)
	}
	if strings.Contains(get.Body.String(), "secret") {
		t.Fatalf("a foreign pick leaked: %s", get.Body.String())
	}
	if pick, _, _ := repository.SessionPick(context.Background(), "owner-b", "foreign-session"); pick.AssetID != "own-secret-asset" {
		t.Fatalf("a foreign write changed the pick: %+v", pick)
	}
}
