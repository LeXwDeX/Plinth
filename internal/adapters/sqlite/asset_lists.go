package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

// AssetListRepository is the owner-scoped SQLite adapter for production lists
// (#327). Every read filters owner_id and (except the request_key replay)
// project_id, so a foreign row is ErrNotFound. Every write runs in one
// BEGIN IMMEDIATE transaction: the state, TTL, version and single-confirmed
// checks must be atomic with the update they justify.
type AssetListRepository struct {
	db *sql.DB
}

func (r *Repository) AssetListRepository() *AssetListRepository {
	return &AssetListRepository{db: r.db}
}

const assetListColumns = `id, request_key, owner_id, project_id, COALESCE(source_session_id, ''),
	origin_message_id, title, status, version, brief_json, decided_at, decided_by,
	decision_reason, superseded_by, created_at, updated_at, expires_at`

const assetListItemColumns = `id, list_id, owner_id, kind, name, description, references_json,
	route_id, route_json, planned_step_count, planned_model3d_step_count, state,
	state_reason, version, COALESCE(session_id, ''), project_asset_id, round,
	last_turn_message_id, next_action_at, block, step_cap_turn_message_id,
	result_json, review_json, accepted_at, return_reason, return_note,
	return_count, work_round,
	created_at, updated_at`

type assetListQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (r *AssetListRepository) withWrite(ctx context.Context, fn func(*sql.Conn) error) error {
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
		// Expiring a draft is a successful write that still reports
		// ErrExpired: commit the recorded status, then surface the error.
		var keep *commitError
		if errors.As(err, &keep) {
			if _, commitErr := conn.ExecContext(ctx, "COMMIT"); commitErr != nil {
				return commitErr
			}
			committed = true
			return keep.err
		}
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// commitError asks withWrite to commit and then report err.
type commitError struct {
	err error
}

func (e *commitError) Error() string { return e.err.Error() }

func (e *commitError) Unwrap() error { return e.err }

func (r *AssetListRepository) ProjectExists(ctx context.Context, ownerID, projectID string) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM projects WHERE id = ? AND owner_id = ?`, projectID, ownerID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read project for asset list: %w", err)
	}
	return true, nil
}

func (r *AssetListRepository) SessionProject(ctx context.Context, ownerID, sessionID string) (string, error) {
	var projectID string
	err := r.db.QueryRowContext(ctx,
		`SELECT project_id FROM go_chat_sessions WHERE id = ? AND owner_id = ?`, sessionID, ownerID).
		Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", assetlist.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read session project: %w", err)
	}
	return projectID, nil
}

func (r *AssetListRepository) FindByRequestKey(ctx context.Context, ownerID, requestKey string) (assetlist.List, bool, error) {
	return findAssetListByRequestKey(ctx, r.db, ownerID, requestKey)
}

func (r *AssetListRepository) ListLists(ctx context.Context, ownerID, projectID string) ([]assetlist.List, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+assetListColumns+`,
		       COALESCE((SELECT COUNT(*) FROM asset_list_items i
		                 WHERE i.list_id = asset_lists.id), 0),
		       COALESCE((SELECT SUM(i.planned_step_count) FROM asset_list_items i
		                 WHERE i.list_id = asset_lists.id), 0),
		       COALESCE((SELECT SUM(i.planned_model3d_step_count) FROM asset_list_items i
		                 WHERE i.list_id = asset_lists.id), 0)
		FROM asset_lists
		WHERE owner_id = ? AND project_id = ?
		ORDER BY updated_at DESC, id DESC`, ownerID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list asset lists: %w", err)
	}
	defer rows.Close()
	result := make([]assetlist.List, 0)
	for rows.Next() {
		value, err := scanAssetListSummary(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *AssetListRepository) GetList(ctx context.Context, ownerID, projectID, listID string) (assetlist.List, error) {
	return loadAssetList(ctx, r.db, ownerID, projectID, listID)
}

// CountLiveLists counts the project's draft+confirmed lists. Terminal lists
// (rejected/expired/superseded) do not consume the project quota.
func (r *AssetListRepository) CountLiveLists(ctx context.Context, ownerID, projectID string) (int, error) {
	return countLiveAssetLists(ctx, r.db, ownerID, projectID)
}

func countLiveAssetLists(ctx context.Context, queryer assetListQueryer, ownerID, projectID string) (int, error) {
	var count int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM asset_lists
		WHERE owner_id = ? AND project_id = ? AND status IN (?, ?)`,
		ownerID, projectID, assetlist.StatusDraft, assetlist.StatusConfirmed).Scan(&count); err != nil {
		return 0, fmt.Errorf("count live asset lists: %w", err)
	}
	return count, nil
}

func (r *AssetListRepository) Propose(ctx context.Context, list assetlist.List, items []assetlist.Item, maximum int) (assetlist.List, error) {
	var stored assetlist.List
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		existing, found, err := findAssetListByRequestKey(ctx, conn, list.OwnerID, list.RequestKey)
		if err != nil {
			return err
		}
		if found {
			stored = existing
			return nil
		}
		var exists int
		err = conn.QueryRowContext(ctx,
			`SELECT 1 FROM projects WHERE id = ? AND owner_id = ?`, list.ProjectID, list.OwnerID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return assetlist.ErrProjectNotFound
		}
		if err != nil {
			return fmt.Errorf("read project for asset list: %w", err)
		}
		// The quota counts live lists before this proposal supersedes anything:
		// a project at the limit must first let old drafts reach a terminal
		// status (confirm/reject), it cannot keep replacing drafts forever. The
		// ErrLimit rollback leaves the existing drafts untouched.
		if maximum > 0 {
			live, err := countLiveAssetLists(ctx, conn, list.OwnerID, list.ProjectID)
			if err != nil {
				return err
			}
			if live >= maximum {
				return assetlist.ErrLimit
			}
		}
		// A new draft supersedes every other draft of the project; a confirmed
		// list is untouched and does not block a proposal.
		if _, err := conn.ExecContext(ctx, `
			UPDATE asset_lists SET status = ?, superseded_by = ?, updated_at = ?
			WHERE owner_id = ? AND project_id = ? AND status = ?`,
			assetlist.StatusSuperseded, list.ID, formatTime(list.UpdatedAt),
			list.OwnerID, list.ProjectID, assetlist.StatusDraft); err != nil {
			return fmt.Errorf("supersede draft asset lists: %w", err)
		}
		if err := insertAssetList(ctx, conn, list); err != nil {
			return err
		}
		for position, item := range items {
			if err := insertAssetListItem(ctx, conn, list.ID, position, item); err != nil {
				return err
			}
		}
		stored, err = loadAssetList(ctx, conn, list.OwnerID, list.ProjectID, list.ID)
		return err
	})
	if err != nil {
		return assetlist.List{}, err
	}
	return stored, nil
}

func (r *AssetListRepository) EditList(ctx context.Context, list assetlist.List, items []assetlist.Item, expectedVersion int64) (assetlist.List, error) {
	var stored assetlist.List
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		current, err := scanAssetList(conn.QueryRowContext(ctx, `
			SELECT `+assetListColumns+` FROM asset_lists
			WHERE id = ? AND owner_id = ? AND project_id = ?`,
			list.ID, list.OwnerID, list.ProjectID))
		if errors.Is(err, sql.ErrNoRows) {
			return assetlist.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read asset list for edit: %w", err)
		}
		if err := assetListDraftState(ctx, conn, current, list.UpdatedAt); err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return assetlist.ErrConflict
		}
		result, err := conn.ExecContext(ctx, `
			UPDATE asset_lists SET title = ?, brief_json = ?, version = version + 1, updated_at = ?
			WHERE id = ? AND owner_id = ? AND project_id = ? AND status = ? AND version = ?`,
			list.Title, string(list.Brief), formatTime(list.UpdatedAt),
			list.ID, list.OwnerID, list.ProjectID, assetlist.StatusDraft, expectedVersion)
		if err != nil {
			return fmt.Errorf("edit asset list: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			// A concurrent write changed the row between the read and the CAS.
			latest, err := scanAssetList(conn.QueryRowContext(ctx, `
				SELECT `+assetListColumns+` FROM asset_lists
				WHERE id = ? AND owner_id = ? AND project_id = ?`,
				list.ID, list.OwnerID, list.ProjectID))
			if errors.Is(err, sql.ErrNoRows) {
				return assetlist.ErrNotFound
			}
			if err != nil {
				return err
			}
			if err := assetListDraftState(ctx, conn, latest, list.UpdatedAt); err != nil {
				return err
			}
			return assetlist.ErrConflict
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM asset_list_items WHERE list_id = ?`, list.ID); err != nil {
			return fmt.Errorf("replace asset list items: %w", err)
		}
		for position, item := range items {
			if err := insertAssetListItem(ctx, conn, list.ID, position, item); err != nil {
				return err
			}
		}
		stored, err = loadAssetList(ctx, conn, list.OwnerID, list.ProjectID, list.ID)
		return err
	})
	if err != nil {
		return assetlist.List{}, err
	}
	return stored, nil
}

func (r *AssetListRepository) Decide(ctx context.Context, decision assetlist.Decision) (assetlist.List, error) {
	var stored assetlist.List
	err := r.withWrite(ctx, func(conn *sql.Conn) error {
		current, err := scanAssetList(conn.QueryRowContext(ctx, `
			SELECT `+assetListColumns+` FROM asset_lists
			WHERE id = ? AND owner_id = ? AND project_id = ?`,
			decision.ListID, decision.OwnerID, decision.ProjectID))
		if errors.Is(err, sql.ErrNoRows) {
			return assetlist.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read asset list for decision: %w", err)
		}
		if current.Status == assetlist.StatusExpired {
			return assetlist.ErrExpired
		}
		if assetListExpired(current, decision.At) {
			if err := expireAssetList(ctx, conn, current, decision.At); err != nil {
				return err
			}
			return &commitError{err: assetlist.ErrExpired}
		}
		// Repeating the same decision is a no-op: a double click confirms or
		// rejects once. Every other terminal or foreign status is a conflict.
		if repeatedDecision(current, decision.Action) {
			stored, err = loadAssetList(ctx, conn, decision.OwnerID, decision.ProjectID, decision.ListID)
			return err
		}
		if current.Status != assetlist.StatusDraft {
			return assetlist.ErrConflict
		}
		now := formatTime(decision.At)
		if decision.Action == assetlist.DecisionReject {
			if _, err := conn.ExecContext(ctx, `
				UPDATE asset_lists SET status = ?, decided_at = ?, decided_by = ?,
					decision_reason = ?, version = version + 1, updated_at = ?
				WHERE id = ? AND owner_id = ? AND project_id = ? AND status = ?`,
				assetlist.StatusRejected, now, decision.OwnerID, decision.Reason, now,
				decision.ListID, decision.OwnerID, decision.ProjectID, assetlist.StatusDraft); err != nil {
				return fmt.Errorf("reject asset list: %w", err)
			}
			stored, err = loadAssetList(ctx, conn, decision.OwnerID, decision.ProjectID, decision.ListID)
			return err
		}
		confirmed, found, err := findConfirmedAssetList(ctx, conn, decision.OwnerID, decision.ProjectID, decision.ListID)
		if err != nil {
			return err
		}
		if found {
			if decision.Action == assetlist.DecisionConfirm {
				return &assetlist.ErrActiveConflict{ListID: confirmed.ID, Title: confirmed.Title}
			}
			working, err := workingAssetListItemCount(ctx, conn, confirmed.ID)
			if err != nil {
				return err
			}
			if working > 0 {
				return &assetlist.ErrActiveWorking{ListID: confirmed.ID}
			}
			if _, err := conn.ExecContext(ctx, `
				UPDATE asset_lists SET status = ?, superseded_by = ?,
					version = version + 1, updated_at = ?
				WHERE id = ? AND owner_id = ? AND project_id = ? AND status = ?`,
				assetlist.StatusSuperseded, decision.ListID, now,
				confirmed.ID, decision.OwnerID, decision.ProjectID, assetlist.StatusConfirmed); err != nil {
				return fmt.Errorf("supersede confirmed asset list: %w", err)
			}
		}
		if _, err := conn.ExecContext(ctx, `
			UPDATE asset_lists SET status = ?, decided_at = ?, decided_by = ?,
				decision_reason = ?, version = version + 1, updated_at = ?
			WHERE id = ? AND owner_id = ? AND project_id = ? AND status = ?`,
			assetlist.StatusConfirmed, now, decision.OwnerID, decision.Reason, now,
			decision.ListID, decision.OwnerID, decision.ProjectID, assetlist.StatusDraft); err != nil {
			return fmt.Errorf("confirm asset list: %w", err)
		}
		stored, err = loadAssetList(ctx, conn, decision.OwnerID, decision.ProjectID, decision.ListID)
		return err
	})
	if err != nil {
		return assetlist.List{}, err
	}
	return stored, nil
}

func repeatedDecision(list assetlist.List, action string) bool {
	switch action {
	case assetlist.DecisionConfirm, assetlist.DecisionReplace:
		return list.Status == assetlist.StatusConfirmed
	case assetlist.DecisionReject:
		return list.Status == assetlist.StatusRejected
	}
	return false
}

func assetListExpired(list assetlist.List, at time.Time) bool {
	return list.Status == assetlist.StatusDraft && !list.ExpiresAt.IsZero() && !at.Before(list.ExpiresAt)
}

// assetListDraftState turns a non-draft list into the matching workflow error
// and records the expiry of an expired draft inside the caller's transaction.
func assetListDraftState(ctx context.Context, conn *sql.Conn, list assetlist.List, at time.Time) error {
	if list.Status == assetlist.StatusExpired {
		return assetlist.ErrExpired
	}
	if assetListExpired(list, at) {
		if err := expireAssetList(ctx, conn, list, at); err != nil {
			return err
		}
		return &commitError{err: assetlist.ErrExpired}
	}
	if list.Status != assetlist.StatusDraft {
		return assetlist.ErrConflict
	}
	return nil
}

func expireAssetList(ctx context.Context, conn *sql.Conn, list assetlist.List, at time.Time) error {
	if _, err := conn.ExecContext(ctx, `
		UPDATE asset_lists SET status = ?, updated_at = ?
		WHERE id = ? AND owner_id = ? AND project_id = ? AND status = ?`,
		assetlist.StatusExpired, formatTime(at),
		list.ID, list.OwnerID, list.ProjectID, assetlist.StatusDraft); err != nil {
		return fmt.Errorf("expire asset list: %w", err)
	}
	return nil
}

func insertAssetList(ctx context.Context, conn *sql.Conn, list assetlist.List) error {
	brief := list.Brief
	if len(brief) == 0 {
		brief = json.RawMessage("{}")
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO asset_lists (id, request_key, owner_id, project_id, source_session_id,
			origin_message_id, title, status, version, brief_json, decided_at, decided_by,
			decision_reason, superseded_by, created_at, updated_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		list.ID, list.RequestKey, list.OwnerID, list.ProjectID, nullString(list.SourceSessionID),
		list.OriginMessageID, list.Title, list.Status, list.Version, string(brief),
		nullAssetListTime(list.DecidedAt), list.DecidedBy, list.DecisionReason, list.SupersededBy,
		formatTime(list.CreatedAt), formatTime(list.UpdatedAt), formatTime(list.ExpiresAt)); err != nil {
		return fmt.Errorf("insert asset list: %w", err)
	}
	return nil
}

func insertAssetListItem(ctx context.Context, conn *sql.Conn, listID string, position int, item assetlist.Item) error {
	references, err := encodeProjectReferences(item.References)
	if err != nil {
		return err
	}
	route := item.Route
	if route == nil {
		route = []assetlist.RouteStep{}
	}
	routeJSON, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("encode asset list route: %w", err)
	}
	result := item.Result
	if len(result) == 0 {
		result = json.RawMessage("{}")
	}
	review := item.Review
	if len(review) == 0 {
		review = json.RawMessage("{}")
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO asset_list_items (id, owner_id, list_id, position, kind, name, description,
			references_json, route_id, route_json, planned_step_count, planned_model3d_step_count,
			state, state_reason, version, session_id, project_asset_id, round,
			last_turn_message_id, next_action_at, block, result_json, review_json,
			accepted_at, return_reason, return_note, return_count, work_round,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.OwnerID, listID, position, item.Kind, item.Name, item.Description,
		references, item.RouteID, string(routeJSON), item.PlannedStepCount,
		item.PlannedModel3DStepCount, item.State, item.StateReason, item.Version,
		nullString(item.SessionID), item.ProjectAssetID, item.Round,
		item.LastTurnMessageID, formatTaskTimeOrEmpty(item.NextActionAt), item.Block,
		string(result), string(review), formatOptionalListTime(item.AcceptedAt),
		item.ReturnReason, item.ReturnNote, item.ReturnCount, item.WorkRound,
		formatTime(item.CreatedAt), formatTime(item.UpdatedAt)); err != nil {
		return fmt.Errorf("insert asset list item: %w", err)
	}
	return nil
}

func nullAssetListTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

// formatOptionalListTime stores a non-null marker column: the empty string is
// the "not happened" value, matching next_action_at and the other item times.
func formatOptionalListTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatTime(*value)
}

func loadAssetList(ctx context.Context, queryer assetListQueryer, ownerID, projectID, listID string) (assetlist.List, error) {
	list, err := scanAssetList(queryer.QueryRowContext(ctx, `
		SELECT `+assetListColumns+` FROM asset_lists
		WHERE id = ? AND owner_id = ? AND project_id = ?`,
		listID, ownerID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return assetlist.List{}, assetlist.ErrNotFound
	}
	if err != nil {
		return assetlist.List{}, fmt.Errorf("read asset list: %w", err)
	}
	items, err := listAssetListItems(ctx, queryer, list.ID)
	if err != nil {
		return assetlist.List{}, err
	}
	list.Items = items
	sumAssetListItems(&list)
	return list, nil
}

func findAssetListByRequestKey(ctx context.Context, queryer assetListQueryer, ownerID, requestKey string) (assetlist.List, bool, error) {
	list, err := scanAssetList(queryer.QueryRowContext(ctx,
		`SELECT `+assetListColumns+` FROM asset_lists WHERE owner_id = ? AND request_key = ?`,
		ownerID, requestKey))
	if errors.Is(err, sql.ErrNoRows) {
		return assetlist.List{}, false, nil
	}
	if err != nil {
		return assetlist.List{}, false, fmt.Errorf("read asset list by request key: %w", err)
	}
	items, err := listAssetListItems(ctx, queryer, list.ID)
	if err != nil {
		return assetlist.List{}, false, err
	}
	list.Items = items
	sumAssetListItems(&list)
	return list, true, nil
}

func findConfirmedAssetList(ctx context.Context, queryer assetListQueryer, ownerID, projectID, exceptID string) (assetlist.List, bool, error) {
	list, err := scanAssetList(queryer.QueryRowContext(ctx, `
		SELECT `+assetListColumns+` FROM asset_lists
		WHERE owner_id = ? AND project_id = ? AND status = ? AND id <> ?`,
		ownerID, projectID, assetlist.StatusConfirmed, exceptID))
	if errors.Is(err, sql.ErrNoRows) {
		return assetlist.List{}, false, nil
	}
	if err != nil {
		return assetlist.List{}, false, fmt.Errorf("read confirmed asset list: %w", err)
	}
	return list, true, nil
}

func workingAssetListItemCount(ctx context.Context, queryer assetListQueryer, listID string) (int, error) {
	var count int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM asset_list_items WHERE list_id = ? AND state = ?`,
		listID, assetlist.StateWorking).Scan(&count); err != nil {
		return 0, fmt.Errorf("count working asset list items: %w", err)
	}
	return count, nil
}

func listAssetListItems(ctx context.Context, queryer assetListQueryer, listID string) ([]assetlist.Item, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT `+assetListItemColumns+` FROM asset_list_items
		WHERE list_id = ? ORDER BY position`, listID)
	if err != nil {
		return nil, fmt.Errorf("list asset list items: %w", err)
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

func sumAssetListItems(list *assetlist.List) {
	list.ItemCount = len(list.Items)
	steps, model3dSteps := 0, 0
	for _, item := range list.Items {
		steps += item.PlannedStepCount
		model3dSteps += item.PlannedModel3DStepCount
	}
	list.PlannedStepCount = steps
	list.PlannedModel3DStepCount = model3dSteps
}

func scanAssetList(scanner interface{ Scan(...any) error }) (assetlist.List, error) {
	var value assetlist.List
	var brief string
	var decidedAt sql.NullString
	var created, updated, expires string
	targets := assetListScanTargets(&value, &brief, &decidedAt, &created, &updated, &expires)
	if err := scanner.Scan(targets...); err != nil {
		return assetlist.List{}, err
	}
	finishAssetList(&value, brief, decidedAt, created, updated, expires)
	return value, nil
}

func scanAssetListSummary(scanner interface{ Scan(...any) error }) (assetlist.List, error) {
	var value assetlist.List
	var brief string
	var decidedAt sql.NullString
	var created, updated, expires string
	targets := assetListScanTargets(&value, &brief, &decidedAt, &created, &updated, &expires)
	targets = append(targets, &value.ItemCount, &value.PlannedStepCount, &value.PlannedModel3DStepCount)
	if err := scanner.Scan(targets...); err != nil {
		return assetlist.List{}, err
	}
	finishAssetList(&value, brief, decidedAt, created, updated, expires)
	return value, nil
}

func assetListScanTargets(
	value *assetlist.List,
	brief *string,
	decidedAt *sql.NullString,
	created, updated, expires *string,
) []any {
	return []any{
		&value.ID, &value.RequestKey, &value.OwnerID, &value.ProjectID, &value.SourceSessionID,
		&value.OriginMessageID, &value.Title, &value.Status, &value.Version, brief,
		decidedAt, &value.DecidedBy, &value.DecisionReason, &value.SupersededBy,
		created, updated, expires,
	}
}

func finishAssetList(
	value *assetlist.List,
	brief string,
	decidedAt sql.NullString,
	created, updated, expires string,
) {
	if brief == "" {
		brief = "{}"
	}
	value.Brief = json.RawMessage(brief)
	if decidedAt.Valid {
		if parsed, err := parseTime(decidedAt.String); err == nil {
			value.DecidedAt = &parsed
		}
	}
	value.CreatedAt, _ = parseTime(created)
	value.UpdatedAt, _ = parseTime(updated)
	value.ExpiresAt, _ = parseTime(expires)
}

func scanAssetListItem(scanner interface{ Scan(...any) error }) (assetlist.Item, error) {
	var value assetlist.Item
	var references, route, result, review, acceptedAt, nextActionAt, created, updated string
	if err := scanner.Scan(
		&value.ID, &value.ListID, &value.OwnerID, &value.Kind, &value.Name, &value.Description,
		&references, &value.RouteID, &route, &value.PlannedStepCount, &value.PlannedModel3DStepCount,
		&value.State, &value.StateReason, &value.Version, &value.SessionID, &value.ProjectAssetID,
		&value.Round, &value.LastTurnMessageID, &nextActionAt, &value.Block, &value.StepCapTurnMessageID,
		&result, &review, &acceptedAt, &value.ReturnReason, &value.ReturnNote,
		&value.ReturnCount, &value.WorkRound,
		&created, &updated,
	); err != nil {
		return assetlist.Item{}, err
	}
	value.References = decodeProjectReferences(references)
	value.Route = decodeAssetListRoute(route)
	if nextActionAt != "" {
		value.NextActionAt, _ = parseTime(nextActionAt)
	}
	if acceptedAt != "" {
		if parsed, err := parseTime(acceptedAt); err == nil {
			value.AcceptedAt = &parsed
		}
	}
	if result == "" {
		result = "{}"
	}
	if review == "" {
		review = "{}"
	}
	value.Result = json.RawMessage(result)
	value.Review = json.RawMessage(review)
	value.CreatedAt, _ = parseTime(created)
	value.UpdatedAt, _ = parseTime(updated)
	return value, nil
}

func decodeAssetListRoute(raw string) []assetlist.RouteStep {
	var steps []assetlist.RouteStep
	if json.Unmarshal([]byte(raw), &steps) != nil || steps == nil {
		return []assetlist.RouteStep{}
	}
	return steps
}
