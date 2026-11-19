package assetlist_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// reviewTestSchema is testSchema with the #329b acceptance section.
func reviewTestSchema() catalog.AssetListSchema {
	schema := testSchema()
	schema.Review = &catalog.AssetListReviewSchema{
		ReturnReasons: []catalog.AssetListReturnReasonSchema{
			{ID: "style", Label: "风格不对"},
			{ID: "topology", Label: "布线不合要求", Kinds: []string{"character"}},
			{ID: "other", Label: "其他"},
		},
		MaxReworkRounds: 3,
		Messages: catalog.AssetListReviewMessagesSchema{
			ReworkMessage: "打回重做：{name}/{description}/{round}/{reasons}/{note}",
			ReturnLimit:   "第 {round} 轮后不能再打回",
			AssetLimit:    "项目资产已达上限 {maximum} 个，{name} 未登记",
		},
	}
	return schema
}

type fakeProjectAssets struct {
	assets map[string]project.Asset
	calls  int
	err    error
}

func newFakeProjectAssets() *fakeProjectAssets {
	return &fakeProjectAssets{assets: map[string]project.Asset{}}
}

func (fake *fakeProjectAssets) CreateAsset(
	_ context.Context, ownerID, projectID string, input project.AssetInput,
) (project.Asset, error) {
	fake.calls++
	if fake.err != nil {
		return project.Asset{}, fake.err
	}
	if existing, found := fake.assets[input.ID]; found {
		return existing, nil
	}
	asset := project.Asset{
		ID: input.ID, ProjectID: projectID, OwnerID: ownerID, Kind: input.Kind,
		Name: input.Name, Description: input.Description,
		Images: input.Images, Models: input.Models,
	}
	fake.assets[input.ID] = asset
	return asset, nil
}

// reviewFixture confirms one list and moves its first item to review with a
// snapshot of one image and one model.
func reviewFixture(t *testing.T, fake *fakeProjectAssets) (*assetlist.Service, *memoryRepository, assetlist.List) {
	t.Helper()
	service, repository, clock := newService(t, func(config *assetlist.Config) {
		config.Schema = reviewTestSchema()
		config.ProjectAssets = fake
		config.ProjectAssetLimits = assetlist.ProjectAssetLimits{
			MaximumAssets: 200, MaximumImages: 8, MaximumModels: 4,
		}
	})
	ctx := context.Background()
	draft, err := service.Propose(ctx, proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	list, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: draft.ID,
		Decision: assetlist.DecisionConfirm,
	})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	items := repository.items[list.ID]
	snapshot, err := json.Marshal(assetlist.ReviewSnapshot{
		TaskID: "task_1",
		Artifacts: []assetlist.ReviewArtifact{
			{AssetID: "asset_img", Role: "preview", MediaType: "image/png", Size: 10, SHA256: "aa"},
			{AssetID: "asset_model", Role: "model", MediaType: "model/gltf-binary", Size: 20, SHA256: "bb"},
		},
		Model3D: &assetlist.ReviewModel3D{
			AssetID: "asset_model",
			Checks:  []assetlist.ReviewCheck{{Check: "triangles", Status: "pass", Actual: float64(9000)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	items[0].State = assetlist.StateReview
	items[0].Round = 1
	items[0].Review = snapshot
	items[0].Version++
	repository.items[list.ID] = items
	list = repository.withItems(repository.lists[list.ID])
	_ = clock
	return service, repository, list
}

func reviewItemID(list assetlist.List) string { return list.Items[0].ID }

func deterministicAssetID(itemID string, round int) string {
	sum := sha256.Sum256([]byte(itemID + "\x00" + strconv.Itoa(round)))
	return "passet_" + hex.EncodeToString(sum[:])
}

func TestReviewSnapshotDelegatesToTheInstalledPort(t *testing.T) {
	service, _, _ := newService(t, func(config *assetlist.Config) {
		config.Snapshots = reviewSnapshotFunc(func(_ context.Context, input assetlist.ReviewSnapshotInput) (json.RawMessage, error) {
			if input.OwnerID != "owner-a" || input.ItemID != "aitem_1" ||
				string(input.Brief) != `{"purpose":"render"}` {
				t.Fatalf("snapshot input = %+v", input)
			}
			return json.RawMessage(`{"captured_at":"2026-11-18T12:00:00Z"}`), nil
		})
	})
	snapshot, err := service.ReviewSnapshot(context.Background(), assetlist.ReviewSnapshotInput{
		OwnerID: "owner-a", SessionID: "ses_1", ProjectID: "prj_1",
		ListID: "alist_1", ItemID: "aitem_1", Brief: json.RawMessage(`{"purpose":"render"}`),
	})
	if err != nil || string(snapshot) != `{"captured_at":"2026-11-18T12:00:00Z"}` {
		t.Fatalf("snapshot = %s, %v", snapshot, err)
	}
}

type reviewSnapshotFunc func(context.Context, assetlist.ReviewSnapshotInput) (json.RawMessage, error)

func (function reviewSnapshotFunc) Snapshot(ctx context.Context, input assetlist.ReviewSnapshotInput) (json.RawMessage, error) {
	return function(ctx, input)
}

func TestReviewAcceptRegistersOneDeterministicProjectAsset(t *testing.T) {
	fake := newFakeProjectAssets()
	service, repository, list := reviewFixture(t, fake)
	ctx := context.Background()
	itemID := reviewItemID(list)

	outcome, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	wantID := deterministicAssetID(itemID, 1)
	if len(outcome.Results) != 1 || outcome.Results[0].Verdict != assetlist.ReviewVerdictApproved ||
		outcome.Results[0].ProjectAssetID != wantID {
		t.Fatalf("results = %+v; want approved %s", outcome.Results, wantID)
	}
	asset, found := fake.assets[wantID]
	if !found || fake.calls != 1 {
		t.Fatalf("project asset = %+v, calls = %d; want one registration", asset, fake.calls)
	}
	if asset.Kind != "character" || asset.Name != "Hero" ||
		len(asset.Images) != 1 || len(asset.Models) != 1 ||
		asset.Models[0] != (project.Reference{TaskID: "task_1", AssetID: "asset_model"}) ||
		asset.Images[0] != (project.Reference{TaskID: "task_1", AssetID: "asset_img"}) {
		t.Fatalf("registered asset = %+v", asset)
	}
	accepted := repository.items[list.ID][0]
	if accepted.State != assetlist.StateAccepted || accepted.ProjectAssetID != wantID ||
		accepted.AcceptedAt == nil || accepted.Version != 2 {
		t.Fatalf("accepted item = %+v", accepted)
	}
	if outcome.List.Version != list.Version+1 {
		t.Fatalf("list version = %d; want %d", outcome.List.Version, list.Version+1)
	}

	// A repeated click on the same board version is a conflict, and no second
	// asset row appears.
	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	}); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale accept error = %v; want conflict", err)
	}
	if fake.calls != 1 {
		t.Fatalf("asset registrations = %d; want 1", fake.calls)
	}
	// Re-accepting after a refetch finds the item no longer awaiting review.
	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version + 1,
	}); !errors.Is(err, assetlist.ErrItemState) {
		t.Fatalf("second accept error = %v; want item state", err)
	}
	if fake.calls != 1 {
		t.Fatalf("asset registrations after re-accept = %d; want 1", fake.calls)
	}
}

func TestReviewAcceptReportsTheAssetLimitPerItem(t *testing.T) {
	fake := newFakeProjectAssets()
	fake.err = project.ErrLimit
	service, repository, list := reviewFixture(t, fake)
	outcome, err := service.Review(context.Background(), assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{reviewItemID(list)},
		ExpectedVersion: list.Version,
	})
	if err != nil {
		t.Fatalf("accept at the limit: %v", err)
	}
	if len(outcome.Results) != 1 || outcome.Results[0].Error == "" ||
		!strings.Contains(outcome.Results[0].Error, "上限 200 个") ||
		!strings.Contains(outcome.Results[0].Error, "Hero") {
		t.Fatalf("results = %+v; want the Schema asset-limit copy", outcome.Results)
	}
	if item := repository.items[list.ID][0]; item.State != assetlist.StateReview {
		t.Fatalf("item = %s; want it left awaiting review", item.State)
	}
}

func TestReviewReturnBumpsRoundAndStoresReasons(t *testing.T) {
	fake := newFakeProjectAssets()
	service, repository, list := reviewFixture(t, fake)
	itemID := reviewItemID(list)

	outcome, err := service.Review(context.Background(), assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionReturn, ItemIDs: []string{itemID},
		ReasonIDs: []string{"style", "topology"}, Note: "把脸修好",
		ExpectedVersion: list.Version,
	})
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if len(outcome.Results) != 1 || outcome.Results[0].Verdict != assetlist.ReviewVerdictReturned {
		t.Fatalf("results = %+v", outcome.Results)
	}
	item := repository.items[list.ID][0]
	if item.State != assetlist.StateReturned || item.Round != 2 ||
		item.ReturnReason != "风格不对、布线不合要求" || item.ReturnNote != "把脸修好" {
		t.Fatalf("returned item = %+v", item)
	}
	if fake.calls != 0 {
		t.Fatalf("asset registrations = %d; want none", fake.calls)
	}
}

func TestReviewReturnValidatesReasonKindAndReworkLimit(t *testing.T) {
	fake := newFakeProjectAssets()
	service, repository, list := reviewFixture(t, fake)
	itemID := reviewItemID(list)
	ctx := context.Background()

	// rig is not applicable to this character? It is; topology is. Turn the
	// item into an image item to prove the kind filter runs.
	items := repository.items[list.ID]
	items[0].Kind = "image"
	repository.items[list.ID] = items
	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionReturn, ItemIDs: []string{itemID},
		ReasonIDs: []string{"topology"}, ExpectedVersion: list.Version,
	}); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("kind-mismatched reason error = %v; want invalid", err)
	}

	items[0].Kind = "character"
	items[0].Round = 4
	repository.items[list.ID] = items
	_, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionReturn, ItemIDs: []string{itemID},
		ReasonIDs: []string{"style"}, ExpectedVersion: list.Version,
	})
	var limit *assetlist.ReworkLimitError
	if !errors.As(err, &limit) || limit.Round != 4 || limit.Maximum != 3 {
		t.Fatalf("rework limit error = %v", err)
	}
}

func TestReviewRequiresTheBoardVersionAndReviewState(t *testing.T) {
	fake := newFakeProjectAssets()
	service, repository, list := reviewFixture(t, fake)
	ctx := context.Background()
	itemID := reviewItemID(list)

	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version + 1,
	}); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale version error = %v; want conflict", err)
	}

	items := repository.items[list.ID]
	items[0].State = assetlist.StateTodo
	repository.items[list.ID] = items
	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	}); !errors.Is(err, assetlist.ErrItemState) {
		t.Fatalf("todo item accept error = %v; want item state", err)
	}

	// Another owner cannot see or decide the list.
	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-b", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	}); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign accept error = %v; want not found", err)
	}
	if fake.calls != 0 {
		t.Fatalf("asset registrations = %d; want none", fake.calls)
	}
}
