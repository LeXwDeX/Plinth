package sqlite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

// fittingGateway is the continuation gateway behind the kernel's input
// fitting (#276): a source picture over the Offering's limits is frozen as a
// re-encoded copy whose derivation is recorded in generation_input_assets, as
// input.Service.Fit does. Like the blob store, saving the copy fails once the
// context it is given is canceled. compress runs while the copy is made, so a
// test can drop the request there.
type fittingGateway struct {
	*continuationGateway
	source, fitted generation.AssetID
	compress       func()
	copies         int
}

func (g *fittingGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	if bytes.Contains(cmd.Input, quoted(g.source)) {
		if _, err := g.repo.GetInputAsset(ctx, g.fitted); err != nil {
			if g.compress != nil {
				g.compress()
			}
			if err := ctx.Err(); err != nil {
				return generation.PreparedInput{}, fmt.Errorf("写入资产内容: %w", err)
			}
			g.copies++
			if err := g.repo.CreateDerivedInputAsset(ctx, generation.InputAsset{ID: g.fitted, OwnerID: cmd.OwnerID,
				StorageKey: "blobs/" + string(g.fitted), OriginalFilename: "front.jpg", AssetType: "image",
				MediaType: "image/jpeg", Size: 4, SHA256: "sha", CreatedAt: time.Now().UTC()},
				generation.InputAssetDerivation{SourceAssetID: g.source, Method: "jpeg-q90", SourceSize: 40}); err != nil {
				return generation.PreparedInput{}, err
			}
		}
		cmd.Input = bytes.ReplaceAll(cmd.Input, quoted(g.source), quoted(g.fitted))
	}
	return g.continuationGateway.PrepareInput(ctx, cmd)
}

func quoted(id generation.AssetID) []byte { return []byte(`"` + string(id) + `"`) }

// InputAssetDerivationSource is generation.Module's derivation proof.
func (g *fittingGateway) InputAssetDerivationSource(ctx context.Context, owner generation.OwnerID,
	id generation.AssetID,
) (generation.AssetID, bool, error) {
	derivation, found, err := g.repo.InputAssetDerivation(ctx, owner, id)
	return derivation.SourceAssetID, found, err
}

// newFittedFrontFixture finishes the front view as a picture the kernel must
// re-encode before a later view can use it.
func newFittedFrontFixture(t *testing.T) (*continuationFixture, *fittingGateway) {
	t.Helper()
	f := newContinuationFixture(t, true)
	gateway := &fittingGateway{continuationGateway: f.gateway, source: "asset-front", fitted: "asset-front-fit"}
	f.runtime.Generation = gateway
	frontTask := f.startFront(t)
	f.finish(t, frontTask, "asset-front")
	return f, gateway
}

var fittedPerson = executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}

// #629: a later view whose source picture the kernel re-encodes is frozen with
// the copy, not the source. The SkillRun checkpoint takes the copy the
// derivation record proves was made from the source, so the view starts
// instead of answering three_view_conflict on every click.
func TestViewWithAFittedSourceStartsWithoutConflict(t *testing.T) {
	f, gateway := newFittedFrontFixture(t)
	ctx := context.Background()
	group, err := f.views.Advance(ctx, fittedPerson, "session-a", f.run.ID, "left", threeview.Start, 0)
	if err != nil {
		t.Fatalf("start left with a fitted source: %v", err)
	}
	left := slotOf(group, "left")
	if left.TaskID == "" || left.Status != threeview.Running || left.Startable {
		t.Fatalf("left after the start = %+v", left)
	}
	prepared, err := f.repo.GetPreparedInput(ctx, "owner-a", f.step(t, "left").PreparedInputID)
	if err != nil || !bytes.Contains(prepared.NormalizedInput, quoted(gateway.fitted)) ||
		bytes.Contains(prepared.NormalizedInput, quoted(gateway.source)) {
		t.Fatalf("left froze %s err=%v, want the fitted copy", prepared.NormalizedInput, err)
	}
	// The sibling view uses the same copy, made once.
	group, err = f.views.Advance(ctx, fittedPerson, "session-a", f.run.ID, "back", threeview.Start, 0)
	if err != nil || slotOf(group, "back").TaskID == "" {
		t.Fatalf("start back = %+v err=%v", slotOf(group, "back"), err)
	}
	if gateway.copies != 1 {
		t.Fatalf("copies = %d, want 1", gateway.copies)
	}
}

// #629: a copy of another picture is still refused. Only a copy whose
// derivation names the expected source stands for it.
func TestViewFrozenWithAnUnrelatedCopyStaysAConflict(t *testing.T) {
	f, gateway := newFittedFrontFixture(t)
	ctx := context.Background()
	if err := f.repo.CreateDerivedInputAsset(ctx, generation.InputAsset{ID: "asset-other-fit", OwnerID: "owner-a",
		StorageKey: "blobs/other", OriginalFilename: "other.jpg", AssetType: "image", MediaType: "image/jpeg",
		Size: 4, SHA256: "sha", CreatedAt: time.Now().UTC()},
		generation.InputAssetDerivation{SourceAssetID: "asset-other", Method: "jpeg-q90", SourceSize: 40}); err != nil {
		t.Fatal(err)
	}
	gateway.fitted = "asset-other-fit"
	_, err := f.views.Advance(ctx, fittedPerson, "session-a", f.run.ID, "left", threeview.Start, 0)
	if !errors.Is(err, threeview.ErrConflict) {
		t.Fatalf("start left with a copy of another picture: err=%v, want a conflict", err)
	}
}

// #629: the person's request is dropped while the copy is being made (the
// client disconnects during a long re-encode). The start does not depend on
// that request: the copy is kept, the view starts, the listing agrees with
// the server, and starting the same view or its sibling again is no conflict.
func TestCanceledStartOfAFittedViewDoesNotBlockTheGroup(t *testing.T) {
	f, gateway := newFittedFrontFixture(t)
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateway.compress = cancel
	// The answer goes to a client that has gone; only what the server kept matters.
	_, _ = f.views.Advance(request, fittedPerson, "session-a", f.run.ID, "left", threeview.Start, 0)
	gateway.compress = nil

	left := slotOf(f.group(t), "left")
	if left.TaskID == "" || left.Startable || left.Status != threeview.Running {
		t.Fatalf("left after the dropped request = %+v, want it started", left)
	}
	ctx := context.Background()
	again, err := f.views.Advance(ctx, fittedPerson, "session-a", f.run.ID, "left", threeview.Start, 0)
	if err != nil || slotOf(again, "left").TaskID != left.TaskID {
		t.Fatalf("start left again = %+v err=%v, want the same Task %s", slotOf(again, "left"), err, left.TaskID)
	}
	back, err := f.views.Advance(ctx, fittedPerson, "session-a", f.run.ID, "back", threeview.Start, 0)
	if err != nil || slotOf(back, "back").TaskID == "" {
		t.Fatalf("start back = %+v err=%v", slotOf(back, "back"), err)
	}
	if gateway.copies != 1 {
		t.Fatalf("copies = %d, want the copy made once", gateway.copies)
	}
}
