package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func bitmapNodes(count int, vectorCount int) []canvas.Node {
	nodes := make([]canvas.Node, 0, count+vectorCount)
	for index := range count {
		nodes = append(nodes, canvas.Node{ID: fmt.Sprintf("image-%02d", index), Type: "image"})
	}
	for index := range vectorCount {
		nodes = append(nodes, canvas.Node{ID: fmt.Sprintf("shape-%02d", index), Type: "shape"})
	}
	return nodes
}

func TestCanvasBitmapLimitIsAtomicWithRevisionAndOwner(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "canvas-bitmap-limit.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "bitmap-session", OwnerID: "owner-a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	project, err := repository.CreateProject(ctx, canvas.Project{
		ID: "bitmap-project", OwnerID: "owner-a", Name: "Bitmap limit",
		SessionID: "bitmap-session", Viewport: "{}", BGStyle: "dots", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		name             string
		bitmapCount      int
		vectorCount      int
		expectedRevision int64
		wantErr          error
		wantRevision     int64
		wantNodeCount    int
	}{
		{name: "49 images", bitmapCount: 49, expectedRevision: 1, wantRevision: 2, wantNodeCount: 49},
		{name: "50 images and vectors", bitmapCount: 50, vectorCount: 3, expectedRevision: 2, wantRevision: 3, wantNodeCount: 53},
		{name: "51 images rejected", bitmapCount: 51, vectorCount: 3, expectedRevision: 3, wantErr: canvas.ErrBitmapLimitExceeded, wantRevision: 3, wantNodeCount: 53},
	} {
		t.Run(step.name, func(t *testing.T) {
			replaced, err := repository.ReplaceNodes(ctx, "owner-a", project.ID, step.expectedRevision, bitmapNodes(step.bitmapCount, step.vectorCount), nil, nil, 50)
			if !errors.Is(err, step.wantErr) || replaced != (step.wantErr == nil) {
				t.Fatalf("replace=%v error=%v, want %v", replaced, err, step.wantErr)
			}
			stored, found, err := repository.GetProject(ctx, "owner-a", project.ID)
			if err != nil || !found || stored.Revision != step.wantRevision || len(stored.Nodes) != step.wantNodeCount {
				t.Fatalf("stored found=%v error=%v revision=%d nodes=%d", found, err, stored.Revision, len(stored.Nodes))
			}
		})
	}
	// Authorization happens before the limit check and cannot expose or mutate
	// another owner's project, even for an otherwise over-limit payload.
	replaced, err := repository.ReplaceNodes(ctx, "owner-b", project.ID, 3, bitmapNodes(51, 0), nil, nil, 50)
	if err != nil || replaced {
		t.Fatalf("cross-owner replace=%v error=%v", replaced, err)
	}
}

func TestCanvasConcurrentReplaceFrom49SerializesRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "canvas-bitmap-race.db")
	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := first.CreateSession(ctx, chat.Session{ID: "concurrent-bitmap-session", OwnerID: "owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	project, err := first.CreateProject(ctx, canvas.Project{
		ID: "concurrent-bitmap-project", OwnerID: "owner", Name: "Concurrent",
		SessionID: "concurrent-bitmap-session", Viewport: "{}", BGStyle: "dots", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replaced, err := first.ReplaceNodes(ctx, "owner", project.ID, 1, bitmapNodes(49, 0), nil, nil, 50); err != nil || !replaced {
		t.Fatalf("seed 49: replaced=%v error=%v", replaced, err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, repository := range []*sqlite.Repository{first, second} {
		workers.Add(1)
		go func(repository *sqlite.Repository) {
			defer workers.Done()
			<-start
			_, err := repository.ReplaceNodes(ctx, "owner", project.ID, 2, bitmapNodes(50, 0), nil, nil, 50)
			results <- err
		}(repository)
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, canvas.ErrRevisionConflict):
			conflicts++
		default:
			t.Fatalf("concurrent replace error=%v", err)
		}
	}
	stored, found, err := first.GetProject(ctx, "owner", project.ID)
	if successes != 1 || conflicts != 1 || err != nil || !found || stored.Revision != 3 || len(stored.Nodes) != 50 {
		t.Fatalf("successes=%d conflicts=%d found=%v error=%v revision=%d nodes=%d", successes, conflicts, found, err, stored.Revision, len(stored.Nodes))
	}
}
