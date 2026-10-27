package wirefixture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

var (
	ErrCaptureLimit    = errors.New("wirefixture: deferred capture limit exceeded")
	ErrCaptureSealed   = errors.New("wirefixture: deferred capture is sealed")
	ErrCaptureReleased = errors.New("wirefixture: deferred capture released")
	ErrNotDeferred     = errors.New("wirefixture: recorder is not deferred")
)

// DeferredLimits bounds retained body buffer capacity and copied URL/header/
// metadata strings, including in-flight and completed exchanges. MaxExchanges bounds
// empty exchanges and cancellation registrations. Both limits must be positive.
// This is not an App RSS limit: business bodies, allocator overhead and sanitizer
// scratch space are outside this budget. Each body is additionally capped at 8 MiB.
type DeferredLimits struct {
	MaxBytes     int64
	MaxExchanges int
}

const (
	deferredMaxBodyBytes  = 8 << 20
	deferredMaxMediaBytes = 2 << 30
	deferredMaxMediaTotal = 4 << 30
)

// NewDeferredRecorder opts into memory-only capture without a private evidence
// store. Binary bodies cannot finalize in this mode.
func NewDeferredRecorder(metadata Metadata, limits DeferredLimits) (*Recorder, error) {
	return NewDeferredRecorderWithEvidence(metadata, limits, nil)
}

// NewDeferredRecorderWithEvidence additionally spools binary response bodies to
// the private store. Policy remains unused until Finalize; only Finalize can
// publish sanitized exchanges and it requires a reviewed exact Policy after all
// bodies and RoundTrips have ended. Default must remain immutable, as with an
// ordinary Recorder. Call Release on abandonment. Body bytes are never pre-read
// or drained on Close; media bytes are written only as the caller reads them.
func NewDeferredRecorderWithEvidence(metadata Metadata, limits DeferredLimits, evidence *PrivateStore) (*Recorder, error) {
	if limits.MaxBytes <= 0 || limits.MaxExchanges <= 0 {
		return nil, ErrCaptureLimit
	}
	return &Recorder{Default: metadata, Evidence: evidence, deferred: &deferredCapture{limits: limits, store: evidence}}, nil
}

type deferredCapture struct {
	mu        sync.Mutex
	limits    DeferredLimits
	used      int64
	mediaUsed int64
	store     *PrivateStore
	pending   []*deferredExchange // Entry order is the evidence sequence.
	evidence  []MediaEvidence
	sealed    bool
	err       error // One fixed error, never a per-request failure list.
	exchanges []Exchange
}

type deferredExchange struct {
	capture
	reqLength       int64
	respLength      int64
	tripDone        bool
	activeIO        int
	stop            func() bool
	mediaSink       *PrivateSink
	mediaType       string
	mediaBytes      int64
	retryableFailed bool
	discarded       bool
}

// Finalize is single-use after quiescence. An early call returns
// ErrIncompleteCapture without sealing, so the caller can wait and try again.
// Once sealed, no second policy can repair a rejected capture. The caller must
// stop new requests first; later requests pass through but invalidate evidence.
// Policy maps are cloned and not retained after validation. They must not be
// mutated concurrently with this call. A smaller MaxBodyBytes is also enforced;
// it cannot raise the 8 MiB collection ceiling. Unknown keys/values fail closed.
func (r *Recorder) Finalize(policy Policy) error {
	d := r.deferred
	if d == nil {
		return ErrNotDeferred
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	if d.sealed {
		return ErrCaptureSealed
	}
	successes := 0
	for _, c := range d.pending {
		if c.retryableFailed || c.discarded {
			continue
		}
		if !c.tripDone || !c.reqDone || !c.respDone || c.activeIO != 0 {
			return fmt.Errorf("%w: exchange %d %s %s trip=%t request=%t response=%t io=%d",
				ErrIncompleteCapture, c.sequence, c.method, c.path, c.tripDone, c.reqDone, c.respDone, c.activeIO)
		}
		successes++
	}
	d.sealed = true
	defer d.clearRawLocked()
	policy = clonePolicy(policy)
	if err := ValidatePolicy(policy); err != nil {
		d.failLocked(fmt.Errorf("%w: %v", ErrInvalidExchange, err))
		return d.err
	}
	if len(policy.Projection) > 0 {
		policy.projector = newProjector()
	}
	// A private ordinary Recorder reuses finish and AssertComplete, including
	// strict JSON/SSE, metadata, header and multipart validation. No disk sink.
	validated := &Recorder{Policy: policy, nextSeq: successes, Evidence: d.store}
	validated.evidence = append([]MediaEvidence(nil), d.evidence...)
	defer func() { validated.Policy = Policy{} }()
	for _, c := range d.pending {
		if c.retryableFailed || c.discarded {
			continue
		}
		if int64(len(c.reqRaw)) > policy.limit() || int64(len(c.respRaw)) > policy.limit() {
			d.failLocked(ErrCaptureLimit)
			return d.err
		}
		c.rec = validated
		c.finish()
		c.rec = nil
	}
	if err := validated.AssertComplete(); err != nil {
		detail := err.Error()
		for _, exchange := range validated.exchanges {
			if exchange.Invalid != "" {
				detail = fmt.Sprintf("exchange %d: %s", exchange.Sequence, exchange.Invalid)
				break
			}
		}
		d.failLocked(fmt.Errorf("%w: %s", ErrInvalidExchange, detail))
		return d.err
	}
	d.exchanges = validated.exchanges
	return nil
}

// Release permanently invalidates deferred evidence and drops capture-owned raw
// data and cancellation callbacks. It never reads or closes a business body.
func (r *Recorder) Release() error {
	if r.deferred == nil {
		return ErrNotDeferred
	}
	d := r.deferred
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failLocked(ErrCaptureReleased)
	return nil
}

func (d *deferredCapture) clearRawLocked() {
	for _, c := range d.pending {
		if c.stop != nil {
			c.stop()
			c.stop = nil
		}
		if c.mediaSink != nil {
			c.mediaSink.Abort()
			c.mediaSink = nil
		}
		c.reqRaw, c.respRaw = nil, nil
		c.requestHeaders, c.responseHeaders = nil, nil
		c.method, c.path, c.host, c.scheme, c.rawQuery = "", "", "", "", ""
		c.metadata = Metadata{}
		c.media = nil
		c.rec = nil
	}
	d.pending = nil
	d.used = 0
}

func (d *deferredCapture) failLocked(err error) {
	if d.err == nil {
		d.err = err
	}
	d.sealed = true
	d.exchanges = nil
	for _, item := range d.evidence {
		item.Source.discard()
	}
	d.evidence = nil
	d.mediaUsed = 0
	d.clearRawLocked()
}

func (d *deferredCapture) reserveLocked(n int64) bool {
	if n > d.limits.MaxBytes-d.used {
		d.failLocked(ErrCaptureLimit)
		return false
	}
	d.used += n
	return true
}

// Only headers used by finish are copied. Count slots too, so arbitrarily many
// empty header values cannot bypass the byte budget.
func (d *deferredCapture) headersLocked(headers http.Header, allow map[string]bool) http.Header {
	out := http.Header{}
	for name, values := range headers {
		if !allow[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}
		n := int64(len(name)) + 32
		for _, value := range values {
			n += int64(len(value)) + 16
		}
		if !d.reserveLocked(n) {
			return nil
		}
		copyValues := make([]string, len(values))
		for i, value := range values {
			copyValues[i] = strings.Clone(value)
		}
		out[strings.Clone(name)] = copyValues
	}
	return out
}

func (d *deferredCapture) start(r *Recorder, connection string, req *http.Request) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return 0
	}
	if d.sealed {
		d.failLocked(ErrCaptureSealed)
		return 0
	}
	if req.Context().Err() != nil {
		return 0
	}
	if len(d.pending) >= d.limits.MaxExchanges {
		d.failLocked(ErrCaptureLimit)
		return 0
	}
	if req.URL.User != nil {
		d.failLocked(ErrInvalidExchange)
		return 0
	}
	path := req.URL.EscapedPath()
	rawQuery := req.URL.RawQuery
	meta := r.metadata(connection, req)
	fields := []*string{&meta.Scenario, &meta.Connection, &meta.Protocol, &meta.Endpoint,
		&meta.APIVersion, &meta.RequestedModel, &meta.ReportedModel, &meta.ServerBuild,
		&meta.AppRevision, &meta.CatalogRevision, &meta.Environment}
	n := int64(len(req.Method)) + int64(len(path)) + int64(len(req.URL.Host)) + int64(len(req.URL.Scheme)) +
		int64(len(rawQuery)) + int64(len(meta.Provenance))
	for _, field := range fields {
		n += int64(len(*field))
	}
	if !d.reserveLocked(n) {
		return 0
	}
	for _, field := range fields {
		*field = strings.Clone(*field)
	}
	meta.Provenance = Provenance(strings.Clone(string(meta.Provenance)))
	headers := d.headersLocked(req.Header, requestHeaders)
	if d.err != nil {
		return 0
	}
	seq := len(d.pending) + 1
	c := &deferredExchange{capture: capture{sequence: seq, metadata: meta,
		method: strings.Clone(req.Method), path: strings.Clone(path), host: strings.Clone(req.URL.Host), scheme: strings.Clone(req.URL.Scheme),
		rawQuery: strings.Clone(rawQuery), requestHeaders: headers, reqDone: req.Body == nil || req.Body == http.NoBody}, reqLength: req.ContentLength}
	d.pending = append(d.pending, c)
	c.stop = context.AfterFunc(req.Context(), func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if entry := d.entryLocked(seq); entry != nil {
			entry.discarded = true
			entry.tripDone, entry.reqDone, entry.respDone = true, true, true
		}
	})
	return seq
}

func (d *deferredCapture) entryLocked(seq int) *deferredExchange {
	if d.err != nil || seq <= 0 || seq > len(d.pending) {
		return nil
	}
	entry := d.pending[seq-1]
	if entry.discarded {
		return nil
	}
	return entry
}

// DiscardIncomplete drops exchanges the caller never finished reading. A live
// SSE consumer may stop after the last event without closing the body; such a
// suffix cannot be published, but it must not invalidate completed exchanges.
func (r *Recorder) DiscardIncomplete() error {
	if r.deferred == nil {
		return ErrNotDeferred
	}
	d := r.deferred
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	for _, c := range d.pending {
		if c.retryableFailed || c.discarded {
			continue
		}
		if c.tripDone && c.reqDone && c.respDone && c.activeIO == 0 {
			continue
		}
		if baseMedia(c.responseHeaders.Get("Content-Type")) != "text/event-stream" {
			continue
		}
		c.discarded = true
		c.tripDone, c.reqDone, c.respDone = true, true, true
		c.activeIO = 0
		if c.stop != nil {
			c.stop()
			c.stop = nil
		}
		if c.mediaSink != nil {
			c.mediaSink.Abort()
			c.mediaSink = nil
		}
	}
	return nil
}

func (d *deferredCapture) roundTrip(r *Recorder, connection string, base http.RoundTripper, req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || base == nil {
		d.mu.Lock()
		d.failLocked(ErrTransportFailure)
		d.mu.Unlock()
		if base != nil {
			return base.RoundTrip(req)
		}
		return nil, ErrTransportFailure
	}
	seq := d.start(r, connection, req)
	if seq == 0 {
		return base.RoundTrip(req)
	}
	// A shallow copy replaces only Body; URL, GetBody and ContentLength retain
	// their original values. No callback captures req, its headers or its body.
	clone := new(http.Request)
	*clone = *req
	if req.Body != nil && req.Body != http.NoBody {
		clone.Body = &deferredBody{body: req.Body, capture: d, sequence: seq}
	}
	resp, err := base.RoundTrip(clone)
	if err != nil || resp == nil {
		d.mu.Lock()
		if c := d.entryLocked(seq); c != nil && isRetryableAssetRequest(req) {
			// Read-only asset downloads may be retried by the business layer.
			// This attempt is discarded; a later success is recorded normally.
			c.retryableFailed = true
			c.tripDone = true
			d.stopCompletedLocked(c)
		} else {
			d.failLocked(fmt.Errorf("%w: %s %s", ErrTransportFailure, req.Method, req.URL.EscapedPath()))
		}
		d.mu.Unlock()
		return resp, err
	}
	mediaType := baseMedia(resp.Header.Get("Content-Type"))
	emptyResponse := req.Method == http.MethodHead || resp.StatusCode == http.StatusNoContent ||
		resp.StatusCode == http.StatusNotModified || resp.Body == nil || resp.Body == http.NoBody
	spoolMedia := supportedMediaTypes[mediaType] && !emptyResponse
	var sink *PrivateSink
	if spoolMedia {
		if d.store == nil {
			d.mu.Lock()
			d.failLocked(ErrMediaEvidence)
			d.mu.Unlock()
			return resp, nil
		}
		sink, err = d.store.NewSink(mediaType)
		if err != nil {
			d.mu.Lock()
			d.failLocked(ErrMediaEvidence)
			d.mu.Unlock()
			return resp, nil
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.entryLocked(seq)
	if c == nil {
		if sink != nil {
			sink.Abort()
		}
		return resp, nil
	}
	headers := d.headersLocked(resp.Header, responseHeaders)
	if d.err != nil {
		if sink != nil {
			sink.Abort()
		}
		return resp, nil
	}
	c.status, c.responseHeaders, c.respLength, c.tripDone = resp.StatusCode, headers, resp.ContentLength, true
	c.emptyResponse = emptyResponse
	if spoolMedia {
		c.respDone = false
		c.mediaSink, c.mediaType = sink, mediaType
		resp.Body = &deferredMediaBody{body: resp.Body, capture: d, sequence: seq}
		return resp, nil
	}
	c.respDone = resp.Body == nil || resp.Body == http.NoBody
	d.stopCompletedLocked(c)
	if !c.respDone {
		resp.Body = &deferredBody{body: resp.Body, capture: d, sequence: seq, response: true}
	}
	return resp, nil
}

func (d *deferredCapture) stopCompletedLocked(c *deferredExchange) {
	if c.tripDone && c.reqDone && c.respDone && c.stop != nil {
		stop := c.stop
		c.stop = nil
		if !stop() {
			c.discarded = true
		}
	}
}

type deferredBody struct {
	body     io.ReadCloser
	capture  *deferredCapture // No raw buffer or callbacks live in the body wrapper.
	sequence int
	response bool
}

func (b *deferredBody) Read(p []byte) (int, error) {
	b.capture.beginIO(b.sequence)
	n, err := b.body.Read(p)
	b.capture.observe(b.sequence, b.response, p[:n], err, false)
	return n, err
}

func (b *deferredBody) Close() error {
	b.capture.beginIO(b.sequence)
	err := b.body.Close()
	b.capture.observe(b.sequence, b.response, nil, err, true)
	return err
}

func (d *deferredCapture) beginIO(seq int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c := d.entryLocked(seq); c != nil {
		c.activeIO++
	}
}

type deferredMediaBody struct {
	body     io.ReadCloser
	capture  *deferredCapture
	sequence int
	mu       sync.Mutex
	done     bool
	closed   bool
}

func (b *deferredMediaBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done || b.closed {
		return 0, io.EOF
	}
	n, err := b.body.Read(p)
	if n > 0 {
		b.capture.writeMedia(b.sequence, p[:n])
	}
	if err == io.EOF {
		b.done = true
		b.capture.finalizeMedia(b.sequence)
	} else if err != nil {
		b.done = true
		b.capture.abortMedia(b.sequence)
	}
	return n, err
}

func (b *deferredMediaBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	err := b.body.Close()
	if !b.done {
		b.done = true
		b.capture.abortMedia(b.sequence)
	}
	return err
}

func (d *deferredCapture) writeMedia(seq int, data []byte) {
	d.mu.Lock()
	c := d.entryLocked(seq)
	if c == nil || c.mediaSink == nil {
		d.mu.Unlock()
		return
	}
	length := int64(len(data))
	if length > deferredMaxMediaBytes-c.mediaBytes || length > deferredMaxMediaTotal-d.mediaUsed {
		d.failLocked(ErrCaptureLimit)
		d.mu.Unlock()
		return
	}
	c.mediaBytes += length
	d.mediaUsed += length
	sink := c.mediaSink
	d.mu.Unlock()
	if _, err := sink.Write(data); err != nil {
		d.mu.Lock()
		d.failLocked(ErrMediaEvidence)
		d.mu.Unlock()
	}
}

func isRetryableAssetRequest(req *http.Request) bool {
	return req != nil && req.URL != nil && req.Method == http.MethodGet &&
		strings.Contains(req.URL.EscapedPath(), "/output/")
}

func (d *deferredCapture) finalizeMedia(seq int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.entryLocked(seq)
	if c == nil || c.mediaSink == nil {
		return
	}
	sink := c.mediaSink
	c.mediaSink = nil
	if c.mediaBytes <= 0 {
		sink.Abort()
		d.failLocked(ErrIncompleteCapture)
		return
	}
	source, err := sink.Source()
	if err != nil || source.length != c.mediaBytes {
		if source != nil {
			source.discard()
		}
		if err == nil {
			err = ErrMediaMismatch
		}
		d.failLocked(ErrMediaEvidence)
		return
	}
	c.media = &Media{MediaType: c.mediaType, Length: source.length}
	c.respDone = true
	d.evidence = append(d.evidence, MediaEvidence{Location: MediaLocation{Sequence: seq, Part: -1}, Source: source})
	d.stopCompletedLocked(c)
}

func (d *deferredCapture) abortMedia(seq int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.entryLocked(seq)
	if c == nil {
		return
	}
	if c.mediaSink != nil {
		c.mediaSink.Abort()
		c.mediaSink = nil
	}
	d.failLocked(fmt.Errorf("%w: media body interrupted", ErrIncompleteCapture))
}

func (d *deferredCapture) observe(seq int, response bool, data []byte, err error, closed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil && (closed || err != io.EOF) {
		if c := d.entryLocked(seq); c != nil && baseMedia(c.responseHeaders.Get("Content-Type")) == "text/event-stream" {
			c.discarded = true
			c.tripDone, c.reqDone, c.respDone = true, true, true
			if c.stop != nil {
				c.stop()
				c.stop = nil
			}
			return
		}
		side, action := "request", "read"
		if response {
			side = "response"
		}
		if closed {
			action = "close"
		}
		d.failLocked(fmt.Errorf("%w: %s body %s failed", ErrIncompleteCapture, side, action))
		return
	}
	c := d.entryLocked(seq)
	if c == nil {
		return
	}
	c.activeIO--
	raw, done, length, headers, status := &c.reqRaw, &c.reqDone, c.reqLength, c.requestHeaders, 0
	if response {
		raw, done, length, headers, status = &c.respRaw, &c.respDone, c.respLength, c.responseHeaders, c.status
	}
	if *done {
		if len(data) != 0 {
			d.failLocked(ErrIncompleteCapture)
		}
		return
	}
	if len(data) > deferredMaxBodyBytes-len(*raw) {
		d.failLocked(ErrCaptureLimit)
		return
	}
	if needed := len(*raw) + len(data); needed > cap(*raw) {
		capacity := max(needed, min(2*cap(*raw), deferredMaxBodyBytes))
		if int64(capacity-cap(*raw)) > d.limits.MaxBytes-d.used {
			capacity = needed
		}
		if !d.reserveLocked(int64(capacity - cap(*raw))) {
			return
		}
		buf := make([]byte, len(*raw), capacity)
		copy(buf, *raw)
		*raw = buf
	}
	*raw = append(*raw, data...)
	if closed || err == io.EOF {
		if !completeBody(*raw, length, headers.Get("Content-Type"), c.method, status, err == io.EOF) {
			d.failLocked(ErrIncompleteCapture)
			return
		}
		*done = true
		d.stopCompletedLocked(c)
	}
}
