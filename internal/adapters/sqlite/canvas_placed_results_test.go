package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestCanvasPlacedResultsColumnIsNullableAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canvas-placed-shape.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	raw := openRawDB(t, path)
	var (
		name    string
		notNull int
	)
	if err := raw.QueryRow(
		`SELECT name, "notnull" FROM pragma_table_info('go_canvas_projects') WHERE name = 'placed_results_json'`,
	).Scan(&name, &notNull); err != nil || notNull != 0 {
		t.Fatalf("placed_results_json column=%q notnull=%d error=%v", name, notNull, err)
	}
}

func TestCanvasPlacedResultsRoundTripWithNodesAndRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "canvas-placed.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "placed-session", OwnerID: "owner-a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	project, err := repository.CreateProject(ctx, canvas.Project{
		ID: "placed-project", OwnerID: "owner-a", Name: "Placed",
		SessionID: "placed-session", Viewport: "{}", BGStyle: "dots", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	load := func() canvas.Project {
		t.Helper()
		stored, found, err := repository.GetProject(ctx, "owner-a", project.ID)
		if err != nil || !found {
			t.Fatalf("get project found=%v error=%v", found, err)
		}
		return stored
	}

	// A legacy/new project has never been seeded: NULL, distinct from [].
	if stored := load(); stored.PlacedResultsKnown || stored.PlacedResults != nil {
		t.Fatalf("unseeded project = known:%v %#v", stored.PlacedResultsKnown, stored.PlacedResults)
	}
	raw := openRawDB(t, path)
	var column sql.NullString
	if err := raw.QueryRow(`SELECT placed_results_json FROM go_canvas_projects WHERE id = ?`, project.ID).Scan(&column); err != nil || column.Valid {
		t.Fatalf("unseeded column = %#v error=%v", column, err)
	}

	// Omitting placed results (nil) leaves the stored NULL untouched.
	if replaced, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 1, []canvas.Node{{ID: "n1", Type: "image"}}, nil, nil, 50); err != nil || !replaced {
		t.Fatalf("replace without placed results=%v error=%v", replaced, err)
	}
	if stored := load(); stored.PlacedResultsKnown || stored.Revision != 2 || len(stored.Nodes) != 1 {
		t.Fatalf("after omitted placed results = %#v", stored)
	}

	// Initialized but empty is stored as [] and read back as known + empty.
	empty := []string{}
	if replaced, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 2, []canvas.Node{{ID: "n1", Type: "image"}}, &empty, nil, 50); err != nil || !replaced {
		t.Fatalf("replace with empty placed results=%v error=%v", replaced, err)
	}
	if stored := load(); !stored.PlacedResultsKnown || stored.PlacedResults == nil || len(stored.PlacedResults) != 0 || stored.Revision != 3 {
		t.Fatalf("after empty placed results = known:%v %#v revision=%d", stored.PlacedResultsKnown, stored.PlacedResults, stored.Revision)
	}
	if err := raw.QueryRow(`SELECT placed_results_json FROM go_canvas_projects WHERE id = ?`, project.ID).Scan(&column); err != nil || column.String != "[]" {
		t.Fatalf("empty column = %#v error=%v", column, err)
	}

	// Values are stored with the nodes in the same revision bump, order preserved.
	values := []string{"task-2#asset-b", "task-1#/api/assets/a"}
	if replaced, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 3, []canvas.Node{}, &values, nil, 50); err != nil || !replaced {
		t.Fatalf("replace with placed results=%v error=%v", replaced, err)
	}
	stored := load()
	if !stored.PlacedResultsKnown || !reflect.DeepEqual(stored.PlacedResults, values) || stored.Revision != 4 || len(stored.Nodes) != 0 {
		t.Fatalf("after values = %#v", stored)
	}

	// A stale revision rejects both nodes and placed results atomically.
	stale := []string{"stale#x"}
	if _, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 3, []canvas.Node{{ID: "stale", Type: "image"}}, &stale, nil, 50); !errors.Is(err, canvas.ErrRevisionConflict) {
		t.Fatalf("stale replace error=%v", err)
	}
	// An over-limit save also rolls back the placed results.
	if _, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 4, bitmapNodes(2, 0), &stale, nil, 1); !errors.Is(err, canvas.ErrBitmapLimitExceeded) {
		t.Fatalf("over-limit replace error=%v", err)
	}
	// Another owner can neither read nor overwrite the record.
	if replaced, err := repository.ReplaceNodes(ctx, "owner-b", project.ID, 0, nil, &stale, nil, 50); err != nil || replaced {
		t.Fatalf("cross-owner replace=%v error=%v", replaced, err)
	}
	if _, found, err := repository.GetProject(ctx, "owner-b", project.ID); err != nil || found {
		t.Fatalf("cross-owner get found=%v error=%v", found, err)
	}
	if after := load(); !reflect.DeepEqual(after.PlacedResults, values) || after.Revision != 4 || len(after.Nodes) != 0 {
		t.Fatalf("rejected writes changed the record: %#v", after)
	}

	// Session lookup returns the same record; metadata updates leave it intact.
	bySession, found, err := repository.GetProjectBySession(ctx, "owner-a", "placed-session")
	if err != nil || !found || !reflect.DeepEqual(bySession.PlacedResults, values) {
		t.Fatalf("by session = %#v found=%v error=%v", bySession, found, err)
	}
	bySession.Name = "Renamed"
	if updated, err := repository.UpdateProject(ctx, bySession); err != nil || !updated {
		t.Fatalf("update=%v error=%v", updated, err)
	}
	if after := load(); !reflect.DeepEqual(after.PlacedResults, values) {
		t.Fatalf("metadata update changed placed results: %#v", after.PlacedResults)
	}
}
