package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLookupByIdempotencyIsOwnerScopedAndTombstoneStopsReplay(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "task.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	now := time.Now().UTC()
	task := generation.Task{
		ID: "accepted-task", OwnerID: "owner-a", Capability: generation.TextToImage,
		Offering: generation.Offering{ID: "test/offering", ProviderID: "test", ModelID: "test", Capability: generation.TextToImage},
		Input:    json.RawMessage(`{"prompt":"existing"}`), RequestHash: "frozen-input-hash",
		IdempotencyKey: "processing:plan:0:0", State: generation.TaskQueued,
		Version: 1, CreatedAt: now, UpdatedAt: now, NextActionAt: now,
	}
	task = approveCreateFixture(t, repository, task)
	if _, replayed, err := repository.Create(ctx, task, generation.Event{
		TaskID: task.ID, Type: "task.submitted", Data: json.RawMessage(`{}`), CreatedAt: now,
	}, 0); err != nil || replayed {
		t.Fatalf("create accepted task replayed=%v err=%v", replayed, err)
	}
	got, err := repository.GetByIdempotency(ctx, "owner-a", task.IdempotencyKey)
	if err != nil || got.ID != task.ID || got.RequestHash != task.RequestHash {
		t.Fatalf("lookup task=%+v err=%v", got, err)
	}
	for _, query := range []struct {
		owner generation.OwnerID
		key   string
	}{
		{"owner-b", task.IdempotencyKey}, {"owner-a", "another-key"},
	} {
		if _, err := repository.GetByIdempotency(ctx, query.owner, query.key); !errors.Is(err, generation.RepositoryNotFound) {
			t.Fatalf("lookup owner=%q key=%q err=%v", query.owner, query.key, err)
		}
	}
	if err := repository.Tombstone(ctx, generation.Tombstone{
		TaskID: task.ID, ActorID: "admin", ExpectedVersion: task.Version,
		DeletedAt: now.Add(time.Second), Event: generation.Event{TaskID: task.ID, Type: "task.deleted", Data: json.RawMessage(`{}`), CreatedAt: now.Add(time.Second)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetByIdempotency(ctx, "owner-a", task.IdempotencyKey); !errors.Is(err, generation.RepositoryConflict) {
		t.Fatalf("tombstoned paid key should remain reserved, got %v", err)
	}
}
