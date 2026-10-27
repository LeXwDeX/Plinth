// Package wirefixture captures and replays sanitized JSON, SSE, narrow
// multipart file-upload and binary HTTP exchanges. It is a test-only
// foundation. Query-bearing requests, redirects and unsupported media types
// fail validation. Binary evidence is private and never exported.
package wirefixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"
)

type Provenance string

const (
	ProvenanceCaptured        Provenance = "captured"
	ProvenanceOfficialExample Provenance = "official-example"
	ProvenanceDerived         Provenance = "derived"
	ProvenanceSynthetic       Provenance = "synthetic"
)

const (
	PayloadNone      = "none"
	PayloadJSON      = "json"
	PayloadSSE       = "sse"
	PayloadMedia     = "media"
	PayloadMultipart = "multipart"

	TransformNormalizedSSE = "normalized_sse"
)

type Metadata struct {
	Scenario        string     `json:"scenario"`
	Connection      string     `json:"connection"`
	Protocol        string     `json:"protocol"`
	Endpoint        string     `json:"endpoint"`
	APIVersion      string     `json:"api_version"`
	RequestedModel  string     `json:"requested_model"`
	ReportedModel   string     `json:"reported_model"`
	ServerBuild     string     `json:"server_build"`
	CapturedAt      time.Time  `json:"captured_at"`
	AppRevision     string     `json:"app_revision"`
	CatalogRevision string     `json:"catalog_revision"`
	Environment     string     `json:"environment"`
	Provenance      Provenance `json:"provenance"`
}

type Payload struct {
	Encoding  string          `json:"encoding"`
	Transform string          `json:"transform,omitempty"`
	JSON      json.RawMessage `json:"json,omitempty"`
	SSE       string          `json:"sse,omitempty"`
	Media     *Media          `json:"media,omitempty"`
	Multipart *Multipart      `json:"multipart,omitempty"`
}

type Request struct {
	Method      string            `json:"method"`
	Host        string            `json:"host"`
	Path        string            `json:"path"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        Payload           `json:"body"`
	Complete    bool              `json:"complete"`
}

type Response struct {
	Status      int               `json:"status"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        Payload           `json:"body"`
	Complete    bool              `json:"complete"`
}

type Exchange struct {
	Sequence int      `json:"sequence"`
	Metadata Metadata `json:"metadata"`
	Request  Request  `json:"request"`
	Response Response `json:"response"`
	Complete bool     `json:"complete"`
	Invalid  string   `json:"invalid,omitempty"`
}

// Policy is fail-closed. Keys approves JSON fields and multipart part names;
// Allowed approves exact public strings and JSON number text. Aliases maps raw
// values to terminal Allowed values, redactions, public URLs or deeply
// sanitized JSON. Alias chains are rejected. Strings still undergo
// credential/address checks.
//
// PublicHosts registers exact lowercase upstream hosts whose URLs may be
// published with fixed fixture paths. Projection maps registered, non-sensitive
// JSON field names to "id" or "url"; dynamic values become deterministic
// placeholders before export. QueryKeys registers request query parameter names;
// values are never retained. Projection is only available to deferred capture,
// where a reviewed final policy is applied after requests stop.
type Policy struct {
	MaxBodyBytes int64
	Keys         map[string]bool
	Allowed      map[string]bool
	Aliases      map[string]string
	PublicHosts  map[string]bool
	Projection   map[string]string
	QueryKeys    map[string]bool

	projector *projector
}

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

var (
	ErrUnsafePublicValue  = errors.New("wirefixture: unsafe public value")
	ErrUnknownKey         = errors.New("wirefixture: unknown JSON key")
	ErrUnsupportedBody    = errors.New("wirefixture: unsupported body type")
	ErrPartialSSE         = errors.New("wirefixture: partial SSE stream")
	ErrInvalidExchange    = errors.New("wirefixture: invalid exchange")
	ErrUnexpectedRequest  = errors.New("wirefixture: unexpected replay request")
	ErrReorderedRequest   = errors.New("wirefixture: reordered replay request")
	ErrExhaustedRequests  = errors.New("wirefixture: replay requests exhausted")
	ErrUnconsumedRequest  = errors.New("wirefixture: replay did not consume every request")
	ErrInsideWorktree     = errors.New("wirefixture: candidate is inside the worktree")
	ErrExistingCandidate  = errors.New("wirefixture: candidate already exists")
	ErrTransportFailure   = errors.New("wirefixture: transport failure")
	ErrIncompleteCapture  = errors.New("wirefixture: incomplete capture")
	ErrMediaEvidence      = errors.New("wirefixture: media evidence unavailable")
	ErrMediaResolver      = errors.New("wirefixture: media resolver mismatch")
	ErrMediaMismatch      = errors.New("wirefixture: media mismatch")
	ErrConnectionMismatch = errors.New("wirefixture: replay connection mismatch")
	ErrReplayFailed       = errors.New("wirefixture: replay failed")
)

func (p Policy) limit() int64 {
	if p.MaxBodyBytes > 0 {
		return p.MaxBodyBytes
	}
	return 8 << 20
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
		return sortedExchanges(r.deferred.exchanges)
	}
	r.mu.Lock()
	out := sortedExchanges(r.exchanges)
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
	exchanges := sortedExchanges(r.exchanges)
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
	if len(policy.Projection) > 0 {
		policy.projector = newProjector()
	}
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

func sortedExchanges(exchanges []Exchange) []Exchange {
	out := slices.Clone(exchanges)
	for i := range out {
		out[i].Request.Headers = maps.Clone(out[i].Request.Headers)
		out[i].Response.Headers = maps.Clone(out[i].Response.Headers)
		out[i].Request.Body.JSON = slices.Clone(out[i].Request.Body.JSON)
		out[i].Response.Body.JSON = slices.Clone(out[i].Response.Body.JSON)
		out[i].Request.Body.Media = cloneMedia(out[i].Request.Body.Media)
		out[i].Response.Body.Media = cloneMedia(out[i].Response.Body.Media)
		out[i].Request.Body.Multipart = cloneMultipart(out[i].Request.Body.Multipart)
		out[i].Response.Body.Multipart = cloneMultipart(out[i].Response.Body.Multipart)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}

func clonePolicy(p Policy) Policy {
	p.Keys = maps.Clone(p.Keys)
	p.Allowed = maps.Clone(p.Allowed)
	p.Aliases = maps.Clone(p.Aliases)
	p.PublicHosts = maps.Clone(p.PublicHosts)
	p.Projection = maps.Clone(p.Projection)
	p.QueryKeys = maps.Clone(p.QueryKeys)
	return p
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
