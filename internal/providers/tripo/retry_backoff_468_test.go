package tripo_test

// #468: a convert child that ended failed or cancelled enters the documented
// backoff (1, 2, 4… seconds) before its format is sent again. The round that
// learns the outcome must carry that wait as PollAfter. Entering the backoff
// also records an attempt, so the "an outcome happened, dispatch nothing" return
// always fires with it: the backoff has to be applied there, or the next round
// (one ordinary poll later) resubmits the format instead of waiting.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func conversionRetryWaitSeconds(t *testing.T, opaque json.RawMessage, format string) int {
	t.Helper()
	var state struct {
		Conversions map[string]struct {
			Status           string `json:"status"`
			RetryWaitSeconds int    `json:"retry_wait_seconds"`
		} `json:"conversions"`
	}
	if err := json.Unmarshal(opaque, &state); err != nil {
		t.Fatalf("decode %s: %v", opaque, err)
	}
	return state.Conversions[format].RetryWaitSeconds
}

func TestFailedConvertChildRoundCarriesItsBackoff(t *testing.T) {
	upstream := newExportFixture(t)
	upstream.set(func(f *exportFixture) {
		f.failFormats["GLTF"] = true
		f.runningPolls = 0
	})
	adapter := newAdapter(t, upstream.server)
	permit := &conversionPermit{}
	request := fullFormatRequest(t)
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	// The first round starts the GLTF child.
	first, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	permit.settle(t, first)
	request.Opaque = first.Opaque
	if posts, _, _ := upstream.snapshot(); len(posts) != 1 || posts[0] != "GLTF" {
		t.Fatalf("first round posts = %v, want the GLTF child", posts)
	}
	// The next round polls the child: it failed, so the format is now waiting
	// out its backoff.
	second, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	permit.settle(t, second)
	if status, _ := conversionStatus(t, second.Opaque, "GLTF"); status != "retry_pending" {
		t.Fatalf("GLTF = %s, want retry_pending after the child failed", status)
	}
	wait := conversionRetryWaitSeconds(t, second.Opaque, "GLTF")
	if wait <= 0 {
		t.Fatalf("retry wait = %d, want the documented backoff", wait)
	}
	if second.PollAfter != time.Duration(wait)*time.Second {
		t.Fatalf("PollAfter = %v, want the format's backoff %ds", second.PollAfter, wait)
	}
	// The round that entered the backoff must not also resubmit the format.
	if posts, _, _ := upstream.snapshot(); len(posts) != 1 {
		t.Fatalf("the round that entered the backoff also dispatched: %v", posts)
	}
	// The wait is a delay, not a dead end: the next round resends GLTF.
	request.Opaque = second.Opaque
	third, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	permit.settle(t, third)
	if posts, _, _ := upstream.snapshot(); len(posts) != 2 || posts[1] != "GLTF" {
		t.Fatalf("posts after the backoff = %v, want GLTF resent", posts)
	}
}
