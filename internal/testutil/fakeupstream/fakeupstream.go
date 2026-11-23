// Package fakeupstream is a test-only fake of the upstream APIs that Plinth's
// Provider Adapters call (Tripo, an OpenAI-compatible LLM gateway). It lets a
// full Plinth stack run in live mode against loopback with zero cost.
//
// Rules the package keeps (issue #96):
//
//   - Versions are append-only. Every upstream API version is a separate
//     Version value with its own route prefix, endpoint table, committed real
//     samples and alignment report. Adding a version never edits an older
//     one; older versions stay as the regression baseline of the Adapter that
//     speaks them. Versions are registered explicitly (see package registry),
//     never switched by a condition.
//   - Response structure comes from committed, sanitized real samples (the
//     wirefixture candidates in each version's testdata), not from a second
//     hand-written structure. An endpoint without a real sample is declared
//     EvidenceConstructed with the official documentation URL and the date it
//     was checked.
//   - Values are fixed. The fake only promises the same structure (field set,
//     types, nesting, status codes, status enums); it never tries to look
//     realistic.
//   - It is never linked into a production binary: no shipping command may
//     depend on any package under internal/testutil
//     (TestProductionBinariesDoNotLinkAnyTestutilPackage, TestOnlyTestToolingImportsTestutil).
package fakeupstream

import (
	"net/http"
	"time"
)

// Evidence says where an endpoint's response structure comes from.
type Evidence string

const (
	// EvidenceReal: the structure is derived from a committed sample captured
	// from the official API (provenance "captured").
	EvidenceReal Evidence = "real"
	// EvidenceConstructed: no real sample exists; the structure is built from
	// the official documentation named in DocURL, checked on CheckedAt.
	EvidenceConstructed Evidence = "constructed-from-docs"
)

// Endpoint describes one route a Version serves.
type Endpoint struct {
	Method string
	// Path is relative to the version prefix and may use {name} segments.
	Path     string
	Evidence Evidence
	// Sample is the key (see SampleKey) of the committed exchange the
	// response structure is derived from; empty for constructed endpoints.
	Sample string
	// DocURL and CheckedAt are mandatory for constructed endpoints and
	// recorded for real ones (the page the structure was cross-checked with).
	DocURL    string
	CheckedAt string
	Note      string
}

// Key is "METHOD /path" with the path template.
func (e Endpoint) Key() string { return e.Method + " " + e.Path }

// Version is one upstream API version.
type Version struct {
	Provider string
	API      string
	// Prefix is the route prefix the version is mounted under, e.g.
	// "/tripo/v3". A Plinth stack points the provider's base URL at
	// <fake origin><Prefix>.
	Prefix string
	// Official is the official base URL the fake mirrors (documentation of
	// what was aligned; never contacted by the server).
	Official  string
	Endpoints []Endpoint
	// Handler builds the version's routes. Requests arrive with Prefix
	// already stripped.
	Handler func(*Env) (http.Handler, error)
}

// Config tunes the fake. Zero values give the documented defaults.
type Config struct {
	// QueuedPolls and RunningPolls are the number of task polls that answer
	// "queued" and "running" before a task answers its terminal state.
	// Defaults 1 and 1, so a task succeeds on its third poll.
	QueuedPolls  int
	RunningPolls int
	// TimeoutHold is how long the "timeout" scenario holds a request open
	// before dropping the connection. Default 2 minutes.
	TimeoutHold time.Duration
	// RetryAfter is the Retry-After value of the "rate-limit" scenario.
	// Default 1 second.
	RetryAfter time.Duration
	// PollRetryAfter is the Retry-After header a non-terminal status poll
	// carries. Default 5 seconds (Meshy documents it; Hyper3D asks clients to
	// honor it too).
	PollRetryAfter time.Duration
	// FailTimes limits how many matching requests a request-phase scenario
	// fails before the fake answers normally again. 0 means always. The
	// X-Fake-Fail-Times request header overrides it per request.
	FailTimes int
	// ConvertURLDelay is how long the Tripo fake takes to answer the creation
	// of a model conversion whose input is a URL: the real API took 47 to 48
	// seconds for it (2026-11-16, #305), because it fetches the model before
	// it answers. The default is 10 ms, so a test stays fast and can raise it
	// to prove that a client's request timeout is long enough; a negative
	// value answers at once.
	ConvertURLDelay time.Duration
	// ArtifactOrigin, when set, replaces the origin of artifact URLs in task
	// outputs ("https://assets.example.test"). A stack whose Adapter only
	// accepts public HTTPS artifact hosts maps that host back to the fake with
	// a test transport; empty means "the origin the client addressed".
	ArtifactOrigin string
	// Now is the clock; tests inject one. Default time.Now.
	Now func() time.Time
	// AccountConcurrencyLimit tunes the task-category pools. 0 keeps the official
	// per-category defaults (3D generation H 10 / P 5, image generation 1,
	// animation 10, model processing 5, mesh operations 10); a positive value pins
	// every category to that many in-flight tasks; a negative value disables the
	// pools. A category over its pool answers task creation with 429 code 2000 and
	// Retry-After (#468). Categories have independent pools, so one filling up
	// never blocks another.
	AccountConcurrencyLimit int
}

func (c Config) withDefaults() Config {
	if c.QueuedPolls <= 0 {
		c.QueuedPolls = 1
	}
	if c.RunningPolls <= 0 {
		c.RunningPolls = 1
	}
	if c.TimeoutHold <= 0 {
		c.TimeoutHold = 2 * time.Minute
	}
	if c.RetryAfter <= 0 {
		c.RetryAfter = time.Second
	}
	if c.PollRetryAfter <= 0 {
		c.PollRetryAfter = 5 * time.Second
	}
	if c.ConvertURLDelay == 0 {
		c.ConvertURLDelay = 10 * time.Millisecond
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}
