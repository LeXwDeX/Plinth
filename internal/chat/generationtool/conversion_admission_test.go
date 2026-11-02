package generationtool

import (
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #330 final-review P1: a conflict caused by an overlapping active conversion
// is a known non-submission. The turn must not wait for a Task that was never
// created.
func TestKnownNotSubmittedRecognizesAnOverlappingConversion330(t *testing.T) {
	err := &generation.Error{Code: generation.ErrConflict, Cause: generation.ErrConversionInProgress}
	if !knownNotSubmitted(err) {
		t.Fatal("an overlapping conversion conflict was treated as a possible submission")
	}
	if knownNotSubmitted(&generation.Error{Code: generation.ErrConflict}) {
		t.Fatal("an unrelated conflict must keep the unknown-submission path")
	}
}

// A full in-flight admission is a known non-submission that carries the wait
// the kernel asks for; the orchestrator queues the call on it. Other errors
// carry none.
func TestAdmissionRetryAfterOnlyForTooManyRequests(t *testing.T) {
	full := &generation.Error{Code: generation.ErrTooManyRequests, RetryAfter: 30 * time.Second}
	if admissionRetryAfter(full) != 30*time.Second || !knownNotSubmitted(full) {
		t.Fatal("admission refusal must be a known non-submission with its RetryAfter")
	}
	if admissionRetryAfter(&generation.Error{Code: generation.ErrTooManyRequests}) != 0 ||
		admissionRetryAfter(&generation.Error{Code: generation.ErrConflict, RetryAfter: time.Second}) != 0 ||
		admissionRetryAfter(errors.New("other")) != 0 {
		t.Fatal("only an admission refusal with a wait may queue a call")
	}
}
