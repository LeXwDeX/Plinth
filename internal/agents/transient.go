package agents

import (
	"errors"
	"time"
)

// ErrTransient marks a model call that failed in a way the same request may
// not meet again (#509): the upstream was busy or broken (HTTP 408, 429 that
// the Schema does not read as a used-up quota, 500, 502, 503, 504), the
// connection broke, no first byte came in time, the stream reported an error
// or stopped before its last chunk, or a streaming request was not answered
// with a stream. Only such a failure may be retried, and only before the
// caller received any output (Retrying).
var ErrTransient = errors.New("agent upstream call failed transiently")

// TransientFailure is an ErrTransient that carries the wait the upstream asked
// for.
type TransientFailure struct {
	// RetryAfter is the upstream's Retry-After; 0 when it named none.
	RetryAfter time.Duration
}

func (failure *TransientFailure) Error() string { return ErrTransient.Error() }

func (failure *TransientFailure) Is(target error) bool { return target == ErrTransient }

// Transient marks err as an ErrTransient with the upstream's Retry-After.
func Transient(err error, retryAfter time.Duration) error {
	return errors.Join(&TransientFailure{RetryAfter: retryAfter}, err)
}

// TransientRetryAfter is the Retry-After of a transient failure in err; false
// when err is not one.
func TransientRetryAfter(err error) (time.Duration, bool) {
	var failure *TransientFailure
	if !errors.As(err, &failure) {
		return 0, false
	}
	return failure.RetryAfter, true
}
