package agents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// answersInTurn is an Adapter that gives its answers in turn, emitting the
// deltas listed with each, and repeats the last one.
type answersInTurn struct {
	answers []answer
	calls   int
}

type answer struct {
	deltas     []agents.Delta
	completion agents.Completion
	err        error
}

func (adapter *answersInTurn) Stream(_ context.Context, _ agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	next := adapter.answers[min(adapter.calls, len(adapter.answers)-1)]
	adapter.calls++
	for _, delta := range next.deltas {
		if err := emit(delta); err != nil {
			return agents.Completion{}, err
		}
	}
	return next.completion, next.err
}

func (adapter *answersInTurn) Complete(context.Context, agents.Request) (agents.Completion, error) {
	adapter.calls++
	return agents.Completion{}, nil
}

// #638: a round that answered with nothing the reply can hold (no text, no
// call; only its thinking signal, no usage) is sent again EmptyRetries times,
// apart from the transient attempts; the retry's answer is the round's. Still
// empty after them, the empty completion is returned for the caller to end the
// turn on. A picture the model drew itself, a round that showed text, a policy
// without empty retries and Complete are sent once.
func TestAnEmptyAnswerIsSentAgainWithinTheEmptyRetries(t *testing.T) {
	thought := []agents.Delta{{Thinking: true}}
	empty := answer{deltas: thought, completion: agents.Completion{ReasoningContent: "r"}}
	called := answer{completion: agents.Completion{ToolCalls: []agents.ToolCall{{ID: "c", Name: "n", Arguments: []byte(`{}`)}}}}
	var waits []time.Duration
	policy := agents.RetryPolicy{
		MaxAttempts: 3, InitialBackoff: 100 * time.Millisecond, MaxBackoff: time.Second, MaxRetryAfter: time.Second,
		EmptyRetries: 1,
		Wait: func(ctx context.Context, wait time.Duration) error {
			waits = append(waits, wait)
			return ctx.Err()
		},
	}
	sink := func(agents.Delta) error { return nil }

	recovers := &answersInTurn{answers: []answer{empty, called}}
	completion, err := agents.NewRetrying(recovers, policy).Stream(context.Background(), hello("go"), sink)
	if err != nil || len(completion.ToolCalls) != 1 || recovers.calls != 2 || len(waits) != 1 || waits[0] != 100*time.Millisecond {
		t.Fatalf("recovers: %d calls, waits %v, %#v %v", recovers.calls, waits, completion, err)
	}

	stays := &answersInTurn{answers: []answer{empty}}
	completion, err = agents.NewRetrying(stays, policy).Stream(context.Background(), hello("go"), sink)
	if err != nil || !completion.Empty() || stays.calls != 2 {
		t.Fatalf("stays empty: %d calls, %#v %v", stays.calls, completion, err)
	}

	// The retry's transient failure is retried within MaxAttempts as usual.
	transient := answer{err: agents.Transient(errors.New("cut"), 0)}
	mixed := &answersInTurn{answers: []answer{empty, transient, called}}
	if completion, err := agents.NewRetrying(mixed, policy).Stream(context.Background(), hello("go"), sink); err != nil ||
		len(completion.ToolCalls) != 1 || mixed.calls != 3 {
		t.Fatalf("empty then transient: %d calls, %#v %v", mixed.calls, completion, err)
	}

	// A picture the model drew itself is billed as a generated image: it is
	// returned at once, never sent again (#638).
	picture := &answersInTurn{answers: []answer{{err: agents.ErrInlineImageOutput}, called}}
	if _, err := agents.NewRetrying(picture, policy).Stream(context.Background(), hello("go"), sink); !errors.Is(err, agents.ErrInlineImageOutput) ||
		picture.calls != 1 {
		t.Fatalf("inline picture: %d calls, %v", picture.calls, err)
	}

	shown := &answersInTurn{answers: []answer{{deltas: []agents.Delta{{Content: "  "}}, completion: agents.Completion{Content: "  "}}, called}}
	if _, err := agents.NewRetrying(shown, policy).Stream(context.Background(), hello("go"), sink); err != nil || shown.calls != 1 {
		t.Fatalf("shown text: %d calls, %v", shown.calls, err)
	}

	none := policy
	none.EmptyRetries = 0
	once := &answersInTurn{answers: []answer{empty, called}}
	if completion, err := agents.NewRetrying(once, none).Stream(context.Background(), hello("go"), sink); err != nil ||
		!completion.Empty() || once.calls != 1 {
		t.Fatalf("no empty retries: %d calls, %#v %v", once.calls, completion, err)
	}
	complete := &answersInTurn{answers: []answer{empty}}
	if _, err := agents.NewRetrying(complete, policy).Complete(context.Background(), hello("go")); err != nil || complete.calls != 1 {
		t.Fatalf("complete: %d calls, %v", complete.calls, err)
	}

	// A policy with only empty retries still wraps the Adapter.
	if wrapped := agents.NewRetrying(once, agents.RetryPolicy{MaxAttempts: 1, EmptyRetries: 1}); wrapped == agents.Adapter(once) {
		t.Fatal("a policy with empty retries left the Adapter unwrapped")
	}
}
