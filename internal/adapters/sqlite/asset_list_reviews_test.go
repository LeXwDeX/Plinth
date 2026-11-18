package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

func insertAssetListItemReview(t *testing.T, repository *Repository, review assetlist.ItemReview) {
	t.Helper()
	raw := review.Review
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if _, err := repository.db.Exec(`
		INSERT INTO asset_list_item_reviews (id, owner_id, list_id, item_id, round,
			verdict, reason, reviewer_id, review_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		review.ID, review.OwnerID, review.ListID, review.ItemID, review.Round,
		review.Verdict, review.Reason, review.ReviewerID, string(raw),
		formatTaskTime(review.CreatedAt)); err != nil {
		t.Fatalf("insert asset list item review: %v", err)
	}
}

func TestAssetListItemReviewsMigrationCreatesTableAndIndexes(t *testing.T) {
	repository, _ := newAssetListRepository(t)
	var name string
	if err := repository.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'asset_list_item_reviews'`).
		Scan(&name); err != nil || name != "asset_list_item_reviews" {
		t.Fatalf("asset_list_item_reviews table missing: %v", err)
	}
	for _, index := range []string{
		"asset_list_item_reviews_item_created",
		"asset_list_item_reviews_owner_created",
	} {
		if err := repository.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).
			Scan(&name); err != nil || name != index {
			t.Fatalf("index %s missing: %v", index, err)
		}
	}
}

func TestAssetListReviewRepositoryReadsOwnerScopedNewestFirst(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	reviews := repository.AssetListReviewRepository()
	ctx := context.Background()
	list := proposeAssetList(t, lists, newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1"),
		newAssetListItem("owner-a", "aitem_1", "Hero"), newAssetListItem("owner-a", "aitem_2", "Icon"))

	insertAssetListItemReview(t, repository, assetlist.ItemReview{
		ID: "rev_old", OwnerID: "owner-a", ListID: list.ID, ItemID: "aitem_1", Round: 1,
		Verdict: assetlist.ReviewVerdictReturned, Reason: "size", ReviewerID: "owner-a",
		Review: json.RawMessage(`{"steps":3}`), CreatedAt: assetListTestNow,
	})
	insertAssetListItemReview(t, repository, assetlist.ItemReview{
		ID: "rev_new", OwnerID: "owner-a", ListID: list.ID, ItemID: "aitem_1", Round: 2,
		Verdict: assetlist.ReviewVerdictApproved, Reason: "ok", ReviewerID: "owner-a",
		Review: json.RawMessage(`{"steps":5}`), CreatedAt: assetListTestNow.Add(time.Minute),
	})
	insertAssetListItemReview(t, repository, assetlist.ItemReview{
		ID: "rev_foreign", OwnerID: "owner-b", ListID: list.ID, ItemID: "aitem_2", Round: 1,
		Verdict: assetlist.ReviewVerdictApproved, ReviewerID: "owner-b",
		CreatedAt: assetListTestNow.Add(2 * time.Minute),
	})

	stored, err := reviews.ItemReviews(ctx, "owner-a", list.ID)
	if err != nil {
		t.Fatalf("item reviews: %v", err)
	}
	if len(stored) != 2 || stored[0].ID != "rev_new" || stored[1].ID != "rev_old" {
		t.Fatalf("reviews = %+v", stored)
	}
	if stored[0].Round != 2 || stored[0].Verdict != assetlist.ReviewVerdictApproved ||
		stored[0].ReviewerID != "owner-a" || string(stored[0].Review) != `{"steps":5}` ||
		!stored[0].CreatedAt.Equal(assetListTestNow.Add(time.Minute)) {
		t.Fatalf("latest review = %+v", stored[0])
	}
	foreign, err := reviews.ItemReviews(ctx, "owner-b", list.ID)
	if err != nil || len(foreign) != 1 || foreign[0].OwnerID != "owner-b" {
		t.Fatalf("foreign reviews = %+v, %v", foreign, err)
	}
	missing, err := reviews.ItemReviews(ctx, "owner-a", "alist_missing")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing list reviews = %+v, %v", missing, err)
	}
}

func TestAssetListItemReviewsCascadeFromList(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	list := proposeAssetList(t, lists, newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1"),
		newAssetListItem("owner-a", "aitem_1", "Hero"))
	insertAssetListItemReview(t, repository, assetlist.ItemReview{
		ID: "rev_1", OwnerID: "owner-a", ListID: list.ID, ItemID: "aitem_1", Round: 1,
		Verdict: assetlist.ReviewVerdictApproved, CreatedAt: assetListTestNow,
	})
	if _, err := repository.db.Exec(`DELETE FROM asset_lists WHERE id = ?`, list.ID); err != nil {
		t.Fatalf("delete list: %v", err)
	}
	var count int
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM asset_list_item_reviews`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reviews after list delete = %d, %v", count, err)
	}
}
