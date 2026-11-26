package agents_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// #665: an empty answer sent again keeps the usage the first attempt reported
// when the kept answer reports none, and the caller sees one thinking signal,
// not one per attempt.
func TestAnEmptyRetryKeepsTheUsageAndOneThinkingSignal(t *testing.T) {
	usage := &agents.Usage{PromptTokens: 900, CompletionTokens: 12}
	adapter := &answersInTurn{answers: []answer{
		{deltas: []agents.Delta{{Thinking: true}}, completion: agents.Completion{Usage: usage}},
		{deltas: []agents.Delta{{Thinking: true}, {Content: "done"}}, completion: agents.Completion{Content: "done"}},
	}}
	retrying := agents.NewRetrying(adapter, agents.RetryPolicy{MaxAttempts: 1, EmptyRetries: 1,
		Wait: func(context.Context, time.Duration) error { return nil }})
	var deltas []agents.Delta
	completion, err := retrying.Stream(context.Background(), agents.Request{}, func(delta agents.Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil || completion.Content != "done" || adapter.calls != 2 {
		t.Fatalf("completion = %#v, %v after %d calls", completion, err, adapter.calls)
	}
	if completion.Usage == nil || *completion.Usage != *usage {
		t.Fatalf("usage = %#v, want the first attempt's", completion.Usage)
	}
	thinking := 0
	for _, delta := range deltas {
		if delta.Thinking {
			thinking++
		}
	}
	if thinking != 1 || len(deltas) != 2 {
		t.Fatalf("deltas = %#v, want one thinking signal and the text", deltas)
	}
}
