// Package wirefixture captures and replays sanitized JSON, SSE, narrow
// multipart file-upload and binary HTTP exchanges. It is a test-only
// foundation. Query-bearing requests, redirects and unsupported media types
// fail validation. Binary evidence is private and never exported.
//
// The sanitizer, Validate, the draft derivation and the upstream recorder live
// in internal/wirecapture, which production code may link (#233); this package
// builds the test-side capture (Recorder, deferred capture, private media
// evidence) and replay on those rules and re-exports their types
// (aliases.go) so existing fixtures and tools keep one import path.
package wirefixture

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/wirecapture"
)

var (
	ErrUnexpectedRequest  = errors.New("wirefixture: unexpected replay request")
	ErrReorderedRequest   = errors.New("wirefixture: reordered replay request")
	ErrExhaustedRequests  = errors.New("wirefixture: replay requests exhausted")
	ErrUnconsumedRequest  = errors.New("wirefixture: replay did not consume every request")
	ErrTransportFailure   = errors.New("wirefixture: transport failure")
	ErrIncompleteCapture  = errors.New("wirefixture: incomplete capture")
	ErrConnectionMismatch = errors.New("wirefixture: replay connection mismatch")
	ErrReplayFailed       = errors.New("wirefixture: replay failed")
)

type Recorder struct {
	// Default must stay immutable during capture. Policy and Evidence are
	// caller-owned and immutable in immediate mode; deferred mode ignores them.
	Policy   Policy
	Default  Metadata
	Evidence *PrivateStore

	mu        sync.Mutex
	nextSeq   int
	exchanges []Exchange
	evidence  []MediaEvidence
	failures  []error
	deferred  *deferredCapture
}

type Replay struct {
	policy    Policy
	exchanges []Exchange
	media     MediaResolver
	next      int
	failed    bool
	mu        sync.Mutex
}

func (r *Recorder) Wrap(connection string, base http.RoundTripper) http.RoundTripper {
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return r.roundTrip(connection, base, request)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (r *Recorder) Exchanges() []Exchange {
	if r.deferred != nil {
		r.deferred.mu.Lock()
		defer r.deferred.mu.Unlock()
		return wirecapture.SortedExchanges(r.deferred.exchanges)
	}
	r.mu.Lock()
	out := wirecapture.SortedExchanges(r.exchanges)
	r.mu.Unlock()
	return out
}

func (r *Recorder) Failures() []error {
	if r.deferred != nil {
		r.deferred.mu.Lock()
		defer r.deferred.mu.Unlock()
		if r.deferred.err != nil {
			return []error{r.deferred.err}
		}
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error(nil), r.failures...)
}

// AssertComplete checks a quiescent capture before export: every started
// request must have finished, no transport failure may be present, and the
// snapshot must pass Validate.
func (r *Recorder) AssertComplete() error {
	if r.deferred != nil {
		r.deferred.mu.Lock()
		defer r.deferred.mu.Unlock()
		if r.deferred.err != nil {
			return r.deferred.err
		}
		if !r.deferred.sealed {
			return ErrIncompleteCapture
		}
		return nil // Finalize validated the immutable, private snapshot.
	}
	r.mu.Lock()
	started := r.nextSeq
	failed := len(r.failures) != 0
	exchanges := wirecapture.SortedExchanges(r.exchanges)
	evidence := append([]MediaEvidence(nil), r.evidence...)
	policy := r.Policy
	r.mu.Unlock()
	if failed {
		return fmt.Errorf("%w: transport failures recorded", ErrTransportFailure)
	}
	if len(exchanges) != started {
		return fmt.Errorf("%w: %d started, %d completed", ErrIncompleteCapture, started, len(exchanges))
	}
	// Stored exchanges already contain public projection tokens. Keep the
	// projection active so Validate accepts those tokens; the projector returns
	// existing id-N tokens unchanged instead of renaming them.
	policy = policy.WithProjection()
	if err := Validate(exchanges, policy); err != nil {
		return err
	}
	resolver, err := NewPrivateMediaResolver(evidence)
	if err != nil {
		return fmt.Errorf("%w: media evidence", ErrIncompleteCapture)
	}
	for _, exchange := range exchanges {
		if media := exchange.Response.Body.Media; media != nil && media.Recipe == "" {
			if err := resolver.checkLength(MediaLocation{Sequence: exchange.Sequence, Part: -1}, *media); err != nil {
				return fmt.Errorf("%w: response media evidence", ErrIncompleteCapture)
			}
		}
		if multipart := exchange.Request.Body.Multipart; multipart != nil {
			for i, part := range multipart.Parts {
				if part.Media == nil || part.Media.Recipe != "" {
					continue
				}
				if err := resolver.checkLength(MediaLocation{Sequence: exchange.Sequence, Part: i}, *part.Media); err != nil {
					return fmt.Errorf("%w: request media evidence", ErrIncompleteCapture)
				}
			}
		}
	}
	return nil
}

func (r *Recorder) sequence() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextSeq++
	return r.nextSeq
}

func (r *Recorder) addExchange(exchange Exchange) {
	r.mu.Lock()
	r.exchanges = append(r.exchanges, exchange)
	r.mu.Unlock()
}

func (r *Recorder) addFailure() {
	r.mu.Lock()
	r.failures = append(r.failures, ErrTransportFailure)
	r.mu.Unlock()
}

func (r *Recorder) addEvidence(evidence MediaEvidence) {
	r.mu.Lock()
	r.evidence = append(r.evidence, evidence)
	r.mu.Unlock()
}

// MediaEvidence returns a snapshot of private evidence locations. The sources
// are not part of Exchange and cannot be serialized.
func (r *Recorder) MediaEvidence() []MediaEvidence {
	if r.deferred != nil {
		r.deferred.mu.Lock()
		defer r.deferred.mu.Unlock()
		return append([]MediaEvidence(nil), r.deferred.evidence...)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]MediaEvidence(nil), r.evidence...)
}
