package wirecapture

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sort"
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

var (
	ErrUnsafePublicValue = errors.New("wirecapture: unsafe public value")
	ErrUnknownKey        = errors.New("wirecapture: unknown JSON key")
	ErrUnsupportedBody   = errors.New("wirecapture: unsupported body type")
	ErrPartialSSE        = errors.New("wirecapture: partial SSE stream")
	ErrInvalidExchange   = errors.New("wirecapture: invalid exchange")
	ErrInsideWorktree    = errors.New("wirecapture: candidate is inside the worktree")
	ErrExistingCandidate = errors.New("wirecapture: candidate already exists")
	ErrMediaEvidence     = errors.New("wirecapture: media evidence unavailable")
	ErrMediaResolver     = errors.New("wirecapture: media resolver mismatch")
	ErrMediaMismatch     = errors.New("wirecapture: media mismatch")
)

// Limit is the per-body buffering cap (8 MiB unless the policy lowers it).
func (p Policy) Limit() int64 {
	if p.MaxBodyBytes > 0 {
		return p.MaxBodyBytes
	}
	return 8 << 20
}

// WithProjection returns the policy with a fresh capture-scoped projector when
// it registers projections, and unchanged otherwise. The projector maps raw
// dynamic values to stable placeholders for one capture; a policy copy that
// already carries one keeps sharing it.
func (p Policy) WithProjection() Policy {
	if len(p.Projection) > 0 {
		p.projector = newProjector()
	}
	return p
}

// ClonePolicy copies every registration map so the caller can keep mutating its
// own policy. The capture-scoped projector is not carried over.
func ClonePolicy(p Policy) Policy {
	p.Keys = maps.Clone(p.Keys)
	p.Allowed = maps.Clone(p.Allowed)
	p.Aliases = maps.Clone(p.Aliases)
	p.PublicHosts = maps.Clone(p.PublicHosts)
	p.Projection = maps.Clone(p.Projection)
	p.QueryKeys = maps.Clone(p.QueryKeys)
	return p
}

// SortedExchanges returns a deep copy ordered by Sequence (stable).
func SortedExchanges(exchanges []Exchange) []Exchange {
	out := slices.Clone(exchanges)
	for i := range out {
		out[i].Request.Headers = maps.Clone(out[i].Request.Headers)
		out[i].Response.Headers = maps.Clone(out[i].Response.Headers)
		out[i].Request.Body.JSON = slices.Clone(out[i].Request.Body.JSON)
		out[i].Response.Body.JSON = slices.Clone(out[i].Response.Body.JSON)
		out[i].Request.Body.Media = CloneMedia(out[i].Request.Body.Media)
		out[i].Response.Body.Media = CloneMedia(out[i].Response.Body.Media)
		out[i].Request.Body.Multipart = CloneMultipart(out[i].Request.Body.Multipart)
		out[i].Response.Body.Multipart = CloneMultipart(out[i].Response.Body.Multipart)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}

// RoundTripperFunc adapts a function to http.RoundTripper.
type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (f RoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
