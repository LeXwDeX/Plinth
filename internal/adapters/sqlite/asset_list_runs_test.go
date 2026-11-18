package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

func newAssetListRunRepositories(t *testing.T) (*Repository, *AssetListRepository, *AssetListRunRepository, *AssetListPresenceRepository) {
	t.Helper()
	repository, lists := newAssetListRepository(t)
	return repository, lists, repository.AssetListRunRepository(), repository.AssetListPresenceRepository()
}

func newAssetListRun(id, ownerID, projectID, listID, status string) assetlist.Run {
	return assetlist.Run{
		ID: id, OwnerID: ownerID, ProjectID: projectID, ListID: listID,
		Status: status, CreatedAt: assetListTestNow, UpdatedAt: assetListTestNow,
	}
}

// seedRunList proposes one list with count todo items named aitem_<list>_<n>.
func seedRunList(t *testing.T, lists *AssetListRepository, listID string, count int) []assetlist.Item {
	t.Helper()
	items := make([]assetlist.Item, 0, count)
	for index := 1; index <= count; index++ {
		items = append(items, newAssetListItem(
			"owner-a", fmt.Sprintf("aitem_%s_%d", listID, index), fmt.Sprintf("Item %d", index)))
	}
	proposeAssetList(t, lists, newAssetList(listID, "owner-a", "prj_1", "ses_1", "req_"+listID), items...)
	return items
}

func TestAssetListRunsMigrationCreatesTablesColumnsIndexesAndChecks(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	for _, table := range []string{"asset_list_runs", "asset_list_presence"} {
		var name string
		err := repository.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil || name != table {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	for _, index := range []string{
		"asset_list_runs_owner_status",
		"asset_list_runs_project_status",
		"asset_list_items_session",
		"asset_list_presence_project_seen",
	} {
		var name string
		err := repository.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name)
		if err != nil || name != index {
			t.Fatalf("index %s missing: %v", index, err)
		}
	}
	var partial string
	if err := repository.db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'asset_list_items_session'`).
		Scan(&partial); err != nil {
		t.Fatalf("read partial item session index: %v", err)
	}
	if !strings.Contains(partial, "WHERE session_id IS NOT NULL") {
		t.Fatalf("item session index is not partial: %s", partial)
	}
	for _, column := range []string{"last_turn_message_id", "next_action_at", "block"} {
		var (
			name    string
			notNull int
			dflt    string
		)
		err := repository.db.QueryRow(
			`SELECT name, "notnull", COALESCE(dflt_value, '') FROM pragma_table_info('asset_list_items')
			 WHERE name = ?`, column).Scan(&name, &notNull, &dflt)
		if err != nil || name != column || notNull != 1 || dflt != `''` {
			t.Fatalf("column asset_list_items.%s = %q/%d/%q, %v; want NOT NULL DEFAULT ''",
				column, name, notNull, dflt, err)
		}
	}

	item := seedRunList(t, lists, "alist_1", 1)[0]
	at := formatTime(assetListTestNow)
	invalidStatements := []struct {
		label string
		query string
		args  []any
	}{
		{"run status", `INSERT INTO asset_list_runs
			(id, owner_id, project_id, list_id, status, created_at, updated_at)
			VALUES ('run_bad', 'owner-a', 'prj_1', ?, 'bogus', ?, ?)`, []any{"alist_1", at, at}},
		{"pause reason", `INSERT INTO asset_list_runs
			(id, owner_id, project_id, list_id, status, pause_reason, created_at, updated_at)
			VALUES ('run_bad', 'owner-a', 'prj_1', ?, 'paused', 'bogus', ?, ?)`, []any{"alist_1", at, at}},
		{"item block", `UPDATE asset_list_items SET block = 'bogus' WHERE id = ?`, []any{item.ID}},
	}
	for _, statement := range invalidStatements {
		if _, err := repository.db.Exec(statement.query, statement.args...); err == nil {
			t.Fatalf("invalid %s must violate its CHECK constraint", statement.label)
		}
	}
}

func TestAssetListRunRepositoryCreateRunIsUniquePerList(t *testing.T) {
	_, lists, runs, _ := newAssetListRunRepositories(t)
	ctx := context.Background()
	seedRunList(t, lists, "alist_1", 1)

	stored, err := runs.CreateRun(ctx, newAssetListRun("run_1", "owner-a", "prj_1", "alist_1", assetlist.RunRunning))
	if err != nil || stored.ID != "run_1" || stored.Status != assetlist.RunRunning || !stored.Active() {
		t.Fatalf("created run = %+v, %v", stored, err)
	}
	replayed, err := runs.CreateRun(ctx, newAssetListRun("run_2", "owner-a", "prj_1", "alist_1", assetlist.RunIdle))
	if err != nil || replayed.ID != "run_1" || replayed.Status != assetlist.RunRunning {
		t.Fatalf("replayed run = %+v, %v; want the stored running run", replayed, err)
	}
	var count int
	if err := runs.db.QueryRow(`SELECT COUNT(*) FROM asset_list_runs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("runs after replay = %d, %v; want 1", count, err)
	}

	if _, err := runs.GetRunByList(ctx, "owner-b", "prj_1", "alist_1"); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign owner run = %v; want ErrNotFound", err)
	}
	if _, err := runs.GetRunByList(ctx, "owner-a", "prj_2", "alist_1"); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign project run = %v; want ErrNotFound", err)
	}
	if _, err := runs.CreateRun(ctx, newAssetListRun("run_3", "owner-a", "prj_1", "alist_missing", assetlist.RunIdle)); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("run for missing list = %v; want ErrNotFound", err)
	}
	if _, err := runs.CreateRun(ctx, assetlist.Run{}); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("run without identity = %v; want ErrInvalid", err)
	}

	running, err := runs.RunsByStatus(ctx, assetlist.RunRunning)
	if err != nil || len(running) != 1 || running[0].ID != "run_1" {
		t.Fatalf("running runs = %+v, %v", running, err)
	}
	if idle, err := runs.RunsByStatus(ctx, assetlist.RunIdle); err != nil || len(idle) != 0 {
		t.Fatalf("idle runs = %+v, %v", idle, err)
	}
	if _, err := runs.RunsByStatus(ctx, "bogus"); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("bogus status scan = %v; want ErrInvalid", err)
	}
}

func TestAssetListRunRepositoryClaimItemClaimsInPositionOrder(t *testing.T) {
	_, lists, runs, _ := newAssetListRunRepositories(t)
	ctx := context.Background()
	seedRunList(t, lists, "alist_1", 3)
	if _, err := runs.CreateRun(ctx, newAssetListRun("run_1", "owner-a", "prj_1", "alist_1", assetlist.RunRunning)); err != nil {
		t.Fatalf("create run: %v", err)
	}

	for index := 1; index <= 3; index++ {
		item, found, err := runs.ClaimItem(ctx, "owner-a", "alist_1", assetListTestNow)
		if err != nil || !found {
			t.Fatalf("claim %d = %+v, %v, %v", index, item, found, err)
		}
		wantID := fmt.Sprintf("aitem_alist_1_%d", index)
		if item.ID != wantID || item.State != assetlist.StateWorking ||
			item.Round != 1 || item.Version != 1 || !item.NextActionAt.IsZero() {
			t.Fatalf("claim %d = %+v; want %s working round 1 version 1", index, item, wantID)
		}
	}
	if item, found, err := runs.ClaimItem(ctx, "owner-a", "alist_1", assetListTestNow); err != nil || found {
		t.Fatalf("claim after all items = %+v, %v, %v; want none", item, found, err)
	}
	items, err := runs.ItemsByList(ctx, "owner-a", "alist_1")
	if err != nil || len(items) != 3 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	for _, item := range items {
		if item.State != assetlist.StateWorking || item.Round != 1 {
			t.Fatalf("claimed item = %+v; want working round 1", item)
		}
	}

	if _, _, err := runs.ClaimItem(ctx, "owner-a", "alist_missing", assetListTestNow); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("claim without run = %v; want ErrNotFound", err)
	}
	seedRunList(t, lists, "alist_2", 1)
	paused := newAssetListRun("run_2", "owner-a", "prj_1", "alist_2", assetlist.RunPaused)
	paused.PauseReason = assetlist.PauseUser
	if _, err := runs.CreateRun(ctx, paused); err != nil {
		t.Fatalf("create paused run: %v", err)
	}
	if item, found, err := runs.ClaimItem(ctx, "owner-a", "alist_2", assetListTestNow); err != nil || found {
		t.Fatalf("claim on paused run = %+v, %v, %v; want none", item, found, err)
	}
}

func TestAssetListRunRepositoryClaimItemRaceClaimsOnce(t *testing.T) {
	_, lists, runs, _ := newAssetListRunRepositories(t)
	ctx := context.Background()
	seedRunList(t, lists, "alist_1", 1)
	if _, err := runs.CreateRun(ctx, newAssetListRun("run_1", "owner-a", "prj_1", "alist_1", assetlist.RunRunning)); err != nil {
		t.Fatalf("create run: %v", err)
	}

	type result struct {
		found bool
		err   error
	}
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, found, err := runs.ClaimItem(ctx, "owner-a", "alist_1", assetListTestNow)
			results <- result{found: found, err: err}
		}()
	}
	wait.Wait()
	close(results)
	claimed := 0
	for value := range results {
		if value.err != nil {
			t.Fatalf("concurrent claim: %v", value.err)
		}
		if value.found {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("concurrent claims = %d; want exactly 1", claimed)
	}
	items, err := runs.ItemsByList(ctx, "owner-a", "alist_1")
	if err != nil || len(items) != 1 || items[0].Round != 1 || items[0].Version != 1 {
		t.Fatalf("item after race = %+v, %v; want one round-1 claim", items, err)
	}
}

func TestAssetListRunRepositoryFinishItemGuardsWorkingState(t *testing.T) {
	_, lists, runs, _ := newAssetListRunRepositories(t)
	ctx := context.Background()
	seedRunList(t, lists, "alist_1", 3)
	if _, err := runs.CreateRun(ctx, newAssetListRun("run_1", "owner-a", "prj_1", "alist_1", assetlist.RunRunning)); err != nil {
		t.Fatalf("create run: %v", err)
	}
	first, found, err := runs.ClaimItem(ctx, "owner-a", "alist_1", assetListTestNow)
	if err != nil || !found {
		t.Fatalf("claim first: %v, %v", found, err)
	}

	finished, err := runs.FinishItem(ctx, assetlist.FinishItemInput{
		OwnerID: "owner-a", ListID: "alist_1", ItemID: first.ID,
		State: assetlist.StateReview, StateReason: "submitted",
		Result: json.RawMessage(`{"task_id":"task_1"}`), Review: json.RawMessage(`{}`),
		At: assetListTestNow.Add(time.Minute),
	})
	if err != nil || finished.State != assetlist.StateReview || finished.StateReason != "submitted" ||
		finished.Version != 2 || string(finished.Result) != `{"task_id":"task_1"}` {
		t.Fatalf("finished item = %+v, %v", finished, err)
	}
	if _, err := runs.FinishItem(ctx, assetlist.FinishItemInput{
		OwnerID: "owner-a", ListID: "alist_1", ItemID: first.ID, State: assetlist.StateReview,
		At: assetListTestNow.Add(2 * time.Minute),
	}); !errors.Is(err, assetlist.ErrItemState) {
		t.Fatalf("finish review item = %v; want ErrItemState", err)
	}
	if _, err := runs.FinishItem(ctx, assetlist.FinishItemInput{
		OwnerID: "owner-a", ListID: "alist_1", ItemID: "aitem_missing", State: assetlist.StateFailed,
		At: assetListTestNow,
	}); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("finish missing item = %v; want ErrNotFound", err)
	}
	if _, err := runs.FinishItem(ctx, assetlist.FinishItemInput{
		OwnerID: "owner-a", ListID: "alist_1", ItemID: first.ID, State: assetlist.StateTodo,
		At: assetListTestNow,
	}); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("finish to todo = %v; want ErrInvalid", err)
	}

	nextActionAt := assetListTestNow.Add(3 * time.Minute)
	second, found, err := runs.ClaimItem(ctx, "owner-a", "alist_1", assetListTestNow)
	if err != nil || !found {
		t.Fatalf("claim second: %v, %v", found, err)
	}
	failed, err := runs.FinishItem(ctx, assetlist.FinishItemInput{
		OwnerID: "owner-a", ListID: "alist_1", ItemID: second.ID,
		State: assetlist.StateFailed, StateReason: "upstream_refused",
		NextActionAt: nextActionAt, At: assetListTestNow.Add(time.Minute),
	})
	if err != nil || failed.State != assetlist.StateFailed ||
		failed.StateReason != "upstream_refused" || !failed.NextActionAt.Equal(nextActionAt) {
		t.Fatalf("failed item = %+v, %v", failed, err)
	}
	todo, err := runs.ItemByID(ctx, "owner-a", "alist_1", "aitem_alist_1_3")
	if err != nil || todo.State != assetlist.StateTodo {
		t.Fatalf("unclaimed todo = %+v, %v", todo, err)
	}
	if _, err := runs.FinishItem(ctx, assetlist.FinishItemInput{
		OwnerID: "owner-a", ListID: "alist_1", ItemID: todo.ID, State: assetlist.StateSkipped,
		At: assetListTestNow,
	}); !errors.Is(err, assetlist.ErrItemState) {
		t.Fatalf("finish todo item = %v; want ErrItemState", err)
	}
}

func TestAssetListRunRepositorySetItemFieldsAndVersionCAS(t *testing.T) {
	_, lists, runs, _ := newAssetListRunRepositories(t)
	ctx := context.Background()
	item := seedRunList(t, lists, "alist_1", 1)[0]

	blocked, err := runs.SetItemBlock(ctx, "owner-a", "alist_1", item.ID,
		assetlist.BlockAwaitingApproval, 0, assetListTestNow)
	if err != nil || blocked.Block != assetlist.BlockAwaitingApproval || blocked.Version != 1 {
		t.Fatalf("set block = %+v, %v", blocked, err)
	}
	if _, err := runs.SetItemBlock(ctx, "owner-a", "alist_1", item.ID,
		assetlist.BlockNone, 0, assetListTestNow); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale block CAS = %v; want ErrConflict", err)
	}
	unchecked, err := runs.SetItemBlock(ctx, "owner-a", "alist_1", item.ID,
		assetlist.BlockStepCap, assetlist.VersionUnchecked, assetListTestNow)
	if err != nil || unchecked.Block != assetlist.BlockStepCap || unchecked.Version != 2 {
		t.Fatalf("unchecked block = %+v, %v", unchecked, err)
	}
	if _, err := runs.SetItemBlock(ctx, "owner-a", "alist_1", item.ID,
		"bogus", assetlist.VersionUnchecked, assetListTestNow); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("invalid block = %v; want ErrInvalid", err)
	}
	if _, err := runs.SetItemBlock(ctx, "owner-a", "alist_1", "aitem_missing",
		assetlist.BlockNone, assetlist.VersionUnchecked, assetListTestNow); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("missing item block = %v; want ErrNotFound", err)
	}
	if _, err := runs.SetItemBlock(ctx, "owner-b", "alist_1", item.ID,
		assetlist.BlockNone, assetlist.VersionUnchecked, assetListTestNow); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign owner block = %v; want ErrNotFound", err)
	}

	session, err := runs.SetItemSession(ctx, "owner-a", "alist_1", item.ID,
		"ses_1", assetlist.VersionUnchecked, assetListTestNow)
	if err != nil || session.SessionID != "ses_1" {
		t.Fatalf("set session = %+v, %v", session, err)
	}
	var storedSession sql.NullString
	if err := runs.db.QueryRow(`SELECT session_id FROM asset_list_items WHERE id = ?`, item.ID).
		Scan(&storedSession); err != nil || !storedSession.Valid || storedSession.String != "ses_1" {
		t.Fatalf("stored session = %+v, %v", storedSession, err)
	}
	cleared, err := runs.SetItemSession(ctx, "owner-a", "alist_1", item.ID,
		"", assetlist.VersionUnchecked, assetListTestNow)
	if err != nil || cleared.SessionID != "" {
		t.Fatalf("clear session = %+v, %v", cleared, err)
	}
	if err := runs.db.QueryRow(`SELECT session_id FROM asset_list_items WHERE id = ?`, item.ID).
		Scan(&storedSession); err != nil || storedSession.Valid {
		t.Fatalf("cleared session = %+v, %v", storedSession, err)
	}

	next := assetListTestNow.Add(90 * time.Second)
	scheduled, err := runs.SetItemNextActionAt(ctx, "owner-a", "alist_1", item.ID,
		next, assetlist.VersionUnchecked, assetListTestNow)
	if err != nil || !scheduled.NextActionAt.Equal(next) {
		t.Fatalf("set next action = %+v, %v", scheduled, err)
	}
	immediate, err := runs.SetItemNextActionAt(ctx, "owner-a", "alist_1", item.ID,
		time.Time{}, assetlist.VersionUnchecked, assetListTestNow)
	if err != nil || !immediate.NextActionAt.IsZero() {
		t.Fatalf("clear next action = %+v, %v", immediate, err)
	}

	if _, err := runs.ItemByID(ctx, "owner-b", "alist_1", item.ID); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign owner item = %v; want ErrNotFound", err)
	}
	if items, err := runs.ItemsByList(ctx, "owner-b", "alist_1"); err != nil || len(items) != 0 {
		t.Fatalf("foreign owner items = %+v, %v; want none", items, err)
	}
}

func TestAssetListRunRepositoryUpdateRunStatusTransitions(t *testing.T) {
	_, lists, runs, _ := newAssetListRunRepositories(t)
	ctx := context.Background()
	seedRunList(t, lists, "alist_1", 1)
	if _, err := runs.CreateRun(ctx, newAssetListRun("run_1", "owner-a", "prj_1", "alist_1", assetlist.RunIdle)); err != nil {
		t.Fatalf("create idle run: %v", err)
	}
	if _, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunIdle, assetlist.RunPaused, assetlist.PauseUser, assetListTestNow); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("idle→paused = %v; want ErrConflict", err)
	}
	running, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunIdle, assetlist.RunRunning, "", assetListTestNow)
	if err != nil || running.Status != assetlist.RunRunning || running.PauseReason != "" {
		t.Fatalf("idle→running = %+v, %v", running, err)
	}
	paused, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunRunning, assetlist.RunPaused, assetlist.PauseUser, assetListTestNow)
	if err != nil || paused.Status != assetlist.RunPaused || paused.PauseReason != assetlist.PauseUser {
		t.Fatalf("running→paused = %+v, %v", paused, err)
	}
	repeated, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunPaused, assetlist.RunPaused, assetlist.PauseUser, assetListTestNow)
	if err != nil || repeated.Status != assetlist.RunPaused || repeated.PauseReason != assetlist.PauseUser {
		t.Fatalf("repeated pause = %+v, %v", repeated, err)
	}
	if _, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunIdle, assetlist.RunStopped, "", assetListTestNow); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale from status = %v; want ErrConflict", err)
	}
	resumed, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunPaused, assetlist.RunRunning, assetlist.PauseUser, assetListTestNow)
	if err != nil || resumed.Status != assetlist.RunRunning || resumed.PauseReason != "" {
		t.Fatalf("paused→running = %+v, %v", resumed, err)
	}
	presencePaused, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunRunning, assetlist.RunPaused, assetlist.PausePresence, assetListTestNow)
	if err != nil || presencePaused.PauseReason != assetlist.PausePresence {
		t.Fatalf("presence pause = %+v, %v", presencePaused, err)
	}
	if _, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunPaused, assetlist.RunCompleted, "", assetListTestNow); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("paused→completed = %v; want ErrConflict", err)
	}
	if _, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunRunning, assetlist.RunPaused, "", assetListTestNow); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("pause without reason = %v; want ErrInvalid", err)
	}
	if _, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunPaused, assetlist.RunRunning, "", assetListTestNow); err != nil {
		t.Fatalf("presence resume: %v", err)
	}
	completed, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunRunning, assetlist.RunCompleted, "", assetListTestNow)
	if err != nil || completed.Status != assetlist.RunCompleted {
		t.Fatalf("running→completed = %+v, %v", completed, err)
	}
	if _, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_1",
		assetlist.RunCompleted, assetlist.RunRunning, "", assetListTestNow); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("completed→running = %v; want ErrConflict", err)
	}

	seedRunList(t, lists, "alist_2", 1)
	if _, err := runs.CreateRun(ctx, newAssetListRun("run_2", "owner-a", "prj_1", "alist_2", assetlist.RunIdle)); err != nil {
		t.Fatalf("create second run: %v", err)
	}
	stopped, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_2",
		assetlist.RunIdle, assetlist.RunStopped, "", assetListTestNow)
	if err != nil || stopped.Status != assetlist.RunStopped {
		t.Fatalf("idle→stopped = %+v, %v", stopped, err)
	}
	if _, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_2",
		assetlist.RunStopped, assetlist.RunRunning, "", assetListTestNow); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stopped→running = %v; want ErrConflict", err)
	}

	if _, err := runs.UpdateRunStatus(ctx, "owner-b", "alist_2",
		assetlist.RunIdle, assetlist.RunRunning, "", assetListTestNow); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign owner run status = %v; want ErrNotFound", err)
	}
	if _, err := runs.UpdateRunStatus(ctx, "owner-a", "alist_missing",
		assetlist.RunIdle, assetlist.RunRunning, "", assetListTestNow); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("missing run status = %v; want ErrNotFound", err)
	}
}

func TestAssetListPresenceRepositoryUpsertsLeavesAndPrunes(t *testing.T) {
	_, _, _, presence := newAssetListRunRepositories(t)
	ctx := context.Background()
	now := assetListTestNow

	if err := presence.Upsert(ctx, "owner-a", "prj_1", "tab_1", now); err != nil {
		t.Fatalf("upsert tab_1: %v", err)
	}
	if active, err := presence.Active(ctx, "owner-a", "prj_1", now.Add(-120*time.Second)); err != nil || !active {
		t.Fatalf("active after heartbeat = %v, %v", active, err)
	}
	if active, err := presence.Active(ctx, "owner-a", "prj_1", now.Add(time.Second)); err != nil || active {
		t.Fatalf("active after grace cutoff = %v, %v", active, err)
	}
	if err := presence.Upsert(ctx, "owner-a", "prj_1", "tab_1", now.Add(30*time.Second)); err != nil {
		t.Fatalf("refresh tab_1: %v", err)
	}
	var lastSeen string
	if err := presence.db.QueryRow(`
		SELECT last_seen_at FROM asset_list_presence
		WHERE owner_id = ? AND project_id = ? AND tab_id = ?`,
		"owner-a", "prj_1", "tab_1").Scan(&lastSeen); err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if lastSeen != formatTaskTime(now.Add(30*time.Second)) {
		t.Fatalf("heartbeat = %q; want %q", lastSeen, formatTaskTime(now.Add(30*time.Second)))
	}
	if err := presence.Upsert(ctx, "owner-a", "prj_1", "tab_2", now); err != nil {
		t.Fatalf("upsert tab_2: %v", err)
	}

	if active, err := presence.Active(ctx, "owner-b", "prj_1", now.Add(-120*time.Second)); err != nil || active {
		t.Fatalf("foreign owner presence = %v, %v; want inactive", active, err)
	}
	if active, err := presence.Active(ctx, "owner-a", "prj_2", now.Add(-120*time.Second)); err != nil || active {
		t.Fatalf("foreign project presence = %v, %v; want inactive", active, err)
	}

	if err := presence.Leave(ctx, "owner-a", "prj_1", "tab_1"); err != nil {
		t.Fatalf("leave tab_1: %v", err)
	}
	if err := presence.Leave(ctx, "owner-a", "prj_1", "tab_1"); err != nil {
		t.Fatalf("repeat leave: %v", err)
	}
	var count int
	if err := presence.db.QueryRow(`SELECT COUNT(*) FROM asset_list_presence`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("presence rows after leave = %d, %v; want 1", count, err)
	}
	if active, err := presence.Active(ctx, "owner-a", "prj_1", now.Add(-120*time.Second)); err != nil || !active {
		t.Fatalf("presence after one tab left = %v, %v; want active", active, err)
	}

	if err := presence.Upsert(ctx, "owner-a", "prj_1", "tab_old", now.Add(-time.Hour)); err != nil {
		t.Fatalf("upsert stale tab: %v", err)
	}
	removed, err := presence.Prune(ctx, now.Add(-time.Minute))
	if err != nil || removed != 1 {
		t.Fatalf("prune = %d, %v; want 1", removed, err)
	}
	if err := presence.db.QueryRow(`SELECT COUNT(*) FROM asset_list_presence`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("presence rows after prune = %d, %v; want 1", count, err)
	}

	if err := presence.Upsert(ctx, "", "prj_1", "tab_1", now); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("presence without owner = %v; want ErrInvalid", err)
	}
	if err := presence.Leave(ctx, "owner-a", "", "tab_1"); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("leave without project = %v; want ErrInvalid", err)
	}
	if _, err := presence.Active(ctx, "", "prj_1", now); !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("active without owner = %v; want ErrInvalid", err)
	}
}

func TestAssetListRunsCascadeWithListAndProject(t *testing.T) {
	repository, lists, runs, presence := newAssetListRunRepositories(t)
	ctx := context.Background()
	seedRunList(t, lists, "alist_1", 1)
	seedRunList(t, lists, "alist_2", 1)
	if _, err := runs.CreateRun(ctx, newAssetListRun("run_1", "owner-a", "prj_1", "alist_1", assetlist.RunRunning)); err != nil {
		t.Fatalf("create run_1: %v", err)
	}
	if _, err := runs.CreateRun(ctx, newAssetListRun("run_2", "owner-a", "prj_1", "alist_2", assetlist.RunIdle)); err != nil {
		t.Fatalf("create run_2: %v", err)
	}
	if err := presence.Upsert(ctx, "owner-a", "prj_1", "tab_1", assetListTestNow); err != nil {
		t.Fatalf("upsert presence: %v", err)
	}

	if _, err := repository.db.Exec(`DELETE FROM asset_lists WHERE id = ?`, "alist_1"); err != nil {
		t.Fatalf("delete list: %v", err)
	}
	if _, err := runs.GetRunByList(ctx, "owner-a", "prj_1", "alist_1"); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("run after list delete = %v; want ErrNotFound", err)
	}
	if items, err := runs.ItemsByList(ctx, "owner-a", "alist_1"); err != nil || len(items) != 0 {
		t.Fatalf("items after list delete = %+v, %v; want none", items, err)
	}
	if run, err := runs.GetRunByList(ctx, "owner-a", "prj_1", "alist_2"); err != nil || run.ID != "run_2" {
		t.Fatalf("sibling run after list delete = %+v, %v", run, err)
	}
	if active, err := presence.Active(ctx, "owner-a", "prj_1", assetListTestNow); err != nil || !active {
		t.Fatalf("presence after list delete = %v, %v; want active", active, err)
	}

	if _, err := repository.db.Exec(`DELETE FROM projects WHERE id = ?`, "prj_1"); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	for _, table := range []string{"asset_list_runs", "asset_list_presence"} {
		var count int
		if err := repository.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s after project delete = %d, %v", table, count, err)
		}
	}
}

// ItemBySession resolves the item from the session id only within its owner:
// the runner writes the id, and a foreign session must not be attributed to
// the caller's item.
func TestAssetListRunRepositoryItemBySessionIsOwnerScopedAndComplete(t *testing.T) {
	repository, lists, runs, _ := newAssetListRunRepositories(t)
	ctx := context.Background()
	seedAssetListProject(t, repository, "owner-b", "prj_b")
	seedAssetListSession(t, repository, "owner-b", "ses_b", "prj_b")
	seedAssetListSession(t, repository, "owner-a", "ses_item", "prj_1")
	seedAssetListSession(t, repository, "owner-b", "ses_foreign", "prj_b")

	items := seedRunList(t, lists, "alist_a", 2)
	foreign := newAssetListItem("owner-b", "aitem_b", "Foreign item")
	proposeAssetList(t, lists, newAssetList("alist_b", "owner-b", "prj_b", "ses_b", "req_b"), foreign)

	stored, err := runs.SetItemSession(ctx, "owner-a", "alist_a", items[1].ID,
		"ses_item", assetlist.VersionUnchecked, assetListTestNow)
	if err != nil {
		t.Fatalf("set item session: %v", err)
	}
	if _, err := runs.SetItemSession(ctx, "owner-b", "alist_b", foreign.ID,
		"ses_foreign", assetlist.VersionUnchecked, assetListTestNow); err != nil {
		t.Fatalf("set foreign item session: %v", err)
	}

	item, found, err := runs.ItemBySession(ctx, "owner-a", "ses_item")
	if err != nil || !found || !reflect.DeepEqual(item, stored) {
		t.Fatalf("item by session = %+v, %v, %v; want %+v", item, found, err, stored)
	}
	if item.SessionID != "ses_item" || item.RouteID != stored.RouteID || len(item.Route) != 1 ||
		item.PlannedModel3DStepCount != 1 || len(item.References) != 1 {
		t.Fatalf("item fields incomplete: %+v", item)
	}
	// The same session string owned by another owner is still a miss.
	if item, found, err := runs.ItemBySession(ctx, "owner-a", "ses_foreign"); err != nil || found {
		t.Fatalf("foreign owner = %+v, %v, %v; want none", item, found, err)
	}
	if item, found, err := runs.ItemBySession(ctx, "owner-b", "ses_foreign"); err != nil || !found || item.ID != foreign.ID {
		t.Fatalf("foreign item = %+v, %v, %v; want %s", item, found, err, foreign.ID)
	}
	// A session no item runs is an ordinary session, not an error.
	if item, found, err := runs.ItemBySession(ctx, "owner-a", "ses_none"); err != nil || found || item.ID != "" {
		t.Fatalf("unknown session = %+v, %v, %v; want no item", item, found, err)
	}
}
