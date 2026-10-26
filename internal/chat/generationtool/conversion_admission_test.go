package generationtool

import (
	"testing"

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
