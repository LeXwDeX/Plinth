package generation

import (
	"errors"
	"testing"
	"time"
)

// #665 third review: only the permanent subset of the refusals before a write
// may deny an approved grant.
func TestRefusedPermanentlyExcludesTransientRefusals665(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"not submitted":          {refusedBeforeWrite(ErrConflict, "stale", nil), true},
		"invalid input":          {&Error{Code: ErrInvalidInput}, true},
		"unknown offering":       {&Error{Code: ErrUnknownOffering}, true},
		"forbidden":              {&Error{Code: ErrForbidden}, true},
		"conversion in progress": {ErrConversionInProgress, false},
		"wrapped conversion":     {errors.Join(errors.New("x"), ErrConversionInProgress), false},
		"too many requests":      {&Error{Code: ErrTooManyRequests}, false},
		"retry after":            {&Error{Code: ErrTooManyRequests, RetryAfter: time.Second}, false},
		"unknown failure":        {errors.New("response lost"), false},
	} {
		if got := RefusedPermanently(tc.err); got != tc.want {
			t.Errorf("%s: RefusedPermanently = %v, want %v", name, got, tc.want)
		}
	}
}
