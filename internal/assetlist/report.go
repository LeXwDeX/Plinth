package assetlist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// A done report whose item must stay in progress (#459). A 3D result can be a
// success while the platform is still converting its other formats as
// post-success resources: the delivery is not stable, so the item must not
// enter review yet. The report is stored on the working item instead, and the
// runner moves the item into review once every format is terminal.
//
// DeferReport is the one write of that case; the completion tool calls it, and
// the runner's delivery tick calls FinishReportedItem (through the chat
// service) when the formats settled. A deployment whose runner never settles
// the item still has the report stored, so a person or the next turn can
// finish it from the same data.

// DeferReportInput is one deferred done report. ExpectedVersion is the item CAS
// the browser read, or VersionUnchecked when the caller holds no version.
type DeferReportInput struct {
	OwnerID         string
	ListID          string
	ItemID          string
	Result          json.RawMessage
	NextActionAt    time.Time
	ExpectedVersion int64
	At              time.Time
}

// DeferReport stores the report and defers the item without deciding it: the
// item keeps its state, and its next scheduling instant becomes nextActionAt so
// the loop stops opening turns until then. An empty report becomes {} (the
// stored "no result" shape), exactly as FinishItem stores it. A missing or
// foreign item is ErrNotFound; a lost CAS guard is ErrConflict.
func (service *Service) DeferReport(ctx context.Context, input DeferReportInput) (Item, error) {
	if service.runs == nil {
		return Item{}, ErrNotFound
	}
	if input.OwnerID == "" || input.ListID == "" || input.ItemID == "" {
		return Item{}, fmt.Errorf("%w: item identity", ErrInvalid)
	}
	if input.ExpectedVersion < 0 && input.ExpectedVersion != VersionUnchecked {
		return Item{}, fmt.Errorf("%w: expected version", ErrInvalid)
	}
	result := bytes.TrimSpace(input.Result)
	if len(result) == 0 {
		result = json.RawMessage("{}")
	}
	item, err := service.runs.SetItemResult(ctx, input.OwnerID, input.ListID,
		input.ItemID, result, input.ExpectedVersion, input.At)
	if err != nil {
		return Item{}, err
	}
	return service.runs.SetItemNextActionAt(ctx, input.OwnerID, input.ListID,
		input.ItemID, input.NextActionAt, item.Version, input.At)
}

// RescheduleDeferredReportInput is one new check time for a working item whose
// done report is stored and whose delivery is still converting (#459).
type RescheduleDeferredReportInput struct {
	OwnerID         string
	ListID          string
	ItemID          string
	NextActionAt    time.Time
	ExpectedVersion int64
	At              time.Time
}

// RescheduleDeferredReport moves the item's next scheduling instant without
// touching its state or its stored report. The runner's delivery tick calls it
// when a deferred report is still waiting, so the next check is one
// post-success observation interval ahead (capped by the Catalog) instead of
// every tick. A missing or foreign item is ErrNotFound; a lost CAS guard is
// ErrConflict.
func (service *Service) RescheduleDeferredReport(
	ctx context.Context,
	input RescheduleDeferredReportInput,
) (Item, error) {
	if service.runs == nil {
		return Item{}, ErrNotFound
	}
	if input.OwnerID == "" || input.ListID == "" || input.ItemID == "" {
		return Item{}, fmt.Errorf("%w: item identity", ErrInvalid)
	}
	if input.ExpectedVersion < 0 && input.ExpectedVersion != VersionUnchecked {
		return Item{}, fmt.Errorf("%w: expected version", ErrInvalid)
	}
	return service.runs.SetItemNextActionAt(ctx, input.OwnerID, input.ListID,
		input.ItemID, input.NextActionAt, input.ExpectedVersion, input.At)
}
