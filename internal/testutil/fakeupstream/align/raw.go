package align

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
)

// rawExchange is one private, unsanitized HTTP exchange of an official run.
// It exists so a paid run is never lost to a sanitizer rejection: the exporter
// can be re-run offline from the saved exchanges (ReplayTransport) until the
// policy is right.
type rawExchange struct {
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	ReqType     string            `json:"request_content_type,omitempty"`
	ReqAccept   string            `json:"request_accept,omitempty"`
	ReqBody     []byte            `json:"request_body,omitempty"`
	Status      int               `json:"status"`
	RespHeaders map[string]string `json:"response_headers,omitempty"`
	RespBody    []byte            `json:"response_body,omitempty"`
}

// TeeTransport records every exchange passing through base.
type TeeTransport struct {
	base http.RoundTripper
	mu   sync.Mutex
	log  []rawExchange
}

// NewTee wraps base (http.DefaultTransport when nil).
func NewTee(base http.RoundTripper) *TeeTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &TeeTransport{base: base}
}

func (t *TeeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var requestBody []byte
	if request.Body != nil {
		var err error
		if requestBody, err = io.ReadAll(request.Body); err != nil {
			return nil, err
		}
		_ = request.Body.Close()
		request.Body = io.NopCloser(bytes.NewReader(requestBody))
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(requestBody)), nil }
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	responseBody, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(responseBody))
	exchange := rawExchange{
		Method: request.Method, URL: request.URL.String(),
		ReqType: request.Header.Get("Content-Type"), ReqAccept: request.Header.Get("Accept"), ReqBody: requestBody,
		Status: response.StatusCode, RespBody: responseBody, RespHeaders: map[string]string{},
	}
	for _, name := range []string{"Content-Type", "Retry-After"} {
		if value := response.Header.Get(name); value != "" {
			exchange.RespHeaders[name] = value
		}
	}
	t.mu.Lock()
	t.log = append(t.log, exchange)
	t.mu.Unlock()
	return response, nil
}

// Save writes the recorded exchanges (mode 0600). They contain credentials-
// adjacent data (task ids, signed URLs): keep the file outside the worktree.
func (t *TeeTransport) Save(path string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	raw, err := json.Marshal(t.log)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// ReplayTransport serves saved official exchanges in order, so the same script
// produces the same requests and the exporter can run again offline.
type ReplayTransport struct {
	mu    sync.Mutex
	queue map[string][]rawExchange
}

// LoadReplay reads a file written by TeeTransport.Save.
func LoadReplay(path string) (*ReplayTransport, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var log []rawExchange
	if err := json.Unmarshal(raw, &log); err != nil {
		return nil, err
	}
	if len(log) == 0 {
		return nil, errors.New("align: replay log is empty")
	}
	replay := &ReplayTransport{queue: map[string][]rawExchange{}}
	for _, exchange := range log {
		key := exchange.Method + " " + exchange.URL
		replay.queue[key] = append(replay.queue[key], exchange)
	}
	return replay, nil
}

// RoundTrip serves the next unconsumed recorded exchange of the same method and
// URL, so a subset of the script (one Offering's steps) replays against the
// full recording.
func (r *ReplayTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := request.Method + " " + request.URL.String()
	pending := r.queue[key]
	if len(pending) == 0 {
		return nil, fmt.Errorf("align: no recorded exchange for %s", key)
	}
	exchange := pending[0]
	r.queue[key] = pending[1:]
	if request.Body != nil {
		_, _ = io.Copy(io.Discard, request.Body)
		_ = request.Body.Close()
	}
	header := http.Header{}
	for name, value := range exchange.RespHeaders {
		header.Set(name, value)
	}
	return &http.Response{
		StatusCode: exchange.Status, Status: http.StatusText(exchange.Status), Header: header,
		Body: io.NopCloser(bytes.NewReader(exchange.RespBody)), ContentLength: int64(len(exchange.RespBody)),
		Request: request, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}, nil
}
