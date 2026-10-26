package wirefixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

func (r *Recorder) roundTrip(connection string, base http.RoundTripper, request *http.Request) (*http.Response, error) {
	if r.deferred != nil {
		return r.deferred.roundTrip(r, connection, base, request)
	}
	if request == nil || request.URL == nil {
		return nil, fmt.Errorf("%w: nil request", ErrUnexpectedRequest)
	}
	if base == nil {
		err := errors.New("wirefixture: nil base transport")
		r.addFailure()
		return nil, err
	}
	state := &capture{rec: r, sequence: r.sequence(), metadata: r.metadata(connection, request), method: request.Method,
		path: request.URL.EscapedPath(), host: request.URL.Host, scheme: request.URL.Scheme,
		rawQuery: request.URL.RawQuery, requestHeaders: request.Header.Clone()}
	if _, valid := publicQueryKeys(request.URL, r.Policy); !valid {
		state.hasQuery = true
	}
	clone := request.Clone(request.Context())
	if request.Body != nil && request.Body != http.NoBody {
		state.request = newCaptureBody(request.Body, r.Policy.limit(), state.requestDone, func(raw []byte, eof bool) bool {
			return completeBody(raw, request.ContentLength, request.Header.Get("Content-Type"), request.Method, 0, eof)
		}, request.Context(), request.ContentLength, jsonContent(request.Header.Get("Content-Type")))
		clone.Body = state.request
	} else {
		state.requestDone(nil, true)
	}
	response, err := base.RoundTrip(clone)
	if err != nil {
		r.addFailure()
		state.discard()
		return response, err
	}
	if response == nil {
		err := errors.New("wirefixture: nil base response")
		r.addFailure()
		state.discard()
		return nil, err
	}
	state.status = response.StatusCode
	state.responseHeaders = response.Header.Clone()
	state.emptyResponse = request.Method == http.MethodHead || response.StatusCode == http.StatusNoContent ||
		response.StatusCode == http.StatusNotModified || response.Body == http.NoBody
	if response.Body == nil {
		state.responseDone(nil, true)
	} else {
		response.Body = newCaptureBody(response.Body, r.Policy.limit(), state.responseDone, func(raw []byte, eof bool) bool {
			return completeBody(raw, response.ContentLength, response.Header.Get("Content-Type"), request.Method, response.StatusCode, eof)
		}, request.Context(), response.ContentLength, jsonContent(response.Header.Get("Content-Type")))
	}
	return response, nil
}

func (r *Recorder) metadata(connection string, request *http.Request) Metadata {
	meta := r.Default
	if connection != "" {
		// The wrapped transport identifies the connection; a global default
		// must not relabel exchanges recorded through another one.
		meta.Connection = connection
	}
	if meta.Connection == "" {
		meta.Connection = "unknown"
	}
	if meta.CapturedAt.IsZero() {
		meta.CapturedAt = time.Now().UTC()
	} else {
		meta.CapturedAt = meta.CapturedAt.UTC()
	}
	for _, value := range []*string{&meta.APIVersion, &meta.RequestedModel, &meta.ReportedModel, &meta.ServerBuild,
		&meta.AppRevision, &meta.CatalogRevision, &meta.Environment} {
		if *value == "" {
			*value = "unknown"
		}
	}
	return meta
}

type capture struct {
	rec             *Recorder
	sequence        int
	metadata        Metadata
	method          string
	path            string
	host            string
	scheme          string
	rawQuery        string
	hasQuery        bool
	requestHeaders  http.Header
	status          int
	responseHeaders http.Header
	emptyResponse   bool
	request         *captureBody
	media           *Media
	mu              sync.Mutex
	reqRaw          []byte
	reqDone         bool
	reqIncomplete   bool
	respRaw         []byte
	respDone        bool
	respIncomplete  bool
	finished        bool
}

func (c *capture) requestDone(raw []byte, complete bool) {
	c.mu.Lock()
	if !c.reqDone {
		c.reqRaw, c.reqDone, c.reqIncomplete = raw, true, !complete
	}
	c.mu.Unlock()
	c.finish()
}

func (c *capture) responseDone(raw []byte, complete bool) {
	c.mu.Lock()
	if !c.respDone {
		if !complete && c.emptyResponse && len(raw) == 0 {
			complete = true
		}
		c.respRaw, c.respDone, c.respIncomplete = raw, true, !complete
	}
	c.mu.Unlock()
	c.finish()
}

func (c *capture) discard() {
	c.mu.Lock()
	c.finished = true
	c.mu.Unlock()
}

func (c *capture) finish() {
	defer func() { _ = recover() }()
	c.mu.Lock()
	if c.finished || !c.respDone {
		c.mu.Unlock()
		return
	}
	c.finished = true
	reqDone, reqIncomplete, reqRaw := c.reqDone, c.reqIncomplete, c.reqRaw
	respIncomplete, respRaw := c.respIncomplete, c.respRaw
	c.reqRaw, c.respRaw = nil, nil
	c.mu.Unlock()

	p := c.rec.Policy
	var invalid []string
	method := strings.ToUpper(c.method)
	request := Request{Complete: reqDone && !reqIncomplete}
	if !methods[method] {
		invalid = append(invalid, "request method is not registered")
		request.Complete = false
	} else {
		request.Method = method
	}
	if !reqDone || reqIncomplete {
		if baseMedia(c.requestHeaders.Get("Content-Type")) == "text/event-stream" {
			invalid = append(invalid, "partial SSE stream")
		} else {
			invalid = append(invalid, "request body incomplete or truncated")
		}
		request.Complete = false
	}
	if c.scheme != "https" {
		invalid = append(invalid, "request URL scheme is not https")
		request.Complete = false
	}
	if c.hasQuery || len(c.rawQuery) > 64<<10 ||
		(c.rawQuery != "" && !queryKeysAllowed(c.rawQuery, p.QueryKeys)) {
		invalid = append(invalid, "request URL has query or userinfo")
		request.Complete = false
	}
	hostValue, pathValue := c.host, c.path
	if publicHost, publicPath, ok := publicRequestURL(p, c.scheme, c.host, c.path, c.rawQuery); ok {
		hostValue, pathValue = publicHost, publicPath
	}
	if host, err := sanitizeHost(hostValue, p); err != nil {
		invalid = append(invalid, "request host is not registered")
		request.Complete = false
	} else {
		request.Host = host
	}
	if path, err := sanitizePath(pathValue, p); err != nil {
		invalid = append(invalid, "request path: "+err.Error())
		request.Complete = false
	} else {
		request.Path = path
	}
	if headers, err := sanitizeHeaders(c.requestHeaders, true, p); err != nil {
		invalid = append(invalid, "request headers: "+err.Error())
		request.Complete = false
	} else {
		request.Headers = headers
	}
	if contentType, err := contentBase(c.requestHeaders.Get("Content-Type")); err != nil {
		invalid = append(invalid, "request content type: "+err.Error())
		request.Complete = false
	} else {
		request.ContentType = contentType
	}
	if request.Complete {
		if request.ContentType == "multipart/form-data" {
			parsed, files, err := parseMultipart(reqRaw, c.requestHeaders.Get("Content-Type"), p)
			if err != nil {
				invalid = append(invalid, "request body: "+err.Error())
				request.Complete = false
			} else if err := c.storeMultipartEvidence(parsed, files); err != nil {
				invalid = append(invalid, "request multipart evidence: "+err.Error())
				request.Complete = false
			} else {
				request.Body = Payload{Encoding: PayloadMultipart, Multipart: parsed}
			}
		} else if body, err := sanitizeBody(reqRaw, request.ContentType, p); err != nil {
			invalid = append(invalid, "request body: "+err.Error())
			request.Complete = false
		} else {
			request.Body = body
		}
	}

	response := Response{Status: c.status, Complete: !respIncomplete}
	unsupportedStatus := c.status >= 300 && c.status < 400
	if respIncomplete && !unsupportedStatus {
		if baseMedia(c.responseHeaders.Get("Content-Type")) == "text/event-stream" {
			invalid = append(invalid, "partial SSE stream")
		} else {
			invalid = append(invalid, "response body incomplete or truncated")
		}
	}
	if unsupportedStatus {
		invalid = append(invalid, "unsupported 3xx response")
		response.Complete = false
	}
	if contentType, err := contentBase(c.responseHeaders.Get("Content-Type")); err != nil {
		invalid = append(invalid, "response content type: "+err.Error())
		response.Complete = false
	} else {
		response.ContentType = contentType
	}
	if headers, err := sanitizeHeaders(c.responseHeaders, false, p); err != nil {
		invalid = append(invalid, "response headers: "+err.Error())
		response.Complete = false
	} else {
		response.Headers = headers
	}
	if response.Complete && !c.emptyResponse && supportedMediaTypes[response.ContentType] {
		if c.media != nil {
			if c.media.MediaType != response.ContentType {
				invalid = append(invalid, "response media type mismatch")
				response.Complete = false
			} else {
				response.Body = Payload{Encoding: PayloadMedia, Media: cloneMedia(c.media)}
			}
		} else if err := c.storeResponseMedia(response.ContentType, respRaw, &response); err != nil {
			invalid = append(invalid, "response media: "+err.Error())
			response.Complete = false
		}
	} else if response.Complete {
		if body, err := sanitizeBody(respRaw, response.ContentType, p); err != nil {
			invalid = append(invalid, "response body: "+err.Error())
			response.Complete = false
		} else {
			response.Body = body
		}
	}

	metadata := c.metadata
	if metadata.Endpoint == "" {
		metadata.Endpoint = request.Method + " " + request.Path
	}
	if metadata.Protocol == "" {
		metadata.Protocol = protocolFor(response.ContentType)
	}
	if err := validateMetadata(metadata, p); err != nil {
		invalid = append(invalid, "metadata is invalid")
		metadata = Metadata{}
	}
	exchange := Exchange{Sequence: c.sequence, Metadata: metadata, Request: request, Response: response,
		Complete: request.Complete && response.Complete && len(invalid) == 0}
	if len(invalid) > 0 {
		exchange.Invalid = strings.Join(invalid, "; ")
	}
	c.rec.addExchange(exchange)
}

func (c *capture) storeMultipartEvidence(parsed *Multipart, files map[int][]byte) error {
	if len(files) == 0 {
		return nil
	}
	if c.rec.Evidence == nil {
		return fmt.Errorf("%w: multipart evidence store is required", ErrMediaEvidence)
	}
	type pending struct {
		index  int
		source *PrivateSource
	}
	var stored []pending
	for index := 0; index < len(parsed.Parts); index++ {
		if parsed.Parts[index].Media == nil {
			continue
		}
		data, ok := files[index]
		if !ok {
			for _, item := range stored {
				item.source.discard()
			}
			return fmt.Errorf("%w: multipart part bytes missing", ErrMediaEvidence)
		}
		source, err := c.rec.Evidence.Spool(parsed.Parts[index].Media.MediaType, data)
		if err != nil {
			for _, item := range stored {
				item.source.discard()
			}
			return fmt.Errorf("%w: multipart evidence store failed", ErrMediaEvidence)
		}
		stored = append(stored, pending{index: index, source: source})
	}
	for _, item := range stored {
		c.rec.addEvidence(MediaEvidence{Location: MediaLocation{Sequence: c.sequence, Part: item.index}, Source: item.source})
	}
	return nil
}

func (c *capture) storeResponseMedia(mediaType string, raw []byte, response *Response) error {
	if len(raw) == 0 {
		return fmt.Errorf("%w: empty binary response", ErrUnsupportedBody)
	}
	if c.rec.Evidence == nil {
		return fmt.Errorf("%w: binary evidence store is required", ErrMediaEvidence)
	}
	source, err := c.rec.Evidence.Spool(mediaType, raw)
	if err != nil {
		return fmt.Errorf("%w: binary evidence store failed", ErrMediaEvidence)
	}
	c.rec.addEvidence(MediaEvidence{Location: MediaLocation{Sequence: c.sequence, Part: -1}, Source: source})
	response.Body = Payload{Encoding: PayloadMedia, Media: &Media{MediaType: mediaType, Length: int64(len(raw))}}
	return nil
}

func contentBase(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	base, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("%w: malformed content type", ErrUnsupportedBody)
	}
	if err := checkText(base, false); err != nil {
		return "", err
	}
	if _, err := payloadKind(base); err != nil {
		return "", err
	}
	return base, nil
}

func completeBody(raw []byte, contentLength int64, contentType, method string, status int, eof bool) bool {
	if status >= 300 && status < 400 {
		return false
	}
	if method == http.MethodHead || status == http.StatusNoContent || status == http.StatusNotModified {
		return len(raw) == 0
	}
	if baseMedia(contentType) == "text/event-stream" {
		return sseFramed(raw)
	}
	if contentLength >= 0 {
		return int64(len(raw)) == contentLength
	}
	return eof
}

func jsonContent(contentType string) bool {
	kind, _ := payloadKind(contentType)
	return kind == PayloadJSON
}

func protocolFor(contentType string) string {
	switch kind, _ := payloadKind(contentType); kind {
	case PayloadJSON:
		return "json"
	case PayloadSSE:
		return "sse"
	case PayloadMultipart:
		return "multipart"
	case PayloadMedia:
		return "media"
	default:
		return "none"
	}
}

type captureBody struct {
	body          io.ReadCloser
	max           int64
	onDone        func([]byte, bool)
	complete      func([]byte, bool) bool
	ctx           context.Context
	contentLength int64
	drainJSON     bool
	buf           bytes.Buffer
	truncated     bool
	failed        bool
	eof           bool
	done          bool
	mu            sync.Mutex
}

func newCaptureBody(body io.ReadCloser, max int64, onDone func([]byte, bool), complete func([]byte, bool) bool,
	ctx context.Context, contentLength int64, drainJSON bool) *captureBody {
	return &captureBody{body: body, max: max, onDone: onDone, complete: complete, ctx: ctx, contentLength: contentLength, drainJSON: drainJSON}
}

func (b *captureBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.mu.Lock()
	if n > 0 {
		remaining := b.max - int64(b.buf.Len())
		if remaining <= 0 {
			b.truncated = true
		} else if int64(n) <= remaining {
			_, _ = b.buf.Write(p[:n])
		} else {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		}
	}
	if err == io.EOF {
		b.eof = true
	} else if err != nil {
		b.failed = true
	}
	b.mu.Unlock()
	if err != nil {
		b.finish(err == io.EOF)
	}
	return n, err
}

func (b *captureBody) Close() error {
	b.drainJSONBody()
	err := b.body.Close()
	if err != nil {
		b.markFailed()
	}
	b.finish(b.eof)
	return err
}

func (b *captureBody) drainJSONBody() {
	if !b.drainJSON {
		return
	}
	b.mu.Lock()
	if b.done || b.eof || (b.contentLength >= 0 && int64(b.buf.Len()) >= b.contentLength) {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	for {
		if b.ctx != nil && b.ctx.Err() != nil {
			b.markFailed()
			return
		}
		b.mu.Lock()
		remaining := b.max - int64(b.buf.Len()) + 1
		b.mu.Unlock()
		if remaining <= 0 {
			b.markTruncated()
			return
		}
		size := int64(32 << 10)
		if size > remaining {
			size = remaining
		}
		p := make([]byte, int(size))
		n, err := b.body.Read(p)
		b.mu.Lock()
		if n > 0 {
			space := b.max - int64(b.buf.Len())
			if int64(n) > space {
				_, _ = b.buf.Write(p[:space])
				b.truncated = true
			} else {
				_, _ = b.buf.Write(p[:n])
			}
		}
		if err == io.EOF {
			b.eof = true
		} else if err != nil {
			b.failed = true
		}
		truncated := b.truncated
		b.mu.Unlock()
		if err != nil || truncated {
			return
		}
	}
}

func (b *captureBody) markFailed() {
	b.mu.Lock()
	b.failed = true
	b.mu.Unlock()
}

func (b *captureBody) markTruncated() {
	b.mu.Lock()
	b.truncated = true
	b.mu.Unlock()
}

func (b *captureBody) finish(eof bool) {
	b.mu.Lock()
	if b.done {
		b.mu.Unlock()
		return
	}
	b.done = true
	raw, truncated, failed, check := append([]byte(nil), b.buf.Bytes()...), b.truncated, b.failed, b.complete
	b.mu.Unlock()
	complete := !truncated && !failed && check != nil && check(raw, eof)
	b.onDone(raw, complete)
}
