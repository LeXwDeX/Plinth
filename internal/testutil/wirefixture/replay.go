package wirefixture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

func validateMetadata(meta Metadata, p Policy) error {
	if meta.CapturedAt.IsZero() || meta.CapturedAt.Location() != time.UTC {
		return fmt.Errorf("%w: captured_at", ErrUnsafePublicValue)
	}
	switch meta.Provenance {
	case ProvenanceCaptured, ProvenanceOfficialExample, ProvenanceDerived, ProvenanceSynthetic:
	default:
		return fmt.Errorf("%w: provenance", ErrUnsafePublicValue)
	}
	for name, value := range map[string]string{
		"scenario": meta.Scenario, "connection": meta.Connection, "endpoint": meta.Endpoint,
		"api_version": meta.APIVersion, "requested_model": meta.RequestedModel,
		"reported_model": meta.ReportedModel, "server_build": meta.ServerBuild,
		"app_revision": meta.AppRevision, "catalog_revision": meta.CatalogRevision,
		"environment": meta.Environment,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: metadata %s", ErrUnsafePublicValue, name)
		}
		if err := checkText(value, false); err != nil {
			return err
		}
	}
	for _, value := range []string{meta.Scenario, meta.Connection, meta.Environment, meta.AppRevision, meta.CatalogRevision} {
		if !identifierRE.MatchString(value) {
			return fmt.Errorf("%w: metadata identifier", ErrUnsafePublicValue)
		}
	}
	for _, value := range []string{meta.APIVersion, meta.RequestedModel, meta.ReportedModel, meta.ServerBuild} {
		if !modelRE.MatchString(value) {
			return fmt.Errorf("%w: metadata model", ErrUnsafePublicValue)
		}
	}
	if meta.Protocol != "json" && meta.Protocol != "sse" && meta.Protocol != "none" &&
		meta.Protocol != "media" && meta.Protocol != "multipart" {
		return fmt.Errorf("%w: metadata protocol", ErrUnsafePublicValue)
	}
	method, path, ok := strings.Cut(meta.Endpoint, " ")
	if !ok || !methods[method] {
		return fmt.Errorf("%w: metadata endpoint", ErrUnsafePublicValue)
	}
	if clean, err := sanitizePath(path, p); err != nil || clean != path {
		return fmt.Errorf("%w: metadata endpoint path", ErrUnsafePublicValue)
	}
	if meta.Provenance == ProvenanceCaptured {
		for _, value := range []string{meta.AppRevision, meta.CatalogRevision, meta.Environment} {
			if value == "unknown" {
				return fmt.Errorf("%w: captured revision", ErrUnsafePublicValue)
			}
		}
	}
	return nil
}

func Validate(exchanges []Exchange, p Policy) error {
	if len(exchanges) == 0 {
		return fmt.Errorf("%w: empty fixture", ErrInvalidExchange)
	}
	seen := make(map[int]bool, len(exchanges))
	for i := range exchanges {
		sequence := exchanges[i].Sequence
		if sequence <= 0 || seen[sequence] {
			return fmt.Errorf("exchange %d: %w: sequence", i, ErrInvalidExchange)
		}
		seen[sequence] = true
		if err := validateExchange(exchanges[i], p); err != nil {
			return fmt.Errorf("exchange %d: %w", i, err)
		}
	}
	return nil
}

func validateExchange(e Exchange, p Policy) error {
	if !e.Complete || e.Invalid != "" {
		switch {
		case strings.Contains(e.Invalid, "unsupported"):
			return fmt.Errorf("%w: unsupported exchange", ErrUnsupportedBody)
		case strings.Contains(e.Invalid, "partial SSE"):
			return ErrPartialSSE
		case strings.Contains(e.Invalid, "unknown JSON key"):
			return fmt.Errorf("%w: unknown JSON key", ErrUnknownKey)
		default:
			return ErrInvalidExchange
		}
	}
	if err := validateMetadata(e.Metadata, p); err != nil {
		return err
	}
	if err := validateRequest(e.Request, e.Metadata, p); err != nil {
		return err
	}
	if err := validateResponse(e.Request.Method, e.Response, e.Metadata, p); err != nil {
		return err
	}
	if e.Metadata.Endpoint != e.Request.Method+" "+e.Request.Path {
		return fmt.Errorf("%w: metadata endpoint", ErrUnsafePublicValue)
	}
	if e.Metadata.Protocol != protocolFor(e.Response.ContentType) {
		return fmt.Errorf("%w: metadata protocol", ErrUnsafePublicValue)
	}
	return nil
}

func validateRequest(r Request, metadata Metadata, p Policy) error {
	if !r.Complete || !methods[r.Method] {
		return fmt.Errorf("%w: request", ErrUnsafePublicValue)
	}
	if clean, err := sanitizeHost(r.Host, p); err != nil || clean != r.Host {
		return fmt.Errorf("%w: request host", ErrUnsafePublicValue)
	}
	if clean, err := sanitizePath(r.Path, p); err != nil || clean != r.Path {
		return fmt.Errorf("%w: request path", ErrUnsafePublicValue)
	}
	base, err := contentBase(r.ContentType)
	if err != nil {
		return err
	}
	if base != r.ContentType {
		return fmt.Errorf("%w: request content type", ErrUnsafePublicValue)
	}
	headers, err := sanitizeHeaders(headersToHTTP(r.Headers), true, p)
	if err != nil || !reflect.DeepEqual(headers, r.Headers) {
		return fmt.Errorf("%w: request headers", ErrUnsafePublicValue)
	}
	return validatePayload(r.Body, r.ContentType, metadata, true, p)
}

func validateResponse(method string, r Response, metadata Metadata, p Policy) error {
	if !r.Complete || r.Status < 100 || r.Status > 599 {
		return fmt.Errorf("%w: response", ErrUnsafePublicValue)
	}
	if r.Status >= 300 && r.Status < 400 {
		return fmt.Errorf("%w: unsupported 3xx response", ErrUnsupportedBody)
	}
	emptyAllowed := method == http.MethodHead || r.Status < 200 || r.Status == http.StatusNoContent || r.Status == http.StatusNotModified
	emptyPayload := r.Body.Encoding == PayloadNone && r.Body.Transform == "" && len(r.Body.JSON) == 0 &&
		r.Body.SSE == "" && r.Body.Media == nil && r.Body.Multipart == nil
	if emptyAllowed && !emptyPayload {
		return fmt.Errorf("%w: response body is not allowed", ErrUnsupportedBody)
	}
	base, err := contentBase(r.ContentType)
	if err != nil {
		return err
	}
	if base != r.ContentType {
		return fmt.Errorf("%w: response content type", ErrUnsafePublicValue)
	}
	headers, err := sanitizeHeaders(headersToHTTP(r.Headers), false, p)
	if err != nil || !reflect.DeepEqual(headers, r.Headers) {
		return fmt.Errorf("%w: response headers", ErrUnsafePublicValue)
	}
	return validatePayload(r.Body, r.ContentType, metadata, false, p)
}

func validatePayload(payload Payload, contentType string, metadata Metadata, request bool, p Policy) error {
	kind, err := payloadKind(contentType)
	if err != nil {
		return err
	}
	if !request && kind == PayloadMultipart {
		return fmt.Errorf("%w: multipart response", ErrUnsupportedBody)
	}
	if request && kind == PayloadMedia {
		return fmt.Errorf("%w: binary request", ErrUnsupportedBody)
	}
	if payload.Encoding == PayloadNone {
		switch kind {
		case PayloadMultipart:
			return fmt.Errorf("%w: multipart payload requires parts", ErrUnsupportedBody)
		case PayloadMedia:
			return fmt.Errorf("%w: empty binary payload", ErrUnsupportedBody)
		}
		if payload.Transform != "" || kind == PayloadSSE || len(payload.JSON) != 0 || payload.SSE != "" ||
			payload.Media != nil || payload.Multipart != nil {
			return fmt.Errorf("%w: empty payload", ErrUnsafePublicValue)
		}
		return nil
	}
	if payload.Encoding != kind {
		return fmt.Errorf("%w: payload encoding", ErrUnsafePublicValue)
	}
	switch kind {
	case PayloadJSON:
		if payload.Transform != "" || payload.SSE != "" || payload.Media != nil || payload.Multipart != nil || !json.Valid(payload.JSON) {
			return fmt.Errorf("%w: JSON payload", ErrUnsafePublicValue)
		}
		clean, err := sanitizeJSON(payload.JSON, p)
		if err != nil {
			return err
		}
		if !bytes.Equal(clean, payload.JSON) {
			return fmt.Errorf("%w: JSON payload is not canonical", ErrUnsafePublicValue)
		}
		return nil
	case PayloadSSE:
		if payload.Transform != TransformNormalizedSSE || payload.Encoding != PayloadSSE || payload.SSE == "" ||
			len(payload.JSON) != 0 || payload.Media != nil || payload.Multipart != nil {
			return fmt.Errorf("%w: SSE payload", ErrUnsafePublicValue)
		}
		clean, err := sanitizeSSE([]byte(payload.SSE), p)
		if err != nil {
			return err
		}
		if clean != payload.SSE {
			return fmt.Errorf("%w: SSE payload is not canonical", ErrUnsafePublicValue)
		}
		return nil
	case PayloadMultipart:
		if !request || payload.Multipart == nil || len(payload.JSON) != 0 || payload.SSE != "" ||
			payload.Transform != "" || payload.Media != nil {
			return fmt.Errorf("%w: multipart payload", ErrUnsafePublicValue)
		}
		return validateMultipart(*payload.Multipart, metadata, p)
	case PayloadMedia:
		if request || payload.Media == nil || len(payload.JSON) != 0 || payload.SSE != "" ||
			payload.Transform != "" || payload.Multipart != nil {
			return fmt.Errorf("%w: media payload", ErrUnsafePublicValue)
		}
		if err := validateMedia(*payload.Media, metadata); err != nil {
			return err
		}
		if payload.Media.MediaType != contentType {
			return fmt.Errorf("%w: media payload content type", ErrUnsafePublicValue)
		}
		return nil
	default:
		return fmt.Errorf("%w: body without content type", ErrUnsupportedBody)
	}
}

func validateMedia(media Media, metadata Metadata) error {
	if !supportedMediaTypes[media.MediaType] || media.Length <= 0 {
		return fmt.Errorf("%w: media descriptor", ErrUnsafePublicValue)
	}
	if media.Recipe == "" {
		if media.Substituted || metadata.Provenance != ProvenanceCaptured {
			return fmt.Errorf("%w: private media provenance", ErrUnsafePublicValue)
		}
		return nil
	}
	spec, ok := syntheticRecipes[media.Recipe]
	if !ok || media.MediaType != spec.mediaType || media.Length != spec.length || !media.Substituted {
		return fmt.Errorf("%w: media recipe descriptor", ErrUnsafePublicValue)
	}
	if metadata.Provenance != ProvenanceDerived && metadata.Provenance != ProvenanceSynthetic {
		return fmt.Errorf("%w: derived media provenance", ErrUnsafePublicValue)
	}
	return nil
}

func validateMultipart(multipart Multipart, metadata Metadata, p Policy) error {
	if len(multipart.Parts) == 0 {
		return fmt.Errorf("%w: empty multipart", ErrUnsafePublicValue)
	}
	for _, part := range multipart.Parts {
		if !identifierRE.MatchString(part.Name) || !p.Keys[part.Name] {
			return fmt.Errorf("%w: multipart part name", ErrUnknownKey)
		}
		if part.Media != nil {
			if part.Value != "" {
				return fmt.Errorf("%w: multipart part is both field and file", ErrUnsafePublicValue)
			}
			if err := validateMedia(*part.Media, metadata); err != nil {
				return err
			}
			continue
		}
		if part.Value == "" {
			continue
		}
		if err := checkText(part.Value, false); err != nil {
			return err
		}
		clean, err := p.resolve(part.Value, 0)
		if err != nil || clean != part.Value {
			return fmt.Errorf("%w: multipart field", ErrUnsafePublicValue)
		}
	}
	return nil
}

func headersToHTTP(headers map[string]string) http.Header {
	out := http.Header{}
	for name, value := range headers {
		out.Set(name, value)
	}
	return out
}

func NewReplay(exchanges []Exchange, p Policy) (*Replay, error) {
	return NewReplayWithMedia(exchanges, p, nil)
}

// NewReplayWithMedia wires a resolver for binary and multipart fixtures.
// NewReplay rejects media exchanges at round-trip time because no resolver can
// supply their bytes.
func NewReplayWithMedia(exchanges []Exchange, p Policy, media MediaResolver) (*Replay, error) {
	sorted := sortedExchanges(exchanges)
	policy := clonePolicy(p)
	if err := Validate(sorted, policy); err != nil {
		return nil, err
	}
	return &Replay{policy: policy, exchanges: sorted, media: media}, nil
}

func (r *Replay) RoundTrip(request *http.Request) (*http.Response, error) {
	return r.roundTrip(request, "")
}

// WithConnection rejects exchanges recorded for another connection. It never
// falls back to a live network.
func (r *Replay) WithConnection(connection string) http.RoundTripper {
	return connectionReplay{replay: r, connection: connection}
}

type connectionReplay struct {
	replay     *Replay
	connection string
}

func (c connectionReplay) RoundTrip(request *http.Request) (*http.Response, error) {
	return c.replay.roundTrip(request, c.connection)
}

func (r *Replay) roundTrip(request *http.Request, connection string) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, fmt.Errorf("%w: nil request", ErrUnexpectedRequest)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed {
		return nil, ErrReplayFailed
	}
	if r.next >= len(r.exchanges) {
		return nil, r.fail(ErrExhaustedRequests)
	}
	raw, err := readBody(request, r.policy.limit())
	if err != nil {
		return nil, r.fail(err)
	}
	got, files, err := sanitizeReplayRequest(request, raw, r.policy)
	if err != nil {
		return nil, r.fail(fmt.Errorf("%w: %v", ErrUnexpectedRequest, err))
	}
	want := r.exchanges[r.next]
	if connection != "" && want.Metadata.Connection != connection {
		return nil, r.fail(ErrConnectionMismatch)
	}
	matched, err := r.exchangeMatches(want, got, files)
	if err != nil {
		return nil, r.fail(err)
	}
	if !matched {
		for i := r.next + 1; i < len(r.exchanges); i++ {
			if requestShapeEqual(r.exchanges[i].Request, got) {
				return nil, r.fail(fmt.Errorf("%w: expected exchange %d", ErrReorderedRequest, r.next))
			}
		}
		return nil, r.fail(fmt.Errorf("%w: expected exchange %d", ErrUnexpectedRequest, r.next))
	}
	response, err := replayResponse(want, request, r.media)
	if err != nil {
		return nil, r.fail(err)
	}
	r.next++
	return response, nil
}

func (r *Replay) fail(err error) error {
	r.failed = true
	return err
}

func (r *Replay) exchangeMatches(want Exchange, got Request, files map[int][]byte) (bool, error) {
	if !requestShapeEqual(want.Request, got) {
		return false, nil
	}
	if want.Request.Body.Encoding != PayloadMultipart {
		return true, nil
	}
	hasFiles := false
	for _, part := range want.Request.Body.Multipart.Parts {
		if part.Media != nil {
			hasFiles = true
			break
		}
	}
	if !hasFiles {
		return true, nil
	}
	if r.media == nil {
		return false, fmt.Errorf("%w: multipart media requires a resolver", ErrMediaResolver)
	}
	for i, part := range want.Request.Body.Multipart.Parts {
		if part.Media == nil {
			continue
		}
		expected, err := r.media.resolve(MediaLocation{Sequence: want.Sequence, Part: i}, *part.Media)
		if err != nil {
			return false, err
		}
		if int64(len(expected)) != part.Media.Length || !bytes.Equal(expected, files[i]) {
			return false, nil
		}
	}
	return true, nil
}

func (r *Replay) AssertConsumed() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed {
		return ErrReplayFailed
	}
	if r.next != len(r.exchanges) {
		return fmt.Errorf("%w: %d of %d", ErrUnconsumedRequest, r.next, len(r.exchanges))
	}
	return nil
}

func readBody(request *http.Request, limit int64) ([]byte, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return nil, nil
	}
	defer request.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: request body", ErrUnexpectedRequest)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: request body too large", ErrUnexpectedRequest)
	}
	return raw, nil
}

func sanitizeReplayRequest(request *http.Request, raw []byte, p Policy) (Request, map[int][]byte, error) {
	if request.Method != strings.ToUpper(request.Method) || !methods[request.Method] {
		return Request{}, nil, fmt.Errorf("%w: method", ErrUnexpectedRequest)
	}
	if request.URL.Scheme != "https" {
		return Request{}, nil, fmt.Errorf("%w: scheme", ErrUnexpectedRequest)
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.User != nil {
		return Request{}, nil, fmt.Errorf("%w: query or userinfo", ErrUnexpectedRequest)
	}
	if request.Host != "" && !strings.EqualFold(request.Host, request.URL.Host) {
		return Request{}, nil, fmt.Errorf("%w: request host override", ErrUnexpectedRequest)
	}
	path, err := sanitizePath(request.URL.EscapedPath(), p)
	if err != nil {
		return Request{}, nil, err
	}
	host, err := sanitizeHost(request.URL.Host, p)
	if err != nil {
		return Request{}, nil, err
	}
	headers, err := sanitizeHeaders(request.Header, true, p)
	if err != nil {
		return Request{}, nil, err
	}
	contentType, err := contentBase(request.Header.Get("Content-Type"))
	if err != nil {
		return Request{}, nil, err
	}
	if contentType == "multipart/form-data" {
		parsed, files, err := parseMultipart(raw, request.Header.Get("Content-Type"), p)
		if err != nil {
			return Request{}, nil, err
		}
		return Request{Method: request.Method, Host: host, Path: path, ContentType: contentType, Headers: headers,
			Body: Payload{Encoding: PayloadMultipart, Multipart: parsed}, Complete: true}, files, nil
	}
	body, err := sanitizeBody(raw, contentType, p)
	if err != nil {
		return Request{}, nil, err
	}
	return Request{Method: request.Method, Host: host, Path: path, ContentType: contentType, Headers: headers,
		Body: body, Complete: true}, nil, nil
}

func requestShapeEqual(want, got Request) bool {
	return want.Method == got.Method && want.Host == got.Host && want.Path == got.Path && want.ContentType == got.ContentType &&
		reflect.DeepEqual(want.Headers, got.Headers) && want.Body.Encoding == got.Body.Encoding &&
		want.Body.Transform == got.Body.Transform &&
		bytes.Equal(want.Body.JSON, got.Body.JSON) && want.Body.SSE == got.Body.SSE &&
		mediaShapeEqual(want.Body.Media, got.Body.Media) &&
		multipartShapeEqual(want.Body.Multipart, got.Body.Multipart)
}

func mediaShapeEqual(want, got *Media) bool {
	if want == nil || got == nil {
		return want == nil && got == nil
	}
	return want.MediaType == got.MediaType && want.Length == got.Length
}

func multipartShapeEqual(want, got *Multipart) bool {
	if want == nil || got == nil {
		return want == nil && got == nil
	}
	if len(want.Parts) != len(got.Parts) {
		return false
	}
	for i := range want.Parts {
		if want.Parts[i].Name != got.Parts[i].Name || want.Parts[i].Value != got.Parts[i].Value ||
			!mediaShapeEqual(want.Parts[i].Media, got.Parts[i].Media) {
			return false
		}
	}
	return true
}

func replayResponse(e Exchange, request *http.Request, resolver MediaResolver) (*http.Response, error) {
	headers := headersToHTTP(e.Response.Headers)
	if e.Response.ContentType != "" {
		headers.Set("Content-Type", e.Response.ContentType)
	}
	var body []byte
	switch e.Response.Body.Encoding {
	case PayloadJSON:
		body = append([]byte(nil), e.Response.Body.JSON...)
	case PayloadSSE:
		body = []byte(e.Response.Body.SSE)
	case PayloadMedia:
		if resolver == nil {
			return nil, fmt.Errorf("%w: media requires a resolver", ErrMediaResolver)
		}
		data, err := resolver.resolve(MediaLocation{Sequence: e.Sequence, Part: -1}, *e.Response.Body.Media)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) != e.Response.Body.Media.Length {
			return nil, fmt.Errorf("%w: replayed media length", ErrMediaMismatch)
		}
		body = data
	case PayloadNone:
	default:
		return nil, fmt.Errorf("%w: replayed body type", ErrUnsupportedBody)
	}
	return &http.Response{
		StatusCode: e.Response.Status, Status: fmt.Sprintf("%d %s", e.Response.Status, http.StatusText(e.Response.Status)),
		Header: headers, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)),
		Request: request, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}, nil
}

func WriteCandidate(worktreeRoot, path string, exchanges []Exchange, p Policy) error {
	sorted := sortedExchanges(exchanges)
	if err := Validate(sorted, p); err != nil {
		return err
	}
	for _, exchange := range sorted {
		if privateMedia(exchange) {
			return fmt.Errorf("%w: private media cannot be exported", ErrMediaEvidence)
		}
	}
	if worktreeRoot == "" || path == "" {
		return fmt.Errorf("%w: root and path", ErrInsideWorktree)
	}
	rootInfo, err := os.Stat(worktreeRoot)
	if err != nil {
		return fmt.Errorf("%w: worktree root unavailable", ErrUnsafePublicValue)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("%w: worktree root is not a directory", ErrUnsafePublicValue)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%w: candidate path", ErrUnsafePublicValue)
	}
	dir := filepath.Dir(absolute)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("%w: candidate directory unavailable", ErrUnsafePublicValue)
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || dirInfo.Mode().Perm() != 0o700 {
		return fmt.Errorf("%w: candidate directory must exist with mode 0700", ErrUnsafePublicValue)
	}
	rootReal, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return fmt.Errorf("%w: worktree root unavailable", ErrUnsafePublicValue)
	}
	dirReal, err := filepath.EvalSymlinks(dir)
	if err != nil || inside(rootReal, dirReal) {
		return fmt.Errorf("%w: candidate directory", ErrInsideWorktree)
	}
	if same, err := sameFileAncestor(rootReal, dirReal); err != nil || same {
		return fmt.Errorf("%w: candidate directory", ErrInsideWorktree)
	}
	if _, err := os.Lstat(absolute); err == nil {
		return fmt.Errorf("%w: candidate exists", ErrExistingCandidate)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: candidate path unavailable", ErrUnsafePublicValue)
	}
	data, err := marshalJSON(sorted)
	if err != nil {
		return fmt.Errorf("%w: candidate encode", ErrUnsafePublicValue)
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: candidate exists", ErrExistingCandidate)
		}
		return fmt.Errorf("%w: candidate create failed", ErrUnsafePublicValue)
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(absolute)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("%w: candidate write failed", ErrUnsafePublicValue)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%w: candidate close failed", ErrUnsafePublicValue)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w: candidate mode", ErrUnsafePublicValue)
	}
	ok = true
	return nil
}

func privateMedia(exchange Exchange) bool {
	if media := exchange.Response.Body.Media; media != nil && media.Recipe == "" {
		return true
	}
	if multipart := exchange.Request.Body.Multipart; multipart != nil {
		for _, part := range multipart.Parts {
			if part.Media != nil && part.Media.Recipe == "" {
				return true
			}
		}
	}
	return false
}

func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func sameFileAncestor(root, dir string) (bool, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false, err
	}
	for current := dir; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil {
			return false, err
		}
		if os.SameFile(rootInfo, info) {
			return true, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
	}
}
