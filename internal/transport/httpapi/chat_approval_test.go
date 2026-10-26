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

func approvalPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/approval"
}

func decodeApproval(t *testing.T, body string) (mode string, credits float64) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("approval body %q: %v", body, err)
	}
	mode, _ = decoded["approvalMode"].(string)
	credits, _ = decoded["estimatedCredits"].(float64)
	return mode, credits
}

func TestChatApprovalModeDefaultsToManualAndSwitchesForOwner(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)

	read := grantTestRequest(t, handler, cookie, http.MethodGet, approvalPath(sessionID), "", "")
	if mode, credits := decodeApproval(t, read.Body.String()); read.Code != http.StatusOK || mode != "manual" || credits != 0 {
		t.Fatalf("default mode: %d %s", read.Code, read.Body.String())
	}
	put := grantTestRequest(t, handler, cookie, http.MethodPut, approvalPath(sessionID), `{"approvalMode":"auto"}`, "")
	if mode, _ := decodeApproval(t, put.Body.String()); put.Code != http.StatusOK || mode != "auto" {
		t.Fatalf("switch to auto: %d %s", put.Code, put.Body.String())
	}
	reread := grantTestRequest(t, handler, cookie, http.MethodGet, approvalPath(sessionID), "", "")
	if mode, _ := decodeApproval(t, reread.Body.String()); mode != "auto" {
		t.Fatalf("mode not persisted: %s", reread.Body.String())
	}
	list := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/chat/sessions", "", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"approvalMode":"auto"`) ||
		!strings.Contains(list.Body.String(), `"estimatedCredits":0`) {
		t.Fatalf("session list lacks the mode and credits: %d %s", list.Code, list.Body.String())
	}
	back := grantTestRequest(t, handler, cookie, http.MethodPut, approvalPath(sessionID), `{"approvalMode":"manual"}`, "")
	if mode, _ := decodeApproval(t, back.Body.String()); back.Code != http.StatusOK || mode != "manual" {
		t.Fatalf("switch back: %d %s", back.Code, back.Body.String())
	}
}

func TestChatApprovalModeRejectsInvalidAndCredentialOnlyRequests(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	for _, body := range []string{`{"approvalMode":"yolo"}`, `{"approvalMode":""}`, `{}`, `not json`} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, approvalPath(sessionID), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q status=%d %s", body, response.Code, response.Body.String())
		}
	}
	// No real browser session cookie: a bare identity cannot raise its own approval level.
	bare := grantTestRequest(t, handler, nil, http.MethodPut, approvalPath(sessionID), `{"approvalMode":"auto"}`, "")
	if bare.Code == http.StatusOK {
		t.Fatalf("credential-only switch accepted: %s", bare.Body.String())
	}
	read := grantTestRequest(t, handler, cookie, http.MethodGet, approvalPath(sessionID), "", "")
	if mode, _ := decodeApproval(t, read.Body.String()); mode != "manual" {
		t.Fatalf("rejected requests changed the mode: %s", read.Body.String())
	}
}

func TestChatApprovalModeIsOwnerScoped(t *testing.T) {
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
	get := grantTestRequest(t, handler, cookie, http.MethodGet, approvalPath("foreign-session"), "", "")
	put := grantTestRequest(t, handler, cookie, http.MethodPut, approvalPath("foreign-session"), `{"approvalMode":"auto"}`, "")
	missing := grantTestRequest(t, handler, cookie, http.MethodPut, approvalPath("no-such-session"), `{"approvalMode":"auto"}`, "")
	if get.Code != http.StatusNotFound || put.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d/%d missing=%d", get.Code, put.Code, missing.Code)
	}
	if get.Body.String() != put.Body.String() && strings.Contains(put.Body.String(), "foreign") {
		t.Fatalf("foreign session existence leaked: %s", put.Body.String())
	}
	stored, found, err := repository.Session(context.Background(), "owner-b", "foreign-session")
	if err != nil || !found || stored.ApprovalMode != "manual" {
		t.Fatalf("foreign session changed: %+v found=%v err=%v", stored, found, err)
	}
}

func TestChatApprovalModeChangeIsAudited(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	for range 2 {
		put := grantTestRequest(t, handler, cookie, http.MethodPut, approvalPath(sessionID), `{"approvalMode":"auto"}`, "")
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
		if event.Action != "set_approval_mode" {
			continue
		}
		changes++
		if event.ResourceID != sessionID || event.ActorID != "owner-a" {
			t.Fatalf("audit event = %+v", event)
		}
		if event.Detail["from"] != "manual" || event.Detail["to"] != "auto" {
			t.Fatalf("audit detail = %+v", event.Detail)
		}
	}
	// Setting the same mode again is not a change and writes no second entry.
	if changes != 1 {
		t.Fatalf("audit entries = %d, want 1", changes)
	}
}

func TestTaskGrantIsApprovedByPolicyOnlyWhenModeAllows(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	prepare := func(capability, offering, input string) string {
		t.Helper()
		response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/prepare",
			`{"session_id":"`+sessionID+`","capability":"`+capability+`","offering_id":"`+offering+`","input":`+input+`}`, "")
		if response.Code != http.StatusCreated {
			t.Fatalf("prepare %s: %d %s", capability, response.Code, response.Body.String())
		}
		var decoded struct {
			ID string `json:"prepared_input_id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.ID == "" {
			t.Fatalf("prepared %s: %v", response.Body.String(), err)
		}
		return decoded.ID
	}
	grant := func(prepared, key string) (string, bool) {
		t.Helper()
		response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants",
			`{"prepared_input_id":"`+prepared+`"}`, key)
		if response.Code != http.StatusCreated {
			t.Fatalf("grant: %d %s", response.Code, response.Body.String())
		}
		var decoded struct {
			Status           string `json:"status"`
			ApprovedByPolicy bool   `json:"approved_by_policy"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded.Status, decoded.ApprovedByPolicy
	}
	model := prepare("text-to-3d", "meshy/meshy-7.1/text-to-3d", `{"prompt":"a carved oak chair"}`)
	if status, byPolicy := grant(model, "model-key-manual"); status != "pending" || byPolicy {
		t.Fatalf("manual 3D grant: status=%s policy=%v", status, byPolicy)
	}
	switched := grantTestRequest(t, handler, cookie, http.MethodPut, approvalPath(sessionID), `{"approvalMode":"auto"}`, "")
	if switched.Code != http.StatusOK {
		t.Fatalf("switch: %d %s", switched.Code, switched.Body.String())
	}
	other := prepare("text-to-3d", "meshy/meshy-7.1/text-to-3d", `{"prompt":"a carved pine table"}`)
	if status, byPolicy := grant(other, "model-key-auto"); status != "approved" || !byPolicy {
		t.Fatalf("auto 3D grant: status=%s policy=%v", status, byPolicy)
	}
	// Asking again for the earlier pending 3D grant applies the mode now in
	// force, because the person has since chosen auto.
	if status, byPolicy := grant(model, "model-key-manual"); status != "approved" || !byPolicy {
		t.Fatalf("replayed grant under auto: status=%s policy=%v", status, byPolicy)
	}
}
