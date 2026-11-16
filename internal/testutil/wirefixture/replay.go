package wirefixture

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
)

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
	raw, err := readBody(request, r.policy.Limit())
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
	if request.Method != strings.ToUpper(request.Method) || !isMethod(request.Method) {
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
