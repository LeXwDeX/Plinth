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

// fakeProjectAssets is the validation-only port of the accept: it answers the
// prepared row (or an injected error) and counts the project's assets. The
// rows themselves are written by the list decision transaction, which the
// memory repository records.
type fakeProjectAssets struct {
	assets map[string]project.Asset
	count  int
	calls  int
	err    error
}

func newFakeProjectAssets() *fakeProjectAssets {
	return &fakeProjectAssets{assets: map[string]project.Asset{}}
}

func (fake *fakeProjectAssets) PrepareAsset(
	_ context.Context, ownerID, projectID string, input project.AssetInput,
) (project.Asset, error) {
	fake.calls++
	if fake.err != nil {
		return project.Asset{}, fake.err
	}
	asset := project.Asset{
		ID: input.ID, ProjectID: projectID, OwnerID: ownerID, Kind: input.Kind,
		Name: input.Name, Description: input.Description,
		Images: input.Images, Models: input.Models,
	}
	return asset, nil
}

func (fake *fakeProjectAssets) CountAssets(_ context.Context, _, _ string) (int, error) {
	if fake.err != nil {
		return 0, fake.err
	}
	return fake.count, nil
}

// reviewFixture confirms one list and moves its first item to review with a
// snapshot of one image and one model.
func reviewFixture(t *testing.T, fake *fakeProjectAssets) (*assetlist.Service, *memoryRepository, assetlist.List) {
	t.Helper()
	service, repository, clock := newService(t, func(config *assetlist.Config) {
		config.Schema = reviewTestSchema()
		if fake != nil {
			config.ProjectAssets = fake
			config.ProjectAssetLimits = assetlist.ProjectAssetLimits{
				MaximumAssets: 200, MaximumImages: 8, MaximumModels: 4,
			}
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
	asset, found := repository.assets[wantID]
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

	// A repeated click on the same board version is a conflict, and the whole
	// write (item and asset) is refused: no second asset row appears and the
	// accepted item keeps the row the winning write stored.
	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	}); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale accept error = %v; want conflict", err)
	}
	if len(repository.assets) != 1 {
		t.Fatalf("project assets = %d; want the one written with the accepted item", len(repository.assets))
	}
	// Re-accepting after a refetch finds the item no longer awaiting review.
	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version + 1,
	}); !errors.Is(err, assetlist.ErrItemState) {
		t.Fatalf("second accept error = %v; want item state", err)
	}
	if len(repository.assets) != 1 {
		t.Fatalf("project assets after re-accept = %d; want 1", len(repository.assets))
	}
}

// A refused decision transaction writes neither the item nor its project
// asset: the accept prepares the row first but the write is one transaction.
func TestReviewAcceptWritesNoAssetWhenTheDecisionTransactionFails(t *testing.T) {
	fake := newFakeProjectAssets()
	service, repository, list := reviewFixture(t, fake)
	repository.decisionFail = assetlist.ErrConflict
	ctx := context.Background()
	itemID := reviewItemID(list)

	_, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	})
	if !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("accept error = %v; want conflict", err)
	}
	if len(repository.assets) != 0 {
		t.Fatalf("project assets = %+v; want none behind a refused decision", repository.assets)
	}
	if item := repository.items[list.ID][0]; item.State != assetlist.StateReview ||
		item.ProjectAssetID != "" {
		t.Fatalf("item = %+v; want it still awaiting review", item)
	}
}

// The ready transaction then writes both, and a retry after a failed one uses
// the same deterministic identity.
func TestReviewAcceptIsRetryableWithTheSameProjectAssetID(t *testing.T) {
	fake := newFakeProjectAssets()
	service, repository, list := reviewFixture(t, fake)
	ctx := context.Background()
	itemID := reviewItemID(list)
	repository.decisionFail = assetlist.ErrConflict
	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	}); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("first accept error = %v; want conflict", err)
	}
	repository.decisionFail = nil
	outcome, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	})
	if err != nil {
		t.Fatalf("retried accept: %v", err)
	}
	wantID := deterministicAssetID(itemID, 1)
	if len(outcome.Results) != 1 || outcome.Results[0].ProjectAssetID != wantID {
		t.Fatalf("results = %+v; want %s", outcome.Results, wantID)
	}
	if _, found := repository.assets[wantID]; !found || len(repository.assets) != 1 {
		t.Fatalf("project assets = %+v; want the deterministic row", repository.assets)
	}
}

func TestReviewAcceptReportsTheAssetLimitPerItem(t *testing.T) {
	fake := newFakeProjectAssets()
	// The project already stores its 200 assets: the item is refused before
	// the write transaction, the other items of the list stay untouched.
	fake.count = 200
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

// reviewLocaleFixture is reviewFixture over a two-item list whose review copy
// is the product's own, with the product localizer installed: the per-item
// answers of a batch must read in the language of the request.
func reviewLocaleFixture(t *testing.T, fake *fakeProjectAssets) (*assetlist.Service, *memoryRepository, assetlist.List) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	schema := reviewTestSchema()
	schema.Review.Messages = application.AssetList.Review.Messages
	service, repository, _ := newService(t, func(config *assetlist.Config) {
		config.Schema = schema
		config.Texts = texts
		config.ProjectAssets = fake
		config.ProjectAssetLimits = assetlist.ProjectAssetLimits{
			MaximumAssets: 200, MaximumImages: 8, MaximumModels: 4,
		}
	})
	ctx := context.Background()
	draft, err := service.Propose(ctx, assetlist.ProposeInput{
		Owner: "owner-a", ProjectID: "prj_1", SessionID: "ses_1",
		RequestKey: "req_locale", Title: "Board",
		Items: []assetlist.ItemInput{
			{Kind: "character", Name: "Hero"},
			{Kind: "image", Name: "Icon", RouteID: "text-to-image"},
		},
	})
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
	for index := range items {
		items[index].State = assetlist.StateReview
		items[index].Round = 1
		items[index].Review = json.RawMessage(`{"task_id":"","artifacts":[]}`)
	}
	repository.items[list.ID] = items
	return service, repository, repository.withItems(repository.lists[list.ID])
}

// #425: the answers a batch gives for one item are Schema copy, so the browser
// reads them in its own language instead of the server's.
func TestReviewBatchAnswersInTheRequestLanguage(t *testing.T) {
	cases := []struct {
		name   string
		count  int
		second string
		want   map[string]string
	}{
		{
			name: "an id outside the list", second: "aitem_missing",
			want: map[string]string{
				"zh": "这个清单项不在当前清单里。",
				"en": "This item is not part of the current list.",
			},
		},
		{
			name: "an item that is not awaiting review", second: "state",
			want: map[string]string{
				"zh": "这个清单项已经不在待验收状态。",
				"en": "This item is no longer awaiting review.",
			},
		},
		{
			name: "a project at its asset bound", count: 200, second: "second",
			want: map[string]string{
				"zh": "项目资产已达上限 200 个，未能把清单项「Icon」登记为项目资产；请先清理项目资产再通过。",
				"en": "The project already has its maximum of 200 assets, so list item \"Icon\" could not be registered as a project asset; clean up project assets before accepting.",
			},
		},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			for _, locale := range []string{"zh", "en"} {
				locale := locale
				t.Run(locale, func(t *testing.T) {
					fake := newFakeProjectAssets()
					fake.count = test.count
					service, repository, list := reviewLocaleFixture(t, fake)
					items := repository.items[list.ID]
					if test.second == "state" {
						items[1].State = assetlist.StateTodo
						repository.items[list.ID] = items
						list = repository.withItems(repository.lists[list.ID])
					}
					second := items[1].ID
					switch test.second {
					case "state":
						items[1].State = assetlist.StateTodo
						repository.items[list.ID] = items
						list = repository.withItems(repository.lists[list.ID])
					case "second":
						// The second item of the list is the refused one.
					default:
						second = test.second
					}
					ctx := context.Background()
					if locale == "en" {
						ctx = catalog.WithLocale(ctx, "en")
					}
					outcome, err := service.Review(ctx, assetlist.ReviewInput{
						OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
						Decision:        assetlist.ReviewDecisionAccept,
						ItemIDs:         []string{items[0].ID, second},
						ExpectedVersion: list.Version,
					})
					if err != nil {
						t.Fatalf("accept: %v", err)
					}
					var answer string
					for _, result := range outcome.Results {
						if result.ItemID == second {
							answer = result.Error
						}
					}
					if answer != test.want[locale] {
						t.Fatalf("%s answer = %q; want %q", locale, answer, test.want[locale])
					}
				})
			}
		})
	}
}

// A bound that moved between the count and the write transaction rolls the
// batch back and answers the request-level limit; nothing is written.
func TestReviewAcceptReportsTheBoundMovedAtWriteTime(t *testing.T) {
	fake := newFakeProjectAssets()
	service, repository, list := reviewFixture(t, fake)
	itemID := reviewItemID(list)
	repository.decisionFail = &assetlist.ProjectAssetBoundError{ItemID: itemID}
	_, err := service.Review(context.Background(), assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	})
	var limit *assetlist.ReviewAssetLimitError
	if !errors.As(err, &limit) || limit.Maximum != 200 || limit.Name != "Hero" {
		t.Fatalf("accept error = %v; want the request-level asset limit", err)
	}
	if len(repository.assets) != 0 {
		t.Fatalf("project assets = %+v; want none", repository.assets)
	}
}

// A deployment without the project service can still settle an item through
// the board: it is accepted, with no project asset to register.
func TestReviewAcceptWithoutTheProjectPortSettlesTheItem(t *testing.T) {
	service, repository, list := reviewFixture(t, nil)
	itemID := reviewItemID(list)
	outcome, err := service.Review(context.Background(), assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionAccept, ItemIDs: []string{itemID},
		ExpectedVersion: list.Version,
	})
	if err != nil {
		t.Fatalf("accept without the project port: %v", err)
	}
	if len(outcome.Results) != 1 || outcome.Results[0].Verdict != assetlist.ReviewVerdictApproved ||
		outcome.Results[0].ProjectAssetID != "" {
		t.Fatalf("results = %+v", outcome.Results)
	}
	if item := repository.items[list.ID][0]; item.State != assetlist.StateAccepted ||
		item.ProjectAssetID != "" {
		t.Fatalf("item = %+v; want accepted without a project asset", item)
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
	// The first make may take all the Agent rounds it likes: the rework limit
	// counts returns, so one return is still allowed at round 4.
	items[0].Round = 4
	items[0].ReturnCount = 2
	repository.items[list.ID] = items
	if _, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionReturn, ItemIDs: []string{itemID},
		ReasonIDs: []string{"style"}, ExpectedVersion: list.Version,
	}); err != nil {
		t.Fatalf("third return at round 4: %v; want it allowed", err)
	}

	// Three returns used: the next one is refused, whatever the round.
	items = repository.items[list.ID]
	items[0].State = assetlist.StateReview
	items[0].Round = 7
	items[0].ReturnCount = 3
	repository.items[list.ID] = items
	list = repository.withItems(repository.lists[list.ID])
	_, err := service.Review(ctx, assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionReturn, ItemIDs: []string{itemID},
		ReasonIDs: []string{"style"}, ExpectedVersion: list.Version,
	})
	var limit *assetlist.ReworkLimitError
	if !errors.As(err, &limit) || limit.Returns != 3 || limit.Maximum != 3 {
		t.Fatalf("rework limit error = %v", err)
	}
}

// A first make that used the Agent rounds still gets its reworks: the return
// limit reads return_count, not round.
func TestReviewReturnCountsReturnsNotAgentRounds(t *testing.T) {
	fake := newFakeProjectAssets()
	service, repository, list := reviewFixture(t, fake)
	itemID := reviewItemID(list)
	items := repository.items[list.ID]
	items[0].Round = 3
	repository.items[list.ID] = items

	outcome, err := service.Review(context.Background(), assetlist.ReviewInput{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.ReviewDecisionReturn, ItemIDs: []string{itemID},
		ReasonIDs: []string{"style"}, ExpectedVersion: list.Version,
	})
	if err != nil {
		t.Fatalf("first return after a three-round make: %v", err)
	}
	if len(outcome.Results) != 1 || outcome.Results[0].Verdict != assetlist.ReviewVerdictReturned {
		t.Fatalf("results = %+v", outcome.Results)
	}
	item := repository.items[list.ID][0]
	if item.State != assetlist.StateReturned || item.ReturnCount != 1 || item.Round != 4 ||
		item.WorkRound != 0 {
		t.Fatalf("returned item = %+v; want return_count 1, round 4, work round 0", item)
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
