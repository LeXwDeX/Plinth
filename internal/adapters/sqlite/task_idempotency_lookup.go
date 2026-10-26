package sqlite

import (
	"context"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// GetByIdempotency recovers a committed Task by its owner-scoped admission key.
// A tombstoned Task still owns its key: report a conflict rather than implying
// that the caller may safely create a replacement paid Task.
func (r *Repository) GetByIdempotency(
	ctx context.Context,
	owner generation.OwnerID,
	key string,
) (generation.Task, error) {
	if owner == "" || key == "" {
		return generation.Task{}, generation.RepositoryNotFound
	}
	id, _, found, err := findIdempotent(ctx, r.db, owner, key)
	if err != nil {
		return generation.Task{}, err
	}
	if !found {
		return generation.Task{}, generation.RepositoryNotFound
	}
	task, err := r.Get(ctx, owner, id)
	if errors.Is(err, generation.RepositoryNotFound) {
		return generation.Task{}, generation.RepositoryConflict
	}
	return task, err
}

// TaskTombstonedByIdempotency reports whether the owner's key is still held by
// a tombstoned Task. GetByIdempotency returns RepositoryConflict for that case
// so callers do not create a replacement; this read distinguishes the
// deterministic tombstone from an unrelated conflict.
func (r *Repository) TaskTombstonedByIdempotency(ctx context.Context, owner generation.OwnerID, key string) (bool, error) {
	id, _, found, err := findIdempotent(ctx, r.db, owner, key)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return r.TaskTombstoned(ctx, id)
}
