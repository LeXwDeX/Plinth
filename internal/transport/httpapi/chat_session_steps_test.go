package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

const chatSessionStepsRoute = "/api/v1/chat/sessions/{sessionID}/steps"

func chatSessionStepsPath(sessionID string) string {
	return strings.ReplaceAll(chatSessionStepsRoute, "{sessionID}", sessionID)
}

type chatSessionStepsWire struct {
	Mode  string `json:"mode"`
	Used  int    `json:"used"`
	Cap   int    `json:"cap"`
	Extra int    `json:"extra"`
}

// mcpStepAssets marks the named sessions as the owner's external MCP assets,
// the reader internal/app installs on the chat service (#332, #330).
type mcpStepAssets struct {
	mark map[string]chat.MCPAsset
}

func (assets mcpStepAssets) MCPAssetsOfSessions(
	_ context.Context, owner string, sessionIDs []string,
) (map[string]chat.MCPAsset, error) {
	found := map[string]chat.MCPAsset{}
	for _, sessionID := range sessionIDs {
		if asset, ok := assets.mark[sessionID]; ok && asset.OwnerID == owner {
			found[sessionID] = asset
		}
	}
	return found, nil
}

// insertCountedStepTask stores one succeeded 3D Task of the session, so the
// session's asset reads as one step used.
func insertCountedStepTask(t *testing.T, harness *assetListHTTPHarness, sessionID, taskID string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+harness.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO generation_tasks
 (id, owner_id, session_id, capability, offering_json, input_json, request_hash, idempotency_key,
  state, progress, current_attempt_id, materialize_failures, version, created_at, updated_at)
 VALUES (?, 'owner-a', ?, 'text-to-3d', '{}', '{}', ?, ?,
  'SUCCEEDED', 0, ?, 0, 1, '2026-10-22T00:00:00Z', '2026-10-22T00:00:00Z')`,
		taskID, sessionID, "hash-"+taskID, "key-"+taskID, "attempt-"+taskID); err != nil {
		t.Fatalf("insert step task: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO generation_attempts
 (id, task_id, ordinal, offering_json, state, submission_key, started_at, updated_at, last_observation_phase)
 VALUES (?, ?, 1, '{}', 'SUCCEEDED', ?, '2026-10-22T00:00:00Z', '2026-10-22T00:00:00Z', 'COMPLETED')`,
		"attempt-"+taskID, taskID, "submission-"+taskID); err != nil {
		t.Fatalf("insert step attempt: %v", err)
	}
}

func saveMCPCap(t *testing.T, harness *assetListHTTPHarness, limit int) {
	t.Helper()
	body := `{"expected_version":0,"list":{"mode":"agent","cap":20},"mcp":{"mode":"cap","cap":` +
		strconv.Itoa(limit) + `}}`
	response := putStepPolicy(t, harness.handler, harness.cookie, body)
	if response.Code != http.StatusOK {
		t.Fatalf("save step policy = %d %s", response.Code, response.Body.String())
	}
}

func decodeSessionSteps(t *testing.T, response *httptest.ResponseRecorder) chatSessionStepsWire {
	t.Helper()
	var body chatSessionStepsWire
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("session steps body %q: %v", response.Body.String(), err)
	}
	return body
}

// #330 §6: an external MCP asset session can be given more steps from the
// browser. The read supplies the standing, the write raises the asset's whole
// grant, and the change is audited with its before and after. The write needs
// the real browser session: a credential header cannot raise a cap, and a
// session that is no asset of the owner is 404.
func TestChatSessionExtraStepsRaiseAnMCPAssetGrantAndAreAudited(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	harness.chat.SetMCPStepSessions(mcpStepAssets{mark: map[string]chat.MCPAsset{
		harness.sessionID: {OwnerID: "owner-a", SessionID: harness.sessionID, Name: "Barrel"},
	}})
	saveMCPCap(t, harness, 1)
	insertCountedStepTask(t, harness, harness.sessionID, "task-mcp-1")
	path := chatSessionStepsPath(harness.sessionID)

	read := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet, path, "", "")
	if read.Code != http.StatusOK {
		t.Fatalf("read steps = %d %s", read.Code, read.Body.String())
	}
	if body := decodeSessionSteps(t, read); body.Mode != "cap" || body.Used != 1 ||
		body.Cap != 1 || body.Extra != 0 {
		t.Fatalf("standing = %+v; want cap 1/1 with no grant", body)
	}
	if cache := read.Header().Get("Cache-Control"); !strings.Contains(cache, "private") {
		t.Fatalf("Cache-Control = %q; the standing is private to the person", cache)
	}

	put := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"extra_steps":2}`, "")
	if put.Code != http.StatusOK {
		t.Fatalf("grant steps = %d %s", put.Code, put.Body.String())
	}
	if body := decodeSessionSteps(t, put); body.Mode != "cap" || body.Used != 1 ||
		body.Cap != 1 || body.Extra != 2 {
		t.Fatalf("grant answer = %+v; want used 1, cap 1, extra 2", body)
	}

	// The same whole grant again is not a second grant and writes no audit.
	again := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"extra_steps":2}`, "")
	if again.Code != http.StatusOK {
		t.Fatalf("repeat grant = %d %s", again.Code, again.Body.String())
	}
	// Lowering an asset's grant is a conflict, not a silent shrink.
	lower := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"extra_steps":1}`, "")
	if lower.Code != http.StatusConflict {
		t.Fatalf("lower grant = %d %s", lower.Code, lower.Body.String())
	}
	// One write may add at most extra_steps.maximum (50).
	single := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"extra_steps":60}`, "")
	if single.Code != http.StatusConflict || !strings.Contains(single.Body.String(), `"code":"step_extra_single_limit"`) {
		t.Fatalf("single-write bound = %d %s", single.Code, single.Body.String())
	}
	// One asset may keep at most extra_steps.total_maximum (200).
	aboveTotal := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"extra_steps":201}`, "")
	if aboveTotal.Code != http.StatusConflict || !strings.Contains(aboveTotal.Body.String(), `"code":"step_extra_limit"`) {
		t.Fatalf("total bound = %d %s", aboveTotal.Code, aboveTotal.Body.String())
	}

	// A credential header beside the browser cookie cannot raise the cap.
	bearer := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"extra_steps":5}`))
	bearer.Header.Set("Content-Type", "application/json")
	bearer.Header.Set("Origin", "http://example.com")
	bearer.Header.Set("Authorization", "Bearer an-external-mcp-token")
	bearer.AddCookie(harness.cookie)
	refused := httptest.NewRecorder()
	harness.handler.ServeHTTP(refused, bearer)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("credential header write = %d %s", refused.Code, refused.Body.String())
	}

	// A plain session and an unknown session have no asset to extend: the
	// write is 404, and the read is 204 with no body (#616), so the studio's
	// read on every ordinary chat is not a failed request in the console.
	plain := chatSessionStepsPath("session-not-an-asset")
	if response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, plain,
		`{"extra_steps":1}`, ""); response.Code != http.StatusNotFound {
		t.Fatalf("plain session grant = %d %s", response.Code, response.Body.String())
	}
	if response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet, plain, "", ""); response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("plain session read = %d %q; want 204 with no body", response.Code, response.Body.String())
	}

	events, err := harness.repository.ListAdministrationAuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entries := 0
	for _, event := range events {
		if event.Action != "add_step_extra" {
			continue
		}
		entries++
		if event.ActorID != "owner-a" || event.ResourceID != harness.sessionID ||
			event.ResourceType != "asset_list_item_steps" {
			t.Fatalf("audit event = %+v", event)
		}
		from := event.Detail["from"].(map[string]any)["extra_steps"]
		to := event.Detail["to"].(map[string]any)["extra_steps"]
		if from != float64(0) || to != float64(2) {
			t.Fatalf("audit detail = %+v; want 0 -> 2", event.Detail)
		}
	}
	if entries != 1 {
		t.Fatalf("audit entries = %d; want exactly the one real grant", entries)
	}
}

// A session that is not an asset of this owner is never 403: the write is 404
// and the read is the same 204 as a plain session (#616), and the asset list
// item sessions keep their own route.
func TestChatSessionStepsRejectsAnotherOwnersAsset(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	harness.chat.SetMCPStepSessions(mcpStepAssets{mark: map[string]chat.MCPAsset{
		harness.sessionID: {OwnerID: "owner-b", SessionID: harness.sessionID, Name: "Barrel"},
	}})
	saveMCPCap(t, harness, 1)
	path := chatSessionStepsPath(harness.sessionID)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		body := ""
		if method == http.MethodPut {
			body = `{"extra_steps":1}`
		}
		want := http.StatusNotFound
		if method == http.MethodGet {
			want = http.StatusNoContent
		}
		response := grantTestRequest(t, harness.handler, harness.cookie, method, path, body, "")
		if response.Code != want {
			t.Fatalf("%s another owner's asset = %d %s", method, response.Code, response.Body.String())
		}
	}
}

// #330 §6: the same session-keyed route also serves a list item's sub-session.
// The raised grant releases the item's step-cap block and resumes the run, so
// a person who extends from the conversation does not leave the list paused.
func TestChatSessionExtraStepsReleaseAListItemAndItsRun(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Session steps", defaultListItem())
	saveListStepCap(t, harness, 1)
	bindAssetListItemSession(t, harness, list, harness.sessionID)
	harness.createRun(t, list.ID, assetlist.RunRunning)
	ctx := context.Background()
	now := time.Now().UTC()
	items := harness.repository.AssetListRunRepository()
	itemID := list.Items[0].ID
	if _, err := items.SetItemBlock(ctx, "owner-a", list.ID, itemID,
		assetlist.BlockStepCap, assetlist.VersionUnchecked, now); err != nil {
		t.Fatal(err)
	}
	if _, err := items.UpdateRunStatus(ctx, "owner-a", list.ID,
		assetlist.RunRunning, assetlist.RunPaused, assetlist.PauseStepCap, now); err != nil {
		t.Fatal(err)
	}

	path := chatSessionStepsPath(harness.sessionID)
	read := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet, path, "", "")
	if read.Code != http.StatusOK {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	if body := decodeSessionSteps(t, read); body.Mode != "cap" || body.Used != 1 || body.Cap != 1 {
		t.Fatalf("standing = %+v; want the list item's cap 1 used 1", body)
	}
	put := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"extra_steps":1}`, "")
	if put.Code != http.StatusOK {
		t.Fatalf("grant = %d %s", put.Code, put.Body.String())
	}

	stored, err := items.ItemByID(ctx, "owner-a", list.ID, itemID)
	if err != nil || stored.Block != assetlist.BlockNone {
		t.Fatalf("item after the grant = %+v, %v; want the block cleared", stored, err)
	}
	run, err := items.GetRunByListID(ctx, "owner-a", list.ID)
	if err != nil || run.Status != assetlist.RunRunning || run.PauseReason != "" {
		t.Fatalf("run after the grant = %+v, %v; want running", run, err)
	}
}
