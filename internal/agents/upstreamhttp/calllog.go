package upstreamhttp

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Call is one model call as the log sees it (#382). Every Agent model call
// leaves one line with these fields, so a slow round can be traced to its model,
// its thinking level and its upstream without a profiler.
//
// What is never here is the request's own content: no prompt, no picture, no
// key. RequestBytes is a size, nothing else.
type Call struct {
	// Model and Connection name the served model and the connection it went
	// to; Purpose is the request's kind ("" for a conversation turn).
	Model      string
	Connection string
	// Effort is the thinking level the request carried, as the wire sent it
	// ("" when the model offers none).
	Effort string
	// Purpose is the request's kind, when it is not a conversation turn.
	Purpose string
	// Stream tells a streamed call from a whole-completion one.
	Stream bool
	// Elapsed is the whole call, from the request to the last byte of the
	// reply.
	Elapsed time.Duration
	// FirstByte is how long the upstream took to answer at all: the response
	// headers of a whole call, the first stream frame of a streamed one.
	FirstByte time.Duration
	// RequestBytes is the encoded request body's size; 0 when unknown.
	RequestBytes int
	// Usage is what the upstream reported, nil when it reported nothing.
	Usage *agents.Usage
	// RequestID is the upstream's own id for the call; empty when it sent none
	// and the logger then names the call locally.
	RequestID string
	// Images counts the request's pictures; OriginalImageBytes and ImageBytes
	// are their inline sizes before and after they were encoded for the wire
	// (chat.agent.request_pictures).
	Images             int
	OriginalImageBytes int64
	ImageBytes         int64
	// Status is the HTTP status the upstream answered with, 0 when the call
	// failed before any answer. ResponseBody is a refusal's body: only an
	// excerpt of it is logged, sanitized (ResponseExcerpt), and never for a
	// call that succeeded.
	Status       int
	ResponseBody []byte
	// Echoes are the request's own texts, removed from the excerpt so that an
	// upstream echoing the prompt does not put it in the log.
	Echoes []string
	// Err is the call's error, nil when it succeeded.
	Err error
}

// ResponseExcerptBytes bounds the refusal body excerpt of a failed call.
const ResponseExcerptBytes = 200

var dataURI = regexp.MustCompile(`(?i)data:[a-z0-9.+/-]*(?:;[a-z0-9=.+-]*)*,[A-Za-z0-9+/=_-]*`)

// ResponseExcerpt is the first ResponseExcerptBytes bytes of an upstream
// response body that may be logged: every data URI is cut out (an upstream may
// echo a picture), then the body goes through the one sanitizer of upstream
// text (generation.UpstreamExcerpt: the request's own texts, URLs, keys and
// e-mail addresses), and the result is cut on a character boundary.
func ResponseExcerpt(body []byte, echoes ...string) string {
	if len(body) == 0 {
		return ""
	}
	text := dataURI.ReplaceAllString(string(body), "[data-uri]")
	text = generation.UpstreamExcerpt(text, len(text)+1, echoes...)
	if len(text) <= ResponseExcerptBytes {
		return text
	}
	cut := ResponseExcerptBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// RequestTexts are the texts of a request that an upstream refusal might echo.
func RequestTexts(request agents.Request) []string {
	var texts []string
	for _, message := range request.Messages {
		if message.Content != "" {
			texts = append(texts, message.Content)
		}
		for _, part := range message.Parts {
			if part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
	}
	return texts
}

// CallLogger writes the one line of each Agent model call. Its zero value (or
// nil, as an Adapter built for a test holds it) writes nothing.
type CallLogger struct {
	logger *slog.Logger
	// requests numbers the calls in this process, so a call whose upstream sent
	// no id still has one in the log.
	requests atomic.Uint64
}

// NewCallLogger returns the logger of one Adapter's calls; nil writes nothing.
func NewCallLogger(logger *slog.Logger) *CallLogger {
	if logger == nil {
		return nil
	}
	return &CallLogger{logger: logger}
}

// Log writes one model call's line, naming the call locally when the upstream
// sent no id.
func (calls *CallLogger) Log(call Call) {
	if calls == nil || calls.logger == nil {
		return
	}
	if call.RequestID == "" {
		call.RequestID = strconv.FormatUint(calls.requests.Add(1), 10)
	}
	level := slog.LevelInfo
	if call.Err != nil {
		level = slog.LevelWarn
	}
	attributes := []any{
		"model", call.Model,
		"connection", call.Connection,
		"stream", call.Stream,
		"elapsed_ms", call.Elapsed.Milliseconds(),
		"first_byte_ms", call.FirstByte.Milliseconds(),
	}
	if call.Effort != "" {
		attributes = append(attributes, "effort", call.Effort)
	}
	if call.Purpose != "" {
		attributes = append(attributes, "purpose", call.Purpose)
	}
	if call.RequestID != "" {
		attributes = append(attributes, "request_id", call.RequestID)
	}
	// A request's size, not its content: the size says whether a round paid
	// for its pictures again (#382) and whether a body grew past what the
	// path to the upstream carries.
	attributes = append(attributes,
		"request_bytes", call.RequestBytes,
		"images", call.Images,
		"image_bytes_before", call.OriginalImageBytes,
		"image_bytes_after", call.ImageBytes)
	if call.Usage != nil {
		attributes = append(attributes,
			"prompt_tokens", call.Usage.PromptTokens,
			"completion_tokens", call.Usage.CompletionTokens)
	}
	if call.Err != nil {
		// A failed call (a refusal, a network error, a broken stream) always
		// has its status, its request id and the head of the upstream's
		// answer, so a gateway's 502 can be traced without the request.
		attributes = append(attributes, "status", call.Status)
		if excerpt := ResponseExcerpt(call.ResponseBody, call.Echoes...); excerpt != "" {
			attributes = append(attributes, "response_excerpt", excerpt)
		}
		// The refusal's error carries the raw body; the line keeps only the
		// sanitized excerpt above.
		message := call.Err.Error()
		if body := strings.TrimSpace(string(call.ResponseBody)); body != "" {
			message = strings.ReplaceAll(message, body, "[response_excerpt]")
		}
		attributes = append(attributes, "error", message)
	}
	calls.logger.Log(context.Background(), level, "Agent 模型调用", attributes...)
}

// RequestID is the upstream's own id for a call, empty when it sent none: the
// header names differ by gateway, so the common ones are read.
func RequestID(header http.Header) string {
	for _, name := range []string{"x-request-id", "x-requestid", "request-id"} {
		if value := header.Get(name); value != "" {
			return value
		}
	}
	return ""
}
