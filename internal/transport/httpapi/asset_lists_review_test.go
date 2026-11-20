package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

const assetListReviewRoute = "/api/v1/projects/{projectID}/asset-lists/{listID}/review"

type assetListReviewDecisionTestWire struct {
	List  assetListTestWire `json:"list"`
	Items []struct {
		ItemID         string `json:"item_id"`
		Verdict        string `json:"verdict"`
		ProjectAssetID string `json:"project_asset_id"`
		Error          string `json:"error"`
	} `json:"items"`
}

func decodeAssetListReviewDecision(t *testing.T, response *httptest.ResponseRecorder) assetListReviewDecisionTestWire {
	t.Helper()
	var wire assetListReviewDecisionTestWire
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatalf("review decision %q: %v", response.Body.String(), err)
	}
	return wire
}

func TestProjectAssetListItemReviewRequiresARealBrowserSession(t *testing.T) {
	t.Parallel()
	handler, _, _, _ := bearerKeyHarness(t)
	path := assetListRoute(assetListReviewRoute, "prj_any", "alist_any")

	body := `{"decision":"accept","item_ids":["aitem_1"],"expected_version":0}`
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodPut, path, strings.NewReader(body)))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous review = %d %s", anonymous.Code, anonymous.Body.String())
	}

	bearer := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	bearer.Header.Set("Authorization", "Bearer "+deployToken)
	bearerResponse := httptest.NewRecorder()
	handler.ServeHTTP(bearerResponse, bearer)
	if bearerResponse.Code != http.StatusForbidden {
		t.Fatalf("bearer review = %d %s", bearerResponse.Code, bearerResponse.Body.String())
	}
}

func TestProjectAssetListItemReviewAcceptsAndReturns(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Board",
		[]assetlist.ItemInput{
			{Kind: "character", Name: "Hero"},
			{Kind: "image", Name: "Icon", RouteID: "text-to-image"},
		})
	decisionPath := assetListRoute(assetListDecisionRoute, harness.projectID, list.ID)
	confirm := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, decisionPath,
		`{"decision":"confirm"}`, "")
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", confirm.Code, confirm.Body.String())
	}
	confirmed := decodeAssetListTestWire(t, confirm)
	// The board carries the list version a decision echoes as expected_version.
	boardResponse := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListBoardRoute, harness.projectID, list.ID), "", "")
	if boardResponse.Code != http.StatusOK {
		t.Fatalf("board = %d %s", boardResponse.Code, boardResponse.Body.String())
	}
	if board := decodeAssetListBoard(t, boardResponse); board.List.Version != confirmed.Version {
		t.Fatalf("board version = %d; want %d", board.List.Version, confirmed.Version)
	}

	db, err := sql.Open("sqlite", "file:"+harness.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, item := range list.Items {
		if _, err := db.Exec(`
			UPDATE asset_list_items SET state = ?, round = 1,
				review_json = '{"task_id":"","artifacts":[]}',
				result_json = '{"task_id":"task_1","assets":[]}'
			WHERE id = ?`, assetlist.StateReview, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	// A completed run is reopened by a return so the runner continues the
	// item's sub-session.
	if _, err := db.Exec(`
		INSERT INTO asset_list_runs (id, owner_id, project_id, list_id, status,
			pause_reason, created_at, updated_at)
		VALUES ('run_review', 'owner-a', ?, ?, 'completed', '',
			'2026-11-18T12:00:00.000000000Z', '2026-11-18T12:00:00.000000000Z')`,
		harness.projectID, list.ID); err != nil {
		t.Fatal(err)
	}

	reviewPath := assetListRoute(assetListReviewRoute, harness.projectID, list.ID)
	accepted := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, reviewPath,
		`{"decision":"accept","item_ids":["`+list.Items[0].ID+`"],"expected_version":`+itoa(confirmed.Version)+`}`, "")
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept = %d %s", accepted.Code, accepted.Body.String())
	}
	acceptedWire := decodeAssetListReviewDecision(t, accepted)
	if len(acceptedWire.Items) != 1 || acceptedWire.Items[0].Verdict != assetlist.ReviewVerdictApproved ||
		acceptedWire.Items[0].ProjectAssetID == "" || acceptedWire.Items[0].Error != "" {
		t.Fatalf("accept decision = %+v", acceptedWire)
	}
	if acceptedWire.List.Version != confirmed.Version+1 {
		t.Fatalf("list version = %d; want %d", acceptedWire.List.Version, confirmed.Version+1)
	}
	assets := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		"/api/v1/projects/"+harness.projectID+"/assets", "", "")
	var listed struct {
		Assets []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"assets"`
	}
	if assets.Code != http.StatusOK || json.Unmarshal(assets.Body.Bytes(), &listed) != nil ||
		len(listed.Assets) != 1 || listed.Assets[0].ID != acceptedWire.Items[0].ProjectAssetID ||
		listed.Assets[0].Kind != "character" || listed.Assets[0].Name != "Hero" {
		t.Fatalf("project assets = %d %s", assets.Code, assets.Body.String())
	}

	// The second item is returned with a declared reason and a note.
	returned := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, reviewPath,
		`{"decision":"return","item_ids":["`+list.Items[1].ID+`"],"reason_ids":["other"],`+
			`"note":"请重画","expected_version":`+itoa(acceptedWire.List.Version)+`}`, "")
	if returned.Code != http.StatusOK {
		t.Fatalf("return = %d %s", returned.Code, returned.Body.String())
	}
	returnedWire := decodeAssetListReviewDecision(t, returned)
	if len(returnedWire.Items) != 1 || returnedWire.Items[0].Verdict != assetlist.ReviewVerdictReturned {
		t.Fatalf("return decision = %+v", returnedWire)
	}
	var state, returnReason, returnNote, reasons, note, reviewVerdict, runStatus string
	var round, returnCount, workRound int
	if err := db.QueryRow(`SELECT state, round, return_count, work_round,
		return_reason, return_note
		FROM asset_list_items WHERE id = ?`, list.Items[1].ID).
		Scan(&state, &round, &returnCount, &workRound, &returnReason, &returnNote); err != nil {
		t.Fatal(err)
	}
	if state != assetlist.StateReturned || round != 2 || returnCount != 1 || workRound != 0 ||
		returnReason != "其他" || returnNote != "请重画" {
		t.Fatalf("returned item = %s round %d returns %d work round %d %q %q",
			state, round, returnCount, workRound, returnReason, returnNote)
	}
	if err := db.QueryRow(`SELECT reasons_json, note, verdict, round
		FROM asset_list_item_reviews WHERE item_id = ?`, list.Items[1].ID).
		Scan(&reasons, &note, &reviewVerdict, &round); err != nil {
		t.Fatal(err)
	}
	if reasons != `["other"]` || note != "请重画" ||
		reviewVerdict != assetlist.ReviewVerdictReturned || round != 1 {
		t.Fatalf("review row = %s %q %s round %d", reasons, note, reviewVerdict, round)
	}
	if err := db.QueryRow(`SELECT status FROM asset_list_runs WHERE list_id = ?`, list.ID).
		Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != assetlist.RunRunning {
		t.Fatalf("run = %s; want running after the return", runStatus)
	}

	// A stale board version is a conflict and changes nothing.
	stale := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, reviewPath,
		`{"decision":"accept","item_ids":["`+list.Items[0].ID+`"],"expected_version":`+itoa(confirmed.Version)+`}`, "")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale accept = %d %s", stale.Code, stale.Body.String())
	}
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }
