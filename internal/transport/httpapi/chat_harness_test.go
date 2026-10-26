package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

func TestChatHarnessPersistsSelectionCASWithoutTask(t *testing.T) {
	t.Parallel()
	handler, cookie, session := grantTestBrowser(t)
	path := "/api/v1/chat/sessions/" + session + "/harness"
	get := grantTestRequest(t, handler, cookie, http.MethodGet, path, "", "")
	if get.Code != http.StatusOK {
		t.Fatalf("harness GET=%d %s", get.Code, get.Body.String())
	}
	var view struct {
		Selection struct {
			Revision int64  `json:"revision"`
			Digest   string `json:"digest"`
		}
		Status string
	}
	if err := json.Unmarshal(get.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Selection.Revision != 0 || view.Selection.Digest == "" || view.Status != "free" {
		t.Fatalf("default=%s", get.Body.String())
	}
	// #327: saving what is already saved keeps revision 0 (no new version).
	same := `{"expected_revision":0,"selection":{"operation_id":"","enabled_operations":[],"sources":[],"fixed_parameters":{},"fillable_parameters":[]}}`
	unchanged := grantTestRequest(t, handler, cookie, http.MethodPut, path, same, "")
	if unchanged.Code != http.StatusOK || !strings.Contains(unchanged.Body.String(), `"revision":0`) {
		t.Fatalf("unchanged=%d %s", unchanged.Code, unchanged.Body.String())
	}
	body := `{"expected_revision":0,"selection":{"operation_id":"","enabled_operations":["remesh"],"sources":[],"fixed_parameters":{},"fillable_parameters":[]}}`
	put := grantTestRequest(t, handler, cookie, http.MethodPut, path, body, "")
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), `"revision":1`) {
		t.Fatalf("put=%d %s", put.Code, put.Body.String())
	}
	again := grantTestRequest(t, handler, cookie, http.MethodPut, path, body, "")
	if again.Code != http.StatusConflict {
		t.Fatalf("stale=%d %s", again.Code, again.Body.String())
	}
	for _, invalid := range []string{
		`{}`, `{"selection":{}}`,
		`{"expected_revision":1,"selection":{},"execute":true}`,
		`{"expected_revision":-1,"selection":{}}`,
		`{"expected_revision":"1","selection":{}}`,
		`{"expected_revision":1,"selection":{"owner_id":"owner-b"}}`,
		`{"expected_revision":1,"selection":{}} {}`,
	} {
		r := grantTestRequest(t, handler, cookie, http.MethodPut, path, invalid, "")
		if r.Code != http.StatusBadRequest {
			t.Fatalf("invalid=%d %s", r.Code, r.Body.String())
		}
	}
	tasks := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/tasks", "", "")
	if tasks.Code != http.StatusOK {
		t.Fatal(tasks.Code, tasks.Body.String())
	}
	var list struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(tasks.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tasks) != 0 {
		t.Fatal("selection created task")
	}
}

func TestChatHarnessIsOwnerScopedWithoutSessionLeakage(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	if _, err := repository.CreateSession(context.Background(), chat.Session{
		ID: "foreign-harness-session", OwnerID: "owner-b", OwnerName: "Other", Title: "foreign",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	var foreignGetBody, foreignPutBody string
	for _, session := range []string{"foreign-harness-session", "missing-harness-session"} {
		get := grantTestRequest(t, handler, cookie, http.MethodGet, harnessPath(session), "", "")
		put := grantTestRequest(t, handler, cookie, http.MethodPut, harnessPath(session), freeHarnessUpdate(0), "")
		if get.Code != http.StatusNotFound || put.Code != http.StatusNotFound {
			t.Fatalf("session %s leaked: GET=%d %s PUT=%d %s", session, get.Code, get.Body.String(), put.Code, put.Body.String())
		}
		// The bodies differ only in their per-request ID (#316).
		if withoutRequestID(t, get.Body.Bytes()) != withoutRequestID(t, put.Body.Bytes()) {
			t.Fatalf("unexpected response difference for inaccessible session %s: GET=%s PUT=%s", session, get.Body.String(), put.Body.String())
		}
		if session == "foreign-harness-session" {
			foreignGetBody, foreignPutBody = withoutRequestID(t, get.Body.Bytes()), withoutRequestID(t, put.Body.Bytes())
		} else if withoutRequestID(t, get.Body.Bytes()) != foreignGetBody || withoutRequestID(t, put.Body.Bytes()) != foreignPutBody {
			t.Fatalf("foreign and missing sessions are distinguishable: foreign GET/PUT=%s/%s missing GET/PUT=%s/%s",
				foreignGetBody, foreignPutBody, get.Body.String(), put.Body.String())
		}
	}
	foreign, err := repository.GetHarnessSelection(context.Background(), "owner-b", "foreign-harness-session")
	if err != nil || foreign.Revision != 0 || foreign.OperationID != "" {
		t.Fatalf("foreign selection changed: revision=%d operation=%q err=%v", foreign.Revision, foreign.OperationID, err)
	}
}

func TestChatHarnessRefreshRestoresExactSelectionWithoutTasksOrPendingGrants(t *testing.T) {
	t.Parallel()
	_, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	path := harnessPath(session)
	before := harnessWorkSet(t, handler, cookie, session)
	body := freeHarnessUpdate(0)
	put := grantTestRequest(t, handler, cookie, http.MethodPut, path, body, "")
	if put.Code != http.StatusOK {
		t.Fatalf("put=%d %s", put.Code, put.Body.String())
	}
	var saved struct {
		Selection struct {
			Revision          int64    `json:"revision"`
			Digest            string   `json:"digest"`
			OperationID       string   `json:"operation_id"`
			EnabledOperations []string `json:"enabled_operations"`
		}
		Status string `json:"status"`
	}
	if err := json.Unmarshal(put.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Selection.Revision != 1 || saved.Selection.Digest == "" || saved.Selection.OperationID != "" ||
		saved.Status != "free" || len(saved.Selection.EnabledOperations) != 1 || saved.Selection.EnabledOperations[0] != "remesh" {
		t.Fatalf("saved selection=%s", put.Body.String())
	}
	// A fresh GET models a page reload: persisted selection and immutable digest
	// must be restored exactly, not recomputed from current transient UI state.
	refreshed := grantTestRequest(t, handler, cookie, http.MethodGet, path, "", "")
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh=%d %s", refreshed.Code, refreshed.Body.String())
	}
	var restored struct {
		Selection struct {
			Revision          int64    `json:"revision"`
			Digest            string   `json:"digest"`
			OperationID       string   `json:"operation_id"`
			EnabledOperations []string `json:"enabled_operations"`
		}
		Status string `json:"status"`
	}
	if err := json.Unmarshal(refreshed.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Selection.Revision != saved.Selection.Revision || restored.Selection.Digest != saved.Selection.Digest ||
		restored.Selection.OperationID != saved.Selection.OperationID || restored.Status != "free" ||
		len(restored.Selection.EnabledOperations) != 1 || restored.Selection.EnabledOperations[0] != "remesh" {
		t.Fatalf("restored selection=%s, saved=%s", refreshed.Body.String(), put.Body.String())
	}
	if after := harnessWorkSet(t, handler, cookie, session); after != before {
		t.Fatalf("Harness selection changed Tasks or pending grants: before=%+v after=%+v", before, after)
	}
}

type harnessWorkSetSnapshot struct{ tasks, pending int }

func harnessWorkSet(t *testing.T, handler http.Handler, cookie *http.Cookie, session string) harnessWorkSetSnapshot {
	t.Helper()
	tasks := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/tasks", "", "")
	if tasks.Code != http.StatusOK {
		t.Fatalf("tasks=%d %s", tasks.Code, tasks.Body.String())
	}
	var taskList struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(tasks.Body.Bytes(), &taskList); err != nil {
		t.Fatal(err)
	}
	approvals := grantTestRequest(t, handler, cookie, http.MethodGet,
		"/api/v1/chat/sessions/"+session+"/pending-approvals", "", "")
	if approvals.Code != http.StatusOK {
		t.Fatalf("pending approvals=%d %s", approvals.Code, approvals.Body.String())
	}
	var pending struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(approvals.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	return harnessWorkSetSnapshot{tasks: len(taskList.Tasks), pending: len(pending.Items)}
}

func harnessPath(session string) string { return "/api/v1/chat/sessions/" + session + "/harness" }

func freeHarnessUpdate(revision int64) string {
	return `{"expected_revision":` + strconv.FormatInt(revision, 10) +
		`,"selection":{"operation_id":"","enabled_operations":["remesh"],"sources":[],"fixed_parameters":{},"fillable_parameters":[]}}`
}

func TestChatHarnessAgentSourceIsExplicitPersistentScopeWithoutWork(t *testing.T) {
	t.Parallel()
	_, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	before := harnessWorkSet(t, handler, cookie, session)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, _ := product.Get("meshy/processing/remesh")
	body, _ := json.Marshal(map[string]any{"expected_revision": 0, "selection": map[string]any{"operation_id": "remesh", "offering_id": o.ID, "offering_revision": o.Revision, "enabled_operations": []string{"remesh"}, "sources": []map[string]string{{"kind": "task_asset", "selection_mode": "agent", "parameter": "*"}}, "fixed_parameters": map[string]any{}, "fillable_parameters": []string{}}})
	saved := grantTestRequest(t, handler, cookie, http.MethodPut, harnessPath(session), string(body), "")
	if saved.Code != http.StatusOK {
		t.Fatalf("agent scope PUT %d %s", saved.Code, saved.Body.String())
	}
	var first, again struct {
		Selection harness.Selection `json:"selection"`
		Status    string            `json:"status"`
	}
	json.Unmarshal(saved.Body.Bytes(), &first)
	if first.Status != "needs_input" || first.Selection.Digest == "" || len(first.Selection.Sources) != 1 || first.Selection.Sources[0].SelectionMode != "agent" || first.Selection.Sources[0].TaskID != "" || first.Selection.Sources[0].AssetID != "" {
		t.Fatal("Agent permission was replaced by an empty/fixed source")
	}
	refreshed := grantTestRequest(t, handler, cookie, http.MethodGet, harnessPath(session), "", "")
	json.Unmarshal(refreshed.Body.Bytes(), &again)
	if refreshed.Code != http.StatusOK || again.Selection.Digest != first.Selection.Digest || again.Selection.Revision != 1 || again.Selection.Sources[0] != first.Selection.Sources[0] {
		t.Fatal("refresh changed source scope identity")
	}
	after := harnessWorkSet(t, handler, cookie, session)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("saving Agent source scope created work")
	}
	schema := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/schema", "", "")
	var public struct {
		Schema struct {
			Chat struct {
				Harness catalog.PublicHarnessSchema `json:"harness"`
			} `json:"chat"`
		} `json:"schema"`
	}
	json.Unmarshal(schema.Body.Bytes(), &public)
	if schema.Code != http.StatusOK || public.Schema.Chat.Harness.DefaultTaskSourceMode != "agent" || len(public.Schema.Chat.Harness.TaskSourceModes) != 2 {
		t.Fatal("source modes missing from public schema")
	}
	if bytes.Contains(saved.Body.Bytes(), []byte("source_candidates")) || bytes.Contains(saved.Body.Bytes(), []byte("fingerprint")) {
		t.Fatal("private candidate evidence leaked")
	}
}
