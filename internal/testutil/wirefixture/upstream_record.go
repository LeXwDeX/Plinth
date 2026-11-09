package wirefixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Opt-in upstream recorder (#220). It wraps a Provider HTTP transport and,
// after each exchange completes, writes one already-sanitized record to a
// private directory outside the worktree. It lives in internal/testutil, so
// production binaries cannot link it (TestProductionBinariesDoNotLinkAnyTestutilPackage);
// test tooling enables it only through UpstreamRecorderFromEnv with an
// explicit ACK. Raw bodies stay in memory for the duration of one exchange
// and are never written; only the sanitized Exchange, sorted query key names
// and fixed skip reasons reach disk.

const (
	EnvUpstreamRecordDir         = "ART_AI_UPSTREAM_RECORD_DIR"
	EnvUpstreamRecordAck         = "ART_AI_UPSTREAM_RECORD_ACK"
	EnvUpstreamRecordEnvironment = "ART_AI_UPSTREAM_RECORD_ENVIRONMENT"

	// UpstreamRecordSuffix names every record file in the private directory.
	UpstreamRecordSuffix = ".exchange.json"

	upstreamRecordVersion = 1
)

// ErrUpstreamRecordRefused reports a configuration the recorder will not run
// with: half-set switches, a production-like environment label or an unsafe
// directory.
var ErrUpstreamRecordRefused = errors.New("wirefixture: upstream recording refused")

// productionLikeRE matches environment labels that name a production or
// human-review stack; recording is refused there.
var productionLikeRE = regexp.MustCompile(`(?i)prod|formal|release|live`)

// UpstreamRecordConfig configures NewUpstreamRecorder.
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

// UpstreamRecord is the on-disk record format.
type UpstreamRecord struct {
	Version   int       `json:"version"`
	Exchange  *Exchange `json:"exchange,omitempty"`
	QueryKeys []string  `json:"query_keys,omitempty"`
	// Skipped is a fixed reason without values when the exchange was not
	// recorded; Connection and Sequence then identify it.
	Skipped    string `json:"skipped,omitempty"`
	Connection string `json:"connection,omitempty"`
	Sequence   int    `json:"sequence,omitempty"`
}

// UpstreamRecorder writes sanitized exchanges; see the file comment.
type UpstreamRecorder struct {
	dir      string
	defaults Metadata
	limit    int64
	ids      *draftIDs
	sequence atomic.Int64
	written  atomic.Int64
	skipped  atomic.Int64
	errMu    sync.Mutex
	writeErr error
}

// UpstreamRecorderFromEnv returns nil when recording is off (the default).
// It refuses (error) when only one of the directory and ACK switches is set,
// when the environment label is missing or production-like, or when the
// directory is unsafe.
func UpstreamRecorderFromEnv(getenv func(string) string, worktree string, defaults Metadata) (*UpstreamRecorder, error) {
	dir := strings.TrimSpace(getenv(EnvUpstreamRecordDir))
	ack := strings.TrimSpace(getenv(EnvUpstreamRecordAck))
	if dir == "" && ack == "" {
		return nil, nil
	}
	if dir == "" || ack != "1" {
		return nil, fmt.Errorf("%w: set both %s and %s=1", ErrUpstreamRecordRefused, EnvUpstreamRecordDir, EnvUpstreamRecordAck)
	}
	if label := strings.TrimSpace(getenv(EnvUpstreamRecordEnvironment)); label != "" {
		defaults.Environment = label
	}
	return NewUpstreamRecorder(UpstreamRecordConfig{Dir: dir, Worktree: worktree, Defaults: defaults})
}

// NewUpstreamRecorder validates the configuration and directory.
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
	if meta.Scenario == "" {
		meta.Scenario = "upstream-record"
	}
	for _, value := range []*string{&meta.APIVersion, &meta.RequestedModel, &meta.ReportedModel, &meta.ServerBuild} {
		if *value == "" {
			*value = "unknown"
		}
	}
	limit := config.MaxBodyBytes
	if limit <= 0 {
		limit = 8 << 20
	}
	return &UpstreamRecorder{dir: dir, defaults: meta, limit: limit, ids: newDraftIDs()}, nil
}

func privateRecordDir(dir, worktree string) (string, error) {
	if dir == "" || !filepath.IsAbs(dir) || worktree == "" {
		return "", fmt.Errorf("%w: directory and worktree must be absolute", ErrUpstreamRecordRefused)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("%w: directory must exist with mode 0700", ErrUpstreamRecordRefused)
	}
	rootReal, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return "", fmt.Errorf("%w: worktree unavailable", ErrUpstreamRecordRefused)
	}
	dirReal, err := filepath.EvalSymlinks(dir)
	if err != nil || inside(rootReal, dirReal) {
		return "", fmt.Errorf("%w: directory is inside the worktree", ErrUpstreamRecordRefused)
	}
	if same, err := sameFileAncestor(rootReal, dirReal); err != nil || same {
		return "", fmt.Errorf("%w: directory is inside the worktree", ErrUpstreamRecordRefused)
	}
	return dirReal, nil
}

// Written and Skipped count records; WriteError reports the first failed write.
func (u *UpstreamRecorder) Written() int64 { return u.written.Load() }
func (u *UpstreamRecorder) Skipped() int64 { return u.skipped.Load() }
func (u *UpstreamRecorder) WriteError() error {
	u.errMu.Lock()
	defer u.errMu.Unlock()
	return u.writeErr
}

// Wrap decorates base for one connection. Business requests, responses and
// errors pass through unchanged; recording failures never affect them.
func (u *UpstreamRecorder) Wrap(connection string, base http.RoundTripper) http.RoundTripper {
	if u == nil {
		return base
	}
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
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
		return nil, errors.New("wirefixture: nil base transport")
	}
	if request == nil || request.URL == nil {
		return base.RoundTrip(request)
	}
	state := &recordedExchange{rec: u, sequence: int(u.sequence.Add(1)), raw: rawExchange{
		connection: connection, method: request.Method, scheme: request.URL.Scheme, host: request.URL.Host,
		path: request.URL.EscapedPath(), requestHeaders: request.Header.Clone(),
	}}
	for key := range request.URL.Query() {
		if identifierRE.MatchString(key) && len(key) <= 64 {
			state.queryKeys = append(state.queryKeys, key)
		}
	}
	clone := request
	if request.Body != nil && request.Body != http.NoBody {
		clone = request.Clone(request.Context())
		state.request = &teeBody{body: request.Body, limit: u.limit, buffer: true}
		clone.Body = state.request
	}
	response, err := base.RoundTrip(clone)
	if err != nil || response == nil {
		state.finish(skipTransport)
		return response, err
	}
	state.raw.status = response.StatusCode
	state.raw.responseHeaders = response.Header.Clone()
	state.raw.emptyResponse = request.Method == http.MethodHead || response.StatusCode == http.StatusNoContent ||
		response.StatusCode == http.StatusNotModified || response.Body == nil || response.Body == http.NoBody
	if state.raw.emptyResponse {
		state.finish("")
		return response, nil
	}
	contentType, _ := contentBase(response.Header.Get("Content-Type"))
	tee := &teeBody{body: response.Body, limit: u.limit, buffer: !supportedMediaTypes[contentType]}
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
			exchange, reason := draftExchange(s.raw, s.sequence, s.rec.defaults, s.rec.ids, s.rec.limit)
			if reason == "" {
				record.Exchange = &exchange
			}
			skip = reason
		}
		// Drop raw bytes as soon as the decision is made.
		s.raw = rawExchange{connection: s.raw.connection}
		if s.request != nil {
			s.request.release()
		}
		if skip != "" {
			record.Skipped = string(skip)
			record.Connection = draftIdentifier(s.raw.connection)
			record.Sequence = s.sequence
			record.QueryKeys = nil
			s.rec.skipped.Add(1)
		}
		if err := s.rec.write(s.sequence, record); err != nil {
			s.rec.errMu.Lock()
			if s.rec.writeErr == nil {
				s.rec.writeErr = err
			}
			s.rec.errMu.Unlock()
			return
		}
		if skip == "" {
			s.rec.written.Add(1)
		}
	})
}

func (u *UpstreamRecorder) write(sequence int, record UpstreamRecord) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(record); err != nil {
		return err
	}
	data := bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
	name := fmt.Sprintf("%s-%06d%s", time.Now().UTC().Format("20060102T150405.000000000Z"), sequence, UpstreamRecordSuffix)
	path := filepath.Join(u.dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
	if err != nil {
		return fmt.Errorf("wirefixture: create upstream record: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("wirefixture: write upstream record: %w", err)
	}
	return file.Close()
}

// teeBody observes the caller's reads without pre-reading or changing bytes.
type teeBody struct {
	mu     sync.Mutex
	body   io.ReadCloser
	limit  int64
	buffer bool
	data   bytes.Buffer
	total  int64
	over   bool
	eof    bool
	done   func(complete bool)
	once   sync.Once
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
	eof := t.eof
	t.mu.Unlock()
	t.signal(eof)
	return err
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

// LoadUpstreamRecords reads every record file in dir (0600 regular files
// only) in name order and returns the recorded exchanges plus the number of
// skipped records.
func LoadUpstreamRecords(dir string) ([]Exchange, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	var exchanges []Exchange
	skipped := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), UpstreamRecordSuffix) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return nil, 0, fmt.Errorf("%w: record %s must be a 0600 regular file", ErrUnsafePublicValue, entry.Name())
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, 0, err
		}
		var record UpstreamRecord
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil || record.Version != upstreamRecordVersion {
			return nil, 0, fmt.Errorf("%w: record %s", ErrInvalidExchange, entry.Name())
		}
		if record.Exchange == nil {
			skipped++
			continue
		}
		exchange := *record.Exchange
		// Records are indented for reading; bodies are canonical when compact.
		for _, body := range []*json.RawMessage{&exchange.Request.Body.JSON, &exchange.Response.Body.JSON} {
			if len(*body) == 0 {
				continue
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, *body); err != nil {
				return nil, 0, fmt.Errorf("%w: record %s body", ErrInvalidExchange, entry.Name())
			}
			*body = compact.Bytes()
		}
		exchanges = append(exchanges, exchange)
	}
	return exchanges, skipped, nil
}

// DraftCandidate is the result of WriteDraftCandidate.
type DraftCandidate struct {
	Candidate string
	Policy    string
	Meta      string
	Exchanges int
}

// WriteDraftCandidate renumbers exchanges, derives the replay policy and
// writes <name>.candidate.json, <name>.policy.json and <name>.meta.json into
// out (an existing 0700 directory outside the worktree) with the same
// layout as committed samples. The result is a draft: review it before
// committing and before using it to align a fake.
func WriteDraftCandidate(worktree, out, name, offering string, exchanges []Exchange) (DraftCandidate, error) {
	if !identifierRE.MatchString(name) || strings.Contains(name, "..") {
		return DraftCandidate{}, fmt.Errorf("%w: draft name", ErrUnsafePublicValue)
	}
	if len(exchanges) == 0 {
		return DraftCandidate{}, fmt.Errorf("%w: no recorded exchanges", ErrInvalidExchange)
	}
	draft := sortedExchanges(exchanges)
	for i := range draft {
		draft[i].Sequence = i + 1
	}
	policy, err := DraftReplayPolicy(draft)
	if err != nil {
		return DraftCandidate{}, err
	}
	result := DraftCandidate{
		Candidate: filepath.Join(out, name+".candidate.json"),
		Policy:    filepath.Join(out, name+".policy.json"),
		Meta:      filepath.Join(out, name+".meta.json"),
		Exchanges: len(draft),
	}
	for _, path := range []string{result.Policy, result.Meta} {
		if _, err := os.Lstat(path); err == nil {
			return DraftCandidate{}, fmt.Errorf("%w: %s exists", ErrExistingCandidate, filepath.Base(path))
		}
	}
	if err := WriteCandidate(worktree, result.Candidate, draft, policy); err != nil {
		return DraftCandidate{}, err
	}
	written, err := os.ReadFile(result.Candidate)
	if err != nil {
		return DraftCandidate{}, err
	}
	sum := sha256.Sum256(written)
	policyJSON, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return DraftCandidate{}, err
	}
	meta := map[string]any{
		"version":          1,
		"scenario":         draft[0].Metadata.Scenario,
		"captured_at_utc":  draft[0].Metadata.CapturedAt.UTC().Format("2006-01-02"),
		"source_revision":  "unknown",
		"api_version":      draft[0].Metadata.APIVersion,
		"provenance":       string(draft[0].Metadata.Provenance),
		"transformation":   "Draft from the opt-in upstream recorder: prompts, long text, URLs and IDs are placeholders; query values, headers outside the replay set and media bytes are not stored.",
		"exchanges":        len(draft),
		"candidate_sha256": hex.EncodeToString(sum[:]),
	}
	if offering != "" {
		if !identifierRE.MatchString(offering) {
			return DraftCandidate{}, fmt.Errorf("%w: offering", ErrUnsafePublicValue)
		}
		meta["offering"] = offering
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return DraftCandidate{}, err
	}
	for path, data := range map[string][]byte{result.Policy: policyJSON, result.Meta: metaJSON} {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
		if err != nil {
			return DraftCandidate{}, err
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			_ = file.Close()
			return DraftCandidate{}, err
		}
		if err := file.Close(); err != nil {
			return DraftCandidate{}, err
		}
	}
	return result, nil
}
