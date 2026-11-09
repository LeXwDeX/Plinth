package wirefixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// Draft sanitization (#220) turns one raw upstream exchange into a public
// Exchange without a hand-written policy. It derives a private, in-memory
// policy from the raw bytes and then runs the ordinary fail-closed sanitizer
// with it, so every stored value still passes the same credential, address,
// identifier and embedded-binary checks. The derived policy is never written:
// its Aliases are keyed by raw values.
//
// Derivation rules: every JSON key and multipart part name is registered;
// numbers are registered; prompt-like fields, long text (over 128 bytes),
// nested JSON text and any value that fails checkText become "[redacted]";
// whole URLs become a fixed example.test URL (query values are never kept);
// opaque identifiers become stable id-N tokens shared with path segments;
// the request host becomes example.test; media bytes become fixed synthetic
// recipes.

const draftLongText = 128

const draftRedactedURL = "https://example.test/redacted"

// draftPromptKeys are compact JSON key names whose string values are user or
// model text. They are redacted whatever their length.
var draftPromptKeys = map[string]bool{
	"prompt": true, "negativeprompt": true, "text": true, "content": true, "input": true,
	"instructions": true, "system": true, "systemprompt": true, "query": true, "description": true,
	"caption": true, "revisedprompt": true, "reasoningcontent": true, "texturprompt": true,
	"textureprompt": true, "message": true, "title": true, "name": true,
}

var draftDigitRE = regexp.MustCompile(`[0-9]`)

// draftMediaRecipes maps response/upload media types to fixed synthetic
// recipes; the real bytes are never stored.
var draftMediaRecipes = map[string]string{
	"image/png": "png-1x1", "image/jpeg": "jpeg-1x1", "image/webp": "webp-1x1",
	"model/gltf-binary": "glb-triangle", "model/stl": "stl-triangle", "model/fbx": "fbx-triangle",
	"model/obj": "obj-triangle", "model/gltf+json": "gltf-json", "model/vnd.usdz+zip": "usdz-empty",
	"model/3mf": "3mf-empty", "application/zip": "zip-empty", "application/octet-stream": "octet-empty",
	"text/plain": "text-plain",
}

// draftIDs keeps raw→token mappings in memory so an ID returned by a create
// call and used later in a poll path maps to the same token.
type draftIDs struct {
	mu   sync.Mutex
	ids  map[string]string
	next int
}

func newDraftIDs() *draftIDs { return &draftIDs{ids: map[string]string{}} }

func (d *draftIDs) token(raw string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if token, ok := d.ids[raw]; ok {
		return token
	}
	d.next++
	token := fmt.Sprintf("id-%d", d.next)
	d.ids[raw] = token
	return token
}

// rawExchange is the private, unsanitized form handed to draftExchange. It
// never leaves this package.
type rawExchange struct {
	connection      string
	method          string
	scheme          string
	host            string
	path            string
	requestHeaders  http.Header
	requestBody     []byte
	status          int
	responseHeaders http.Header
	responseBody    []byte // nil for media responses
	responseLength  int64  // media responses: byte count only
	emptyResponse   bool
}

type draftBuilder struct {
	policy Policy
	ids    *draftIDs
}

func (b *draftBuilder) redact(raw string) {
	if _, done := b.policy.Aliases[raw]; done {
		return
	}
	if wholeURL(raw) {
		b.policy.Aliases[raw] = draftRedactedURL
		return
	}
	b.policy.Aliases[raw] = "[redacted]"
}

// draftDynamic reports opaque identifiers (task IDs, request IDs): a UUID, or
// an identifier of at least minLength bytes with four or more digits.
func draftDynamic(raw string, minLength int) bool {
	if !identifierRE.MatchString(raw) {
		return false
	}
	if uuidRE.MatchString(raw) {
		return true
	}
	return len(raw) >= minLength && len(draftDigitRE.FindAllString(raw, 5)) >= 4
}

func draftModelKey(key string) bool {
	compact := compactKey(key)
	return compact == "model" || strings.HasSuffix(compact, "model") || compact == "version" || compact == "object" ||
		compact == "status" || compact == "type"
}

func draftIDKey(key string) bool {
	compact := compactKey(key)
	return compact == "id" || compact == "ids" || compact == "uuid" || strings.HasSuffix(compact, "id") ||
		strings.HasSuffix(compact, "ids")
}

func (b *draftBuilder) idAlias(raw string) {
	if _, done := b.policy.Aliases[raw]; done {
		return
	}
	token := b.ids.token(raw)
	b.policy.Aliases[raw] = token
	b.policy.Allowed[token] = true
}

func (b *draftBuilder) text(key, raw string) {
	if raw == "" || raw == "[redacted]" {
		return
	}
	if sensitiveKey(key) || identityKeys[compactKey(key)] {
		return // the sanitizer replaces these by key
	}
	trimmed := strings.TrimSpace(raw)
	switch {
	case draftPromptKeys[compactKey(key)], len(raw) > draftLongText, wholeURL(raw),
		len(trimmed) >= 2 && (trimmed[0] == '{' || trimmed[0] == '['):
		b.redact(raw)
	case identifierRE.MatchString(raw) && (draftIDKey(key) || draftDynamic(raw, 24)):
		b.idAlias(raw)
	case checkText(raw, true) != nil || schemeURLRE.MatchString(raw):
		b.redact(raw)
	case draftModelKey(key):
		if _, aliased := b.policy.Aliases[raw]; !aliased {
			b.policy.Allowed[raw] = true
		}
	case strings.ContainsAny(raw, "\n\r\t"):
		b.redact(raw)
	default:
		if _, aliased := b.policy.Aliases[raw]; !aliased {
			b.policy.Allowed[raw] = true
		}
	}
}

func (b *draftBuilder) value(key string, value any, depth int) {
	if depth > 12 {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		for child, item := range typed {
			b.policy.Keys[child] = true
			b.value(child, item, depth+1)
		}
	case []any:
		for _, item := range typed {
			b.value(key, item, depth+1)
		}
	case string:
		b.text(key, typed)
	case json.Number:
		b.policy.Allowed[typed.String()] = true
	}
}

func (b *draftBuilder) jsonBody(raw []byte) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) == nil {
		b.value("", value, 0)
	}
}

// sseBody mirrors normalizeSSE's merge: content and reasoning are merged per
// choice and redacted; tool IDs, names and arguments are merged per tool.
func (b *draftBuilder) sseBody(raw []byte) {
	chunks, err := parseSSE(raw)
	if err != nil {
		return
	}
	type tool struct{ id, name, args, lastArgs strings.Builder }
	type choice struct {
		content, reasoning strings.Builder
		tools              map[int]*tool
		order              []int
	}
	choices := map[int]*choice{}
	var order []int
	for _, chunk := range chunks {
		b.value("", chunk, 0)
		list, _ := chunk["choices"].([]any)
		for _, rawChoice := range list {
			item, _ := rawChoice.(map[string]any)
			index, _ := sseIndex(item["index"])
			state := choices[index]
			if state == nil {
				state = &choice{tools: map[int]*tool{}}
				choices[index] = state
				order = append(order, index)
			}
			delta, _ := item["delta"].(map[string]any)
			if text, ok := sseString(delta["content"]); ok {
				state.content.WriteString(text)
			}
			if text, ok := sseString(delta["reasoning_content"]); ok {
				state.reasoning.WriteString(text)
			}
			tools, _ := delta["tool_calls"].([]any)
			for _, rawTool := range tools {
				call, _ := rawTool.(map[string]any)
				toolIndex, _ := sseIndex(call["index"])
				current := state.tools[toolIndex]
				if current == nil {
					current = &tool{}
					state.tools[toolIndex] = current
					state.order = append(state.order, toolIndex)
				}
				if id, ok := sseString(call["id"]); ok {
					current.id.WriteString(id)
				}
				function, _ := call["function"].(map[string]any)
				if name, ok := sseString(function["name"]); ok {
					current.name.WriteString(name)
				}
				if args, ok := sseString(function["arguments"]); ok && args != "" {
					current.args.WriteString(args)
					current.lastArgs.Reset()
					current.lastArgs.WriteString(args)
				}
			}
		}
	}
	for _, index := range order {
		state := choices[index]
		for _, merged := range []string{state.content.String(), state.reasoning.String()} {
			if merged != "" {
				b.redact(merged)
			}
		}
		for _, toolIndex := range state.order {
			current := state.tools[toolIndex]
			if id := current.id.String(); id != "" {
				b.idAlias(id)
			}
			if name := current.name.String(); name != "" {
				b.text("tool", name)
			}
			args := current.args.String()
			if !json.Valid([]byte(args)) && json.Valid([]byte(current.lastArgs.String())) {
				args = current.lastArgs.String()
			}
			if args != "" {
				b.jsonBody([]byte(args))
			}
		}
	}
}

func (b *draftBuilder) multipartBody(raw []byte, contentType string) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return
	}
	reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err != nil {
			return
		}
		name, isFile, err := partDisposition(part.Header)
		if err == nil {
			b.policy.Keys[name] = true
			if !isFile {
				data, readErr := readPart(part, b.policy.limit())
				if readErr == nil {
					b.text(name, string(data))
				}
			}
		}
		_ = part.Close()
	}
}

func (b *draftBuilder) body(raw []byte, contentType string) {
	if len(raw) == 0 {
		return
	}
	switch kind, _ := payloadKind(contentType); kind {
	case PayloadJSON:
		b.jsonBody(raw)
	case PayloadSSE:
		b.sseBody(raw)
	case PayloadMultipart:
		b.multipartBody(raw, contentType)
	}
}

func (b *draftBuilder) headers(headers http.Header, request bool) {
	allow := responseHeaders
	if request {
		allow = requestHeaders
	}
	for name, values := range headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if !allow[lower] || lower == "content-type" {
			continue
		}
		value := strings.Join(values, ", ")
		if checkText(value, false) != nil || len(value) > draftLongText {
			b.redact(value)
			continue
		}
		b.policy.Allowed[value] = true
	}
}

// draftPolicyFor derives the private policy for one raw exchange.
func draftPolicyFor(raw rawExchange, ids *draftIDs, limit int64) Policy {
	b := &draftBuilder{ids: ids, policy: Policy{
		MaxBodyBytes: limit, Keys: map[string]bool{}, Allowed: map[string]bool{}, Aliases: map[string]string{},
	}}
	host := strings.ToLower(strings.TrimSpace(raw.host))
	if host != "" && host != "example.test" {
		b.policy.Aliases[host] = "example.test"
	}
	for _, segment := range strings.Split(raw.path, "/") {
		if segment == "" {
			continue
		}
		if draftDynamic(segment, 16) || !segmentRE.MatchString(segment) || checkText(segment, false) != nil {
			b.idAlias(segment)
			continue
		}
		b.policy.Allowed[segment] = true
	}
	b.headers(raw.requestHeaders, true)
	b.headers(raw.responseHeaders, false)
	b.body(raw.requestBody, raw.requestHeaders.Get("Content-Type"))
	if raw.responseBody != nil {
		b.body(raw.responseBody, raw.responseHeaders.Get("Content-Type"))
	}
	return b.policy
}

// draftSkip names a fixed, value-free reason an exchange was not recorded.
type draftSkip string

const (
	skipTransport   draftSkip = "transport-error"
	skipIncomplete  draftSkip = "incomplete-body"
	skipTooLarge    draftSkip = "body-over-limit"
	skipUnsupported draftSkip = "unsupported-body"
	skipUnsafe      draftSkip = "sanitizer-refused"
	skipRedirect    draftSkip = "redirect"
)

// draftExchange sanitizes one raw exchange with its derived policy and
// validates the result. It returns a value-free reason when it refuses.
func draftExchange(raw rawExchange, sequence int, meta Metadata, ids *draftIDs, limit int64) (Exchange, draftSkip) {
	if raw.status >= 300 && raw.status < 400 {
		return Exchange{}, skipRedirect
	}
	policy := draftPolicyFor(raw, ids, limit)
	method := strings.ToUpper(raw.method)
	if !methods[method] || raw.scheme != "https" {
		return Exchange{}, skipUnsupported
	}
	request := Request{Method: method, Complete: true}
	var err error
	if request.Host, err = sanitizeHost(raw.host, policy); err != nil {
		return Exchange{}, skipUnsafe
	}
	if request.Path, err = sanitizePath(raw.path, policy); err != nil {
		return Exchange{}, skipUnsafe
	}
	if request.Headers, err = sanitizeHeaders(raw.requestHeaders, true, policy); err != nil {
		return Exchange{}, skipUnsafe
	}
	if request.ContentType, err = contentBase(raw.requestHeaders.Get("Content-Type")); err != nil {
		return Exchange{}, skipUnsupported
	}
	substituted := false
	if request.ContentType == "multipart/form-data" {
		parsed, _, err := parseMultipart(raw.requestBody, raw.requestHeaders.Get("Content-Type"), policy)
		if err != nil {
			return Exchange{}, skipUnsafe
		}
		for i := range parsed.Parts {
			if parsed.Parts[i].Media == nil {
				continue
			}
			synthetic, ok := draftSynthetic(parsed.Parts[i].Media.MediaType)
			if !ok {
				return Exchange{}, skipUnsupported
			}
			parsed.Parts[i].Media = &synthetic
			substituted = true
		}
		request.Body = Payload{Encoding: PayloadMultipart, Multipart: parsed}
	} else if request.Body, err = sanitizeBody(raw.requestBody, request.ContentType, policy); err != nil {
		return Exchange{}, skipUnsafe
	}

	response := Response{Status: raw.status, Complete: true}
	if response.ContentType, err = contentBase(raw.responseHeaders.Get("Content-Type")); err != nil {
		return Exchange{}, skipUnsupported
	}
	if response.Headers, err = sanitizeHeaders(raw.responseHeaders, false, policy); err != nil {
		return Exchange{}, skipUnsafe
	}
	switch {
	case raw.emptyResponse:
		response.Body = Payload{Encoding: PayloadNone}
	case supportedMediaTypes[response.ContentType]:
		if raw.responseLength <= 0 {
			return Exchange{}, skipIncomplete
		}
		synthetic, ok := draftSynthetic(response.ContentType)
		if !ok {
			return Exchange{}, skipUnsupported
		}
		response.ContentType = synthetic.MediaType
		response.Body = Payload{Encoding: PayloadMedia, Media: &synthetic}
		substituted = true
	default:
		if response.Body, err = sanitizeBody(raw.responseBody, response.ContentType, policy); err != nil {
			return Exchange{}, skipUnsafe
		}
	}
	meta.Connection = draftIdentifier(raw.connection)
	meta.Endpoint = request.Method + " " + request.Path
	meta.Protocol = protocolFor(response.ContentType)
	meta.Provenance = ProvenanceCaptured
	if substituted {
		meta.Provenance = ProvenanceDerived
	}
	exchange := Exchange{Sequence: sequence, Metadata: meta, Request: request, Response: response, Complete: true}
	if err := Validate([]Exchange{exchange}, policy); err != nil {
		return Exchange{}, skipUnsafe
	}
	return exchange, ""
}

func draftSynthetic(mediaType string) (Media, bool) {
	recipe, ok := draftMediaRecipes[mediaType]
	if !ok {
		return Media{}, false
	}
	media, _, err := SyntheticMedia(recipe)
	return media, err == nil
}

func draftIdentifier(value string) string {
	if identifierRE.MatchString(value) && checkText(value, false) == nil {
		return value
	}
	return "unknown"
}

// DraftReplayPolicy derives the committed replay policy from already
// sanitized exchanges: every key and terminal public value they contain, and
// nothing else (no Aliases). It fails when the exchanges do not validate.
func DraftReplayPolicy(exchanges []Exchange) (Policy, error) {
	policy := Policy{Keys: map[string]bool{}, Allowed: map[string]bool{}}
	allow := func(value string) {
		if value != "" && value != "[redacted]" && !canonicalExampleURL(value) {
			policy.Allowed[value] = true
		}
	}
	decode := func(text string) any {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
			return nil
		}
		return value
	}
	var collect func(any, int)
	collect = func(value any, depth int) {
		if depth > 12 {
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				policy.Keys[key] = true
				collect(child, depth+1)
			}
		case []any:
			for _, child := range typed {
				collect(child, depth+1)
			}
		case string:
			if nested := decode(typed); nested != nil {
				switch nested.(type) {
				case map[string]any, []any:
					collect(nested, depth+1)
					return
				}
			}
			allow(typed)
		case json.Number:
			allow(typed.String())
		}
	}
	for _, exchange := range exchanges {
		for _, part := range strings.Split(exchange.Request.Path, "/") {
			allow(part)
		}
		for _, headers := range []map[string]string{exchange.Request.Headers, exchange.Response.Headers} {
			for name, value := range headers {
				if name != "content-type" {
					allow(value)
				}
			}
		}
		for _, payload := range []Payload{exchange.Request.Body, exchange.Response.Body} {
			if len(payload.JSON) > 0 {
				collect(decode(string(payload.JSON)), 0)
			}
			for _, line := range strings.Split(payload.SSE, "\n") {
				if text, ok := strings.CutPrefix(line, "data: "); ok && text != "[DONE]" {
					collect(decode(text), 0)
				}
			}
			if payload.Multipart != nil {
				for _, part := range payload.Multipart.Parts {
					policy.Keys[part.Name] = true
					allow(part.Value)
				}
			}
		}
	}
	if err := Validate(exchanges, policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}
