package assetlist

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Review verdicts of one item decision (#329). approved settles the item for
// the batch delivery; returned sends it back for another round. The ids are
// fixed here; the Schema owns the user-facing labels.
const (
	ReviewVerdictApproved = "approved"
	ReviewVerdictReturned = "returned"
)

// ItemReview is one recorded acceptance decision of a list item (#329). It is
// append-only history: each round of an item keeps its own decision, the board
// reads it, and #329b appends one row per decision. Review is the snapshot
// taken when the item entered review; an empty object is the truthful answer
// for a row written before snapshots existed.
type ItemReview struct {
	ID      string `json:"id"`
	OwnerID string `json:"owner_id"`
	ListID  string `json:"list_id"`
	ItemID  string `json:"item_id"`
	Round   int    `json:"round"`
	Verdict string `json:"verdict"`
	// Reason is the human-readable return copy; Reasons keeps the declared
	// reason ids the person picked (#329b).
	Reason     string          `json:"reason,omitempty"`
	Reasons    []string        `json:"reasons,omitempty"`
	Note       string          `json:"note,omitempty"`
	ReviewerID string          `json:"reviewer_id,omitempty"`
	Review     json.RawMessage `json:"review"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ReviewStore reads one owner's list decisions, newest first (#329a). It is a
// read-only port: #329b appends decisions through the list repository. A
// missing or foreign list has no rows; the service checks list ownership
// first.
type ReviewStore interface {
	ItemReviews(ctx context.Context, ownerID, listID string) ([]ItemReview, error)
}

// BoardItem is one list item with its most recent decision. LatestReview is
// nil until the person decides for the first time.
type BoardItem struct {
	Item
	LatestReview *ItemReview `json:"latest_review"`
}

// Board is the read-only acceptance board of one list (#329a): the list
// summary (including the run state when it has one), every item in position
// order with its produced-artifact reference and latest decision. The batch
// ZIP of accepted items lives in archive.go (#329c).
type Board struct {
	List  List        `json:"list"`
	Items []BoardItem `json:"items"`
}

// Board reads the owner's list as an acceptance board. A missing or foreign
// project/list is ErrNotFound. Without a review store every item shows an
// empty latest review, which is exact before any decision exists.
func (service *Service) Board(
	ctx context.Context,
	ownerID, projectID, listID string,
) (Board, error) {
	ownerID = strings.TrimSpace(ownerID)
	projectID = strings.TrimSpace(projectID)
	listID = strings.TrimSpace(listID)
	if ownerID == "" || projectID == "" || listID == "" {
		return Board{}, ErrNotFound
	}
	list, err := service.repository.GetList(ctx, ownerID, projectID, listID)
	if err != nil {
		return Board{}, err
	}
	summaries, err := service.runSummaries(ctx, ownerID, projectID)
	if err != nil {
		return Board{}, err
	}
	list = service.decorateWithRun(list, summaries)
	latest := map[string]ItemReview{}
	if service.reviews != nil {
		reviews, err := service.reviews.ItemReviews(ctx, ownerID, listID)
		if err != nil {
			return Board{}, err
		}
		for _, review := range reviews {
			if _, seen := latest[review.ItemID]; !seen {
				latest[review.ItemID] = review
			}
		}
	}
	items := make([]BoardItem, 0, len(list.Items))
	for _, item := range list.Items {
		entry := BoardItem{Item: item}
		if review, found := latest[item.ID]; found {
			entry.LatestReview = &review
		}
		items = append(items, entry)
	}
	return Board{List: list, Items: items}, nil
}
