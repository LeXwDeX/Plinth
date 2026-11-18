package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

// AssetListReviewRepository is the read-only SQLite adapter of the item
// acceptance decisions (#329a). The decisions are appended by #329b; this
// adapter only reads them, owner-scoped and ordered newest first so the board
// can keep the first row per item.
type AssetListReviewRepository struct {
	db *sql.DB
}

func (r *Repository) AssetListReviewRepository() *AssetListReviewRepository {
	return &AssetListReviewRepository{db: r.db}
}

var _ assetlist.ReviewStore = (*AssetListReviewRepository)(nil)

const assetListItemReviewColumns = `id, owner_id, list_id, item_id, round, verdict,
	reason, reviewer_id, review_json, created_at`

func (r *AssetListReviewRepository) ItemReviews(
	ctx context.Context,
	ownerID, listID string,
) ([]assetlist.ItemReview, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+assetListItemReviewColumns+` FROM asset_list_item_reviews
		WHERE owner_id = ? AND list_id = ?
		ORDER BY created_at DESC, id DESC`, ownerID, listID)
	if err != nil {
		return nil, fmt.Errorf("list asset list item reviews: %w", err)
	}
	defer rows.Close()
	result := make([]assetlist.ItemReview, 0)
	for rows.Next() {
		var review assetlist.ItemReview
		var raw, created string
		if err := rows.Scan(
			&review.ID, &review.OwnerID, &review.ListID, &review.ItemID, &review.Round,
			&review.Verdict, &review.Reason, &review.ReviewerID, &raw, &created,
		); err != nil {
			return nil, fmt.Errorf("scan asset list item review: %w", err)
		}
		if raw == "" {
			raw = "{}"
		}
		review.Review = json.RawMessage(raw)
		review.CreatedAt, _ = parseTime(created)
		result = append(result, review)
	}
	return result, rows.Err()
}
