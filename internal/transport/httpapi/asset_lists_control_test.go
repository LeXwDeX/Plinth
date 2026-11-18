package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

const (
	assetListRunRoute         = "/api/v1/projects/{projectID}/asset-lists/{listID}/run"
	assetListControlItemRoute = "/api/v1/projects/{projectID}/asset-lists/{listID}/items/{itemID}"
)

type assetListTestRunWire struct {
	Status      string `json:"status"`
	PauseReason string `json:"pause_reason"`
	Version     string `json:"version"`
	ItemCounts  struct {
		Todo     int `json:"todo"`
		Working  int `json:"working"`
		Review   int `json:"review"`
		Accepted int `json:"accepted"`
		Returned int `json:"returned"`
		Failed   int `json:"failed"`
		Skipped  int `json:"skipped"`
	} `json:"item_counts"`
}

func assetListItemControlRoute(projectID, listID, itemID string) string {
	path := assetListRoute(assetListControlItemRoute, projectID, listID)
	return strings.ReplaceAll(path, "{itemID}", itemID)
}

// confirmAssetList confirms a proposed draft through the browser route.
func (harness *assetListHTTPHarness) confirmAssetList(t *testing.T, listID string) {
	t.Helper()
	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
		assetListRoute(assetListDecisionRoute, harness.projectID, listID), `{"decision":"confirm"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", response.Code, response.Body.String())
	}
}

// createRun stores the list's run directly; the default Catalog disables
// automatic execution, so confirming does not create it.
func (harness *assetListHTTPHarness) createRun(t *testing.T, listID, status string) assetlist.Run {
	t.Helper()
	now := time.Now().UTC()
	run, err := harness.repository.AssetListRunRepository().CreateRun(context.Background(), assetlist.Run{
		ID: "run_" + listID, OwnerID: "owner-a", ProjectID: harness.projectID,
		ListID: listID, Status: status, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return run
}

func (harness *assetListHTTPHarness) readAssetList(t *testing.T, listID string) assetListTestWire {
	t.Helper()
	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListItemRoute, harness.projectID, listID), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read list = %d %s", response.Code, response.Body.String())
	}
	return decodeAssetListTestWire(t, response)
}

func decodeAssetListRun(t *testing.T, response *httptest.ResponseRecorder) assetListTestRunWire {
	t.Helper()
	var wire assetListTestRunWire
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatalf("run body %q: %v", response.Body.String(), err)
	}
	return wire
}

func (harness *assetListHTTPHarness) controlRun(t *testing.T, listID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return grantTestRequest(t, harness.handler, harness.cookie, http.MethodPost,
		assetListRoute(assetListRunRoute, harness.projectID, listID), body, "")
}

func TestProjectAssetListRunControlFlow(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Run", defaultListItem())

	// A draft has no run: the summary field is null.
	if wire := harness.readAssetList(t, list.ID); wire.Run != nil {
		t.Fatalf("draft run = %+v; want null", wire.Run)
	}
	harness.confirmAssetList(t, list.ID)
	harness.createRun(t, list.ID, assetlist.RunIdle)

	detail := harness.readAssetList(t, list.ID)
	if detail.Run == nil || detail.Run.Status != assetlist.RunIdle || detail.Run.Version == "" ||
		detail.Run.ItemCounts.Todo != 1 {
		t.Fatalf("idle run = %+v", detail.Run)
	}

	started := harness.controlRun(t, list.ID,
		`{"action":"start","expected_version":"`+detail.Run.Version+`"}`)
	if started.Code != http.StatusOK {
		t.Fatalf("start = %d %s", started.Code, started.Body.String())
	}
	run := decodeAssetListRun(t, started)
	if run.Status != assetlist.RunRunning || run.Version == "" {
		t.Fatalf("started run = %+v", run)
	}
	// The idle token is stale now that the run moved to running.
	stale := harness.controlRun(t, list.ID,
		`{"action":"pause","expected_version":"`+detail.Run.Version+`"}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale token = %d %s", stale.Code, stale.Body.String())
	}

	paused := harness.controlRun(t, list.ID,
		`{"action":"pause","expected_version":"`+run.Version+`","reason":"taking a break"}`)
	if paused.Code != http.StatusOK {
		t.Fatalf("pause = %d %s", paused.Code, paused.Body.String())
	}
	run = decodeAssetListRun(t, paused)
	if run.Status != assetlist.RunPaused || run.PauseReason != assetlist.PauseUser {
		t.Fatalf("paused run = %+v", run)
	}
	// A double click repeats the target status without writing.
	repeated := harness.controlRun(t, list.ID,
		`{"action":"pause","expected_version":"`+run.Version+`"}`)
	if repeated.Code != http.StatusOK || decodeAssetListRun(t, repeated).Status != assetlist.RunPaused {
		t.Fatalf("repeated pause = %d %s", repeated.Code, repeated.Body.String())
	}

	resumed := harness.controlRun(t, list.ID,
		`{"action":"resume","expected_version":"`+run.Version+`"}`)
	if resumed.Code != http.StatusOK || decodeAssetListRun(t, resumed).Status != assetlist.RunRunning {
		t.Fatalf("resume = %d %s", resumed.Code, resumed.Body.String())
	}
	run = decodeAssetListRun(t, resumed)

	stopped := harness.controlRun(t, list.ID,
		`{"action":"stop","expected_version":"`+run.Version+`"}`)
	if stopped.Code != http.StatusOK || decodeAssetListRun(t, stopped).Status != assetlist.RunStopped {
		t.Fatalf("stop = %d %s", stopped.Code, stopped.Body.String())
	}
	run = decodeAssetListRun(t, stopped)

	// A stopped run refuses resume; an unknown action and a missing token are
	// invalid; a missing list is 404.
	conflict := harness.controlRun(t, list.ID,
		`{"action":"resume","expected_version":"`+run.Version+`"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("resume stopped = %d %s", conflict.Code, conflict.Body.String())
	}
	invalid := harness.controlRun(t, list.ID,
		`{"action":"bogus","expected_version":"`+run.Version+`"}`)
	if invalid.Code != http.StatusBadRequest || decodeAssetListTestProblem(t, invalid).Code != "invalid_input" {
		t.Fatalf("unknown action = %d %s", invalid.Code, invalid.Body.String())
	}
	missingToken := harness.controlRun(t, list.ID, `{"action":"start"}`)
	if missingToken.Code != http.StatusBadRequest {
		t.Fatalf("missing token = %d %s", missingToken.Code, missingToken.Body.String())
	}
	missing := harness.controlRun(t, "alist_missing",
		`{"action":"start","expected_version":"`+run.Version+`"}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing list = %d %s", missing.Code, missing.Body.String())
	}
}

func TestProjectAssetListItemSkipFlowAndRunCounts(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Skip",
		[]assetlist.ItemInput{
			{Kind: "character", Name: "Hero"},
			{Kind: "character", Name: "Rival"},
			{Kind: "character", Name: "Extra"},
		})
	harness.confirmAssetList(t, list.ID)
	harness.createRun(t, list.ID, assetlist.RunRunning)

	detail := harness.readAssetList(t, list.ID)
	if detail.Run == nil || detail.Run.ItemCounts.Todo != 3 {
		t.Fatalf("initial run = %+v", detail.Run)
	}
	skip := func(itemID string, version int64, bodyAction string) *httptest.ResponseRecorder {
		t.Helper()
		return grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
			assetListItemControlRoute(harness.projectID, list.ID, itemID),
			`{"action":"`+bodyAction+`","expected_version":`+strconv.FormatInt(version, 10)+`,"reason":"not needed"}`, "")
	}
	readItem := func(response *httptest.ResponseRecorder) (state, reason string, version int64) {
		t.Helper()
		var item struct {
			State       string `json:"state"`
			StateReason string `json:"state_reason"`
			Version     int64  `json:"version"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
			t.Fatalf("item body %q: %v", response.Body.String(), err)
		}
		return item.State, item.StateReason, item.Version
	}

	skipped := skip(detail.Items[0].ID, detail.Items[0].Version, "skip")
	if skipped.Code != http.StatusOK {
		t.Fatalf("skip todo = %d %s", skipped.Code, skipped.Body.String())
	}
	if state, reason, version := readItem(skipped); state != assetlist.StateSkipped ||
		reason != "not needed" || version != 1 {
		t.Fatalf("skipped item = %s/%s/%d", state, reason, version)
	}
	// A repeated skip answers the stored row even with the old version.
	repeated := skip(detail.Items[0].ID, detail.Items[0].Version, "skip")
	if repeated.Code != http.StatusOK {
		t.Fatalf("repeated skip = %d %s", repeated.Code, repeated.Body.String())
	}
	if state, reason, _ := readItem(repeated); state != assetlist.StateSkipped || reason != "not needed" {
		t.Fatalf("repeated item = %s/%s", state, reason)
	}

	// A working item skips at its current version.
	claimed, found, err := harness.repository.AssetListRunRepository().ClaimItem(
		context.Background(), "owner-a", list.ID, time.Now().UTC())
	if err != nil || !found || claimed.ID != detail.Items[1].ID {
		t.Fatalf("claim = %+v, %v, %v", claimed, found, err)
	}
	stale := skip(claimed.ID, claimed.Version-1, "skip")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale item version = %d %s", stale.Code, stale.Body.String())
	}
	working := skip(claimed.ID, claimed.Version, "skip")
	if working.Code != http.StatusOK {
		t.Fatalf("skip working = %d %s", working.Code, working.Body.String())
	}

	// Review is no longer skippable.
	reviewItem, found, err := harness.repository.AssetListRunRepository().ClaimItem(
		context.Background(), "owner-a", list.ID, time.Now().UTC())
	if err != nil || !found || reviewItem.ID != detail.Items[2].ID {
		t.Fatalf("claim review = %+v, %v, %v", reviewItem, found, err)
	}
	reviewed, err := harness.repository.AssetListRunRepository().FinishItem(
		context.Background(), assetlist.FinishItemInput{
			OwnerID: "owner-a", ListID: list.ID, ItemID: reviewItem.ID,
			State: assetlist.StateReview, At: time.Now().UTC(),
		})
	if err != nil || reviewed.State != assetlist.StateReview {
		t.Fatalf("finish review = %+v, %v", reviewed, err)
	}
	refused := skip(reviewed.ID, reviewed.Version, "skip")
	if refused.Code != http.StatusConflict ||
		decodeAssetListTestProblem(t, refused).Code != "asset_list_item_state" {
		t.Fatalf("skip review = %d %s", refused.Code, refused.Body.String())
	}

	// The project summary carries the run with the fresh counts.
	summaries := decodeAssetListSummaries(t, grantTestRequest(t, harness.handler, harness.cookie,
		http.MethodGet, assetListRoute(assetListProjectRoute, harness.projectID, ""), "", ""))
	if len(summaries) != 1 || summaries[0].Run == nil ||
		summaries[0].Run.Status != assetlist.RunRunning ||
		summaries[0].Run.ItemCounts.Todo != 0 || summaries[0].Run.ItemCounts.Skipped != 2 ||
		summaries[0].Run.ItemCounts.Review != 1 {
		t.Fatalf("summary run = %+v", summaries[0].Run)
	}

	badAction := skip(detail.Items[0].ID, 0, "start")
	if badAction.Code != http.StatusBadRequest {
		t.Fatalf("bad item action = %d %s", badAction.Code, badAction.Body.String())
	}
	missingToken := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
		assetListItemControlRoute(harness.projectID, list.ID, detail.Items[0].ID), `{"action":"skip"}`, "")
	if missingToken.Code != http.StatusBadRequest {
		t.Fatalf("missing item token = %d %s", missingToken.Code, missingToken.Body.String())
	}
	negative := skip(detail.Items[0].ID, -1, "skip")
	if negative.Code != http.StatusBadRequest {
		t.Fatalf("negative item version = %d %s", negative.Code, negative.Body.String())
	}
	missing := skip("aitem_missing", 0, "skip")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing item = %d %s", missing.Code, missing.Body.String())
	}
}

func TestAssetListControlRequiresARealBrowserSession(t *testing.T) {
	t.Parallel()
	handler, _, _, _ := bearerKeyHarness(t)
	for _, probe := range []struct {
		method, path, body string
	}{
		{http.MethodPost, assetListRoute(assetListRunRoute, "prj_any", "alist_any"),
			`{"action":"start","expected_version":"2026-11-18T12:00:00Z"}`},
		{http.MethodPut, assetListItemControlRoute("prj_any", "alist_any", "aitem_any"),
			`{"action":"skip","expected_version":0}`},
	} {
		anonymous := httptest.NewRecorder()
		anonymousRequest := httptest.NewRequest(probe.method, probe.path, strings.NewReader(probe.body))
		anonymousRequest.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(anonymous, anonymousRequest)
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s = %d %s", probe.path, anonymous.Code, anonymous.Body.String())
		}

		bearer := httptest.NewRequest(probe.method, probe.path, strings.NewReader(probe.body))
		bearer.Header.Set("Authorization", "Bearer "+deployToken)
		bearer.Header.Set("Content-Type", "application/json")
		bearerResponse := httptest.NewRecorder()
		handler.ServeHTTP(bearerResponse, bearer)
		if bearerResponse.Code != http.StatusForbidden {
			t.Fatalf("bearer %s = %d %s", probe.path, bearerResponse.Code, bearerResponse.Body.String())
		}
	}
}
