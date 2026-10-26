package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const retrySessionID = "retry-session"

func retryRoute(taskID string) string { return "/api/v1/tasks/" + taskID + "/retry" }

// retryBrowser logs in as owner-a on a database that already holds failed
// tasks, the way the history of a real session looks.
func retryBrowser(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()
	record := authentication.UserRecord{
		ID: "owner-a", Username: "grant-user",
		PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2.",
		Nickname:     "Grant User", Email: "grant@example.test", Active: true,
	}
	directory := &authDirectory{
		byUsername: map[string]authentication.UserRecord{record.Username: record},
		byID:       map[string]authentication.UserRecord{record.ID: record},
	}
	_, handler, _, _ := newHTTPHarnessWithServices(t, []string{"user"}, nil,
		func(config *authentication.Config) { config.Directory = directory }, nil, nil, seedFailedTasks)
	login := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"grant-user","password":"correct-horse"}`))
	request.Header.Set("Origin", "http://example.com")
	handler.ServeHTTP(login, request)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	return handler, login.Result().Cookies()[0]
}

func seedFailedTasks(t *testing.T, databasePath string) {
	t.Helper()
	repository, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	now := time.Now().UTC().Add(-time.Hour)
	for _, session := range []chat.Session{
		{ID: retrySessionID, OwnerID: "owner-a", OwnerName: "Owner A", Title: "retry", CreatedAt: now, UpdatedAt: now},
		{ID: "foreign-session", OwnerID: "owner-b", OwnerName: "Owner B", Title: "other", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repository.CreateSession(context.Background(), session); err != nil {
			t.Fatal(err)
		}
	}
	importFailedTasks(t, repository)
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	// The import path carries no session; the live path always does.
	for id, session := range map[string]string{
		"failed-retryable": retrySessionID, "failed-final": retrySessionID,
		"failed-ambiguous": retrySessionID, "failed-balance": retrySessionID, "failed-foreign": "foreign-session",
	} {
		if _, err := database.Exec(`UPDATE generation_tasks SET session_id = ? WHERE id = ?`, session, id); err != nil {
			t.Fatal(err)
		}
	}
}

func importFailedTasks(t *testing.T, repository *sqlite.Repository) {
	t.Helper()
	sessionID := retrySessionID
	now := time.Now().UTC().Add(-time.Hour)
	offering := generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ProviderID: "meshy", Capability: generation.TextTo3D}
	build := func(id string, owner generation.OwnerID, session string, failure generation.Failure) sqlite.GenerationTaskImport {
		return sqlite.GenerationTaskImport{
			Task: generation.Task{
				ID: generation.TaskID(id), OwnerID: owner, SessionID: session, Capability: generation.TextTo3D, Offering: offering,
				Input: json.RawMessage(`{"prompt":"a carved oak chair"}`), State: generation.TaskFailed, Version: 1,
				CreatedAt: now, UpdatedAt: now, FinishedAt: &now, Failure: &failure,
			},
			Attempts: []sqlite.GenerationAttemptImport{{
				ID: generation.AttemptID(id + "-attempt-1"), Ordinal: 1, Offering: offering, State: generation.AttemptFailed,
				SubmissionKey: "retry-test:" + id, StartedAt: now, UpdatedAt: now, FinishedAt: &now,
			}},
		}
	}
	items := []sqlite.GenerationTaskImport{
		build("failed-retryable", "owner-a", sessionID, generation.Failure{Class: generation.FailureUpstreamUnavailable, Retryable: true, SafeDetail: "x"}),
		build("failed-final", "owner-a", sessionID, generation.Failure{Class: generation.FailureRejected, SafeDetail: "x"}),
		build("failed-ambiguous", "owner-a", sessionID, generation.Failure{Class: generation.FailureUpstreamUnavailable, Retryable: true, Ambiguous: true, SafeDetail: "x"}),
		build("failed-balance", "owner-a", sessionID, generation.Failure{Class: generation.FailureInsufficientBalance, SafeDetail: "upstream: insufficient balance acct-123"}),
		build("failed-foreign", "owner-b", "foreign-session", generation.Failure{Class: generation.FailureUpstreamUnavailable, Retryable: true, SafeDetail: "x"}),
	}
	if _, err := repository.ImportGenerationTasksAndAssets(context.Background(), items, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTaskRetryFollowsApprovalModeAndNeverDuplicates(t *testing.T) {
	t.Parallel()
	handler, cookie := retryBrowser(t)
	sessionID := retrySessionID

	t.Run("manual mode waits for a person and then runs once", func(t *testing.T) {
		first := grantTestRequest(t, handler, cookie, http.MethodPost, retryRoute("failed-retryable"), "", "")
		if first.Code != http.StatusAccepted {
			t.Fatalf("retry status=%d body=%s", first.Code, first.Body.String())
		}
		var waiting struct {
			Status          string `json:"status"`
			PreparedInputID string `json:"prepared_input_id"`
			RequestID       string `json:"request_id"`
			Key             string `json:"idempotency_key"`
		}
		if err := json.Unmarshal(first.Body.Bytes(), &waiting); err != nil || waiting.Status != "awaiting_decision" ||
			waiting.PreparedInputID == "" || waiting.RequestID == "" || waiting.Key == "" {
			t.Fatalf("waiting=%s err=%v", first.Body.String(), err)
		}
		// A second click reaches the same pending request, not a new one.
		again := grantTestRequest(t, handler, cookie, http.MethodPost, retryRoute("failed-retryable"), "", "")
		var second struct {
			RequestID string `json:"request_id"`
			Key       string `json:"idempotency_key"`
		}
		if err := json.Unmarshal(again.Body.Bytes(), &second); err != nil || again.Code != http.StatusAccepted ||
			second.RequestID != waiting.RequestID || second.Key != waiting.Key {
			t.Fatalf("second click=%d %s", again.Code, again.Body.String())
		}
		decision := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+waiting.RequestID, `{"decision":"approve"}`, "")
		if decision.Code != http.StatusOK {
			t.Fatalf("decision status=%d body=%s", decision.Code, decision.Body.String())
		}
		submit := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks",
			`{"prepared_input_id":"`+waiting.PreparedInputID+`","authorization_request_id":"`+waiting.RequestID+`"}`, waiting.Key)
		if submit.Code != http.StatusAccepted && submit.Code != http.StatusCreated {
			t.Fatalf("submit status=%d body=%s", submit.Code, submit.Body.String())
		}
		// After the person decided, the retry route reports the task that exists.
		done := grantTestRequest(t, handler, cookie, http.MethodPost, retryRoute("failed-retryable"), "", "")
		var result struct {
			Status   string `json:"status"`
			TaskID   string `json:"task_id"`
			Replayed bool   `json:"replayed"`
		}
		if err := json.Unmarshal(done.Body.Bytes(), &result); err != nil || done.Code != http.StatusAccepted ||
			result.Status != "submitted" || result.TaskID == "" || !result.Replayed {
			t.Fatalf("after submit=%d %s", done.Code, done.Body.String())
		}
	})

	t.Run("auto mode submits straight away and replays", func(t *testing.T) {
		put := grantTestRequest(t, handler, cookie, http.MethodPut, approvalPath(sessionID), `{"approvalMode":"auto"}`, "")
		if put.Code != http.StatusOK {
			t.Fatalf("switch status=%d body=%s", put.Code, put.Body.String())
		}
		var tasks []string
		for range 2 {
			response := grantTestRequest(t, handler, cookie, http.MethodPost, retryRoute("failed-ambiguous"), "", "")
			if response.Code != http.StatusConflict {
				t.Fatalf("ambiguous failure must not be retried blindly: %d %s", response.Code, response.Body.String())
			}
		}
		for range 2 {
			response := grantTestRequest(t, handler, cookie, http.MethodPost, retryRoute("failed-retryable"), "", "")
			var result struct {
				Status string `json:"status"`
				TaskID string `json:"task_id"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusAccepted || result.TaskID == "" {
				t.Fatalf("auto retry=%d %s", response.Code, response.Body.String())
			}
			tasks = append(tasks, result.TaskID)
		}
		if tasks[0] != tasks[1] {
			t.Fatalf("double click created two tasks: %v", tasks)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		for name, tc := range map[string]struct {
			id     string
			cookie *http.Cookie
			status int
		}{
			"final failure":    {"failed-final", cookie, http.StatusConflict},
			"foreign owner":    {"failed-foreign", cookie, http.StatusNotFound},
			"missing":          {"no-such-task", cookie, http.StatusNotFound},
			"no browser login": {"failed-retryable", nil, 0},
		} {
			response := grantTestRequest(t, handler, tc.cookie, http.MethodPost, retryRoute(tc.id), "", "")
			if tc.status == 0 {
				if response.Code == http.StatusAccepted {
					t.Fatalf("%s: accepted", name)
				}
				continue
			}
			if response.Code != tc.status {
				t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "foreign-session") {
				t.Fatalf("%s: leaked: %s", name, response.Body.String())
			}
		}
	})
}

// The provider-balance failure has its own public class and Schema text, and
// is not offered for retry (the account must be topped up or another model
// chosen first).
func TestInsufficientBalanceFailureIsPublishedWithItsOwnClassAndText(t *testing.T) {
	t.Parallel()
	handler, cookie := retryBrowser(t)
	response := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/tasks/failed-balance", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Failure struct {
			Class     string `json:"class"`
			Retryable bool   `json:"retryable"`
			Detail    string `json:"detail"`
		} `json:"failure"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Failure.Class != "insufficient_balance" || body.Failure.Retryable ||
		!strings.Contains(body.Failure.Detail, "余额不足") || strings.Contains(response.Body.String(), "acct-123") {
		t.Fatalf("failure = %+v body=%s", body.Failure, response.Body.String())
	}
	retry := grantTestRequest(t, handler, cookie, http.MethodPost, retryRoute("failed-balance"), "", "")
	if retry.Code != http.StatusConflict {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
	}
}
