package chat

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

var ErrInvalidHarnessSnapshot = errors.New("chat: invalid server Harness snapshot")

// HarnessSnapshotResolver reads the authenticated session's selection and its
// readiness. It cannot accept a client snapshot or create execution work.
type HarnessSnapshotResolver func(context.Context, string, string) (resolve.Snapshot, error)

// SetHarnessSnapshotResolver installs the resolver once at application assembly,
// before requests start, just like SetAttachmentVerifier. Historical turns are
// never resolved again; only StartTurn calls it to freeze new turn options.
func (service *Service) SetHarnessSnapshotResolver(resolver HarnessSnapshotResolver) {
	service.harnessSnapshotResolver = resolver
}

func (service *Service) freezeHarnessSnapshot(ctx context.Context, owner, session string, selection []Attachment) (*resolve.Snapshot, error) {
	if service.harnessSnapshotResolver == nil {
		return nil, nil
	}
	preferred := []harness.Source{}
	for _, a := range selection {
		if a.Kind == AttachmentKindModel && a.TaskID != "" {
			preferred = append(preferred, harness.Source{TaskID: a.TaskID, AssetID: a.AssetID})
		}
	}
	ctx = resolve.WithPreferredSources(ctx, preferred)
	snapshot, err := service.harnessSnapshotResolver(ctx, owner, session)
	if err != nil {
		return nil, err
	}
	selected := snapshot.Selection
	if selected.OwnerID != owner || selected.SessionID != session || !selected.Binding.Valid() || selected.Digest == "" {
		return nil, ErrInvalidHarnessSnapshot
	}
	normalized, err := harness.NormalizeSelection(selected)
	if err != nil || normalized.Digest != selected.Digest {
		return nil, ErrInvalidHarnessSnapshot
	}
	// Clone the same safe shape that SQLite options_json persists. This detaches
	// nested maps, RawMessage bytes and slices, and drops private runtime pointers.
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	var frozen resolve.Snapshot
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	// OwnerID is deliberately absent from JSON; authenticated turn scope restores
	// it for in-memory consumers without putting it in the serialized selection.
	frozen.Selection.OwnerID = owner
	return &frozen, nil
}
