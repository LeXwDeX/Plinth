package upstreamhttp

import (
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
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
	// Err is the call's error, nil when it succeeded.
	Err error
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
	if call.Usage != nil {
		attributes = append(attributes,
			"prompt_tokens", call.Usage.PromptTokens,
			"completion_tokens", call.Usage.CompletionTokens)
	}
	if call.Err != nil {
		attributes = append(attributes, "error", call.Err)
	}
	calls.logger.Log(nil, level, "Agent 模型调用", attributes...)
	if call.RequestBytes > 0 {
		// A request's size, not its content: the size is what says whether a
		// round paid for its pictures again (#382).
		calls.logger.Debug("Agent 模型请求体积",
			"model", call.Model, "stream", call.Stream,
			"request_bytes", call.RequestBytes)
	}
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
