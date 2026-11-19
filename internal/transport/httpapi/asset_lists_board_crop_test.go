package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// assetListBoardCropTestWire decodes the #331 crop contract of the acceptance
// board beside the item name and result.
type assetListBoardCropTestWire struct {
	Items []struct {
		ID                    string          `json:"id"`
		Result                json.RawMessage `json:"result"`
		ReferenceAssetID      string          `json:"reference_asset_id"`
		RepresentativeAssetID string          `json:"representative_asset_id"`
		Source                *struct {
			SourceInputAssetID string `json:"source_input_asset_id"`
			SourceKind         string `json:"source_kind"`
			SourceWidth        int    `json:"source_width"`
			SourceHeight       int    `json:"source_height"`
		} `json:"source"`
		Region *struct {
			X         float64 `json:"x"`
			Y         float64 `json:"y"`
			Width     float64 `json:"width"`
			Height    float64 `json:"height"`
			PixelRect struct {
				X0 int `json:"x0"`
				Y0 int `json:"y0"`
				X1 int `json:"x1"`
				Y1 int `json:"y1"`
			} `json:"pixel_rect"`
		} `json:"region"`
	} `json:"items"`
}

// #331: the acceptance board carries each crop item's reference asset id, the
// source provenance and the cut geometry, so one read explains every item
// beside its source picture. The representative asset follows the result's
// role priority (model over image over the first asset).
func TestProjectAssetListBoardExposesCropProvenance(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Board", defaultListItem())
	seedAssetListCrop(t, harness.repository, "crop_1", "scene_a")
	seedAssetListInputAsset(t, harness.repository, "owner-a", "in_1", "image/png", "image")
	edit := `{"title":"Cropped","expected_version":0,"items":[` +
		`{"kind":"character","name":"Knight","references":[{"input_asset_id":"crop_1"}]},` +
		`{"kind":"prop","name":"Lantern","references":[{"input_asset_id":"in_1"}]}]}`
	edited := harness.editDraft(t, list.ID, edit)
	if edited.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", edited.Code, edited.Body.String())
	}
	var editedWire assetListTestWire
	if err := json.Unmarshal(edited.Body.Bytes(), &editedWire); err != nil || len(editedWire.Items) != 2 {
		t.Fatalf("edited list = %q: %v", edited.Body.String(), err)
	}
	db, err := sql.Open("sqlite", "file:"+harness.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result := `{"task_id":"task_1","assets":[` +
		`{"id":"asset_image","name":"image","role":"image"},` +
		`{"id":"asset_model","name":"model/glb","role":"model"},` +
		`{"id":"asset_concept","name":"concept","role":"concept"}]}`
	if _, err := db.Exec(`UPDATE asset_list_items SET result_json = ? WHERE id = ?`,
		result, editedWire.Items[0].ID); err != nil {
		t.Fatal(err)
	}

	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListBoardRoute, harness.projectID, list.ID), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("board = %d %s", response.Code, response.Body.String())
	}
	var board assetListBoardCropTestWire
	if err := json.Unmarshal(response.Body.Bytes(), &board); err != nil {
		t.Fatalf("board %q: %v", response.Body.String(), err)
	}
	if len(board.Items) != 2 {
		t.Fatalf("board items = %+v", board.Items)
	}
	cropped := board.Items[0]
	if cropped.ReferenceAssetID != "crop_1" || cropped.Source == nil || cropped.Region == nil {
		t.Fatalf("cropped item = %+v", cropped)
	}
	if cropped.Source.SourceInputAssetID != "scene_a" ||
		cropped.Source.SourceKind != listcrop.SourceKindInput ||
		cropped.Source.SourceWidth != 1000 || cropped.Source.SourceHeight != 1000 {
		t.Fatalf("crop source = %+v", cropped.Source)
	}
	if cropped.Region.X != 0 || cropped.Region.Y != 0 ||
		cropped.Region.Width != 0.5 || cropped.Region.Height != 0.5 ||
		cropped.Region.PixelRect.X0 != 0 || cropped.Region.PixelRect.Y0 != 0 ||
		cropped.Region.PixelRect.X1 != 500 || cropped.Region.PixelRect.Y1 != 500 {
		t.Fatalf("crop region = %+v", cropped.Region)
	}
	if cropped.RepresentativeAssetID != "asset_model" {
		t.Fatalf("representative = %q", cropped.RepresentativeAssetID)
	}
	plain := board.Items[1]
	if plain.ReferenceAssetID != "" || plain.Source != nil || plain.Region != nil ||
		plain.RepresentativeAssetID != "" || string(plain.Result) != `{}` {
		t.Fatalf("plain item = %+v", plain)
	}
}

// #331: a crop reference backed by another owner's provenance stays invisible;
// the board must not reveal the foreign source snapshot.
func TestProjectAssetListBoardLeavesForeignCropInvisible(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Board", defaultListItem())
	seedAssetListInputAsset(t, harness.repository, "owner-b", "crop_foreign", "image/png", "list_crop")
	if _, _, err := harness.repository.InputAssetCropRepository().InsertOrGet(
		context.Background(), listcrop.Crop{
			AssetID:            generation.AssetID("crop_foreign"),
			OwnerID:            generation.OwnerID("owner-b"),
			SourceAssetID:      generation.AssetID("scene_b"),
			SourceKind:         listcrop.SourceKindInput,
			SourceInputAssetID: generation.AssetID("scene_b"),
			SourceWidth:        1000,
			SourceHeight:       1000,
			Region:             listcrop.Region{X: 0, Y: 0, Width: 0.5, Height: 0.5},
			PixelRect:          listcrop.PixelRect{X0: 0, Y0: 0, X1: 500, Y1: 500},
			SHA256:             strings.Repeat("c", 64),
			CreatedAt:          time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
		}); err != nil {
		t.Fatalf("seed foreign crop: %v", err)
	}
	// The seed service has no reference port, so it stores a reference the
	// public edit path would refuse; the read path must still refuse it.
	if _, err := harness.seed.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: harness.projectID, ListID: list.ID,
		ExpectedVersion: 0, Title: "Stored",
		Items: []assetlist.ItemInput{{Kind: "character", Name: "Knight",
			References: []project.Reference{{InputAssetID: "crop_foreign"}}}},
		SessionID: harness.sessionID,
	}); err != nil {
		t.Fatalf("seed stored reference: %v", err)
	}
	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListBoardRoute, harness.projectID, list.ID), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("board = %d %s", response.Code, response.Body.String())
	}
	var board assetListBoardCropTestWire
	if err := json.Unmarshal(response.Body.Bytes(), &board); err != nil {
		t.Fatalf("board %q: %v", response.Body.String(), err)
	}
	if len(board.Items) != 1 {
		t.Fatalf("board items = %+v", board.Items)
	}
	item := board.Items[0]
	if item.ReferenceAssetID != "" || item.Source != nil || item.Region != nil {
		t.Fatalf("foreign crop leaked = %+v", item)
	}
	if strings.Contains(response.Body.String(), "scene_b") {
		t.Fatalf("foreign crop source leaked: %s", response.Body.String())
	}
}
