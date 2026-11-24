package generation_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #576: a Tripo render the asset store refused (a content verdict) left its
// background slot pending for the whole post-success window, so the task said
// resources_pending long after everything that could arrive had arrived. A
// refusal the same bytes would meet again now settles the slot as failed with
// the Schema's reason key; the task, its other resources and its assets are
// untouched, and the content check itself is not relaxed.

// rejectedThumbnail is a PNG followed by bytes after its end chunk: the store
// refuses it at the inspect stage (raster_trailing_data) every time.
func rejectedThumbnail() []byte {
	return append(postSuccessThumbnailPNG(), []byte("junk")...)
}

func assertThumbnailRejected(t *testing.T, task generation.Task) {
	t.Helper()
	slot, ok := task.Resources["thumbnail|rendered_image|"]
	if !ok || slot.State != generation.ResourceFailed || slot.Reason != generation.ResourceRejectedMessageKey {
		t.Fatalf("thumbnail slot = %+v (present %v), want failed/%s", slot, ok, generation.ResourceRejectedMessageKey)
	}
	for _, asset := range task.Assets {
		if asset.Role == "thumbnail" {
			t.Fatalf("a refused thumbnail was stored: %+v", asset)
		}
	}
}

func TestRejectedBackgroundThumbnailSettlesAtSuccess576(t *testing.T) {
	adapter := newPostSuccessAdapter()
	adapter.thumbnail = rejectedThumbnail()
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-rejected-thumbnail.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF","FBX"],"authorize_extra_credits":true}`)
	aggregate, err := h.repository.Load(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Settled in the success commit itself: no observation round is spent on it.
	assertThumbnailRejected(t, aggregate.Task)
	if state := aggregate.Task.Resources["model||"]; state.State != generation.ResourceReady {
		t.Fatalf("model slot = %+v", state)
	}
	for _, format := range []string{"GLTF", "FBX"} {
		if state := convertedSlot(t, aggregate.Task, format); state.State != generation.ResourcePending {
			t.Fatalf("converted.%s slot = %+v, want pending: the refusal touches only its own slot", lower(format), state)
		}
	}
	final := drivePostSuccess(t, h, task.ID, 16)
	if final.State != generation.TaskSucceeded || final.Failure != nil {
		t.Fatalf("task = %s %+v", final.State, final.Failure)
	}
	assertThumbnailRejected(t, final)
	for _, format := range []string{"GLTF", "FBX"} {
		if state := convertedSlot(t, final, format); state.State != generation.ResourceReady {
			t.Fatalf("converted.%s slot = %+v, want ready", lower(format), state)
		}
	}
	if generation.ResourcesPending(final.Resources) || !final.NextActionAt.IsZero() {
		t.Fatalf("resources_pending stays: %+v next=%v", final.Resources, final.NextActionAt)
	}
}

func TestRejectedLateThumbnailSettlesInPostSuccess576(t *testing.T) {
	adapter := newPostSuccessAdapter()
	adapter.omitThumbnail = true
	adapter.lateThumbnail = rejectedThumbnail()
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-rejected-late.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF"],"authorize_extra_credits":true}`)
	if state := task.Resources["thumbnail|rendered_image|"]; state.State != generation.ResourcePending {
		t.Fatalf("thumbnail slot at success = %+v, want pending", state)
	}
	final := drivePostSuccess(t, h, task.ID, 16)
	if final.State != generation.TaskSucceeded || final.Failure != nil {
		t.Fatalf("task = %s %+v", final.State, final.Failure)
	}
	assertThumbnailRejected(t, final)
	if state := convertedSlot(t, final, "GLTF"); state.State != generation.ResourceReady {
		t.Fatalf("converted.gltf slot = %+v, want ready", state)
	}
	if generation.ResourcesPending(final.Resources) || !final.NextActionAt.IsZero() {
		t.Fatalf("resources_pending stays: %+v next=%v", final.Resources, final.NextActionAt)
	}
}

// A thumbnail the store accepts still lands ready: the rule only settles
// refusals.
func TestAcceptedLateThumbnailStillLandsReady576(t *testing.T) {
	adapter := newPostSuccessAdapter()
	adapter.omitThumbnail = true
	adapter.lateThumbnail = postSuccessThumbnailPNG()
	h := newHistoricalPostSuccessHarness(t, adapter, filepath.Join(t.TempDir(), "ps-late-ok.db"))
	task := submitPostSuccess(t, h, `{"prompt":"a lantern","output_formats":["GLTF"],"authorize_extra_credits":true}`)
	final := drivePostSuccess(t, h, task.ID, 16)
	if state := final.Resources["thumbnail|rendered_image|"]; state.State != generation.ResourceReady {
		t.Fatalf("thumbnail slot = %+v, want ready", state)
	}
	if generation.ResourcesPending(final.Resources) {
		t.Fatalf("resources_pending stays: %+v", final.Resources)
	}
}
