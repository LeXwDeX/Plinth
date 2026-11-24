package meshy_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// intentRecordingCheckpointer acknowledges every private dispatch intent and
// keeps its bytes: they are what the coordinator stores as the Attempt's
// opaque state before the paid POST leaves.
type intentRecordingCheckpointer struct {
	mu      sync.Mutex
	intents []json.RawMessage
}

func (c *intentRecordingCheckpointer) Checkpoint(
	_ context.Context,
	intent generation.DispatchIntent,
) (generation.DispatchAck, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.intents = append(c.intents, append(json.RawMessage(nil), intent.Bytes...))
	return generation.DispatchAck{
		Sequence: int64(len(c.intents)),
		AckToken: "ack-meshy-000000000000000000000000563",
		Opaque:   intent.Bytes,
	}, nil
}

func (c *intentRecordingCheckpointer) last(t *testing.T) json.RawMessage {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.intents) == 0 {
		t.Fatal("no dispatch intent was checkpointed")
	}
	return c.intents[len(c.intents)-1]
}

// #563: Meshy accepted the paid refine create (the fake keeps the task) but
// the response was lost. The next observation reads only the dispatch intent:
// the preview was charged and the refine may have been, so the failure is an
// ambiguous submission (counted as possibly charged, never retried), not a
// rejection, and the refine is not sent again.
func TestRefineWhoseResponseWasLostEndsAmbiguousWithoutResend563(t *testing.T) {
	upstream := newRefineRefusingUpstream(t, fakeupstream.ScenarioLostResponse, 1)
	adapter, request := startRefineTask(t, upstream)
	checkpoints := &intentRecordingCheckpointer{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), checkpoints)

	if _, err := observeUntilRefineSubmit(t, ctx, adapter, upstream, request); err == nil {
		t.Fatal("a lost refine response must not read as a submitted refine")
	}
	if previews, refines := upstream.posts(); previews != 1 || refines != 1 {
		t.Fatalf("posts = preview %d refine %d, want the refine sent once", previews, refines)
	}

	request.Opaque = checkpoints.last(t)
	observation, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamFailed || observation.Failure == nil {
		t.Fatalf("observation = phase %s failure %+v", observation.Phase, observation.Failure)
	}
	if !observation.Failure.Ambiguous || observation.Failure.Retryable ||
		observation.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("an unknown refine submission must be ambiguous: %+v", observation.Failure)
	}
	if _, refines := upstream.posts(); refines != 1 {
		t.Fatalf("refine posts = %d, the refine was sent again", refines)
	}
}
