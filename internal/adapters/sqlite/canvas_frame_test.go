package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #246: the canvas pixel size is saved with the nodes in one transaction and one revision bump.
func TestCanvasFrameRoundTripWithNodesAndRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "canvas-frame.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "frame-session", OwnerID: "owner-a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	project, err := repository.CreateProject(ctx, canvas.Project{
		ID: "frame-project", OwnerID: "owner-a", Name: "Frame",
		SessionID: "frame-session", Viewport: "{}", BGStyle: "dots", CreatedAt: now, UpdatedAt: now,
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
	raw := openRawDB(t, path)
	var width, height, version sql.NullInt64
	readColumns := func() {
		t.Helper()
		if err := raw.QueryRow(
			`SELECT canvas_width, canvas_height, coordinate_version FROM go_canvas_projects WHERE id = ?`, project.ID,
		).Scan(&width, &height, &version); err != nil {
			t.Fatal(err)
		}
	}

	// A new or legacy project has no frame: NULL columns, nil Frame.
	if stored := load(); stored.Frame != nil {
		t.Fatalf("new project frame = %#v", stored.Frame)
	}
	readColumns()
	if width.Valid || height.Valid || version.Valid {
		t.Fatalf("new project columns = %v %v %v", width, height, version)
	}

	// Omitting the frame leaves it NULL.
	if replaced, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 1, []canvas.Node{{ID: "n1", Type: "image"}}, nil, nil, 50); err != nil || !replaced {
		t.Fatalf("replace without frame=%v error=%v", replaced, err)
	}
	if stored := load(); stored.Frame != nil || stored.Revision != 2 {
		t.Fatalf("after omitted frame = %#v", stored)
	}

	frame := canvas.Frame{Width: 3840, Height: 2160, CoordinateVersion: 2}
	if replaced, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 2, []canvas.Node{{ID: "n1", Type: "image"}}, nil, &frame, 50); err != nil || !replaced {
		t.Fatalf("replace with frame=%v error=%v", replaced, err)
	}
	if stored := load(); stored.Frame == nil || *stored.Frame != frame || stored.Revision != 3 {
		t.Fatalf("after frame = %#v", stored)
	}

	// Once sized, a save without a frame is refused and changes nothing.
	if _, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 3, nil, nil, nil, 50); !errors.Is(err, canvas.ErrCanvasFrameRequired) {
		t.Fatalf("frame-less replace on a sized canvas error=%v", err)
	}

	// A stale revision and an over-limit save both roll the frame back.
	other := canvas.Frame{Width: 1024, Height: 1024, CoordinateVersion: 2}
	if _, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 2, nil, nil, &other, 50); !errors.Is(err, canvas.ErrRevisionConflict) {
		t.Fatalf("stale replace error=%v", err)
	}
	if _, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, 3, bitmapNodes(2, 0), nil, &other, 1); !errors.Is(err, canvas.ErrBitmapLimitExceeded) {
		t.Fatalf("over-limit replace error=%v", err)
	}
	// Another owner can neither read nor overwrite it.
	if replaced, err := repository.ReplaceNodes(ctx, "owner-b", project.ID, 0, nil, nil, &other, 50); err != nil || replaced {
		t.Fatalf("cross-owner replace=%v error=%v", replaced, err)
	}
	if stored := load(); *stored.Frame != frame || stored.Revision != 3 {
		t.Fatalf("rejected writes changed the frame: %#v", stored)
	}

	// The session lookup returns it, and a metadata update keeps it.
	bySession, found, err := repository.GetProjectBySession(ctx, "owner-a", "frame-session")
	if err != nil || !found || bySession.Frame == nil || *bySession.Frame != frame {
		t.Fatalf("by session = %#v found=%v error=%v", bySession, found, err)
	}
	bySession.Name = "Renamed"
	if updated, err := repository.UpdateProject(ctx, bySession); err != nil || !updated {
		t.Fatalf("update=%v error=%v", updated, err)
	}
	if stored := load(); *stored.Frame != frame {
		t.Fatalf("metadata update changed the frame: %#v", stored.Frame)
	}

	// A partially written row (never produced by the repository) reads as a legacy canvas.
	if _, err := raw.Exec(`UPDATE go_canvas_projects SET coordinate_version = NULL WHERE id = ?`, project.ID); err != nil {
		t.Fatal(err)
	}
	if stored := load(); stored.Frame != nil {
		t.Fatalf("partial frame = %#v", stored.Frame)
	}
}

// #246: a project created by the service carries the current coordinate version from the start (its nodes are
// pixels even before a size is saved); one created without a version (imports, older rows) is legacy.
func TestCanvasCoordinateVersionIsStampedAtCreation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "canvas-version.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"pixel-session", "legacy-session"} {
		if _, err := repository.CreateSession(ctx, chat.Session{ID: id, OwnerID: "owner", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	pixel, err := repository.CreateProject(ctx, canvas.Project{ID: "pixel", OwnerID: "owner", Name: "p", SessionID: "pixel-session",
		Viewport: "{}", BGStyle: "dots", CoordinateVersion: 2, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := repository.CreateProject(ctx, canvas.Project{ID: "legacy", OwnerID: "owner", Name: "l", SessionID: "legacy-session",
		Viewport: "{}", BGStyle: "dots", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	stored, _, err := repository.GetProject(ctx, "owner", pixel.ID)
	if err != nil || stored.CoordinateVersion != 2 || stored.Frame != nil {
		t.Fatalf("pixel project = version %d frame %#v error=%v", stored.CoordinateVersion, stored.Frame, err)
	}
	stored, _, err = repository.GetProject(ctx, "owner", legacy.ID)
	if err != nil || stored.CoordinateVersion != 0 || stored.Frame != nil {
		t.Fatalf("legacy project = version %d frame %#v error=%v", stored.CoordinateVersion, stored.Frame, err)
	}
	// A pixel canvas refuses a save without a frame even before it is sized; only a legacy canvas accepts one.
	if _, err := repository.ReplaceNodes(ctx, "owner", pixel.ID, 1, []canvas.Node{{ID: "n", Type: "image"}}, nil, nil, 50); !errors.Is(err, canvas.ErrCanvasFrameRequired) {
		t.Fatalf("frame-less replace on an unsized pixel canvas error=%v", err)
	}
	if replaced, err := repository.ReplaceNodes(ctx, "owner", legacy.ID, 1, []canvas.Node{{ID: "n", Type: "image"}}, nil, nil, 50); err != nil || !replaced {
		t.Fatalf("frame-less replace on a legacy canvas=%v error=%v", replaced, err)
	}
}
