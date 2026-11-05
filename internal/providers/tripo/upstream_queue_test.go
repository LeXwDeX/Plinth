package tripo_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #131: Tripo's queued status word waits in the upstream queue (declared
// queued in the default Schema). The task object reports no queue position,
// so the queue carries no count; running reports no queue.
func TestQueuedTripoTaskReportsTheUpstreamQueueWithoutCount(t *testing.T) {
	for _, test := range []struct {
		status string
		queued bool
	}{{"queued", true}, {"running", false}} {
		t.Run(test.status, func(t *testing.T) {
			upstream := newObserveServer(t, test.status, 0, nil)
			defer upstream.Close()
			observed, err := newAdapter(t, upstream).Observe(context.Background(), generation.ObserveRequest{
				AttemptID: "att-1",
				Offering:  tripoOffering(generation.TextTo3D),
				Job:       generation.UpstreamJob{ProviderID: "tripo", RemoteID: "uuid-1"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if (observed.Queue != nil) != test.queued || (observed.Queue != nil && observed.Queue.Ahead != nil) {
				t.Fatalf("%s queue = %#v", test.status, observed.Queue)
			}
		})
	}
}
