package tripo_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// mixed_drive_test.go holds the driving helpers of the mixed post-success shape
// (#462): the public task succeeds as soon as the blocking conversion (the GLTF
// export that produces the self-contained primary model) has landed, and the
// requested formats are converted after success, one claim per round. A caller
// that wants the whole delivery therefore drives both phases.
//
// The kernel keeps observing while any resource slot is pending; the adapter
// settles a round in which it has nothing left to do (children that are still in
// flight are polled by the next round). The post-success driver below stops on
// that same condition, read from the private state it already writes.

// conversionStatuses decodes the private conversion table of an observation.
func conversionStatuses(t *testing.T, opaque json.RawMessage) map[string]string {
	t.Helper()
	var state struct {
		Conversions map[string]struct {
			Status string `json:"status"`
		} `json:"conversions"`
	}
	if len(opaque) == 0 {
		return nil
	}
	if err := json.Unmarshal(opaque, &state); err != nil {
		t.Fatalf("decode private conversion state: %v", err)
	}
	statuses := make(map[string]string, len(state.Conversions))
	for format, item := range state.Conversions {
		statuses[format] = item.Status
	}
	return statuses
}

// conversionInFlight reports whether a format is still being worked on.
func conversionInFlight(status string) bool {
	switch status {
	case "submitted", "dispatching", "rate_limited", "retry_pending":
		return true
	}
	return false
}

// observePreCompletion drives the pre-completion observation of a mixed Offering
// to the phase in which the task may succeed.
func observePreCompletion(
	t *testing.T,
	adapter *tripo.Adapter,
	request generation.ObserveRequest,
	rounds int,
) generation.Observation {
	t.Helper()
	for round := 0; round < rounds; round++ {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		request.Opaque = observation.Opaque
		switch observation.Phase {
		case generation.UpstreamCompleted, generation.UpstreamFailed:
			return observation
		}
	}
	t.Fatal("pre-completion conversions did not finish")
	return generation.Observation{}
}

// observePostSuccess drives the post-success observation until the run settles,
// and returns every round. A round that delivers nothing while a child is still
// in flight is not the end: the next round polls it.
func observePostSuccess(
	t *testing.T,
	adapter *tripo.Adapter,
	request generation.ObserveRequest,
	rounds int,
) []generation.Observation {
	t.Helper()
	request.PostSuccess = true
	var driven []generation.Observation
	for round := 0; round < rounds; round++ {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatalf("post-success observe: %v", err)
		}
		if observation.Phase == generation.UpstreamFailed {
			t.Fatalf("post-success run failed: %+v", observation.Failure)
		}
		driven = append(driven, observation)
		request.Opaque = observation.Opaque
		settled := observation.Phase == generation.UpstreamCompleted && len(observation.Artifacts) == 0
		if !settled {
			continue
		}
		pending := false
		for _, status := range conversionStatuses(t, observation.Opaque) {
			if conversionInFlight(status) {
				pending = true
			}
		}
		if !pending {
			return driven
		}
	}
	t.Fatal("post-success conversions did not finish")
	return nil
}

// drivePostSuccess is observePostSuccess with the private dispatch permit: the
// post-success phase may send paid children, so every round is settled like the
// kernel's checkpoint does.
func drivePostSuccess(
	t *testing.T,
	adapter *tripo.Adapter,
	request generation.ObserveRequest,
	permit *conversionPermit,
	rounds int,
) generation.Observation {
	t.Helper()
	ctx := generation.WithDispatchCheckpointer(context.Background(), permit)
	request.PostSuccess = true
	var observation generation.Observation
	for round := 0; round < rounds; round++ {
		var err error
		observation, err = adapter.Observe(ctx, request)
		if err != nil {
			t.Fatalf("post-success observe: %v", err)
		}
		permit.settle(t, observation)
		request.Opaque = observation.Opaque
		if observation.Phase == generation.UpstreamFailed {
			t.Fatalf("post-success run failed: %+v", observation.Failure)
		}
		if observation.Phase == generation.UpstreamCompleted && len(observation.Artifacts) == 0 {
			pending := false
			for _, status := range conversionStatuses(t, observation.Opaque) {
				if conversionInFlight(status) {
					pending = true
				}
			}
			if !pending {
				return observation
			}
		}
	}
	t.Fatal("post-success conversions did not finish")
	return observation
}
