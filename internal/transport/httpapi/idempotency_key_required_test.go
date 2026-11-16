package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #302: the browser routes that start a paid step are keyed by the client's
// Idempotency-Key header. A request without a usable key used to fall through
// to the service layer and come back as the generic 400 "invalid request" (the
// log said "execution grant: invalid identity"), which tells an API caller
// nothing. Every such route now answers 400 idempotency_key_required with
// Schema copy that names the header; a usable key behaves exactly as before.

const idempotencyKeyRequiredCode = "idempotency_key_required"

type idempotencyKeyProblem struct {
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	RequestID string `json:"request_id"`
}

// unusableIdempotencyKeys are header values that give a request no usable key:
// no header at all, a blank one, and one longer than the 200 characters the
// task services accept.
func unusableIdempotencyKeys() map[string]string {
	return map[string]string{
		"no header": "",
		"blank":     "   ",
		"too long":  strings.Repeat("k", 201),
	}
}

func requireIdempotencyKeyProblem(t *testing.T, response *httptest.ResponseRecorder, wantDetail string) {
	t.Helper()
	var problem idempotencyKeyProblem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("body %s: %v", response.Body.String(), err)
	}
	if response.Code != http.StatusBadRequest || problem.Code != idempotencyKeyRequiredCode {
		t.Fatalf("status=%d body=%s, want 400 %s", response.Code, response.Body.String(), idempotencyKeyRequiredCode)
	}
	if problem.Detail != wantDetail {
		t.Fatalf("detail = %q, want the Schema copy %q", problem.Detail, wantDetail)
	}
	if !strings.HasPrefix(problem.RequestID, "req_") {
		t.Fatalf("the answer names no request ID to look the cause up by: %s", response.Body.String())
	}
}

// idempotencyKeyCopy is the Schema copy of the shared answer; the test fails
// when the Schema declares none.
func idempotencyKeyCopy(t *testing.T) string {
	t.Helper()
	application, _ := englishBundle(t)
	text := application.Content.Messages["error.idempotency_key_required"]
	if strings.TrimSpace(text) == "" {
		t.Fatal("the Product Schema has no copy for error.idempotency_key_required")
	}
	return text
}

// idempotencyKeyPrepared freezes a 3D input in the session and returns its ID.
func idempotencyKeyPrepared(t *testing.T, handler http.Handler, cookie *http.Cookie, session string) string {
	t.Helper()
	prepare := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/prepare",
		`{"session_id":"`+session+`","capability":"text-to-3d","offering_id":"meshy/meshy-7.1/text-to-3d","input":{"prompt":"a carved oak chair"}}`, "")
	var prepared struct {
		ID string `json:"prepared_input_id"`
	}
	if prepare.Code != http.StatusCreated || json.Unmarshal(prepare.Body.Bytes(), &prepared) != nil || prepared.ID == "" {
		t.Fatalf("prepare status=%d body=%s", prepare.Code, prepare.Body.String())
	}
	return prepared.ID
}

func TestTaskGrantWithoutAUsableIdempotencyKeyNamesTheHeader(t *testing.T) {
	t.Parallel()
	want := idempotencyKeyCopy(t)
	handler, cookie, session := grantTestBrowser(t)
	body := `{"prepared_input_id":"` + idempotencyKeyPrepared(t, handler, cookie, session) + `"}`
	for name, key := range unusableIdempotencyKeys() {
		t.Run(name, func(t *testing.T) {
			response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants", body, key)
			requireIdempotencyKeyProblem(t, response, want)
		})
	}
	// A usable key is unchanged, up to the longest one the services accept.
	granted := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants", body, strings.Repeat("k", 200))
	if granted.Code != http.StatusCreated {
		t.Fatalf("a 200-character key = %d %s, want 201", granted.Code, granted.Body.String())
	}
}

func TestTaskSubmitWithoutAUsableIdempotencyKeyNamesTheHeader(t *testing.T) {
	t.Parallel()
	want := idempotencyKeyCopy(t)
	handler, cookie, session := grantTestBrowser(t)
	prepared := idempotencyKeyPrepared(t, handler, cookie, session)
	key := strings.Repeat("s", 200)
	grant := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants", `{"prepared_input_id":"`+prepared+`"}`, key)
	var requested struct {
		ID string `json:"request_id"`
	}
	if grant.Code != http.StatusCreated || json.Unmarshal(grant.Body.Bytes(), &requested) != nil || requested.ID == "" {
		t.Fatalf("grant status=%d body=%s", grant.Code, grant.Body.String())
	}
	if decision := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+requested.ID,
		`{"decision":"approve"}`, ""); decision.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", decision.Code, decision.Body.String())
	}
	submitBody := `{"prepared_input_id":"` + prepared + `","authorization_request_id":"` + requested.ID + `"}`
	for name, bad := range unusableIdempotencyKeys() {
		t.Run(name, func(t *testing.T) {
			response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", submitBody, bad)
			requireIdempotencyKeyProblem(t, response, want)
		})
	}
	// The refused requests left the approved grant alone: its own key submits the task.
	accepted := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", submitBody, key)
	if accepted.Code != http.StatusAccepted || !strings.Contains(accepted.Body.String(), `"replayed":false`) {
		t.Fatalf("submit with the grant's key = %d %s", accepted.Code, accepted.Body.String())
	}
}

func TestProcessingPlanWithoutAUsableIdempotencyKeyNamesTheHeader(t *testing.T) {
	t.Parallel()
	want := idempotencyKeyCopy(t)
	module, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	source, asset := processingPurposeSource(t, module, session, "tripo/tripo-v3.1-full/text-to-3d",
		`{"prompt":"a knight","authorize_extra_credits":true}`)
	body := processingPurposeBody(t, session, source, asset, "tripo/geometry/decimate-triangle",
		map[string]any{"authorize_extra_credits": true})
	keys := unusableIdempotencyKeys()
	// A plan ID is derived from its key, so the service wants at least 16 characters.
	keys["too short"] = strings.Repeat("k", 15)
	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/processing-plans", body, key)
			requireIdempotencyKeyProblem(t, response, want)
		})
	}
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/processing-plans", body, strings.Repeat("p", 16))
	if created.Code != http.StatusCreated {
		t.Fatalf("a 16-character key = %d %s, want 201", created.Code, created.Body.String())
	}
}

func TestSkillRunWithoutAUsableIdempotencyKeyNamesTheHeader(t *testing.T) {
	t.Parallel()
	want := idempotencyKeyCopy(t)
	_, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	asset := uploadSkillSource(t, handler, cookie)
	body := `{"session_id":"` + session + `","recipe_id":"image-three-view",` +
		`"recipe_revision":"2026-11-10.243.1","offering_id":"openai/gpt-image-2/image-to-image",` +
		`"source_asset_id":"` + asset + `"}`
	for name, key := range map[string]string{"no header": "", "blank": "   "} {
		t.Run(name, func(t *testing.T) {
			response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/skill-runs", body, key)
			requireIdempotencyKeyProblem(t, response, want)
		})
	}
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/skill-runs", body, "skill-run-key")
	if created.Code != http.StatusCreated {
		t.Fatalf("a usable key = %d %s, want 201", created.Code, created.Body.String())
	}
}

// Reroll already refused a request without the header, with its own Schema
// copy (#242). The same refusal now covers a blank or over-long key, which
// reached the service and came back as the generic invalid-input answer.
func TestTaskRerollWithoutAUsableIdempotencyKeyNamesTheHeader(t *testing.T) {
	t.Parallel()
	application, _ := englishBundle(t)
	want := application.Chat.Reroll.Messages.IdempotencyRequired
	if strings.TrimSpace(want) == "" {
		t.Fatal("the Product Schema has no copy for chat.reroll.messages.idempotency_required")
	}
	handler, cookie := retryBrowser(t)
	for name, key := range unusableIdempotencyKeys() {
		t.Run(name, func(t *testing.T) {
			response := grantTestRequest(t, handler, cookie, http.MethodPost, rerollRoute("failed-retryable"), "", key)
			requireIdempotencyKeyProblem(t, response, want)
		})
	}
	// A usable key reaches the service, which says a failed task is not rerollable.
	usable := grantTestRequest(t, handler, cookie, http.MethodPost, rerollRoute("failed-retryable"), "", strings.Repeat("r", 200))
	if usable.Code != http.StatusConflict || !strings.Contains(usable.Body.String(), "task_not_rerollable") {
		t.Fatalf("a 200-character key = %d %s, want 409 task_not_rerollable", usable.Code, usable.Body.String())
	}
}

// The answer is Schema copy, so it reads in the language of the request; the
// code stays the same in every language.
func TestIdempotencyKeyProblemFollowsTheRequestLanguage(t *testing.T) {
	t.Parallel()
	application, english := englishBundle(t)
	handler, cookie, session := grantTestBrowser(t)
	body := `{"prepared_input_id":"` + idempotencyKeyPrepared(t, handler, cookie, session) + `"}`
	grants := func(locale string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/grants", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://example.com")
		request.AddCookie(cookie)
		return serveWithLocale(handler, request, locale)
	}
	wantDefault := application.Content.Messages["error.idempotency_key_required"]
	wantEnglish := english.Messages["error.idempotency_key_required"]
	if wantEnglish == "" || wantEnglish == wantDefault {
		t.Fatalf("English copy %q must exist and differ from the default %q", wantEnglish, wantDefault)
	}
	requireIdempotencyKeyProblem(t, grants("en"), wantEnglish)
	requireIdempotencyKeyProblem(t, grants(""), wantDefault)
}
