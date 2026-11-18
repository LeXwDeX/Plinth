package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

// AssetListRunRepository is the owner-scoped SQLite adapter of the list
// execution state (#328): one run per list, the item claim/finish CAS and the
// per-tab presence heartbeat. Every write runs in one BEGIN IMMEDIATE
// transaction; the status/version guards must be atomic with the update they
// justify. next_action_at and last_seen_at are fixed-width UTC instants
// (formatTaskTime) so TEXT comparison is time order.
type AssetListRunRepository struct {
	db *sql.DB
}

// AssetListPresenceRepository is the owner/project-scoped tab heartbeat
// adapter. Present is an UPSERT per tab; expired rows are deleted, never kept.
type AssetListPresenceRepository struct {
	db *sql.DB
}

func (r *Repository) AssetListRunRepository() *AssetListRunRepository {
	return &AssetListRunRepository{db: r.db}
}

func (r *Repository) AssetListPresenceRepository() *AssetListPresenceRepository {
	return &AssetListPresenceRepository{db: r.db}
}

var (
	_ assetlist.RunStore      = (*AssetListRunRepository)(nil)
	_ assetlist.PresenceStore = (*AssetListPresenceRepository)(nil)
)

const assetListRunColumns = `id, owner_id, project_id, list_id, status, pause_reason, created_at, updated_at`

func (r *AssetListRunRepository) withWrite(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// formatTaskTimeOrEmpty renders a scheduling instant as the fixed-width form
// used by next_action_at, or the empty marker for "immediately actionable".
func formatTaskTimeOrEmpty(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatTaskTime(value)
}

func validAssetListRunStatus(status string) bool {
	switch status {
	case assetlist.RunIdle, assetlist.RunRunning, assetlist.RunPaused,
		assetlist.RunStopped, assetlist.RunCompleted:
		return true
	}
	return false
}

func validAssetListPauseReason(reason string) bool {
	switch reason {
	case assetlist.PauseNone, assetlist.PauseUser, assetlist.PausePresence,
		assetlist.PauseStepCap, assetlist.PauseFailure:
		return true
	}
	return false
}

// assetListRunTransition is the Go-layer guard of the run state machine:
// idle→running, running↔paused, idle/running/paused→stopped and
// running→completed. Stopped and completed are terminal, paused never turns
// directly into completed.
func assetListRunTransition(from, to string) bool {
	switch from {
	case assetlist.RunIdle:
		return to == assetlist.RunRunning || to == assetlist.RunStopped
	case assetlist.RunRunning:
		return to == assetlist.RunPaused || to == assetlist.RunStopped ||
			to == assetlist.RunCompleted
	case assetlist.RunPaused:
		return to == assetlist.RunRunning || to == assetlist.RunStopped
	}
	return false
}

// CreateRun stores the list's run. A second CreateRun for the same list
// returns the stored run untouched (idempotent, like a repeated decision); a
// list that is missing or belongs to another owner is ErrNotFound.
func (r *AssetListRunRepository) CreateRun(ctx context.Context, run assetlist.Run) (assetlist.Run, error) {
	if run.ID == "" || run.OwnerID == "" || run.ProjectID == "" || run.ListID == "" {
		return assetlist.Run{}, fmt.Errorf("%w: run identity", assetlist.ErrInvalid)
	}
	if !validAssetListRunStatus(run.Status) {
		return assetlist.Run{}, fmt.Errorf("%w: run status %q", assetlist.ErrInvalid, run.Status)
	}
	var stored assetlist.Run
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		if err := assetListBelongsTo(ctx, conn, run.OwnerID, run.ProjectID, run.ListID); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO asset_list_runs (id, owner_id, project_id, list_id, status,
				pause_reason, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(list_id) DO NOTHING`,
			run.ID, run.OwnerID, run.ProjectID, run.ListID, run.Status,
			run.PauseReason, formatTime(run.CreatedAt), formatTime(run.UpdatedAt)); err != nil {
			return fmt.Errorf("insert asset list run: %w", err)
		}
		var err error
		stored, err = loadAssetListRunByList(ctx, conn, run.OwnerID, run.ProjectID, run.ListID)
		return err
	})
	if err != nil {
		return assetlist.Run{}, err
	}
	return stored, nil
}

func assetListBelongsTo(ctx context.Context, queryer assetListQueryer, ownerID, projectID, listID string) error {
	var exists int
	err := queryer.QueryRowContext(ctx, `
		SELECT 1 FROM asset_lists WHERE id = ? AND owner_id = ? AND project_id = ?`,
		listID, ownerID, projectID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return assetlist.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read asset list for run: %w", err)
	}
	return nil
}

func (r *AssetListRunRepository) GetRunByList(ctx context.Context, ownerID, projectID, listID string) (assetlist.Run, error) {
	run, err := scanAssetListRun(r.db.QueryRowContext(ctx, `
		SELECT `+assetListRunColumns+` FROM asset_list_runs
		WHERE owner_id = ? AND project_id = ? AND list_id = ?`,
		ownerID, projectID, listID))
	if errors.Is(err, sql.ErrNoRows) {
		return assetlist.Run{}, assetlist.ErrNotFound
	}
	if err != nil {
		return assetlist.Run{}, fmt.Errorf("read asset list run: %w", err)
	}
	return run, nil
}

// GetRunByListID reads the run when the caller knows the item's list but not
// its project (the submission gate). The run carries its own project.
func (r *AssetListRunRepository) GetRunByListID(ctx context.Context, ownerID, listID string) (assetlist.Run, error) {
	run, err := loadAssetListRunByListID(ctx, r.db, ownerID, listID)
	if err != nil {
		if errors.Is(err, assetlist.ErrNotFound) {
			return assetlist.Run{}, err
		}
		return assetlist.Run{}, fmt.Errorf("read asset list run: %w", err)
	}
	return run, nil
}

// RunsByStatus scans the runner's due runs across owners; it has no owner
// filter because the runner is the server-side scheduler, not a person's
// request.
func (r *AssetListRunRepository) RunsByStatus(ctx context.Context, status string) ([]assetlist.Run, error) {
	if !validAssetListRunStatus(status) {
		return nil, fmt.Errorf("%w: run status %q", assetlist.ErrInvalid, status)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+assetListRunColumns+` FROM asset_list_runs
		WHERE status = ? ORDER BY created_at, id`, status)
	if err != nil {
		return nil, fmt.Errorf("list asset list runs: %w", err)
	}
	defer rows.Close()
	result := make([]assetlist.Run, 0)
	for rows.Next() {
		run, err := scanAssetListRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

// UpdateRunStatus applies one guarded run transition. Repeating the target
// status returns the current run (a double click pauses once); any other
// status mismatch or an illegal transition is ErrConflict. A pause must name
// a reason and every other target clears it.
func (r *AssetListRunRepository) UpdateRunStatus(
	ctx context.Context,
	ownerID, listID, from, to, pauseReason string,
	at time.Time,
) (assetlist.Run, error) {
	if ownerID == "" || listID == "" || !validAssetListRunStatus(from) || !validAssetListRunStatus(to) {
		return assetlist.Run{}, fmt.Errorf("%w: run status transition", assetlist.ErrInvalid)
	}
	if to == assetlist.RunPaused {
		if pauseReason == assetlist.PauseNone || !validAssetListPauseReason(pauseReason) {
			return assetlist.Run{}, fmt.Errorf("%w: pause reason %q", assetlist.ErrInvalid, pauseReason)
		}
	} else {
		pauseReason = assetlist.PauseNone
	}
	var stored assetlist.Run
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		current, err := scanAssetListRun(conn.QueryRowContext(ctx, `
			SELECT `+assetListRunColumns+` FROM asset_list_runs
			WHERE owner_id = ? AND list_id = ?`, ownerID, listID))
		if errors.Is(err, sql.ErrNoRows) {
			return assetlist.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read asset list run for status: %w", err)
		}
		if current.Status == to {
			stored = current
			return nil
		}
		if current.Status != from || !assetListRunTransition(from, to) {
			return assetlist.ErrConflict
		}
		if _, err := conn.ExecContext(ctx, `
			UPDATE asset_list_runs SET status = ?, pause_reason = ?, updated_at = ?
			WHERE owner_id = ? AND list_id = ? AND status = ?`,
			to, pauseReason, formatTime(at), ownerID, listID, from); err != nil {
			return fmt.Errorf("update asset list run status: %w", err)
		}
		stored, err = loadAssetListRunByListID(ctx, conn, ownerID, listID)
		return err
	})
	if err != nil {
		return assetlist.Run{}, err
	}
	return stored, nil
}

// ClaimItem moves the first todo item by position to working with round+1 and
// version+1, but only while the run is running. It reports false when nothing
// is claimable; an update that lost the todo CAS is ErrConflict.
func (r *AssetListRunRepository) ClaimItem(
	ctx context.Context,
	ownerID, listID string,
	at time.Time,
) (assetlist.Item, bool, error) {
	var claimed assetlist.Item
	found := false
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		var status string
		err := conn.QueryRowContext(ctx, `
			SELECT status FROM asset_list_runs WHERE owner_id = ? AND list_id = ?`,
			ownerID, listID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return assetlist.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read asset list run for claim: %w", err)
		}
		if status != assetlist.RunRunning {
			return nil
		}
		var itemID string
		err = conn.QueryRowContext(ctx, `
			SELECT id FROM asset_list_items
			WHERE owner_id = ? AND list_id = ? AND state = ?
			ORDER BY position LIMIT 1`,
			ownerID, listID, assetlist.StateTodo).Scan(&itemID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read next asset list item: %w", err)
		}
		result, err := conn.ExecContext(ctx, `
			UPDATE asset_list_items SET state = ?, round = round + 1,
				version = version + 1, next_action_at = '', updated_at = ?
			WHERE id = ? AND state = ?`,
			assetlist.StateWorking, formatTime(at), itemID, assetlist.StateTodo)
		if err != nil {
			return fmt.Errorf("claim asset list item: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			// Another transaction claimed the item between the read and the CAS.
			return assetlist.ErrConflict
		}
		claimed, err = loadAssetListItemByID(ctx, conn, ownerID, listID, itemID)
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return assetlist.Item{}, false, err
	}
	return claimed, found, nil
}

// FinishItem moves one working item to review, failed or skipped. A different
// current state is ErrItemState; a missing or foreign item is ErrNotFound.
func (r *AssetListRunRepository) FinishItem(ctx context.Context, input assetlist.FinishItemInput) (assetlist.Item, error) {
	switch input.State {
	case assetlist.StateReview, assetlist.StateFailed, assetlist.StateSkipped:
	default:
		return assetlist.Item{}, fmt.Errorf("%w: finish state %q", assetlist.ErrInvalid, input.State)
	}
	if input.OwnerID == "" || input.ListID == "" || input.ItemID == "" {
		return assetlist.Item{}, fmt.Errorf("%w: item identity", assetlist.ErrInvalid)
	}
	result := input.Result
	if len(bytes.TrimSpace(result)) == 0 {
		result = json.RawMessage("{}")
	}
	review := input.Review
	if len(bytes.TrimSpace(review)) == 0 {
		review = json.RawMessage("{}")
	}
	var stored assetlist.Item
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		update, err := conn.ExecContext(ctx, `
			UPDATE asset_list_items SET state = ?, state_reason = ?, result_json = ?,
				review_json = ?, next_action_at = ?, version = version + 1, updated_at = ?
			WHERE id = ? AND owner_id = ? AND list_id = ? AND state = ?`,
			input.State, input.StateReason, string(result), string(review),
			formatTaskTimeOrEmpty(input.NextActionAt), formatTime(input.At),
			input.ItemID, input.OwnerID, input.ListID, assetlist.StateWorking)
		if err != nil {
			return fmt.Errorf("finish asset list item: %w", err)
		}
		affected, err := update.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			if _, err := loadAssetListItemByID(ctx, conn, input.OwnerID, input.ListID, input.ItemID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return assetlist.ErrNotFound
				}
				return err
			}
			return assetlist.ErrItemState
		}
		stored, err = loadAssetListItemByID(ctx, conn, input.OwnerID, input.ListID, input.ItemID)
		return err
	})
	if err != nil {
		return assetlist.Item{}, err
	}
	return stored, nil
}

func (r *AssetListRunRepository) SetItemBlock(
	ctx context.Context,
	ownerID, listID, itemID, block string,
	expectedVersion int64,
	at time.Time,
) (assetlist.Item, error) {
	switch block {
	case assetlist.BlockNone, assetlist.BlockAwaitingApproval,
		assetlist.BlockPaused, assetlist.BlockStepCap:
	default:
		return assetlist.Item{}, fmt.Errorf("%w: item block %q", assetlist.ErrInvalid, block)
	}
	return r.updateAssetListItem(ctx, ownerID, listID, itemID, expectedVersion, at,
		`block = ?`, block)
}

func (r *AssetListRunRepository) SetItemSession(
	ctx context.Context,
	ownerID, listID, itemID, sessionID string,
	expectedVersion int64,
	at time.Time,
) (assetlist.Item, error) {
	return r.updateAssetListItem(ctx, ownerID, listID, itemID, expectedVersion, at,
		`session_id = ?`, nullString(sessionID))
}

func (r *AssetListRunRepository) SetItemNextActionAt(
	ctx context.Context,
	ownerID, listID, itemID string,
	nextActionAt time.Time,
	expectedVersion int64,
	at time.Time,
) (assetlist.Item, error) {
	return r.updateAssetListItem(ctx, ownerID, listID, itemID, expectedVersion, at,
		`next_action_at = ?`, formatTaskTimeOrEmpty(nextActionAt))
}

// RecordItemTurn stores the assistant message of a turn the server opened for
// a working item and sets its started-round count. A missing or foreign item
// is ErrNotFound; a different state is ErrItemState; a lost CAS guard is
// ErrConflict.
func (r *AssetListRunRepository) RecordItemTurn(
	ctx context.Context,
	ownerID, listID, itemID, assistantMessageID string,
	round int,
	expectedVersion int64,
	at time.Time,
) (assetlist.Item, error) {
	if ownerID == "" || listID == "" || itemID == "" {
		return assetlist.Item{}, fmt.Errorf("%w: item identity", assetlist.ErrInvalid)
	}
	if assistantMessageID == "" || round <= 0 {
		return assetlist.Item{}, fmt.Errorf("%w: item turn", assetlist.ErrInvalid)
	}
	var stored assetlist.Item
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		query := `UPDATE asset_list_items SET last_turn_message_id = ?, round = ?,
			version = version + 1, updated_at = ?
			WHERE id = ? AND owner_id = ? AND list_id = ? AND state = ?`
		args := []any{assistantMessageID, round, formatTime(at),
			itemID, ownerID, listID, assetlist.StateWorking}
		if expectedVersion != assetlist.VersionUnchecked {
			if expectedVersion < 0 {
				return fmt.Errorf("%w: expected version", assetlist.ErrInvalid)
			}
			query += ` AND version = ?`
			args = append(args, expectedVersion)
		}
		update, err := conn.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("record asset list item turn: %w", err)
		}
		affected, err := update.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			existing, err := loadAssetListItemByID(ctx, conn, ownerID, listID, itemID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return assetlist.ErrNotFound
				}
				return err
			}
			if expectedVersion != assetlist.VersionUnchecked && existing.Version != expectedVersion {
				return assetlist.ErrConflict
			}
			return assetlist.ErrItemState
		}
		stored, err = loadAssetListItemByID(ctx, conn, ownerID, listID, itemID)
		return err
	})
	if err != nil {
		return assetlist.Item{}, err
	}
	return stored, nil
}

// updateAssetListItem applies one column update and always bumps the version.
// A non-negative expectedVersion is a CAS guard; a lost guard is ErrConflict.
func (r *AssetListRunRepository) updateAssetListItem(
	ctx context.Context,
	ownerID, listID, itemID string,
	expectedVersion int64,
	at time.Time,
	assignment string,
	value any,
) (assetlist.Item, error) {
	if ownerID == "" || listID == "" || itemID == "" {
		return assetlist.Item{}, fmt.Errorf("%w: item identity", assetlist.ErrInvalid)
	}
	var stored assetlist.Item
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		query := `UPDATE asset_list_items SET ` + assignment + `,
			version = version + 1, updated_at = ?
			WHERE id = ? AND owner_id = ? AND list_id = ?`
		args := []any{value, formatTime(at), itemID, ownerID, listID}
		if expectedVersion != assetlist.VersionUnchecked {
			if expectedVersion < 0 {
				return fmt.Errorf("%w: expected version", assetlist.ErrInvalid)
			}
			query += ` AND version = ?`
			args = append(args, expectedVersion)
		}
		update, err := conn.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("update asset list item: %w", err)
		}
		affected, err := update.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			if _, err := loadAssetListItemByID(ctx, conn, ownerID, listID, itemID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return assetlist.ErrNotFound
				}
				return err
			}
			return assetlist.ErrConflict
		}
		stored, err = loadAssetListItemByID(ctx, conn, ownerID, listID, itemID)
		return err
	})
	if err != nil {
		return assetlist.Item{}, err
	}
	return stored, nil
}

func (r *AssetListRunRepository) ItemByID(ctx context.Context, ownerID, listID, itemID string) (assetlist.Item, error) {
	item, err := loadAssetListItemByID(ctx, r.db, ownerID, listID, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	if err != nil {
		return assetlist.Item{}, fmt.Errorf("read asset list item: %w", err)
	}
	return item, nil
}

func (r *AssetListRunRepository) ItemsByList(ctx context.Context, ownerID, listID string) ([]assetlist.Item, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+assetListItemColumns+` FROM asset_list_items
		WHERE owner_id = ? AND list_id = ? ORDER BY position`, ownerID, listID)
	if err != nil {
		return nil, fmt.Errorf("list asset list items for run: %w", err)
	}
	defer rows.Close()
	result := make([]assetlist.Item, 0)
	for rows.Next() {
		item, err := scanAssetListItem(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ItemBySession finds the item one sub-session runs (#328). The session id is
// server-written by the runner, so at most one item matches; the owner filter
// keeps a foreign session from being attributed to the caller's item.
func (r *AssetListRunRepository) ItemBySession(
	ctx context.Context,
	ownerID, sessionID string,
) (assetlist.Item, bool, error) {
	item, err := scanAssetListItem(r.db.QueryRowContext(ctx, `
		SELECT `+assetListItemColumns+` FROM asset_list_items
		WHERE owner_id = ? AND session_id = ? LIMIT 1`, ownerID, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return assetlist.Item{}, false, nil
	}
	if err != nil {
		return assetlist.Item{}, false, fmt.Errorf("read asset list item by session: %w", err)
	}
	return item, true, nil
}

func loadAssetListItemByID(ctx context.Context, queryer assetListQueryer, ownerID, listID, itemID string) (assetlist.Item, error) {
	return scanAssetListItem(queryer.QueryRowContext(ctx, `
		SELECT `+assetListItemColumns+` FROM asset_list_items
		WHERE id = ? AND owner_id = ? AND list_id = ?`, itemID, ownerID, listID))
}

func scanAssetListRun(scanner interface{ Scan(...any) error }) (assetlist.Run, error) {
	var value assetlist.Run
	var created, updated string
	if err := scanner.Scan(
		&value.ID, &value.OwnerID, &value.ProjectID, &value.ListID,
		&value.Status, &value.PauseReason, &created, &updated,
	); err != nil {
		return assetlist.Run{}, err
	}
	value.CreatedAt, _ = parseTime(created)
	value.UpdatedAt, _ = parseTime(updated)
	return value, nil
}

func loadAssetListRunByList(ctx context.Context, queryer assetListQueryer, ownerID, projectID, listID string) (assetlist.Run, error) {
	run, err := scanAssetListRun(queryer.QueryRowContext(ctx, `
		SELECT `+assetListRunColumns+` FROM asset_list_runs
		WHERE owner_id = ? AND project_id = ? AND list_id = ?`,
		ownerID, projectID, listID))
	if errors.Is(err, sql.ErrNoRows) {
		return assetlist.Run{}, assetlist.ErrNotFound
	}
	return run, err
}

func loadAssetListRunByListID(ctx context.Context, queryer assetListQueryer, ownerID, listID string) (assetlist.Run, error) {
	run, err := scanAssetListRun(queryer.QueryRowContext(ctx, `
		SELECT `+assetListRunColumns+` FROM asset_list_runs
		WHERE owner_id = ? AND list_id = ?`, ownerID, listID))
	if errors.Is(err, sql.ErrNoRows) {
		return assetlist.Run{}, assetlist.ErrNotFound
	}
	return run, err
}

// Upsert stores one tab heartbeat. Present is idempotent per tab; an existing
// row only moves last_seen_at forward.
func (r *AssetListPresenceRepository) Upsert(ctx context.Context, ownerID, projectID, tabID string, at time.Time) error {
	if ownerID == "" || projectID == "" || tabID == "" {
		return fmt.Errorf("%w: presence identity", assetlist.ErrInvalid)
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_list_presence (owner_id, project_id, tab_id, last_seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(owner_id, project_id, tab_id) DO UPDATE SET last_seen_at = excluded.last_seen_at`,
		ownerID, projectID, tabID, formatTaskTime(at)); err != nil {
		return fmt.Errorf("upsert asset list presence: %w", err)
	}
	return nil
}

// Leave removes that tab's row. A second leave is a no-op.
func (r *AssetListPresenceRepository) Leave(ctx context.Context, ownerID, projectID, tabID string) error {
	if ownerID == "" || projectID == "" || tabID == "" {
		return fmt.Errorf("%w: presence identity", assetlist.ErrInvalid)
	}
	if _, err := r.db.ExecContext(ctx, `
		DELETE FROM asset_list_presence
		WHERE owner_id = ? AND project_id = ? AND tab_id = ?`,
		ownerID, projectID, tabID); err != nil {
		return fmt.Errorf("delete asset list presence: %w", err)
	}
	return nil
}

// Active reports whether any tab of the owner's project was seen at or after
// since (the caller passes now minus the grace window).
func (r *AssetListPresenceRepository) Active(ctx context.Context, ownerID, projectID string, since time.Time) (bool, error) {
	if ownerID == "" || projectID == "" {
		return false, fmt.Errorf("%w: presence identity", assetlist.ErrInvalid)
	}
	var seen int
	err := r.db.QueryRowContext(ctx, `
		SELECT 1 FROM asset_list_presence
		WHERE owner_id = ? AND project_id = ? AND last_seen_at >= ?
		LIMIT 1`, ownerID, projectID, formatTaskTime(since)).Scan(&seen)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read asset list presence: %w", err)
	}
	return true, nil
}

// Prune deletes every heartbeat last seen before the instant and reports how
// many rows went away.
func (r *AssetListPresenceRepository) Prune(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM asset_list_presence WHERE last_seen_at < ?`, formatTaskTime(before))
	if err != nil {
		return 0, fmt.Errorf("prune asset list presence: %w", err)
	}
	return result.RowsAffected()
}
