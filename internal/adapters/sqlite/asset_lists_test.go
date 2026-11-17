package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/project"
)

var assetListTestNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func newAssetListRepository(t *testing.T) (*Repository, *AssetListRepository) {
	t.Helper()
	repository, err := openTestRepository(filepath.Join(t.TempDir(), "asset-lists.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	seedAssetListProject(t, repository, "owner-a", "prj_1")
	seedAssetListSession(t, repository, "owner-a", "ses_1", "prj_1")
	return repository, repository.AssetListRepository()
}

func seedAssetListProject(t *testing.T, repository *Repository, ownerID, projectID string) {
	t.Helper()
	at := formatTime(assetListTestNow)
	if _, err := repository.db.Exec(`
		INSERT INTO projects (id, owner_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, projectID, ownerID, "Project", at, at); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func seedAssetListSession(t *testing.T, repository *Repository, ownerID, sessionID, projectID string) {
	t.Helper()
	at := formatTime(assetListTestNow)
	if _, err := repository.db.Exec(`
		INSERT INTO go_chat_sessions (id, owner_id, owner_name, title, created_at, updated_at, project_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, sessionID, ownerID, ownerID, "Session", at, at, projectID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func newAssetList(id, ownerID, projectID, sessionID, requestKey string) assetlist.List {
	return assetlist.List{
		ID: id, RequestKey: requestKey, OwnerID: ownerID, ProjectID: projectID,
		SourceSessionID: sessionID, OriginMessageID: "msg_1", Title: "List " + id,
		Status: assetlist.StatusDraft, Brief: json.RawMessage(`{}`), Version: 0,
		CreatedAt: assetListTestNow, UpdatedAt: assetListTestNow,
		ExpiresAt: assetListTestNow.Add(7 * 24 * time.Hour),
	}
}

func newAssetListItem(ownerID, itemID, name string) assetlist.Item {
	return assetlist.Item{
		ID: itemID, OwnerID: ownerID, Kind: "character", Name: name,
		References: []project.Reference{{InputAssetID: "input_1"}},
		RouteID:    "text-to-3d",
		Route: []assetlist.RouteStep{
			{Capability: "text-to-3d", OfferingID: "off_1", Available: true},
		},
		PlannedStepCount: 1, PlannedModel3DStepCount: 1,
		State: assetlist.StateTodo, Result: json.RawMessage(`{}`), Review: json.RawMessage(`{}`),
		CreatedAt: assetListTestNow, UpdatedAt: assetListTestNow,
	}
}

func proposeAssetList(t *testing.T, repository *AssetListRepository, list assetlist.List, items ...assetlist.Item) assetlist.List {
	t.Helper()
	stored, err := repository.Propose(context.Background(), list, items, 0)
	if err != nil {
		t.Fatalf("propose %s: %v", list.ID, err)
	}
	return stored
}

func seedAssetListRow(t *testing.T, repository *Repository, list assetlist.List) {
	t.Helper()
	if _, err := repository.db.Exec(`
		INSERT INTO asset_lists (id, request_key, owner_id, project_id, source_session_id,
			origin_message_id, title, status, version, brief_json, decided_at, decided_by,
			decision_reason, superseded_by, created_at, updated_at, expires_at)
		VALUES (?, ?, ?, ?, NULL, '', ?, ?, 0, '{}', NULL, '', '', '', ?, ?, ?)`,
		list.ID, list.RequestKey, list.OwnerID, list.ProjectID, list.Title, list.Status,
		formatTime(list.CreatedAt), formatTime(list.UpdatedAt), formatTime(list.ExpiresAt)); err != nil {
		t.Fatalf("seed asset list row: %v", err)
	}
}

func TestAssetListRepositoryEnforcesProjectListLimit(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	ctx := context.Background()
	for index := 0; index < 19; index++ {
		seedAssetListRow(t, repository, newAssetList(
			fmt.Sprintf("alist_seed_%d", index), "owner-a", "prj_1", "ses_1",
			fmt.Sprintf("seed_%d", index)))
	}
	confirmed := newAssetList("alist_confirmed", "owner-a", "prj_1", "ses_1", "seed_confirmed")
	confirmed.Status = assetlist.StatusConfirmed
	seedAssetListRow(t, repository, confirmed)
	if live, err := lists.CountLiveLists(ctx, "owner-a", "prj_1"); err != nil || live != 20 {
		t.Fatalf("live lists = %d, %v", live, err)
	}
	_, err := lists.Propose(ctx, newAssetList("alist_new", "owner-a", "prj_1", "ses_1", "req_new"),
		[]assetlist.Item{newAssetListItem("owner-a", "aitem_new", "New")}, 20)
	if !errors.Is(err, assetlist.ErrLimit) {
		t.Fatalf("proposal at the limit error = %v", err)
	}
	var rows int
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM asset_lists`).Scan(&rows); err != nil || rows != 20 {
		t.Fatalf("rows after refused proposal = %d, %v", rows, err)
	}
	// Only terminal lists stop consuming the quota; then a new draft is
	// admitted and supersedes nothing.
	if _, err := repository.db.Exec(
		`UPDATE asset_lists SET status = 'superseded' WHERE project_id = ? AND status = 'draft'`,
		"prj_1"); err != nil {
		t.Fatalf("supersede seeded drafts: %v", err)
	}
	if live, err := lists.CountLiveLists(ctx, "owner-a", "prj_1"); err != nil || live != 1 {
		t.Fatalf("live lists after supersede = %d, %v", live, err)
	}
	stored, err := lists.Propose(ctx, newAssetList("alist_new", "owner-a", "prj_1", "ses_1", "req_new"),
		[]assetlist.Item{newAssetListItem("owner-a", "aitem_new", "New")}, 20)
	if err != nil || stored.ID != "alist_new" || stored.Status != assetlist.StatusDraft {
		t.Fatalf("proposal after supersede = %+v, %v", stored, err)
	}
	if live, err := lists.CountLiveLists(ctx, "owner-a", "prj_1"); err != nil || live != 2 {
		t.Fatalf("live lists after proposal = %d, %v", live, err)
	}
}

func TestAssetListsMigrationCreatesTablesAndIndexes(t *testing.T) {
	repository, _ := newAssetListRepository(t)
	for _, table := range []string{"asset_lists", "asset_list_items"} {
		var name string
		err := repository.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil || name != table {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	expectedIndexes := []string{
		"asset_lists_project_status",
		"asset_lists_owner_updated",
		"asset_lists_confirmed_project",
		"asset_list_items_list_position",
		"asset_list_items_list_state",
		"asset_list_items_owner_state",
	}
	for _, index := range expectedIndexes {
		var name string
		err := repository.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name)
		if err != nil || name != index {
			t.Fatalf("index %s missing: %v", index, err)
		}
	}
	var partial string
	if err := repository.db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'asset_lists_confirmed_project'`).
		Scan(&partial); err != nil {
		t.Fatalf("read partial index: %v", err)
	}
	if !strings.Contains(partial, "WHERE status = 'confirmed'") {
		t.Fatalf("partial index is not restricted to confirmed lists: %s", partial)
	}
}

func TestAssetListRepositoryProposeSupersedesDraftsAndReplaysRequestKey(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	ctx := context.Background()
	first := proposeAssetList(t, lists, newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1"),
		newAssetListItem("owner-a", "aitem_1", "Hero"))
	second := proposeAssetList(t, lists, newAssetList("alist_2", "owner-a", "prj_1", "ses_1", "req_2"),
		newAssetListItem("owner-a", "aitem_2", "Villain"))

	storedFirst, err := lists.GetList(ctx, "owner-a", "prj_1", first.ID)
	if err != nil {
		t.Fatalf("read superseded draft: %v", err)
	}
	if storedFirst.Status != assetlist.StatusSuperseded || storedFirst.SupersededBy != second.ID {
		t.Fatalf("first draft = %+v", storedFirst)
	}
	if storedSecond, err := lists.GetList(ctx, "owner-a", "prj_1", second.ID); err != nil ||
		storedSecond.Status != assetlist.StatusDraft {
		t.Fatalf("second draft = %+v, %v", storedSecond, err)
	}

	replayed := proposeAssetList(t, lists, newAssetList("alist_3", "owner-a", "prj_1", "ses_1", "req_1"))
	if replayed.ID != first.ID || replayed.Status != assetlist.StatusSuperseded || len(replayed.Items) != 1 {
		t.Fatalf("replayed request key = %+v", replayed)
	}
	var count int
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM asset_lists`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("lists after replay = %d, %v", count, err)
	}
	if _, err := repository.db.Exec(`
		INSERT INTO asset_lists (id, request_key, owner_id, project_id, title, status,
			created_at, updated_at, expires_at)
		VALUES ('alist_dup', 'req_1', 'owner-a', 'prj_1', 'Dup', 'draft', ?, ?, ?)`,
		formatTime(assetListTestNow), formatTime(assetListTestNow), formatTime(assetListTestNow.Add(time.Hour))); err == nil {
		t.Fatal("duplicate request_key must fail the UNIQUE constraint")
	}
}

func TestAssetListRepositoryEditUsesVersionCASAndDraftOnly(t *testing.T) {
	_, lists := newAssetListRepository(t)
	ctx := context.Background()
	list := proposeAssetList(t, lists, newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1"),
		newAssetListItem("owner-a", "aitem_1", "Hero"))

	edit := list
	edit.Title = "Edited"
	edit.Brief = json.RawMessage(`{"purpose":"icon"}`)
	edit.UpdatedAt = assetListTestNow.Add(time.Minute)
	edited, err := lists.EditList(ctx, edit,
		[]assetlist.Item{newAssetListItem("owner-a", "aitem_2", "Icon")}, 0)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if edited.Version != 1 || edited.Title != "Edited" || len(edited.Items) != 1 ||
		edited.Items[0].Name != "Icon" || string(edited.Brief) != `{"purpose":"icon"}` {
		t.Fatalf("edited list = %+v", edited)
	}
	if _, err := lists.EditList(ctx, edit,
		[]assetlist.Item{newAssetListItem("owner-a", "aitem_3", "Stale")}, 0); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale version error = %v", err)
	}
	if _, err := lists.EditList(ctx, assetlist.List{
		ID: list.ID, OwnerID: "owner-b", ProjectID: "prj_1", Title: "Foreign",
		Brief: json.RawMessage(`{}`), UpdatedAt: assetListTestNow,
	}, nil, 1); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign edit error = %v", err)
	}
	if _, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Action: assetlist.DecisionConfirm, At: assetListTestNow.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := lists.EditList(ctx, edit,
		[]assetlist.Item{newAssetListItem("owner-a", "aitem_4", "Confirmed")}, 1); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("confirmed edit error = %v", err)
	}
}

func TestAssetListRepositoryEditRecordsExpiry(t *testing.T) {
	_, lists := newAssetListRepository(t)
	ctx := context.Background()
	list := newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1")
	list.ExpiresAt = assetListTestNow.Add(-time.Minute)
	proposeAssetList(t, lists, list, newAssetListItem("owner-a", "aitem_1", "Hero"))
	edit := list
	edit.Title = "Late"
	edit.Brief = json.RawMessage(`{}`)
	edit.UpdatedAt = assetListTestNow
	if _, err := lists.EditList(ctx, edit,
		[]assetlist.Item{newAssetListItem("owner-a", "aitem_2", "Late")}, 0); !errors.Is(err, assetlist.ErrExpired) {
		t.Fatalf("expired edit error = %v", err)
	}
	stored, err := lists.GetList(ctx, "owner-a", "prj_1", list.ID)
	if err != nil || stored.Status != assetlist.StatusExpired {
		t.Fatalf("stored expired list = %+v, %v", stored, err)
	}
}

func TestAssetListRepositoryConfirmsOnePerProjectAndIsIdempotent(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	ctx := context.Background()
	first := proposeAssetList(t, lists, newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1"),
		newAssetListItem("owner-a", "aitem_1", "Hero"))
	confirmed, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: first.ID,
		Action: assetlist.DecisionConfirm, At: assetListTestNow.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// A confirmed list does not block a new proposal; the proposal only
	// supersedes other drafts.
	second := proposeAssetList(t, lists, newAssetList("alist_2", "owner-a", "prj_1", "ses_1", "req_2"),
		newAssetListItem("owner-a", "aitem_2", "Villain"))
	if confirmed.Status != assetlist.StatusConfirmed || confirmed.DecidedBy != "owner-a" ||
		confirmed.DecidedAt == nil || confirmed.Version != 1 {
		t.Fatalf("confirmed = %+v", confirmed)
	}
	again, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: first.ID,
		Action: assetlist.DecisionConfirm, At: assetListTestNow.Add(2 * time.Minute),
	})
	if err != nil || again.ID != first.ID || again.Version != confirmed.Version {
		t.Fatalf("repeat confirm = %+v, %v", again, err)
	}
	_, err = lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: second.ID,
		Action: assetlist.DecisionConfirm, At: assetListTestNow.Add(2 * time.Minute),
	})
	var active *assetlist.ErrActiveConflict
	if !errors.As(err, &active) || active.ListID != first.ID || active.Title != "List alist_1" {
		t.Fatalf("active conflict = %+v (%v)", active, err)
	}
	if _, err := repository.db.Exec(
		`UPDATE asset_lists SET status = 'confirmed' WHERE id = ?`, second.ID); err == nil {
		t.Fatal("the partial unique index must refuse a second confirmed list")
	}
	storedSecond, err := lists.GetList(ctx, "owner-a", "prj_1", second.ID)
	if err != nil || storedSecond.Status != assetlist.StatusDraft {
		t.Fatalf("draft after refused confirm = %+v, %v", storedSecond, err)
	}
}

func TestAssetListRepositoryReplaceSupersedesConfirmed(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	ctx := context.Background()
	first := proposeAssetList(t, lists, newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1"),
		newAssetListItem("owner-a", "aitem_1", "Hero"))
	if _, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: first.ID,
		Action: assetlist.DecisionConfirm, At: assetListTestNow.Add(time.Minute),
	}); err != nil {
		t.Fatalf("confirm first: %v", err)
	}
	second := proposeAssetList(t, lists, newAssetList("alist_2", "owner-a", "prj_1", "ses_1", "req_2"),
		newAssetListItem("owner-a", "aitem_2", "Villain"))

	// A running item of the old confirmed list refuses the replace and the
	// whole transaction rolls back (BEGIN IMMEDIATE write transaction).
	if _, err := repository.db.Exec(
		`UPDATE asset_list_items SET state = 'working' WHERE list_id = ?`, first.ID); err != nil {
		t.Fatalf("mark working: %v", err)
	}
	_, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: second.ID,
		Action: assetlist.DecisionReplace, At: assetListTestNow.Add(2 * time.Minute),
	})
	var working *assetlist.ErrActiveWorking
	if !errors.As(err, &working) || working.ListID != first.ID {
		t.Fatalf("working guard = %+v (%v)", working, err)
	}
	if stored, err := lists.GetList(ctx, "owner-a", "prj_1", first.ID); err != nil ||
		stored.Status != assetlist.StatusConfirmed {
		t.Fatalf("old confirmed after refused replace = %+v, %v", stored, err)
	}
	if stored, err := lists.GetList(ctx, "owner-a", "prj_1", second.ID); err != nil ||
		stored.Status != assetlist.StatusDraft {
		t.Fatalf("draft after refused replace = %+v, %v", stored, err)
	}

	if _, err := repository.db.Exec(
		`UPDATE asset_list_items SET state = 'todo' WHERE list_id = ?`, first.ID); err != nil {
		t.Fatalf("clear working: %v", err)
	}
	replaced, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: second.ID,
		Action: assetlist.DecisionReplace, At: assetListTestNow.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if replaced.Status != assetlist.StatusConfirmed {
		t.Fatalf("replacement = %+v", replaced)
	}
	old, err := lists.GetList(ctx, "owner-a", "prj_1", first.ID)
	if err != nil || old.Status != assetlist.StatusSuperseded || old.SupersededBy != second.ID {
		t.Fatalf("superseded confirmed = %+v, %v", old, err)
	}
}

func TestAssetListRepositoryRejectAndExpiry(t *testing.T) {
	_, lists := newAssetListRepository(t)
	ctx := context.Background()
	list := proposeAssetList(t, lists, newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1"),
		newAssetListItem("owner-a", "aitem_1", "Hero"))
	rejected, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Action: assetlist.DecisionReject, Reason: "not this quarter",
		At: assetListTestNow.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != assetlist.StatusRejected || rejected.DecisionReason != "not this quarter" ||
		rejected.DecidedBy != "owner-a" {
		t.Fatalf("rejected = %+v", rejected)
	}
	if again, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Action: assetlist.DecisionReject, At: assetListTestNow.Add(2 * time.Minute),
	}); err != nil || again.ID != list.ID {
		t.Fatalf("repeat reject = %+v, %v", again, err)
	}
	if _, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Action: assetlist.DecisionConfirm, At: assetListTestNow.Add(2 * time.Minute),
	}); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("confirm rejected error = %v", err)
	}

	expiring := newAssetList("alist_2", "owner-a", "prj_1", "ses_1", "req_2")
	expiring.ExpiresAt = assetListTestNow.Add(-time.Minute)
	proposeAssetList(t, lists, expiring, newAssetListItem("owner-a", "aitem_2", "Expired"))
	if _, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-a", ProjectID: "prj_1", ListID: expiring.ID,
		Action: assetlist.DecisionConfirm, At: assetListTestNow,
	}); !errors.Is(err, assetlist.ErrExpired) {
		t.Fatalf("expired confirm error = %v", err)
	}
	stored, err := lists.GetList(ctx, "owner-a", "prj_1", expiring.ID)
	if err != nil || stored.Status != assetlist.StatusExpired {
		t.Fatalf("stored expired = %+v, %v", stored, err)
	}
}

func TestAssetListRepositorySessionDeleteSetsNullAndProjectDeleteCascades(t *testing.T) {
	repository, lists := newAssetListRepository(t)
	ctx := context.Background()
	list := newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1")
	item := newAssetListItem("owner-a", "aitem_1", "Hero")
	item.SessionID = "ses_1"
	proposeAssetList(t, lists, list, item)

	if _, err := repository.db.Exec(`DELETE FROM go_chat_sessions WHERE id = ?`, "ses_1"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	var sourceSession, itemSession sql.NullString
	if err := repository.db.QueryRow(
		`SELECT source_session_id FROM asset_lists WHERE id = ?`, list.ID).Scan(&sourceSession); err != nil {
		t.Fatalf("read source session: %v", err)
	}
	if err := repository.db.QueryRow(
		`SELECT session_id FROM asset_list_items WHERE id = ?`, item.ID).Scan(&itemSession); err != nil {
		t.Fatalf("read item session: %v", err)
	}
	if sourceSession.Valid || itemSession.Valid {
		t.Fatalf("session FKs = %v / %v; want NULL", sourceSession, itemSession)
	}
	stored, err := lists.GetList(ctx, "owner-a", "prj_1", list.ID)
	if err != nil || stored.SourceSessionID != "" {
		t.Fatalf("list after session delete = %+v, %v", stored, err)
	}

	if _, err := repository.db.Exec(`DELETE FROM projects WHERE id = ?`, "prj_1"); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	for _, table := range []string{"asset_lists", "asset_list_items"} {
		var count int
		if err := repository.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s after project delete = %d, %v", table, count, err)
		}
	}
}

func TestAssetListRepositoryFiltersOwnerAndListsSummaries(t *testing.T) {
	_, lists := newAssetListRepository(t)
	ctx := context.Background()
	first := proposeAssetList(t, lists, newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1"),
		newAssetListItem("owner-a", "aitem_1", "Hero"))
	secondInput := newAssetList("alist_2", "owner-a", "prj_1", "ses_1", "req_2")
	secondInput.UpdatedAt = assetListTestNow.Add(time.Minute)
	proposeAssetList(t, lists, secondInput, newAssetListItem("owner-a", "aitem_2", "Villain"))

	summaries, err := lists.ListLists(ctx, "owner-a", "prj_1")
	if err != nil || len(summaries) != 2 {
		t.Fatalf("summaries = %+v, %v", summaries, err)
	}
	if summaries[0].ID != secondInput.ID || summaries[0].Items != nil ||
		summaries[0].PlannedStepCount != 1 || summaries[0].PlannedModel3DStepCount != 1 {
		t.Fatalf("newest summary = %+v", summaries[0])
	}
	if foreign, err := lists.ListLists(ctx, "owner-b", "prj_1"); err != nil || len(foreign) != 0 {
		t.Fatalf("foreign summaries = %+v, %v", foreign, err)
	}
	if _, err := lists.GetList(ctx, "owner-b", "prj_1", first.ID); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign get error = %v", err)
	}
	// FindByRequestKey reports not-found as found=false, not as an error.
	if _, found, err := lists.FindByRequestKey(ctx, "owner-b", "req_1"); err != nil || found {
		t.Fatalf("foreign request key found = %v, %v", found, err)
	}
	if exists, err := lists.ProjectExists(ctx, "owner-b", "prj_1"); err != nil || exists {
		t.Fatalf("foreign project exists = %v, %v", exists, err)
	}
	if projectID, err := lists.SessionProject(ctx, "owner-a", "ses_missing"); !errors.Is(err, assetlist.ErrNotFound) || projectID != "" {
		t.Fatalf("missing session project = %q, %v", projectID, err)
	}
	if _, err := lists.Decide(ctx, assetlist.Decision{
		OwnerID: "owner-b", ProjectID: "prj_1", ListID: first.ID,
		Action: assetlist.DecisionReject, At: assetListTestNow,
	}); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign decide error = %v", err)
	}
}

func TestAssetListRepositoryRoundTripsJSONAndItems(t *testing.T) {
	_, lists := newAssetListRepository(t)
	ctx := context.Background()
	list := newAssetList("alist_1", "owner-a", "prj_1", "ses_1", "req_1")
	list.Brief = json.RawMessage(`{"purpose":"hero","platform":"pc"}`)
	item := newAssetListItem("owner-a", "aitem_1", "Hero")
	item.References = []project.Reference{
		{InputAssetID: "input_1"}, {TaskID: "task_1", AssetID: "asset_1"},
	}
	item.Route = []assetlist.RouteStep{
		{Capability: "text-to-3d", OfferingID: "off_1", Available: false, Reason: "disabled"},
		{Capability: "rigging", OfferingID: "off_2", Available: true},
	}
	item.PlannedStepCount = 2
	item.PlannedModel3DStepCount = 2
	item.SessionID = "ses_1"
	item.Result = json.RawMessage(`{"task_id":"task_1"}`)
	item.Review = json.RawMessage(`{"state":"pending"}`)
	proposeAssetList(t, lists, list, item)

	stored, err := lists.GetList(ctx, "owner-a", "prj_1", list.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(stored.Brief) != string(list.Brief) {
		t.Fatalf("brief = %s", stored.Brief)
	}
	if len(stored.Items) != 1 {
		t.Fatalf("items = %+v", stored.Items)
	}
	storedItem := stored.Items[0]
	if len(storedItem.References) != 2 || storedItem.References[1].TaskID != "task_1" ||
		len(storedItem.Route) != 2 || storedItem.Route[0].Available ||
		storedItem.Route[0].Reason != "disabled" || !storedItem.Route[1].Available ||
		storedItem.SessionID != "ses_1" || string(storedItem.Result) != `{"task_id":"task_1"}` ||
		string(storedItem.Review) != `{"state":"pending"}` {
		t.Fatalf("stored item = %+v", storedItem)
	}
	if stored.PlannedStepCount != 2 || stored.PlannedModel3DStepCount != 2 {
		t.Fatalf("plan totals = %d/%d", stored.PlannedStepCount, stored.PlannedModel3DStepCount)
	}
}
