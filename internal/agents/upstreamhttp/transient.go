package upstreamhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/moderation"
)

// TransientStatus reports whether a refusal's HTTP status may not meet the same
// request again (#509): a request timeout, too many requests, and the server
// errors that say the upstream is broken or busy for now. A 429 that the
// Schema's error_mapping reads as a used-up quota is classified before this,
// so it is never one.
func TransientStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// RetryAfter is the wait a response's Retry-After header asks for, in seconds
// or as an HTTP date (RFC 9110 §10.2.3); 0 when the header is absent, invalid or
// already past.
func RetryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

// StreamError is the error for an error the upstream reported inside a 200
// answer: an error frame of a stream (data: {"error": {...}}), or the error
// object of a plain body sent where a stream was asked for. As on a refusal, a
// content-safety refusal and a used-up quota keep their classes; any other is
// transient, because the upstream accepted the request and broke off (#509).
func StreamError(payload []byte, exhaustedQuota func(status int, body []byte) (string, bool)) error {
	text := strings.TrimSpace(string(JSON(payload)))
	if runes := []rune(text); len(runes) > 500 {
		text = string(runes[:500])
	}
	err := fmt.Errorf("agent stream reported an error: %s", text)
	if code := ModerationCode(payload); code != "" {
		reasons := moderation.Reasons(string(payload))
		if len(reasons) == 0 {
			reasons = moderation.Tokens(code)
		}
		return errors.Join(&agents.ContentRejection{Reasons: reasons}, err)
	}
	if exhaustedQuota != nil {
		if class, exhausted := exhaustedQuota(http.StatusOK, JSON(payload)); exhausted {
			return errors.Join(&agents.QuotaExhaustion{Class: class}, err)
		}
	}
	return agents.Transient(err, 0)
}

// HasError reports whether a JSON payload is an error object: one with a
// non-null error member.
func HasError(payload []byte) bool {
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(JSON(payload), &body) != nil {
		return false
	}
	trimmed := strings.TrimSpace(string(body.Error))
	return trimmed != "" && trimmed != "null"
}

// NotAStream is the error for a 200 answer to a streaming request that had no
// stream frame at all (#509). head is its first line: an error object there is
// read as StreamError reads it; anything else (the plain completion object of
// a gateway that ignores stream=true, a proxy's page) is transient. The body
// itself stays out of the error: it may be a page or an answer, not a reason.
func NotAStream(contentType, head string, exhaustedQuota func(status int, body []byte) (string, bool)) error {
	if HasError([]byte(head)) {
		return StreamError([]byte(head), exhaustedQuota)
	}
	return agents.Transient(fmt.Errorf("agent stream: a 200 answer of type %q carried no stream frame", contentType), 0)
}

// errNoFirstByte is the cause of a call the first-byte timeout ended.
var errNoFirstByte = errors.New("agent upstream sent no first byte")

// FirstByteGuard bounds how long a streamed call waits for its first frame,
// the response headers included (chat.agent.first_byte_timeout_seconds, #509).
// The call runs on Context; Arrived stops the clock at the first frame, and
// Release ends the guard when the call returns.
type FirstByteGuard struct {
	parent  context.Context
	ctx     context.Context
	cancel  context.CancelCauseFunc
	timer   *time.Timer
	timeout time.Duration
}

// GuardFirstByte starts the clock; a timeout of 0 or less guards nothing.
func GuardFirstByte(ctx context.Context, timeout time.Duration) *FirstByteGuard {
	guard := &FirstByteGuard{parent: ctx, ctx: ctx, timeout: timeout}
	if timeout <= 0 {
		return guard
	}
	guard.ctx, guard.cancel = context.WithCancelCause(ctx)
	guard.timer = time.AfterFunc(timeout, func() { guard.cancel(errNoFirstByte) })
	return guard
}

// Context is the context the call runs on.
func (guard *FirstByteGuard) Context() context.Context { return guard.ctx }

// Arrived stops the clock: the first frame came.
func (guard *FirstByteGuard) Arrived() {
	if guard.timer != nil {
		guard.timer.Stop()
	}
}

// Release ends the guard.
func (guard *FirstByteGuard) Release() {
	guard.Arrived()
	if guard.cancel != nil {
		guard.cancel(context.Canceled)
	}
}

// Err classifies a failure to send the request or to read its answer: no
// first byte in time and a broken connection are transient; a call its caller
// gave up on (the caller's context ended) is not.
func (guard *FirstByteGuard) Err(err error) error {
	if err == nil {
		return nil
	}
	if guard.cancel != nil && errors.Is(context.Cause(guard.ctx), errNoFirstByte) {
		return agents.Transient(fmt.Errorf("%w within %s: %w", errNoFirstByte, guard.timeout, err), 0)
	}
	if guard.parent.Err() != nil {
		return err
	}
	return agents.Transient(err, 0)
}
