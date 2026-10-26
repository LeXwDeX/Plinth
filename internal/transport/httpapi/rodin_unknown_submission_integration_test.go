package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/transport/httpapi"
)

const rodinHTTPTestOffering = "hyper3d/rodin-gen-2.5/text-to-3d"

// The upstream is deliberately local. A successful create response requires
// both uuid and jobs.subscription_key; omitting the latter models an accepted
// submission whose receipt cannot safely be reconciled, not a real paid run.
func TestRodinHTTPSubmissionFailureKeepsGrantAndDispatchIdentity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		failure      generation.FailureClass
		attemptState generation.AttemptState
		ambiguous    bool
	}{
		{
			name: "accepted_without_subscription_receipt", status: http.StatusCreated,
			body:    `{"uuid":"123e4567-e89b-12d3-a456-426614174000","jobs":{}}`,
			failure: generation.FailureSubmissionUnknown, attemptState: generation.AttemptSubmissionUnknown,
			ambiguous: true,
		},
		{
			name: "explicit_authentication_rejection", status: http.StatusUnauthorized,
			failure: generation.FailureAuthentication, attemptState: generation.AttemptFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			source := rodinHTTPTestCatalog(t)
			offering, ok := source.Get(rodinHTTPTestOffering)
			if !ok {
				t.Fatal("Rodin text Offering is missing")
			}
			var creates, otherRequests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v2/rodin" {
					otherRequests.Add(1)
					http.NotFound(w, r)
					return
				}
				creates.Add(1)
				if r.Header.Get("Authorization") != "Bearer synthetic-rodin-http-key" {
					t.Error("create did not use the fixture credential")
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Errorf("create multipart: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("prompt") != "a carved oak chair" || r.FormValue("tier") != "Gen-2.5-Extreme-High" {
					t.Errorf("unexpected wire prompt=%q tier=%q", r.FormValue("prompt"), r.FormValue("tier"))
				}
				if _, leaked := r.MultipartForm.Value["authorize_extra_credits"]; leaked {
					t.Error("execution consent leaked into the Provider request")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(upstream.Close)
			adapter := rodinHTTPTestAdapter(t, source, offering.AdapterKey, upstream)
			module, handler, cookie, foreignCookie, sessionID := rodinHTTPTestBrowser(t, source, offering.AdapterKey, adapter)
			fixture, ok := fixtureExecutions.Load(module)
			if !ok {
				t.Fatal("missing HTTP repository fixture")
			}
			repository := fixture.(*fixtureExecutionState).repository
			assertDispatches := func(want int32) {
				t.Helper()
				if got := creates.Load(); got != want || otherRequests.Load() != 0 {
					t.Fatalf("upstream create=%d want=%d other requests=%d", got, want, otherRequests.Load())
				}
			}

			input := json.RawMessage(`{"prompt":"a carved oak chair","authorize_extra_credits":true}`)
			prepareBody, err := json.Marshal(map[string]any{
				"session_id": sessionID, "capability": "text-to-3d", "offering_id": rodinHTTPTestOffering, "input": input,
			})
			if err != nil {
				t.Fatal(err)
			}
			prepare := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/prepare", string(prepareBody), "")
			var preparedView struct {
				ID        generation.PreparedInputID `json:"prepared_input_id"`
				InputHash string                     `json:"input_hash"`
			}
			if err := json.Unmarshal(prepare.Body.Bytes(), &preparedView); err != nil || prepare.Code != http.StatusCreated || preparedView.ID == "" {
				t.Fatalf("prepare=%d %s err=%v", prepare.Code, prepare.Body.String(), err)
			}
			prepared, err := repository.GetPreparedInput(ctx, "owner-a", preparedView.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, normalized, err := source.Normalize(generation.TextTo3D, rodinHTTPTestOffering, input)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.OwnerID != "owner-a" || prepared.SessionID != sessionID || prepared.Capability != generation.TextTo3D ||
				prepared.OfferingID != offering.ID || prepared.OfferingRevision != offering.Revision ||
				len(prepared.InputHash) != 64 || prepared.InputHash != preparedView.InputHash || !bytes.Equal(prepared.NormalizedInput, normalized) {
				t.Fatalf("prepared identity or normalized input diverged: %+v", prepared)
			}

			const key = "rodin-http-approved-create"
			grant := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants",
				`{"prepared_input_id":"`+string(prepared.ID)+`"}`, key)
			var requested struct {
				ID     string                `json:"request_id"`
				Status executiongrant.Status `json:"status"`
			}
			if err := json.Unmarshal(grant.Body.Bytes(), &requested); err != nil || grant.Code != http.StatusCreated || requested.ID == "" || requested.Status != executiongrant.Pending {
				t.Fatalf("grant=%d %s err=%v", grant.Code, grant.Body.String(), err)
			}
			submitBody := `{"prepared_input_id":"` + string(prepared.ID) + `","authorization_request_id":"` + requested.ID + `"}`
			pending := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", submitBody, key)
			if pending.Code != http.StatusForbidden {
				t.Fatalf("pending grant submit=%d %s", pending.Code, pending.Body.String())
			}
			assertDispatches(0)
			decision := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+requested.ID, `{"decision":"approve"}`, "")
			if decision.Code != http.StatusOK {
				t.Fatalf("approve=%d %s", decision.Code, decision.Body.String())
			}
			approved, err := repository.GetExecutionGrant(ctx, "owner-a", requested.ID)
			if err != nil || approved.Status != executiongrant.Approved || approved.DecidedBy != "owner-a" || approved.DecidedAt == nil {
				t.Fatalf("human decision=%+v err=%v", approved, err)
			}
			wrongKey := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", submitBody, "different-key")
			if wrongKey.Code != http.StatusForbidden {
				t.Fatalf("changed key submit=%d %s", wrongKey.Code, wrongKey.Body.String())
			}
			foreign := grantTestRequest(t, handler, foreignCookie, http.MethodPost, "/api/v1/tasks", submitBody, key)
			if foreign.Code != http.StatusNotFound {
				t.Fatalf("foreign owner submit=%d %s", foreign.Code, foreign.Body.String())
			}
			changedBody := bytes.Replace(prepareBody, []byte("a carved oak chair"), []byte("a marble statue"), 1)
			changed := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/prepare", string(changedBody), "")
			var different struct {
				ID generation.PreparedInputID `json:"prepared_input_id"`
			}
			if err := json.Unmarshal(changed.Body.Bytes(), &different); err != nil || changed.Code != http.StatusCreated || different.ID == "" || different.ID == prepared.ID {
				t.Fatalf("changed prepare=%d %s err=%v", changed.Code, changed.Body.String(), err)
			}
			forgedBody := `{"prepared_input_id":"` + string(different.ID) + `","authorization_request_id":"` + requested.ID + `"}`
			forged := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", forgedBody, key)
			if forged.Code != http.StatusForbidden {
				t.Fatalf("approval reused for changed input: %d %s", forged.Code, forged.Body.String())
			}
			assertDispatches(0)

			submit := func(replayed bool, taskID generation.TaskID) generation.TaskID {
				t.Helper()
				response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", submitBody, key)
				var accepted generation.Accepted
				if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil || response.Code != http.StatusAccepted || accepted.TaskID == "" || accepted.Replayed != replayed {
					t.Fatalf("submit=%d %s err=%v", response.Code, response.Body.String(), err)
				}
				if taskID != "" && accepted.TaskID != taskID {
					t.Fatalf("replay changed task: %s -> %s", taskID, accepted.TaskID)
				}
				return accepted.TaskID
			}
			taskID := submit(false, "")
			submit(true, taskID)
			assertDispatches(0)
			cfg := generation.CoordinatorConfig{WorkerID: "rodin-http-test", BatchSize: 1, Concurrency: 1, LeaseFor: time.Minute}
			if count, err := module.ReconcileOnce(ctx, cfg); err != nil || count != 1 {
				t.Fatalf("reconcile count=%d err=%v", count, err)
			}
			assertDispatches(1)
			aggregate, err := repository.Load(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			task := aggregate.Task
			attempt := aggregate.CurrentAttempt
			if task.State != generation.TaskFailed || task.FinishedAt == nil || task.Failure == nil ||
				task.Failure.Class != tc.failure || task.Failure.Ambiguous != tc.ambiguous || task.Failure.Retryable {
				t.Fatalf("terminal task=%+v failure=%+v", task, task.Failure)
			}
			if task.OwnerID != prepared.OwnerID || task.SessionID != prepared.SessionID || task.Offering.ID != prepared.OfferingID ||
				task.Offering.Revision != prepared.OfferingRevision || task.IdempotencyKey != key || !bytes.Equal(task.Input, prepared.NormalizedInput) ||
				task.RequestHash != generation.ExpectedRequestHash(prepared.OfferingID, prepared.NormalizedInput) {
				t.Fatalf("task did not retain frozen admission identity: %+v", task)
			}
			if attempt == nil || attempt.ID != task.CurrentAttemptID || attempt.TaskID != task.ID || attempt.Ordinal != 1 ||
				attempt.SubmissionKey == "" || attempt.State != tc.attemptState || attempt.FinishedAt == nil || attempt.UpstreamJob != nil ||
				attempt.Offering.ID != task.Offering.ID || attempt.Offering.Revision != task.Offering.Revision || attempt.Failure == nil ||
				attempt.Failure.Class != tc.failure || attempt.Failure.Ambiguous != tc.ambiguous || attempt.Failure.Retryable {
				t.Fatalf("terminal attempt=%+v", attempt)
			}
			var dispatch struct {
				Stage         string `json:"stage"`
				SubmissionKey string `json:"submission_key"`
				Endpoint      string `json:"endpoint"`
			}
			if err := json.Unmarshal(attempt.Opaque, &dispatch); err != nil || dispatch.Stage != "rodin.create" || dispatch.Endpoint != "rodin" || dispatch.SubmissionKey != attempt.SubmissionKey {
				t.Fatalf("dispatch identity=%s err=%v", attempt.Opaque, err)
			}
			bound, err := repository.GetExecutionGrant(ctx, "owner-a", requested.ID)
			if err != nil || bound.Status != executiongrant.Bound || bound.TaskID != string(task.ID) ||
				bound.OwnerID != string(prepared.OwnerID) || bound.SessionID != prepared.SessionID || bound.OfferingID != string(prepared.OfferingID) ||
				bound.OfferingRevision != prepared.OfferingRevision || bound.PreparedInputID != string(prepared.ID) || bound.PreparedInputHash != prepared.InputHash ||
				bound.IdempotencyKey != task.IdempotencyKey || bound.OperationID != httpapi.StandaloneOperationID(prepared.ID) || bound.PlanStepID != "" {
				t.Fatalf("bound grant=%+v err=%v", bound, err)
			}
			admitted, err := repository.AdmittedPreparedInput(ctx, task.OwnerID, task.ID)
			if err != nil || admitted != prepared.ID || len(task.Assets) != 0 || len(aggregate.Claims) != 0 {
				t.Fatalf("admitted=%s assets=%d claims=%d err=%v", admitted, len(task.Assets), len(aggregate.Claims), err)
			}

			// Replaying the approved input and asking for a retry cannot authorize
			// a second create after either an ambiguous receipt or a definitive 401.
			submit(true, taskID)
			retry := grantTestRequest(t, handler, cookie, http.MethodPost, retryRoute(string(taskID)), "", "")
			if retry.Code != http.StatusConflict {
				t.Fatalf("terminal failure retry=%d %s", retry.Code, retry.Body.String())
			}
			forged = grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", forgedBody, key)
			if forged.Code != http.StatusConflict {
				t.Fatalf("changed prepared input reused grant: %d %s", forged.Code, forged.Body.String())
			}
			foreignRetry := grantTestRequest(t, handler, foreignCookie, http.MethodPost, retryRoute(string(taskID)), "", "")
			if foreignRetry.Code != http.StatusNotFound {
				t.Fatalf("foreign retry=%d %s", foreignRetry.Code, foreignRetry.Body.String())
			}
			for range 2 {
				if count, err := module.ReconcileOnce(ctx, cfg); err != nil || count != 0 {
					t.Fatalf("terminal task reclaimed count=%d err=%v", count, err)
				}
			}
			assertDispatches(1)
			after, err := repository.Load(ctx, taskID)
			if err != nil || after.CurrentAttempt == nil || after.CurrentAttempt.ID != attempt.ID || after.CurrentAttempt.Ordinal != 1 ||
				after.CurrentAttempt.SubmissionKey != attempt.SubmissionKey || after.Task.State != generation.TaskFailed {
				t.Fatalf("terminal replay changed attempt: %+v err=%v", after, err)
			}
		})
	}
}

func rodinHTTPTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "catalog", "default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range doc["offerings"].([]any) {
		offering := item.(map[string]any)
		if offering["id"] == rodinHTTPTestOffering {
			offering["enabled"] = true
			found = true
		}
	}
	if !found {
		t.Fatal("Rodin text Offering is missing")
	}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	source, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func rodinHTTPTestAdapter(t *testing.T, source *catalog.Catalog, key string, upstream *httptest.Server) *hyper3d.Adapter {
	t.Helper()
	for _, connection := range source.Application().Runtime.ProviderConnections {
		if connection.AdapterKey != key {
			continue
		}
		adapter, err := hyper3d.New(hyper3d.Config{
			BaseURL: upstream.URL + "/api/v2", APIKey: "synthetic-rodin-http-key",
			AuthHeader: connection.AuthHeader, AuthPrefix: connection.AuthPrefix,
			HTTPClient: upstream.Client(), RequestTimeout: 3 * time.Second,
			ErrorMapping: connection.ErrorMapping, StatusMapping: connection.StatusMapping,
		})
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	}
	t.Fatalf("missing Provider connection for %s", key)
	return nil
}

func rodinHTTPTestBrowser(t *testing.T, source *catalog.Catalog, key string, adapter generation.ProviderPort) (*generation.Module, http.Handler, *http.Cookie, *http.Cookie, string) {
	t.Helper()
	directory := &authDirectory{byUsername: map[string]authentication.UserRecord{}, byID: map[string]authentication.UserRecord{}}
	for _, id := range []string{"owner-a", "owner-b"} {
		record := authentication.UserRecord{
			ID: id, Username: id, Nickname: id, Email: id + "@example.test", Active: true,
			PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2.",
		}
		directory.byUsername[id], directory.byID[id] = record, record
	}
	module, handler, _, _ := newHTTPHarnessWithCatalogAndProviders(t, []string{"user"}, nil,
		func(config *authentication.Config) { config.Directory = directory }, nil, nil, source,
		map[string]generation.ProviderPort{key: adapter})
	login := func(username string) *http.Cookie {
		t.Helper()
		response := grantTestRequest(t, handler, nil, http.MethodPost, "/api/auth/login",
			`{"username":"`+username+`","password":"correct-horse"}`, "")
		if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
			t.Fatalf("login %s=%d %s", username, response.Code, response.Body.String())
		}
		return response.Result().Cookies()[0]
	}
	cookie, foreignCookie := login("owner-a"), login("owner-b")
	response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions", `{"title":"Rodin HTTP receipt test"}`, "")
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || response.Code != http.StatusCreated || session.ID == "" {
		t.Fatalf("session=%d %s err=%v", response.Code, response.Body.String(), err)
	}
	return module, handler, cookie, foreignCookie, session.ID
}
