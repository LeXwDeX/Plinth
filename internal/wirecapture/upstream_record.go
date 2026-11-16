package wirecapture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Opt-in upstream recorder (#220, #233). It wraps a Provider HTTP transport
// and, after each exchange completes, writes one already-sanitized record to a
// private directory. Two front ends build it:
//
//   - NewUpstreamRecorder: test tooling. A directory outside the worktree,
//     non-production labels, every record kept.
//   - NewRuntimeRecorder: the application itself, enabled by the operator. The
//     directory is created with mode 0700, files are rotated by count and total
//     size, and the strict redaction profile applies (see draft.go).
//
// Raw bodies stay in memory for the duration of one exchange and are never
// written; only the sanitized Exchange, sorted query key names and fixed skip
// reasons reach disk. Recording failures never reach the business request: the
// recorder only observes the caller's reads and recovers from its own faults.

const (
	// UpstreamRecordSuffix names every record file in the private directory.
	UpstreamRecordSuffix = ".exchange.json"

	upstreamRecordVersion = 1

	// defaultRecordBodyLimit caps each buffered JSON/SSE/multipart body of the
	// tooling recorder; defaultRuntimeBodyLimit is the application's cap.
	defaultRecordBodyLimit  = 8 << 20
	defaultRuntimeBodyLimit = 1 << 20

	// recordTimeLayout starts every record file name: fixed width and UTC, so
	// name order is time order.
	recordTimeLayout = "20060102T150405.000000000Z"
)

// ErrUpstreamRecordRefused reports a configuration the recorder will not run
// with: an unsafe directory, a production-like environment label for the
// tooling recorder, missing revisions or missing limits.
var ErrUpstreamRecordRefused = errors.New("wirecapture: upstream recording refused")

// productionLikeRE matches environment labels that name a production or
// human-review stack; the tooling recorder is refused there. The application
// recorder is meant for deployed stacks and does not apply it.
var productionLikeRE = regexp.MustCompile(`(?i)prod|formal|release|live`)

// UpstreamRecordConfig configures NewUpstreamRecorder (test tooling).
type UpstreamRecordConfig struct {
	// Dir is an existing 0700 directory outside Worktree.
	Dir      string
	Worktree string
	// Defaults supplies Scenario, AppRevision, CatalogRevision and other
	// metadata. Environment must be a non-production identifier.
	Defaults Metadata
	// MaxBodyBytes caps each buffered JSON/SSE/multipart body; larger
	// exchanges pass through unrecorded. Media responses are only counted.
	MaxBodyBytes int64
}

// RuntimeConfig configures NewRuntimeRecorder (the application, #233).
type RuntimeConfig struct {
	// Dir is created with mode 0700 when missing; an existing directory must be
	// a real directory (no symbolic link) and is tightened to 0700.
	Dir string
	// Defaults supplies the identifier-shaped Environment, AppRevision and
	// CatalogRevision that every record carries.
	Defaults Metadata
	// MaxRecords and MaxBytes bound the directory: when a write would exceed
	// either, the oldest record files are removed.
	MaxRecords int
	MaxBytes   int64
	// MaxBodyBytes caps each buffered body (default 1 MiB); larger exchanges
	// pass through unrecorded with a fixed skip reason.
	MaxBodyBytes int64
	// Logger receives one warning for the first failed write and one for every
	// further thousand. Optional.
	Logger *slog.Logger
}

// UpstreamRecord is the on-disk record format.
type UpstreamRecord struct {
	Version   int       `json:"version"`
	Exchange  *Exchange `json:"exchange,omitempty"`
	QueryKeys []string  `json:"query_keys,omitempty"`
	// Skipped is a fixed reason without values when the exchange was not
	// recorded; Connection, Sequence, a registered HTTP Method and the Status
	// code (0 when no response arrived) then identify it.
	Skipped    string `json:"skipped,omitempty"`
	Connection string `json:"connection,omitempty"`
	Method     string `json:"method,omitempty"`
	Status     int    `json:"status,omitempty"`
	Sequence   int    `json:"sequence,omitempty"`
}

// RecorderStatus is the read-only state of a recorder, safe to show to an
// administrator: counts, limits and the directory the operator configured.
type RecorderStatus struct {
	Enabled bool `json:"enabled"`
	// Directory is the configured (resolved) record directory.
	Directory string `json:"directory,omitempty"`
	// MaxRecords and MaxBytes are the retention limits; zero means unbounded.
	MaxRecords int   `json:"max_records"`
	MaxBytes   int64 `json:"max_bytes"`
	// Records and Bytes are what the directory holds now (skip markers included).
	Records int   `json:"records"`
	Bytes   int64 `json:"bytes"`
	// Written, Skipped, Rotated and WriteErrors count since the process
	// started: sanitized exchanges recorded, exchanges recorded only as a
	// skip marker, files removed by rotation and failed writes.
	Written     int64 `json:"written"`
	Skipped     int64 `json:"skipped"`
	Rotated     int64 `json:"rotated"`
	WriteErrors int64 `json:"write_errors"`
}

// UpstreamRecorder writes sanitized exchanges; see the file comment.
type UpstreamRecorder struct {
	dir      string
	defaults Metadata
	limit    int64
	options  draftOptions
	ids      *draftIDs
	store    *recordStore
	logger   *slog.Logger
	now      func() time.Time
	sequence atomic.Int64
	written  atomic.Int64
	skipped  atomic.Int64
	errMu    sync.Mutex
	writeErr error
}

// NewUpstreamRecorder validates the tooling configuration and directory.
func NewUpstreamRecorder(config UpstreamRecordConfig) (*UpstreamRecorder, error) {
	environment := strings.TrimSpace(config.Defaults.Environment)
	if environment == "" || !identifierRE.MatchString(environment) || productionLikeRE.MatchString(environment) {
		return nil, fmt.Errorf("%w: environment label must be a non-production identifier", ErrUpstreamRecordRefused)
	}
	for _, value := range []string{config.Defaults.AppRevision, config.Defaults.CatalogRevision} {
		if value == "" || value == "unknown" || !identifierRE.MatchString(value) {
			return nil, fmt.Errorf("%w: app and catalog revisions are required", ErrUpstreamRecordRefused)
		}
	}
	dir, err := privateRecordDir(config.Dir, config.Worktree)
	if err != nil {
		return nil, err
	}
	meta := config.Defaults
	meta.Environment = environment
	limit := config.MaxBodyBytes
	if limit <= 0 {
		limit = defaultRecordBodyLimit
	}
	store, err := openRecordStore(dir, 0, 0)
	if err != nil {
		return nil, err
	}
	return newUpstreamRecorder(dir, meta, limit, draftOptions{limit: limit}, store, nil), nil
}

// NewRuntimeRecorder validates the application configuration, prepares the
// 0700 directory and starts accounting for the record files already in it. It
// refuses to start on anything unsafe; a recorder that cannot be enabled
// must not look enabled.
func NewRuntimeRecorder(config RuntimeConfig) (*UpstreamRecorder, error) {
	meta := config.Defaults
	for name, value := range map[string]string{
		"environment": meta.Environment, "app revision": meta.AppRevision, "catalog revision": meta.CatalogRevision,
	} {
		if value == "" || value == "unknown" || !identifierRE.MatchString(value) {
			return nil, fmt.Errorf("%w: %s must be an identifier", ErrUpstreamRecordRefused, name)
		}
	}
	if config.MaxRecords <= 0 || config.MaxBytes <= 0 {
		return nil, fmt.Errorf("%w: record and size limits are required", ErrUpstreamRecordRefused)
	}
	limit := config.MaxBodyBytes
	if limit <= 0 {
		limit = defaultRuntimeBodyLimit
	}
	if limit > defaultRecordBodyLimit {
		limit = defaultRecordBodyLimit
	}
	dir, err := prepareRuntimeDir(config.Dir)
	if err != nil {
		return nil, err
	}
	store, err := openRecordStore(dir, config.MaxRecords, config.MaxBytes)
	if err != nil {
		return nil, err
	}
	// Strict redaction (text must be a single enumeration-shaped token, numeric
	// identifiers become 0) and plain-HTTP upstreams: self-hosted services and
	// the fake upstream are not on https.
	options := draftOptions{limit: limit, strict: true, allowHTTP: true}
	return newUpstreamRecorder(dir, meta, limit, options, store, config.Logger), nil
}

func newUpstreamRecorder(dir string, meta Metadata, limit int64, options draftOptions, store *recordStore, logger *slog.Logger) *UpstreamRecorder {
	if meta.Scenario == "" {
		meta.Scenario = "upstream-record"
	}
	for _, value := range []*string{&meta.APIVersion, &meta.RequestedModel, &meta.ReportedModel, &meta.ServerBuild} {
		if *value == "" {
			*value = "unknown"
		}
	}
	recorder := &UpstreamRecorder{dir: dir, defaults: meta, limit: limit, options: options, ids: newDraftIDs(),
		store: store, logger: logger, now: time.Now}
	// Sequence numbers continue after the files already in the directory, so
	// records of different runs keep one order when a draft renumbers them.
	recorder.sequence.Store(int64(store.maxSequence()))
	return recorder
}

// Written and Skipped count records since the process started; WriteError
// reports the first failed write.
func (u *UpstreamRecorder) Written() int64 { return u.written.Load() }
func (u *UpstreamRecorder) Skipped() int64 { return u.skipped.Load() }
func (u *UpstreamRecorder) WriteError() error {
	u.errMu.Lock()
	defer u.errMu.Unlock()
	return u.writeErr
}

// Status is the recorder's read-only state. A nil recorder is a disabled one.
func (u *UpstreamRecorder) Status() RecorderStatus {
	if u == nil {
		return RecorderStatus{}
	}
	status := u.store.status()
	status.Enabled = true
	status.Directory = u.dir
	status.Written = u.written.Load()
	status.Skipped = u.skipped.Load()
	return status
}

// Wrap decorates base for one connection. Business requests, responses and
// errors pass through unchanged; recording failures never affect them. A nil
// recorder returns base itself: recording off costs nothing.
func (u *UpstreamRecorder) Wrap(connection string, base http.RoundTripper) http.RoundTripper {
	if u == nil {
		return base
	}
	return RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return u.roundTrip(connection, base, request)
	})
}

type recordedExchange struct {
	rec       *UpstreamRecorder
	sequence  int
	raw       rawExchange
	queryKeys []string
	request   *teeBody
	once      sync.Once
}

func (u *UpstreamRecorder) roundTrip(connection string, base http.RoundTripper, request *http.Request) (*http.Response, error) {
	if base == nil {
		return nil, errors.New("wirecapture: nil base transport")
	}
	if request == nil || request.URL == nil {
		return base.RoundTrip(request)
	}
	state := &recordedExchange{rec: u, sequence: int(u.sequence.Add(1)), raw: rawExchange{
		connection: connection, method: request.Method, scheme: request.URL.Scheme, host: request.URL.Host,
		path: request.URL.EscapedPath(), requestHeaders: request.Header.Clone(),
	}}
	normalizeGenericBinary(state.raw.requestHeaders)
	for key := range request.URL.Query() {
		if identifierRE.MatchString(key) && len(key) <= 64 {
			state.queryKeys = append(state.queryKeys, key)
		}
	}
	sort.Strings(state.queryKeys)
	clone := request
	if request.Body != nil && request.Body != http.NoBody {
		clone = request.Clone(request.Context())
		state.request = &teeBody{body: request.Body, limit: u.limit, buffer: true, contentLength: -1}
		clone.Body = state.request
	}
	response, err := base.RoundTrip(clone)
	if err != nil || response == nil {
		state.finish(skipTransport)
		return response, err
	}
	state.raw.status = response.StatusCode
	state.raw.responseHeaders = response.Header.Clone()
	normalizeGenericBinary(state.raw.responseHeaders)
	state.raw.emptyResponse = request.Method == http.MethodHead || response.StatusCode == http.StatusNoContent ||
		response.StatusCode == http.StatusNotModified || response.Body == nil || response.Body == http.NoBody
	if state.raw.emptyResponse {
		state.finish("")
		return response, nil
	}
	contentType, _ := ContentBase(state.raw.responseHeaders.Get("Content-Type"))
	kind, _ := PayloadKind(contentType)
	tee := &teeBody{body: response.Body, limit: u.limit, buffer: !supportedMediaTypes[contentType],
		contentLength: response.ContentLength, kind: kind}
	tee.done = func(complete bool) {
		if !complete {
			state.finish(skipIncomplete)
			return
		}
		data, over, _ := tee.snapshot()
		if over {
			state.finish(skipTooLarge)
			return
		}
		if tee.buffer {
			state.raw.responseBody = data
			if state.raw.responseBody == nil {
				state.raw.responseBody = []byte{}
			}
		}
		state.raw.responseLength = tee.length()
		tee.release()
		state.finish("")
	}
	response.Body = tee
	return response, nil
}

func (s *recordedExchange) finish(skip draftSkip) {
	s.once.Do(func() {
		// A fault in the recorder must never reach the business caller whose
		// Read or Close ran this.
		defer func() {
			if recover() != nil {
				s.raw = rawExchange{}
				s.rec.recordFailure(errors.New("wirecapture: recorder fault while recording an exchange"))
			}
		}()
		if skip == "" && s.request != nil {
			data, over, eof := s.request.snapshot()
			switch {
			case over:
				skip = skipTooLarge
			case !eof:
				skip = skipIncomplete
			default:
				s.raw.requestBody = data
			}
		}
		record := UpstreamRecord{Version: upstreamRecordVersion, QueryKeys: s.queryKeys}
		if skip == "" {
			meta := s.rec.defaults
			if meta.CapturedAt.IsZero() {
				meta.CapturedAt = s.rec.now().UTC()
			}
			exchange, reason := draftExchange(s.raw, s.sequence, meta, s.rec.ids, s.rec.options)
			if reason == "" {
				record.Exchange = &exchange
			}
			skip = reason
		}
		// Drop raw bytes as soon as the decision is made. A skip marker keeps
		// the structure that needs no value: connection, a registered method
		// and the status code.
		connection, method, status := s.raw.connection, strings.ToUpper(s.raw.method), s.raw.status
		s.raw = rawExchange{}
		if s.request != nil {
			s.request.release()
		}
		if skip != "" {
			record.Skipped = string(skip)
			record.Connection = draftIdentifier(connection)
			if methods[method] {
				record.Method = method
			}
			record.Status = status
			record.Sequence = s.sequence
			record.QueryKeys = nil
			s.rec.skipped.Add(1)
		}
		if err := s.rec.write(s.sequence, record); err != nil {
			s.rec.recordFailure(err)
			return
		}
		if skip == "" {
			s.rec.written.Add(1)
		}
	})
}

// recordFailure keeps the first write error and counts every one. The log gets
// the first and then one in a thousand, so a full disk cannot flood it.
func (u *UpstreamRecorder) recordFailure(err error) {
	count := u.store.failed()
	u.errMu.Lock()
	if u.writeErr == nil {
		u.writeErr = err
	}
	u.errMu.Unlock()
	if u.logger != nil && (count == 1 || count%1000 == 0) {
		u.logger.Warn("上游脱敏录制写入失败，业务请求不受影响", "failures", count, "error", err)
	}
}

func (u *UpstreamRecorder) write(sequence int, record UpstreamRecord) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(record); err != nil {
		return err
	}
	// Encode ends with a newline already.
	err := u.store.create(func() string {
		return fmt.Sprintf("%s-%06d%s", u.now().UTC().Format(recordTimeLayout), sequence, UpstreamRecordSuffix)
	}, encoded.Bytes())
	// The record is written even when rotation could not remove an old file.
	if rotateErr := u.store.takeRotateError(); rotateErr != nil {
		u.recordFailure(rotateErr)
	}
	return err
}

// teeBody observes the caller's reads without pre-reading or changing bytes.
type teeBody struct {
	mu     sync.Mutex
	body   io.ReadCloser
	limit  int64
	buffer bool
	// contentLength is the announced length (-1 when unknown) and kind the
	// payload kind of a response body; they let Close tell a body the caller
	// stopped reading early from one that was delivered whole.
	contentLength int64
	kind          string
	data          bytes.Buffer
	total         int64
	over          bool
	eof           bool
	done          func(complete bool)
	once          sync.Once
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.body.Read(p)
	t.mu.Lock()
	if n > 0 {
		t.total += int64(n)
		if t.buffer && !t.over {
			if int64(t.data.Len()+n) > t.limit {
				t.over = true
				t.data = bytes.Buffer{}
			} else {
				t.data.Write(p[:n])
			}
		}
	}
	if err == io.EOF {
		t.eof = true
	}
	t.mu.Unlock()
	if err == io.EOF {
		t.signal(true)
	} else if err != nil {
		t.signal(false)
	}
	return n, err
}

func (t *teeBody) Close() error {
	err := t.body.Close()
	t.mu.Lock()
	complete := t.eof || t.deliveredLocked()
	t.mu.Unlock()
	t.signal(complete)
	return err
}

// deliveredLocked reports a body that arrived whole although the caller never
// saw its EOF: a JSON decoder stops after its value, an SSE reader after
// [DONE]. Every announced byte, a complete JSON document or a framed stream
// counts; anything else the caller walked away from stays incomplete.
func (t *teeBody) deliveredLocked() bool {
	if t.contentLength >= 0 && t.total >= t.contentLength {
		return true
	}
	if t.over || !t.buffer {
		return false
	}
	switch t.kind {
	case PayloadJSON:
		return json.Valid(t.data.Bytes())
	case PayloadSSE:
		return SSEFramed(t.data.Bytes())
	}
	return false
}

// snapshot returns a copy of the buffered bytes and the read state.
func (t *teeBody) snapshot() ([]byte, bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return bytes.Clone(t.data.Bytes()), t.over, t.eof
}

func (t *teeBody) length() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

func (t *teeBody) release() {
	t.mu.Lock()
	t.data = bytes.Buffer{}
	t.mu.Unlock()
}

func (t *teeBody) signal(complete bool) {
	if t.done != nil {
		t.once.Do(func() { t.done(complete) })
	}
}
