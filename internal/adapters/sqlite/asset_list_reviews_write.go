package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

// DecideItems applies one board submission (#329b) in one write transaction:
// the list version CAS gates the batch, every item must still be awaiting
// review, the per-round review rows are appended, and a return reopens a
// completed run so the runner continues the item's sub-session. Any conflict
// rolls the whole batch back; the service reports per-item asset errors
// before it gets here.
func (r *AssetListRepository) DecideItems(ctx context.Context, batch assetlist.DecisionBatch) (assetlist.List, error) {
	if batch.OwnerID == "" || batch.ProjectID == "" || batch.ListID == "" ||
		len(batch.Items) == 0 || batch.ExpectedVersion < 0 {
		return assetlist.List{}, fmt.Errorf("%w: decision batch", assetlist.ErrInvalid)
	}
	var stored assetlist.List
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		current, err := loadAssetList(ctx, conn, batch.OwnerID, batch.ProjectID, batch.ListID)
		if err != nil {
			return err
		}
		if current.Version != batch.ExpectedVersion {
			return assetlist.ErrConflict
		}
		reopened := false
		for _, decision := range batch.Items {
			if decision.ReviewID == "" || decision.ItemID == "" {
				return fmt.Errorf("%w: decision identity", assetlist.ErrInvalid)
			}
			var state string
			var round int
			var reviewJSON string
			err := conn.QueryRowContext(ctx, `
				SELECT state, round, review_json FROM asset_list_items
				WHERE id = ? AND owner_id = ? AND list_id = ?`,
				decision.ItemID, batch.OwnerID, batch.ListID).Scan(&state, &round, &reviewJSON)
			if errors.Is(err, sql.ErrNoRows) {
				return assetlist.ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("read asset list item for decision: %w", err)
			}
			if state != assetlist.StateReview {
				return assetlist.ErrItemState
			}
			switch decision.Verdict {
			case assetlist.ReviewVerdictApproved:
				if decision.ProjectAssetID == "" {
					return fmt.Errorf("%w: accepted item needs its project asset", assetlist.ErrInvalid)
				}
				result, err := conn.ExecContext(ctx, `
					UPDATE asset_list_items SET state = ?, project_asset_id = ?,
						accepted_at = ?, return_reason = '', return_note = '',
						version = version + 1, updated_at = ?
					WHERE id = ? AND state = ?`,
					assetlist.StateAccepted, decision.ProjectAssetID,
					formatTime(batch.At), formatTime(batch.At),
					decision.ItemID, assetlist.StateReview)
				if err != nil {
					return fmt.Errorf("accept asset list item: %w", err)
				}
				if err := expectOneRow(result); err != nil {
					return err
				}
			case assetlist.ReviewVerdictReturned:
				if decision.Reason == "" {
					return fmt.Errorf("%w: a return needs its reason copy", assetlist.ErrInvalid)
				}
				result, err := conn.ExecContext(ctx, `
					UPDATE asset_list_items SET state = ?, round = round + 1,
						return_reason = ?, return_note = ?, next_action_at = '',
						block = '', version = version + 1, updated_at = ?
					WHERE id = ? AND state = ?`,
					assetlist.StateReturned, decision.Reason, decision.Note,
					formatTime(batch.At), decision.ItemID, assetlist.StateReview)
				if err != nil {
					return fmt.Errorf("return asset list item: %w", err)
				}
				if err := expectOneRow(result); err != nil {
					return err
				}
				reopened = true
			default:
				return fmt.Errorf("%w: decision verdict %q", assetlist.ErrInvalid, decision.Verdict)
			}
			reasons, err := json.Marshal(decision.Reasons)
			if err != nil {
				return fmt.Errorf("encode review reasons: %w", err)
			}
			if len(decision.Reasons) == 0 {
				reasons = []byte("[]")
			}
			if reviewJSON == "" {
				reviewJSON = "{}"
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO asset_list_item_reviews (id, owner_id, list_id, item_id, round,
					verdict, reason, reasons_json, note, reviewer_id, review_json, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				decision.ReviewID, batch.OwnerID, batch.ListID, decision.ItemID, round,
				decision.Verdict, decision.Reason, string(reasons), decision.Note,
				batch.ReviewerID, reviewJSON, formatTime(batch.At)); err != nil {
				return fmt.Errorf("insert asset list item review: %w", err)
			}
		}
		update, err := conn.ExecContext(ctx, `
			UPDATE asset_lists SET version = version + 1, updated_at = ?
			WHERE id = ? AND owner_id = ? AND project_id = ? AND version = ?`,
			formatTime(batch.At), batch.ListID, batch.OwnerID, batch.ProjectID,
			batch.ExpectedVersion)
		if err != nil {
			return fmt.Errorf("bump asset list version: %w", err)
		}
		if err := expectOneRow(update); err != nil {
			return err
		}
		if reopened {
			// A returned item is work again: a completed run resumes so the
			// runner claims it; stopped and paused runs keep their state.
			if _, err := conn.ExecContext(ctx, `
				UPDATE asset_list_runs SET status = ?, pause_reason = '', updated_at = ?
				WHERE owner_id = ? AND list_id = ? AND status = ?`,
				assetlist.RunRunning, formatTime(batch.At),
				batch.OwnerID, batch.ListID, assetlist.RunCompleted); err != nil {
				return fmt.Errorf("reopen asset list run: %w", err)
			}
		}
		stored, err = loadAssetList(ctx, conn, batch.OwnerID, batch.ProjectID, batch.ListID)
		return err
	})
	if err != nil {
		return assetlist.List{}, err
	}
	return stored, nil
}

// expectOneRow turns a lost conditional update into ErrConflict.
func expectOneRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return assetlist.ErrConflict
	}
	return nil
}
