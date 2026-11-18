package assetlist_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

type memoryReviewStore struct {
	reviews []assetlist.ItemReview
	err     error
	ownerID string
	listID  string
}

func (store *memoryReviewStore) ItemReviews(
	_ context.Context,
	ownerID, listID string,
) ([]assetlist.ItemReview, error) {
	store.ownerID, store.listID = ownerID, listID
	if store.err != nil {
		return nil, store.err
	}
	return append([]assetlist.ItemReview(nil), store.reviews...), nil
}

func TestBoardAggregatesLatestReviewsAndArtifacts(t *testing.T) {
	t.Parallel()
	store := &memoryReviewStore{}
	service, repository, clock := newService(t, func(config *assetlist.Config) {
		config.Reviews = store
	})
	ctx := context.Background()
	input := proposal()
	input.Items = []assetlist.ItemInput{
		{Kind: "character", Name: "Hero"},
		{Kind: "image", Name: "Icon"},
	}
	list, err := service.Propose(ctx, input)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	items := repository.items[list.ID]
	items[0].State = assetlist.StateAccepted
	items[0].Round = 2
	items[0].ProjectAssetID = "passet_hero"
	items[0].Result = json.RawMessage(`{"format":"glb","task_id":"task_1"}`)
	items[1].State = assetlist.StateReturned
	items[1].Round = 1
	items[1].Result = json.RawMessage(`{"format":"png","task_id":"task_2"}`)
	repository.items[list.ID] = items

	store.reviews = []assetlist.ItemReview{{
		ID: "rev_new", OwnerID: "owner-a", ListID: list.ID, ItemID: items[0].ID, Round: 2,
		Verdict: assetlist.ReviewVerdictApproved, Reason: "ok", ReviewerID: "owner-a",
		Review: json.RawMessage(`{"steps":4}`), CreatedAt: clock.Now(),
	}, {
		ID: "rev_old", OwnerID: "owner-a", ListID: list.ID, ItemID: items[0].ID, Round: 1,
		Verdict: assetlist.ReviewVerdictReturned, Reason: "size", ReviewerID: "owner-a",
		Review: json.RawMessage(`{"steps":2}`), CreatedAt: clock.Now().Add(-time.Minute),
	}}

	board, err := service.Board(ctx, "owner-a", "prj_1", list.ID)
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	if store.ownerID != "owner-a" || store.listID != list.ID {
		t.Fatalf("review read scope = %s/%s", store.ownerID, store.listID)
	}
	if board.List.ID != list.ID || board.List.ItemCount != 2 ||
		board.List.PlannedStepCount != items[0].PlannedStepCount+items[1].PlannedStepCount {
		t.Fatalf("board list = %+v", board.List)
	}
	if len(board.Items) != 2 || board.Items[0].ID != items[0].ID || board.Items[1].ID != items[1].ID {
		t.Fatalf("board items = %+v", board.Items)
	}
	hero := board.Items[0]
	if hero.State != assetlist.StateAccepted || hero.Round != 2 ||
		hero.ProjectAssetID != "passet_hero" || string(hero.Result) != `{"format":"glb","task_id":"task_1"}` {
		t.Fatalf("hero = %+v", hero)
	}
	if hero.LatestReview == nil || hero.LatestReview.ID != "rev_new" ||
		hero.LatestReview.Verdict != assetlist.ReviewVerdictApproved ||
		hero.LatestReview.Round != 2 || string(hero.LatestReview.Review) != `{"steps":4}` {
		t.Fatalf("hero latest review = %+v", hero.LatestReview)
	}
	icon := board.Items[1]
	if icon.State != assetlist.StateReturned || icon.Round != 1 || icon.LatestReview != nil ||
		string(icon.Result) != `{"format":"png","task_id":"task_2"}` {
		t.Fatalf("icon = %+v", icon)
	}
}

func TestBoardWithoutReviewsLeavesDecisionEmpty(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, nil)
	list, err := service.Propose(context.Background(), proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	board, err := service.Board(context.Background(), "owner-a", "prj_1", list.ID)
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	if len(board.Items) != 1 || board.Items[0].LatestReview != nil {
		t.Fatalf("board items = %+v", board.Items)
	}
}

func TestBoardIsOwnerAndProjectScoped(t *testing.T) {
	t.Parallel()
	store := &memoryReviewStore{}
	service, _, _ := newService(t, func(config *assetlist.Config) {
		config.Reviews = store
	})
	list, err := service.Propose(context.Background(), proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	ctx := context.Background()
	if _, err := service.Board(ctx, "owner-b", "prj_1", list.ID); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign owner error = %v", err)
	}
	if _, err := service.Board(ctx, "owner-a", "prj_2", list.ID); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("wrong project error = %v", err)
	}
	if store.ownerID != "" || store.listID != "" {
		t.Fatalf("review store read before ownership check: %s/%s", store.ownerID, store.listID)
	}
}

func TestBoardPropagatesReviewReadFailure(t *testing.T) {
	t.Parallel()
	readErr := errors.New("reviews unavailable")
	service, _, _ := newService(t, func(config *assetlist.Config) {
		config.Reviews = &memoryReviewStore{err: readErr}
	})
	list, err := service.Propose(context.Background(), proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := service.Board(context.Background(), "owner-a", "prj_1", list.ID); !errors.Is(err, readErr) {
		t.Fatalf("board error = %v", err)
	}
}
