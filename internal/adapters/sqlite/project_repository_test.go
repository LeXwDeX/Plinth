package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/project"
)

func openProjectRepository(t *testing.T) (*Repository, *ProjectRepository) {
	t.Helper()
	repository, err := openTestRepository(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository, repository.ProjectRepository()
}

func testProject(id, owner, name string) project.Project {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	return project.Project{ID: id, OwnerID: owner, Name: name, CreatedAt: now, UpdatedAt: now}
}

func TestProjectRepositoryIsOwnerScopedAndBounded(t *testing.T) {
	ctx := context.Background()
	_, projects := openProjectRepository(t)
	first, err := projects.CreateProject(ctx, testProject("p1", "owner-a", "One"), 2)
	if err != nil || first.Name != "One" {
		t.Fatalf("create = %+v err=%v", first, err)
	}
	if _, err := projects.CreateProject(ctx, testProject("p2", "owner-a", "Two"), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.CreateProject(ctx, testProject("p3", "owner-a", "Three"), 2); !errors.Is(err, project.ErrLimit) {
		t.Fatalf("limit err = %v", err)
	}
	if _, err := projects.CreateProject(ctx, testProject("other", "owner-b", "Other"), 2); err != nil {
		t.Fatal(err)
	}
	listed, err := projects.ListProjects(ctx, "owner-a", 10)
	if err != nil || len(listed) != 2 {
		t.Fatalf("list = %+v err=%v", listed, err)
	}
	if _, err := projects.GetProject(ctx, "owner-b", "p1"); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign get err = %v", err)
	}
	if _, err := projects.RenameProject(ctx, "owner-b", "p1", "Stolen", time.Now()); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign rename err = %v", err)
	}
	renamed, err := projects.RenameProject(ctx, "owner-a", "p1", "Renamed", time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC))
	if err != nil || renamed.Name != "Renamed" {
		t.Fatalf("rename = %+v err=%v", renamed, err)
	}

	now := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	guide := project.StyleGuide{
		ProjectID: "p1", Enabled: true, Text: "ink", Palette: []string{"#112233"},
		References: []project.Reference{{InputAssetID: "picture-1"}}, UpdatedAt: now,
	}
	if _, err := projects.SetStyleGuide(ctx, "owner-b", "p1", guide); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign style guide err = %v", err)
	}
	if stored, err := projects.SetStyleGuide(ctx, "owner-a", "p1", guide); err != nil || !stored.Enabled || stored.Text != "ink" || len(stored.Palette) != 1 || stored.References[0].InputAssetID != "picture-1" {
		t.Fatalf("style guide = %+v err=%v", stored, err)
	}
	if stored, err := projects.StyleGuide(ctx, "owner-a", "p2"); err != nil || stored.Enabled || stored.Text != "" || len(stored.References) != 0 {
		t.Fatalf("default style guide = %+v err=%v", stored, err)
	}
	defaultGuide, err := projects.StyleGuide(ctx, "owner-b", "other")
	if err != nil || defaultGuide.ProjectID != "other" {
		t.Fatalf("other default guide = %+v err=%v", defaultGuide, err)
	}

	conventions := project.Conventions{
		ProjectID: "p1", Brief: []byte(`{"purpose":"game_character"}`),
		Texts: map[string]string{"naming": "hero_"}, UpdatedAt: now,
	}
	if _, err := projects.SetConventions(ctx, "owner-b", "p1", conventions); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign conventions err = %v", err)
	}
	if stored, err := projects.SetConventions(ctx, "owner-a", "p1", conventions); err != nil || string(stored.Brief) != `{"purpose":"game_character"}` || stored.Texts["naming"] != "hero_" {
		t.Fatalf("conventions = %+v err=%v", stored, err)
	}

	asset := project.Asset{
		ID: "a1", ProjectID: "p1", OwnerID: "owner-a", Kind: "character", Name: "Hero",
		Images: []project.Reference{{InputAssetID: "picture-1"}}, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := projects.CreateAsset(ctx, "p1", asset, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.CreateAsset(ctx, "p1", project.Asset{ID: "a2", OwnerID: "owner-a", Kind: "prop", Name: "Chair", CreatedAt: now, UpdatedAt: now}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.CreateAsset(ctx, "p1", project.Asset{ID: "a3", OwnerID: "owner-a", Kind: "prop", Name: "Third", CreatedAt: now, UpdatedAt: now}, 2); !errors.Is(err, project.ErrLimit) {
		t.Fatalf("asset limit err = %v", err)
	}
	if _, err := projects.CreateAsset(ctx, "p1", project.Asset{ID: "foreign", OwnerID: "owner-b", Kind: "prop", Name: "Foreign"}, 2); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign asset create err = %v", err)
	}
	if listed, err := projects.ListAssets(ctx, "owner-a", "p1", 10); err != nil || len(listed) != 2 {
		t.Fatalf("assets = %+v err=%v", listed, err)
	}
	if _, err := projects.ListAssets(ctx, "owner-b", "p1", 10); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign assets list err = %v", err)
	}
	if _, err := projects.GetAsset(ctx, "owner-b", "p1", "a1"); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign asset get err = %v", err)
	}
	updated, err := projects.UpdateAsset(ctx, "owner-a", "p1", "a1", "prop", "Renamed", "desc", now)
	if err != nil || updated.Kind != "prop" || updated.Name != "Renamed" || updated.Description != "desc" {
		t.Fatalf("update asset = %+v err=%v", updated, err)
	}
	if _, err := projects.UpdateAsset(ctx, "owner-b", "p1", "a1", "prop", "Stolen", "", now); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign update err = %v", err)
	}
	if found, err := projects.DeleteAsset(ctx, "owner-b", "p1", "a1"); err != nil || found {
		t.Fatalf("foreign delete = %v %v", found, err)
	}
	if found, err := projects.DeleteAsset(ctx, "owner-a", "p1", "a1"); err != nil || !found {
		t.Fatalf("delete = %v %v", found, err)
	}
	if _, err := projects.GetAsset(ctx, "owner-a", "p1", "a1"); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("deleted asset get err = %v", err)
	}

	if err := projects.DeleteProject(ctx, "owner-b", "p1"); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign project delete err = %v", err)
	}
	if err := projects.DeleteProject(ctx, "owner-a", "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.GetProject(ctx, "owner-a", "p1"); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("deleted project err = %v", err)
	}
	if listed, err := projects.ListAssets(ctx, "owner-a", "p1", 10); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("assets after delete = %+v err=%v", listed, err)
	}
}

func insertTaskFixture(t *testing.T, repository *Repository, owner, task, attempt, assetID, role, mediaType, state string) {
	t.Helper()
	now := "2026-11-11T09:00:00Z"
	if _, err := repository.db.Exec(`
		INSERT INTO generation_tasks (id, owner_id, capability, offering_json, input_json, request_hash, state, progress, version, created_at, updated_at)
		VALUES (?, ?, 'text-to-image', '{}', '{}', 'hash', ?, 0, 1, ?, ?)`,
		task, owner, state, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(`
		INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key, started_at, updated_at)
		VALUES (?, ?, 0, '{}', 'SUCCEEDED', 'submit', ?, ?)`, attempt, task, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.Exec(`
		INSERT INTO generation_assets (id, task_id, attempt_id, role, required, ordinal, storage_key, media_type, size_bytes, sha256, created_at)
		VALUES (?, ?, ?, ?, 0, 0, 'blobs/x', ?, 3, 'sha', ?)`,
		assetID, task, attempt, role, mediaType, now); err != nil {
		t.Fatal(err)
	}
}

func TestProjectAssetSourceVerifiesOwnerStateAndMedia(t *testing.T) {
	ctx := context.Background()
	repository, _ := openProjectRepository(t)
	source := repository.ProjectAssetSource()
	now := "2026-11-11T09:00:00Z"
	if _, err := repository.db.Exec(`
		INSERT INTO generation_input_assets (id, owner_id, storage_key, media_type, size_bytes, sha256, created_at)
		VALUES ('picture-a', 'owner-a', 'blobs/a', 'image/png', 3, 'sha-a', ?),
		       ('picture-b', 'owner-b', 'blobs/b', 'image/png', 3, 'sha-b', ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	insertTaskFixture(t, repository, "owner-a", "task-image", "attempt-image", "asset-image", "image", "image/png", "SUCCEEDED")
	insertTaskFixture(t, repository, "owner-a", "task-model", "attempt-model", "asset-model", "model", "model/gltf-binary", "SUCCEEDED")
	insertTaskFixture(t, repository, "owner-b", "task-foreign", "attempt-foreign", "asset-foreign", "image", "image/png", "SUCCEEDED")

	for _, probe := range []struct {
		name      string
		reference project.Reference
		want      bool
	}{
		{"own input picture", project.Reference{InputAssetID: "picture-a"}, true},
		{"foreign input picture", project.Reference{InputAssetID: "picture-b"}, false},
		{"own task picture", project.Reference{TaskID: "task-image", AssetID: "asset-image"}, true},
		{"own task model as picture", project.Reference{TaskID: "task-model", AssetID: "asset-model"}, false},
		{"foreign task picture", project.Reference{TaskID: "task-foreign", AssetID: "asset-foreign"}, false},
		{"unknown asset", project.Reference{TaskID: "task-image", AssetID: "missing"}, false},
		{"invalid shape", project.Reference{AssetID: "asset-image"}, false},
	} {
		_, err := source.Picture(ctx, "owner-a", probe.reference)
		if got := err == nil; got != probe.want {
			t.Errorf("%s: ok=%v err=%v", probe.name, got, err)
		}
	}
	for _, probe := range []struct {
		name      string
		reference project.Reference
		want      bool
	}{
		{"own task model", project.Reference{TaskID: "task-model", AssetID: "asset-model"}, true},
		{"own task picture as model", project.Reference{TaskID: "task-image", AssetID: "asset-image"}, false},
		{"foreign task model", project.Reference{TaskID: "task-foreign", AssetID: "asset-foreign"}, false},
	} {
		_, err := source.Model(ctx, "owner-a", probe.reference)
		if got := err == nil; got != probe.want {
			t.Errorf("%s: ok=%v err=%v", probe.name, got, err)
		}
	}

	if _, err := repository.db.Exec(`UPDATE generation_tasks SET state = 'FAILED' WHERE id = 'task-image'`); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Picture(ctx, "owner-a", project.Reference{TaskID: "task-image", AssetID: "asset-image"}); err == nil {
		t.Fatal("a failed Task asset was accepted")
	}
}

func TestDeletingProjectKeepsGenerationAssetsAndUnassignsSessions(t *testing.T) {
	ctx := context.Background()
	repository, projects := openProjectRepository(t)
	insertTaskFixture(t, repository, "owner-a", "task-1", "attempt-1", "asset-1", "image", "image/png", "SUCCEEDED")
	if _, err := projects.CreateProject(ctx, testProject("p1", "owner-a", "One"), 10); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	if _, err := projects.SetStyleGuide(ctx, "owner-a", "p1", project.StyleGuide{
		ProjectID: "p1", Enabled: true, References: []project.Reference{{TaskID: "task-1", AssetID: "asset-1"}}, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.CreateAsset(ctx, "p1", project.Asset{
		ID: "project-asset", OwnerID: "owner-a", Kind: "character", Name: "Hero",
		Images: []project.Reference{{TaskID: "task-1", AssetID: "asset-1"}}, CreatedAt: now, UpdatedAt: now,
	}, 10); err != nil {
		t.Fatal(err)
	}
	createApprovalSession(t, repository, "session-a", "owner-a", chat.Session{ProjectID: "p1"})
	if err := projects.DeleteProject(ctx, "owner-a", "p1"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM project_assets`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("project assets after delete = %d err=%v", count, err)
	}
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM project_style_guides`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("style guides after delete = %d err=%v", count, err)
	}
	if err := repository.db.QueryRow(`SELECT COUNT(*) FROM generation_assets WHERE id = 'asset-1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("generation assets after delete = %d err=%v", count, err)
	}
	session, found, err := repository.Session(ctx, "owner-a", "session-a")
	if err != nil || !found || session.ProjectID != "" {
		t.Fatalf("session after project delete = %+v found=%v err=%v", session, found, err)
	}
}

// openRepositoryAtHead builds the schema exactly as an older binary left it,
// then closes it so the current binary applies the remaining migrations.
func openRepositoryAtHead(t *testing.T, path string, head int) *Repository {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	repository := &Repository{db: db, clock: func() time.Time { return time.Now().UTC() }}
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS go_schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	all := migrations()
	if head < 1 || head > len(all) {
		t.Fatalf("invalid head %d", head)
	}
	if err := repository.runMigrations(context.Background(), all[:head]); err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestMigration0090BackfillsExistingStyleLockSource(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "source-backfill.db")
	pre := openRepositoryAtHead(t, path, 88)
	now := "2026-01-01T00:00:00Z"
	for _, row := range []struct {
		id, enabled, text, references string
	}{
		{"enabled", "1", "", "[]"},
		{"text", "0", "ink", "[]"},
		{"references", "0", "", `["asset-1"]`},
		{"empty", "0", "", "[]"},
	} {
		if _, err := pre.db.Exec(`INSERT INTO go_chat_sessions
			(id, owner_id, owner_name, title, created_at, updated_at, style_lock_enabled, style_lock_text, style_lock_references)
			VALUES (?, 'owner-a', 'owner', 'old', ?, ?, ?, ?, ?)`,
			row.id, now, now, row.enabled, row.text, row.references); err != nil {
			t.Fatal(err)
		}
	}
	if err := pre.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for id, want := range map[string]string{
		"enabled": chat.StyleLockSourceSession, "text": chat.StyleLockSourceSession,
		"references": chat.StyleLockSourceSession, "empty": chat.StyleLockSourceProject,
	} {
		session, found, err := repository.Session(ctx, "owner-a", id)
		if err != nil || !found || session.StyleLockSource != want {
			t.Errorf("%s source = %q found=%v err=%v; want %q", id, session.StyleLockSource, found, err, want)
		}
	}
	var head int
	if err := repository.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head != MigrationHead() {
		t.Fatalf("head = %d err=%v; want %d", head, err, MigrationHead())
	}
}

func TestBranchCopiesProjectAndStyleSource(t *testing.T) {
	ctx := context.Background()
	repository, _ := openProjectRepository(t)
	createApprovalSession(t, repository, "parent", "owner-a", chat.Session{
		Title: "Parent", ProjectID: "prj_1", StyleLockSource: chat.StyleLockSourceSession,
	})
	if _, err := repository.db.Exec(`INSERT INTO go_chat_messages (id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at)
		VALUES ('m1','parent','user','one','[]','[]','[]','','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	branch, err := repository.BranchSession(ctx, chat.BranchRequest{
		OwnerID: "owner-a", SessionID: "parent", MessageID: "m1", NewSessionID: "branch",
		TitleTemplate: "{title}（分支）", At: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC),
		NewID:        func(string) string { return "new" },
		ActiveStates: []string{"running", "queued", "waiting_task"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if branch.ProjectID != "prj_1" || branch.StyleLockSource != chat.StyleLockSourceSession {
		t.Fatalf("branch project/source = %q/%q", branch.ProjectID, branch.StyleLockSource)
	}
}

func TestProjectRegisteredPictureChecksOnlyTheOwnerProjectReferences(t *testing.T) {
	ctx := context.Background()
	_, projects := openProjectRepository(t)
	if _, err := projects.CreateProject(ctx, testProject("p1", "owner-a", "One"), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.CreateProject(ctx, testProject("p2", "owner-b", "Other"), 10); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 12, 2, 0, 0, 0, time.UTC)
	if _, err := projects.SetStyleGuide(ctx, "owner-a", "p1", project.StyleGuide{
		ProjectID: "p1", Enabled: true,
		References: []project.Reference{{TaskID: "task-guide", AssetID: "asset-guide"}},
		UpdatedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.CreateAsset(ctx, "p1", project.Asset{
		ID: "passet_1", ProjectID: "p1", OwnerID: "owner-a", Kind: "character", Name: "Role A",
		Images:    []project.Reference{{TaskID: "task-asset", AssetID: "asset-asset"}},
		CreatedAt: now, UpdatedAt: now,
	}, 10); err != nil {
		t.Fatal(err)
	}
	for name, probe := range map[string]struct {
		owner, project, task, asset string
		want                        bool
	}{
		"asset picture":       {"owner-a", "p1", "task-asset", "asset-asset", true},
		"style guide picture": {"owner-a", "p1", "task-guide", "asset-guide", true},
		"wrong asset":         {"owner-a", "p1", "task-asset", "asset-other", false},
		"other project":       {"owner-a", "p2", "task-asset", "asset-asset", false},
		"other owner":         {"owner-b", "p1", "task-asset", "asset-asset", false},
		"unknown project":     {"owner-a", "p9", "task-asset", "asset-asset", false},
	} {
		got, err := projects.RegisteredPicture(ctx, probe.owner, probe.project, probe.task, probe.asset)
		if err != nil || got != probe.want {
			t.Errorf("%s = %v err=%v want %v", name, got, err, probe.want)
		}
	}
}

func TestProjectMigrationsCreateExpectedTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.db")
	repository, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	tables := map[string]bool{}
	rows, err := repository.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = true
	}
	for _, name := range []string{"projects", "project_style_guides", "project_conventions", "project_assets"} {
		if !tables[name] {
			t.Errorf("missing migration table %s", name)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
