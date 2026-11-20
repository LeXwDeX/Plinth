package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// reviewAssetRow is the deterministic project asset row of one item version.
func reviewAssetRow(itemID, ownerID string) project.Asset {
	return project.Asset{
		ID: "passet_" + itemID, ProjectID: "prj_1", OwnerID: ownerID,
		Kind: "character", Name: "Hero", Images: []project.Reference{},
		Models:    []project.Reference{{TaskID: "task_1", AssetID: "asset_model"}},
		CreatedAt: assetListTestNow, UpdatedAt: assetListTestNow,
	}
}

// seedReviewList proposes one list whose item is already awaiting review. The
// proposal stores the item draft state; the explicit update puts it in review,
// which is what the board decides.
func seedReviewList(t *testing.T, repository *Repository, lists *AssetListRepository) assetlist.List {
	t.Helper()
	item := newAssetListItem("owner-a", "aitem_1", "Hero")
	item.Round = 1
	list := proposeAssetList(t, lists,
		newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1"), item)
	if _, err := repository.db.Exec(
		`UPDATE asset_list_items SET state = ? WHERE id = ?`,
		assetlist.StateReview, item.ID); err != nil {
		t.Fatalf("seed item state: %v", err)
	}
	stored, err := lists.GetList(context.Background(), "owner-a", "prj_1", list.ID)
	if err != nil {
		t.Fatalf("read seeded list: %v", err)
	}
	return stored
}

func reviewBatch(list assetlist.List, itemID, verdict, reviewID string) assetlist.DecisionBatch {
	batch := assetlist.DecisionBatch{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		ExpectedVersion: list.Version, ReviewerID: "owner-a",
		At: assetListTestNow.Add(time.Minute), AssetMaximum: 200,
	}
	decision := assetlist.ItemDecision{
		ReviewID: reviewID, ItemID: itemID, Verdict: verdict,
	}
	if verdict == assetlist.ReviewVerdictApproved {
		decision.ProjectAssetID = reviewAssetRow(itemID, "owner-a").ID
		batch.Assets = []project.Asset{reviewAssetRow(itemID, "owner-a")}
	} else {
		decision.Reasons = []string{"style"}
		decision.Reason = "风格不对"
	}
	batch.Items = []assetlist.ItemDecision{decision}
	return batch
}

func countProjectAssets(t *testing.T, repository *Repository) int {
	t.Helper()
	var count int
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM project_assets`).Scan(&count); err != nil {
		t.Fatalf("count project assets: %v", err)
	}
	return count
}

func TestAssetListDecideItemsWritesTheProjectAssetInOneTransaction(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	list := seedReviewList(t, repository, lists)
	itemID := list.Items[0].ID

	updated, err := lists.DecideItems(context.Background(),
		reviewBatch(list, itemID, assetlist.ReviewVerdictApproved, "arev_1"))
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	accepted := updated.Items[0]
	if accepted.State != assetlist.StateAccepted || accepted.ProjectAssetID != reviewAssetRow(itemID, "owner-a").ID ||
		accepted.AcceptedAt == nil {
		t.Fatalf("accepted item = %+v", accepted)
	}
	if count := countProjectAssets(t, repository); count != 1 {
		t.Fatalf("project assets = %d; want the accepted item's one row", count)
	}
	var reviewRows int
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM asset_list_item_reviews`).Scan(&reviewRows); err != nil || reviewRows != 1 {
		t.Fatalf("review rows = %d, %v; want 1", reviewRows, err)
	}

	// A stale version is refused before any write: the stored item and its
	// asset stay exactly as the winning write left them.
	if _, err := lists.DecideItems(context.Background(),
		reviewBatch(list, itemID, assetlist.ReviewVerdictApproved, "arev_2")); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale decision = %v; want ErrConflict", err)
	}
	if count := countProjectAssets(t, repository); count != 1 {
		t.Fatalf("project assets after the refused retry = %d; want 1", count)
	}
}

func TestAssetListDecideItemsRollsBackTheAssetWithTheDecision(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	list := seedReviewList(t, repository, lists)
	itemID := list.Items[0].ID

	// The return is the same version's decision: the accept's asset row must
	// not survive a batch that lost the CAS.
	_, err := lists.DecideItems(context.Background(),
		reviewBatch(list, itemID, assetlist.ReviewVerdictReturned, "arev_return"))
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if count := countProjectAssets(t, repository); count != 0 {
		t.Fatalf("project assets after a return = %d; want none", count)
	}
	if _, err := lists.DecideItems(context.Background(),
		reviewBatch(list, itemID, assetlist.ReviewVerdictApproved, "arev_accept")); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("accept after the return = %v; want ErrConflict", err)
	}
	if count := countProjectAssets(t, repository); count != 0 {
		t.Fatalf("project assets after the refused accept = %d; want none", count)
	}
}

func TestAssetListDecideItemsRefusesAProjectAssetBeyondTheBound(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	list := seedReviewList(t, repository, lists)
	itemID := list.Items[0].ID
	batch := reviewBatch(list, itemID, assetlist.ReviewVerdictApproved, "arev_1")
	batch.AssetMaximum = 0 // the project bound is already used up

	_, err := lists.DecideItems(context.Background(), batch)
	var bound *assetlist.ProjectAssetBoundError
	if !errors.As(err, &bound) || bound.ItemID != itemID {
		t.Fatalf("over-bound accept = %v; want the item's bound error", err)
	}
	if count := countProjectAssets(t, repository); count != 0 {
		t.Fatalf("project assets = %d; want the whole batch rolled back", count)
	}
	stored, err := lists.GetList(context.Background(), "owner-a", "prj_1", list.ID)
	if err != nil {
		t.Fatalf("read list: %v", err)
	}
	if stored.Version != list.Version || stored.Items[0].State != assetlist.StateReview {
		t.Fatalf("list = version %d item %s; want an untouched board",
			stored.Version, stored.Items[0].State)
	}
}

// Two people (or two tabs) deciding the same board version write exactly one
// decision, and an accept's project asset exists only when that accept won.
func TestAssetListDecideItemsSerializesConcurrentSameVersionDecisions(t *testing.T) {
	for _, acceptFirst := range []bool{true, false} {
		repository, lists := newAssetListRepository(t)
		list := seedReviewList(t, repository, lists)
		itemID := list.Items[0].ID
		accept := reviewBatch(list, itemID, assetlist.ReviewVerdictApproved, "arev_accept")
		reject := reviewBatch(list, itemID, assetlist.ReviewVerdictReturned, "arev_return")
		batches := []assetlist.DecisionBatch{reject, accept}
		if acceptFirst {
			batches = []assetlist.DecisionBatch{accept, reject}
		}

		var wait sync.WaitGroup
		errorsSeen := make([]error, len(batches))
		for index, batch := range batches {
			wait.Add(1)
			go func(index int, batch assetlist.DecisionBatch) {
				defer wait.Done()
				_, errorsSeen[index] = lists.DecideItems(context.Background(), batch)
			}(index, batch)
		}
		wait.Wait()

		wins := 0
		for index, err := range errorsSeen {
			if err == nil {
				wins++
				continue
			}
			if !errors.Is(err, assetlist.ErrConflict) {
				t.Fatalf("concurrent decision %d = %v; want ErrConflict", index, err)
			}
		}
		if wins != 1 {
			t.Fatalf("wins = %d (%v); want exactly one decision at one version", wins, errorsSeen)
		}
		stored, err := lists.GetList(context.Background(), "owner-a", "prj_1", list.ID)
		if err != nil {
			t.Fatalf("read list: %v", err)
		}
		item := stored.Items[0]
		switch item.State {
		case assetlist.StateAccepted:
			if count := countProjectAssets(t, repository); count != 1 {
				t.Fatalf("accepted item's project assets = %d; want its one row", count)
			}
		case assetlist.StateReturned:
			if count := countProjectAssets(t, repository); count != 0 {
				t.Fatalf("returned item left %d project assets; want none", count)
			}
		default:
			t.Fatalf("item state = %s; want the winning decision", item.State)
		}
	}
}
