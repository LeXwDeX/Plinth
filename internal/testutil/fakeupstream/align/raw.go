package align

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"sort"
	"strings"
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

// ReplayTransport serves saved official exchanges, so the same script produces
// the same requests and the exporter can run again offline. An exchange is
// identified by method, URL and a digest of the request body: two steps that hit
// one endpoint with different bodies (a rejected empty create and the real one)
// never take each other's recording. Repeats of the same request (polls) are
// served in recorded order. Multipart bodies carry a random boundary, so they
// are identified by method and URL alone.
type ReplayTransport struct {
	mu    sync.Mutex
	queue map[string][]rawExchange
	log   []rawExchange
}

// MultipartFile returns the bytes of the first file part named field with the
// given file name among the saved requests. A step whose file came from an
// earlier step's artifact (File.FromVar) sent exactly these bytes, so an offline
// export needs no second download of an URL that may have expired.
func (r *ReplayTransport) MultipartFile(field, filename string) ([]byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, exchange := range r.log {
		_, params, err := mime.ParseMediaType(exchange.ReqType)
		if err != nil || params["boundary"] == "" || !strings.HasPrefix(strings.ToLower(exchange.ReqType), "multipart/") {
			continue
		}
		reader := multipart.NewReader(bytes.NewReader(exchange.ReqBody), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			if part.FormName() == field && part.FileName() == filename {
				return data, true
			}
		}
	}
	return nil, false
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
	replay := &ReplayTransport{queue: map[string][]rawExchange{}, log: log}
	for _, exchange := range log {
		key := replayKey(exchange.Method, exchange.URL, exchange.ReqType, exchange.ReqBody)
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
	var sent []byte
	if request.Body != nil {
		sent, _ = io.ReadAll(request.Body)
		_ = request.Body.Close()
	}
	key := replayKey(request.Method, request.URL.String(), request.Header.Get("Content-Type"), sent)
	pending := r.queue[key]
	if len(pending) == 0 {
		return nil, fmt.Errorf("align: no recorded exchange for %s %s", request.Method, request.URL)
	}
	exchange := pending[0]
	r.queue[key] = pending[1:]
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

func replayKey(method, url, contentType string, body []byte) string {
	if strings.HasPrefix(strings.ToLower(contentType), "multipart/") {
		body = multipartDigestInput(contentType, body)
	}
	sum := sha256.Sum256(body)
	return method + " " + url + " " + hex.EncodeToString(sum[:8])
}

// multipartDigestInput is the boundary-free content of a multipart body: its
// text fields and the name, file name and length of its files, so two steps
// posting different forms to one endpoint never take each other's recording.
func multipartDigestInput(contentType string, body []byte) []byte {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return nil
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var parts []string
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			parts = append(parts, fmt.Sprintf("%s=file:%s:%d", part.FormName(), part.FileName(), len(data)))
		} else {
			parts = append(parts, part.FormName()) // the value may be an id the export replaces
		}
	}
	sort.Strings(parts)
	return []byte(strings.Join(parts, "\n"))
}
