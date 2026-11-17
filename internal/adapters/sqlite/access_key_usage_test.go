package sqlite

import (
	"context"
	"strings"
	"testing"
)

// keyTask makes a key's step a real Task: it approves a key grant of the
// capability, binds it to a Task and gives the Task its 3D capability and
// state.
func (f *stepFixture) keyTask(t *testing.T, owner, session, capability, taskID, state, phase string) {
	t.Helper()
	identity, grant := f.keyGrant(t, owner, session, capability, "key-one")
	approved := f.mustApproveKey(t, grant)
	bindBudgetTask(t, f.r, identity, approved.RequestID, taskID)
	if _, err := f.r.db.Exec(`UPDATE generation_tasks SET capability = ?, state = ? WHERE id = ?`, capability, state, taskID); err != nil {
		t.Fatal(err)
	}
	if phase != "" {
		if _, err := f.r.db.Exec(`INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key,
 started_at, updated_at, last_observation_phase) VALUES (?, ?, 1, '{}', 'FAILED', ?, '2026-10-22T00:00:00Z', '2026-10-22T00:00:00Z', ?)`,
			"attempt-"+taskID, taskID, "submission-"+taskID, phase); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAccessKeyStepsTotalCountsOnlyTheKeysStepsOfTheOwner(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	total := func(owner string) int {
		t.Helper()
		count, err := f.r.AccessKeyStepsTotal(ctx, owner, f.steps.Counted)
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	if total("owner-a") != 0 {
		t.Fatal("a person with no key step has a total")
	}
	// Two key Tasks in two assets, one of them failed before the provider ran
	// (never charged), one key step approved but without a Task yet.
	f.keyTask(t, "owner-a", "asset-1", "text-to-3d", "key-task-1", "SUCCEEDED", "")
	f.keyTask(t, "owner-a", "asset-2", "image-to-3d", "key-task-2", "RUNNING", "")
	f.keyTask(t, "owner-a", "asset-2", "image-to-3d", "key-task-uncharged", "FAILED", "")
	_, held := f.keyGrant(t, "owner-a", "asset-3", "rigging", "key-one")
	f.mustApproveKey(t, held)
	// A 2D key Task never counts; a browser step and another person's key step
	// are not this person's key steps.
	f.keyTask(t, "owner-a", "asset-1", "text-to-image", "key-task-2d", "SUCCEEDED", "")
	f.task(t, "browser-step", "owner-a", "asset-1", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	f.keyTask(t, "owner-b", "asset-b", "text-to-3d", "key-task-b", "SUCCEEDED", "")
	if got := total("owner-a"); got != 3 {
		t.Fatalf("owner-a total = %d; want 2 Tasks + 1 held step", got)
	}
	if got := total("owner-b"); got != 1 {
		t.Fatalf("owner-b total = %d; want 1", got)
	}
	// A charged failure still counts: the provider ran and billed it.
	f.keyTask(t, "owner-a", "asset-1", "retexture", "key-task-charged", "FAILED", "COMPLETED")
	if got := total("owner-a"); got != 4 {
		t.Fatalf("owner-a total after a charged failure = %d; want 4", got)
	}
	if _, err := f.r.AccessKeyStepsTotal(ctx, "", f.steps.Counted); err == nil {
		t.Fatal("an empty owner was accepted")
	}
	if got, err := f.r.AccessKeyStepsTotal(ctx, "owner-a", nil); err != nil || got != 0 {
		t.Fatalf("no counted capability = %d err=%v", got, err)
	}
}

func TestAccessKeyAssetsListsTheKeysAssetsNewestFirstWithTheirSteps(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	f.keyTask(t, "owner-a", "asset-old", "text-to-3d", "task-old", "SUCCEEDED", "")
	f.keyTask(t, "owner-a", "asset-new", "text-to-3d", "task-new-1", "SUCCEEDED", "")
	f.keyTask(t, "owner-a", "asset-new", "retexture", "task-new-2", "SUCCEEDED", "")
	// The asset's step count includes what the browser did in it, as the cap does.
	f.task(t, "browser-step", "owner-a", "asset-new", "image-to-3d", "SUCCEEDED", "", "COMPLETED")
	// A session is an asset of the service only when the service named it
	// (mcp_assets): one a key never reached, or the browser alone worked in, is
	// not; another person's is not mine.
	f.task(t, "plain-step", "owner-a", "plain-session", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	f.keyTask(t, "owner-b", "asset-b", "text-to-3d", "task-b", "SUCCEEDED", "")
	for _, asset := range [][3]string{{"owner-a", "asset-old", "Old"}, {"owner-a", "asset-new", "木桶"}, {"owner-b", "asset-b", "B"}} {
		if _, err := f.r.db.Exec(`INSERT INTO mcp_assets (owner_id, name_key, name, session_id, created_at) VALUES (?, ?, ?, ?, '2026-02-01T00:00:00Z')`,
			asset[0], strings.ToLower(asset[2]), asset[2], asset[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.r.db.Exec(`UPDATE go_chat_sessions SET title = 'MCP · 木桶' WHERE id = 'asset-new'`); err != nil {
		t.Fatal(err)
	}
	// Order is by the last key grant: make asset-old older.
	if _, err := f.r.db.Exec(`UPDATE execution_grants SET created_at = '2026-01-01T00:00:00Z' WHERE session_id = 'asset-old'`); err != nil {
		t.Fatal(err)
	}
	assets, more, err := f.r.AccessKeyAssets(ctx, "owner-a", f.steps.Counted, 10)
	if err != nil || more || len(assets) != 2 {
		t.Fatalf("assets = %+v more=%v err=%v", assets, more, err)
	}
	if assets[0].SessionID != "asset-new" || assets[0].Title != "MCP · 木桶" || assets[0].Used != 3 || assets[0].Name != "木桶" || assets[0].LastActivityAt.IsZero() {
		t.Fatalf("newest = %+v; want asset-new with 3 steps (2 key + 1 browser)", assets[0])
	}
	if assets[1].SessionID != "asset-old" || assets[1].Used != 1 || assets[1].Title != "test" {
		t.Fatalf("oldest = %+v", assets[1])
	}
	// The list is bounded and says when older assets are left out.
	bounded, more, err := f.r.AccessKeyAssets(ctx, "owner-a", f.steps.Counted, 1)
	if err != nil || !more || len(bounded) != 1 || bounded[0].SessionID != "asset-new" {
		t.Fatalf("bounded = %+v more=%v err=%v", bounded, more, err)
	}
	if _, _, err := f.r.AccessKeyAssets(ctx, "owner-a", f.steps.Counted, 0); err == nil {
		t.Fatal("a zero limit was accepted")
	}
	if _, _, err := f.r.AccessKeyAssets(ctx, "", f.steps.Counted, 5); err == nil {
		t.Fatal("an empty owner was accepted")
	}
}

// An asset the service made but no key asked a step on yet is still an asset:
// the marker, not the grants, says so, and its activity is its creation.
func TestAccessKeyAssetsIncludeAnAssetWithoutAnyGrant(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	f.session(t, "owner-a", "fresh")
	f.session(t, "owner-a", "stale")
	for _, row := range [][2]string{{"fresh", "2026-03-01T00:00:00Z"}, {"stale", "2026-01-01T00:00:00Z"}} {
		if _, err := f.r.db.Exec(`INSERT INTO mcp_assets (owner_id, name_key, name, session_id, created_at) VALUES ('owner-a', ?, ?, ?, ?)`,
			row[0], row[0], row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	assets, more, err := f.r.AccessKeyAssets(ctx, "owner-a", f.steps.Counted, 10)
	if err != nil || more || len(assets) != 2 || assets[0].SessionID != "fresh" || assets[1].SessionID != "stale" ||
		assets[0].Used != 0 || assets[0].LastActivityAt.Year() != 2026 || assets[0].LastActivityAt.Month() != 3 {
		t.Fatalf("assets = %+v more=%v err=%v", assets, more, err)
	}
}

func TestAssetStepsUsedCountsInEveryMode(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	f.task(t, "step-1", "owner-a", "asset", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	f.task(t, "step-2", "owner-a", "asset", "image-to-3d", "RUNNING", "", "")
	f.task(t, "image", "owner-a", "asset", "text-to-image", "SUCCEEDED", "", "COMPLETED")
	// The default mode is agent, which the steps decision never counts; the
	// status block still needs the number.
	if used, err := f.r.AssetStepsUsed(ctx, "owner-a", "asset", f.steps.Counted); err != nil || used != 2 {
		t.Fatalf("used = %d err=%v; want 2", used, err)
	}
	if used, err := f.r.AssetStepsUsed(ctx, "owner-b", "asset", f.steps.Counted); err != nil || used != 0 {
		t.Fatalf("another owner's asset = %d err=%v", used, err)
	}
	if _, err := f.r.AssetStepsUsed(ctx, "owner-a", "", f.steps.Counted); err == nil {
		t.Fatal("an empty asset was accepted")
	}
}

func TestAssetTitleIsOwnerScoped(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	f.session(t, "owner-a", "asset")
	if title, found, err := f.r.AssetTitle(ctx, "owner-a", "asset"); err != nil || !found || title != "test" {
		t.Fatalf("own asset = %q %v %v", title, found, err)
	}
	for name, ids := range map[string][2]string{"another owner": {"owner-b", "asset"}, "no session": {"owner-a", "missing"}, "empty": {"", ""}} {
		if title, found, err := f.r.AssetTitle(ctx, ids[0], ids[1]); err != nil || found || title != "" {
			t.Errorf("%s = %q %v %v; want not found", name, title, found, err)
		}
	}
}
