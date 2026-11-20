package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// seedSessionDeletionFixtures gives owner local-user four sessions with a
// SkillRun each: two whose run has work in flight (a Task still running) and
// two whose run is at rest (its Task succeeded). One of each is owned by a
// canvas project, so both deletion routes are exercised.
func seedSessionDeletionFixtures(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const stamp = "2026-10-22T00:00:00Z"
	statements := []string{}
	for _, fixture := range []struct{ session, taskState string }{
		{"busy", "RUNNING"}, {"resting", "SUCCEEDED"}, {"canvas-busy", "RUNNING"}, {"canvas-resting", "SUCCEEDED"},
	} {
		s, state := fixture.session, fixture.taskState
		statements = append(statements,
			`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at)
				VALUES ('`+s+`','local-user','Owner','`+s+`','`+stamp+`','`+stamp+`')`,
			`INSERT INTO generation_tasks (id, owner_id, capability, offering_json, input_json, request_hash,
				idempotency_key, state, version, created_at, updated_at, session_id)
				VALUES ('task-`+s+`','local-user','image-to-image','{}','{}','hash-`+s+`','key-`+s+`','`+state+`',1,'`+stamp+`','`+stamp+`','`+s+`')`,
			`INSERT INTO skill_runs (id, owner_id, session_id, recipe_id, recipe_revision, recipe_hash,
				selected_offering_id, source_asset_id, idempotency_key, request_hash, created_at, updated_at)
				VALUES ('run-`+s+`','local-user','`+s+`','recipe','rev','hash','offering','asset','run-key-`+s+`','request','`+stamp+`','`+stamp+`')`,
			`INSERT INTO skill_run_steps (run_id, ordinal, step_key, spec_json, phase, operation_id, idempotency_key,
				plan_step_id, prepared_input_id, prepared_input_hash, grant_request_id, task_id, review)
				VALUES ('run-`+s+`',0,'front','{}','task_bound','operation-`+s+`','step-key-`+s+`','plan-`+s+`',
				'prepared-`+s+`','prepared-hash','grant-`+s+`','task-`+s+`','unreviewed')`,
		)
	}
	for _, fixture := range []struct{ project, session string }{
		{"project-busy", "canvas-busy"}, {"project-resting", "canvas-resting"},
	} {
		statements = append(statements,
			`INSERT INTO go_canvas_projects (id, owner_id, name, session_id, owns_session, viewport, bg_style, created_at, updated_at)
				VALUES ('`+fixture.project+`','local-user','`+fixture.project+`','`+fixture.session+`',1,'{}','dots','`+stamp+`','`+stamp+`')`)
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed: %v\n%s", err, statement)
		}
	}
}

func deleteAs(handler http.Handler, target, acceptLanguage string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodDelete, target, nil)
	request.Header.Set("X-User-ID", "local-user")
	if acceptLanguage != "" {
		request.Header.Set("Accept-Language", acceptLanguage)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func errorText(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", response.Body.String(), err)
	}
	text, _ := body["error"].(string)
	return text
}

// A session (or the canvas project that owns one) whose SkillRun has work in
// flight is not deleted: 409 with the Schema's text in the request's language,
// and nothing is lost. Once the work ended the same deletion succeeds and takes
// the SkillRun with it, where it used to fail with 500 (#384).
func TestDeletingASessionWithSkillRunWorkInFlightAnswers409WithTheSchemaText(t *testing.T) {
	t.Parallel()
	application, english := englishBundle(t)
	_, handler, _, _ := newHTTPHarnessWithServices(t, []string{"admin"}, nil, nil, nil, nil, seedSessionDeletionFixtures)
	zh := application.Chat.Presentation.Messages.SessionDeleteBlocked
	en := english.Fields["chat.presentation.messages.session_delete_blocked"]
	if zh == "" || en == "" || zh == en {
		t.Fatalf("the Schema's texts: zh %q, en %q", zh, en)
	}

	for _, route := range []struct{ name, target string }{
		{"chat session", "/api/chat/sessions/busy"},
		{"canvas project", "/api/canvas/projects/project-busy"},
	} {
		t.Run(route.name, func(t *testing.T) {
			for _, locale := range []struct{ accept, want string }{{"", zh}, {"zh-CN", zh}, {"en-US", en}} {
				response := deleteAs(handler, route.target, locale.accept)
				if response.Code != http.StatusConflict {
					t.Fatalf("[%s] status = %d body = %s", locale.accept, response.Code, response.Body)
				}
				if got := errorText(t, response); got != locale.want {
					t.Fatalf("[%s] error = %q, want %q", locale.accept, got, locale.want)
				}
			}
		})
	}
	// The refusals changed nothing: both sessions are still there.
	for _, target := range []string{"/api/chat/sessions/busy", "/api/chat/sessions/canvas-busy"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("X-User-ID", "local-user")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s after a refused deletion = %d", target, response.Code)
		}
	}

	if response := deleteAs(handler, "/api/chat/sessions/resting", ""); response.Code != http.StatusNoContent {
		t.Fatalf("deleting a session whose SkillRun is at rest = %d body = %s", response.Code, response.Body)
	}
	if response := deleteAs(handler, "/api/canvas/projects/project-resting", ""); response.Code != http.StatusOK {
		t.Fatalf("deleting a canvas project whose session's SkillRun is at rest = %d body = %s", response.Code, response.Body)
	}
	for _, target := range []string{"/api/chat/sessions/resting", "/api/chat/sessions/canvas-resting"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("X-User-ID", "local-user")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s after its deletion = %d, want 404", target, response.Code)
		}
	}
}
