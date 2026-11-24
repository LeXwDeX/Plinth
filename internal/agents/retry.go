package agents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// RetryPolicy is how a model call meets a transient failure (#509): the values
// of chat.agent.retry and chat.agent.round_timeout_seconds in the Product
// Schema; the code holds none of its own.
type RetryPolicy struct {
	// MaxAttempts is how many times one call is sent at most, the first
	// included; 1 or less sends it once.
	MaxAttempts int
	// InitialBackoff is the wait before the second attempt; each later wait
	// doubles, up to MaxBackoff.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	// MaxRetryAfter bounds the upstream's Retry-After: a failure that asks for
	// a longer wait is returned at once instead of waited for.
	MaxRetryAfter time.Duration
	// RoundTimeout bounds one call with all its attempts and waits; 0: only
	// each attempt's own request timeout bounds it.
	RoundTimeout time.Duration
	// Wait waits between attempts; nil uses a timer. Tests record the waits
	// instead.
	Wait func(context.Context, time.Duration) error
	// Logger takes one line for every retry; nil writes nothing.
	Logger *slog.Logger
}

// Retrying is an Adapter that sends a streamed call (an Agent round) again
// after a transient failure (ErrTransient), within its policy, and bounds the
// round. A model call has no side effect before it
// returns, except what it streamed to the caller: a stream is replayed only
// while it has emitted no delta, so a person never sees the same output twice;
// one that broke after its first delta returns its failure, for the caller to
// end the turn on (#509).
type Retrying struct {
	next   Adapter
	policy RetryPolicy
}

// NewRetrying wraps next; a policy without retries and without a round bound
// leaves next as it is.
func NewRetrying(next Adapter, policy RetryPolicy) Adapter {
	if policy.MaxAttempts <= 1 && policy.RoundTimeout <= 0 {
		return next
	}
	return &Retrying{next: next, policy: policy}
}

// errRoundTimeout is the cause of a call its round timeout ended.
var errRoundTimeout = errors.New("agent model round timed out")

func (retrying *Retrying) Stream(ctx context.Context, request Request, emit func(Delta) error) (Completion, error) {
	emitted := false
	guarded := emit
	if emit != nil {
		guarded = func(delta Delta) error {
			emitted = true
			return emit(delta)
		}
	}
	return retrying.run(ctx, request, func(ctx context.Context) (Completion, error) {
		return retrying.next.Stream(ctx, request, guarded)
	}, func() bool { return emitted })
}

// Complete is sent once, as it is: its callers (the session title, the
// context summary, the prompt enhancement and the administrator's connection
// test) fail quietly or report the connection as it is now, and a retry would
// only hide that or keep the person waiting.
func (retrying *Retrying) Complete(ctx context.Context, request Request) (Completion, error) {
	return retrying.next.Complete(ctx, request)
}

func (retrying *Retrying) run(
	parent context.Context, request Request,
	call func(context.Context) (Completion, error), emitted func() bool,
) (Completion, error) {
	ctx := parent
	if retrying.policy.RoundTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(parent, retrying.policy.RoundTimeout, errRoundTimeout)
		defer cancel()
	}
	for attempt := 1; ; attempt++ {
		completion, err := call(ctx)
		if err == nil {
			return completion, nil
		}
		if parent.Err() == nil && errors.Is(context.Cause(ctx), errRoundTimeout) {
			// The round, not the caller, ran out: the call failed for the
			// upstream's slowness.
			return completion, Transient(fmt.Errorf("%w after %s and %d attempt(s): %w",
				errRoundTimeout, retrying.policy.RoundTimeout, attempt, err), 0)
		}
		if !errors.Is(err, ErrTransient) || emitted() || ctx.Err() != nil || attempt >= retrying.policy.MaxAttempts {
			return completion, err
		}
		wait := retrying.backoff(attempt)
		if retryAfter, _ := TransientRetryAfter(err); retryAfter > 0 {
			if retryAfter > retrying.policy.MaxRetryAfter {
				return completion, err
			}
			wait = max(wait, retryAfter)
		}
		if deadline, bounded := ctx.Deadline(); bounded && time.Until(deadline) <= wait {
			return completion, err
		}
		if logger := retrying.policy.Logger; logger != nil {
			logger.Warn("对话模型调用瞬时失败，稍后重试",
				"model", request.Model, "connection", request.Connection, "purpose", request.Purpose,
				"attempt", attempt, "max_attempts", retrying.policy.MaxAttempts,
				"wait_ms", wait.Milliseconds(), "error", err)
		}
		if waitErr := retrying.wait(ctx, wait); waitErr != nil {
			return completion, err
		}
	}
}

// backoff is the wait after the attempt-th failure.
func (retrying *Retrying) backoff(attempt int) time.Duration {
	wait := retrying.policy.InitialBackoff
	for step := 1; step < attempt && wait < retrying.policy.MaxBackoff; step++ {
		wait *= 2
	}
	if retrying.policy.MaxBackoff > 0 {
		wait = min(wait, retrying.policy.MaxBackoff)
	}
	return wait
}

func (retrying *Retrying) wait(ctx context.Context, wait time.Duration) error {
	if retrying.policy.Wait != nil {
		return retrying.policy.Wait(ctx, wait)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
