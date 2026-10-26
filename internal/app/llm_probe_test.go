package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
)

type probeAdapter struct {
	requests []agents.Request
	answer   func(request agents.Request, hasImage bool) error
}

func (adapter *probeAdapter) Stream(context.Context, agents.Request, func(agents.Delta) error) (agents.Completion, error) {
	return agents.Completion{}, errors.New("the probe must not stream")
}

func (adapter *probeAdapter) Complete(_ context.Context, request agents.Request) (agents.Completion, error) {
	adapter.requests = append(adapter.requests, request)
	hasImage := false
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			hasImage = hasImage || part.URL != ""
		}
	}
	if err := adapter.answer(request, hasImage); err != nil {
		return agents.Completion{}, err
	}
	return agents.Completion{Content: "OK"}, nil
}

// #322 Scope 2: "test connection" is one minimal request carrying a small
// generated picture; only when the picture is refused does a text-only
// request follow, so a single click also tells whether the model reads
// images. Outcomes are classified, never the upstream body.
func TestConnectionProbeClassifiesReachabilityAndImageInput(t *testing.T) {
	schema := llmConnectionSchema(t)
	glm := llmsettings.Connection{ID: "GLM", Model: "glm-5.3"}
	upstreamBody := "upstream body with secret-ish detail"
	for name, test := range map[string]struct {
		answer   func(agents.Request, bool) error
		want     llmsettings.ProbeResult
		requests int
	}{
		"reads images": {
			answer: func(agents.Request, bool) error { return nil },
			want:   llmsettings.ProbeResult{Reachable: true, Vision: llmsettings.VisionSupported, Outcome: llmsettings.OutcomeOK}, requests: 1,
		},
		"refuses images": {
			answer: func(_ agents.Request, image bool) error {
				if image {
					return errors.Join(agents.ErrImagesRejected, errors.New(upstreamBody))
				}
				return nil
			},
			want: llmsettings.ProbeResult{Reachable: true, Vision: llmsettings.VisionUnsupported, Outcome: llmsettings.OutcomeOK}, requests: 2,
		},
		"generic refusal of the picture": {
			answer: func(_ agents.Request, image bool) error {
				if image {
					return fmt.Errorf("%w: %s", &agents.StatusError{Status: 400}, upstreamBody)
				}
				return nil
			},
			want: llmsettings.ProbeResult{Reachable: true, Vision: llmsettings.VisionUnsupported, Outcome: llmsettings.OutcomeOK}, requests: 2,
		},
		"bad key": {
			answer: func(agents.Request, bool) error {
				return fmt.Errorf("%w: %s", &agents.StatusError{Status: 401}, upstreamBody)
			},
			want: llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: llmsettings.OutcomeUnauthorized}, requests: 1,
		},
		"unknown model": {
			answer: func(agents.Request, bool) error { return &agents.StatusError{Status: 404} },
			want:   llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: llmsettings.OutcomeNotFound}, requests: 1,
		},
		"rate limited": {
			answer: func(agents.Request, bool) error { return &agents.StatusError{Status: 429} },
			want:   llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: llmsettings.OutcomeRateLimited}, requests: 1,
		},
		"server error": {
			answer: func(agents.Request, bool) error { return &agents.StatusError{Status: 503} },
			want:   llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: llmsettings.OutcomeUpstreamError}, requests: 1,
		},
		"timeout": {
			answer: func(agents.Request, bool) error { return fmt.Errorf("call agent: %w", context.DeadlineExceeded) },
			want:   llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: llmsettings.OutcomeTimeout}, requests: 1,
		},
		"unreachable": {
			answer: func(agents.Request, bool) error { return errors.New("call agent: dial tcp: connection refused") },
			want:   llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: llmsettings.OutcomeUnreachable}, requests: 1,
		},
		"not configured": {
			answer: func(agents.Request, bool) error { return agents.ErrNotConfigured },
			want:   llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: llmsettings.OutcomeNotConfigured}, requests: 1,
		},
	} {
		adapter := &probeAdapter{answer: test.answer}
		got := connectionProber(adapter, schema, nil)(context.Background(), glm)
		if got != test.want || len(adapter.requests) != test.requests {
			t.Fatalf("%s: got %+v after %d requests, want %+v after %d", name, got, len(adapter.requests), test.want, test.requests)
		}
		first := adapter.requests[0]
		if first.Connection != "GLM" || first.Model != "glm-5.3" || len(first.Messages) != 1 || len(first.Messages[0].Parts) != 2 ||
			!strings.HasPrefix(first.Messages[0].Parts[1].URL, "data:image/png;base64,") {
			t.Fatalf("%s: first request = %+v", name, first)
		}
		// The cheapest level the model offers keeps the test small.
		if first.ReasoningEffort != "low" {
			t.Fatalf("%s: effort = %q", name, first.ReasoningEffort)
		}
	}
}
