// Package logging builds the process logger: JSON lines, a runtime-configured
// level (debug by default on every test stack), and redaction applied to
// every attribute so DEBUG never writes credentials or signed URLs.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

// ParseLevel accepts the runtime setting's values. An empty value means debug.
func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("log level %q is not one of debug/info/warn/error", value)
}

// New returns a JSON logger whose attributes pass through Redact.
func New(writer io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactAttr,
	}))
}

var sensitiveKey = regexp.MustCompile(`(?i)(pass(word)?|secret|token|api[_-]?key|authorization|cookie|credential|signature)`)

func redactAttr(_ []string, attr slog.Attr) slog.Attr {
	if attr.Key == slog.TimeKey || attr.Key == slog.LevelKey {
		return attr
	}
	if sensitiveKey.MatchString(attr.Key) && !isIdentifierKey(attr.Key) {
		return slog.String(attr.Key, redacted)
	}
	switch attr.Value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, Redact(attr.Value.String()))
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok && err != nil {
			return slog.String(attr.Key, Redact(err.Error()))
		}
		if stringer, ok := attr.Value.Any().(fmt.Stringer); ok {
			return slog.String(attr.Key, Redact(stringer.String()))
		}
	}
	return attr
}

// Keys that only name an identity (token_count, password_reset_id, ...) are
// not secrets; only the request or secret value itself is.
func isIdentifierKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, "_id") || strings.HasSuffix(lower, "_count") || strings.HasSuffix(lower, "_name")
}

const redacted = "[redacted]"

var (
	urlQuery      = regexp.MustCompile(`(?i)\b(https?://[^\s"'<>?#]+)[?#][^\s"'<>]*`)
	bearer        = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	assignment    = regexp.MustCompile(`(?i)\b((?:[a-z_-]*?)(?:password|passwd|secret|token|api[_-]?key|authorization|cookie|session))("?\s*[:=]\s*"?)[^\s"',;&]+`)
	providerKey   = regexp.MustCompile(`\b(sk|rk|pk|tsk|msy|key)-[A-Za-z0-9_-]{12,}\b`)
	dataURIBase64 = regexp.MustCompile(`(?i)data:[a-z0-9.+/-]+;base64,[A-Za-z0-9+/=]{64,}`)
)

// Redact removes credential-shaped values and URL query strings (signed URL
// parameters) from free text such as Go error chains.
func Redact(text string) string {
	if text == "" {
		return text
	}
	text = urlQuery.ReplaceAllString(text, "$1?"+redacted)
	text = bearer.ReplaceAllString(text, "$1 "+redacted)
	text = assignment.ReplaceAllString(text, "$1$2"+redacted)
	text = providerKey.ReplaceAllString(text, redacted)
	text = dataURIBase64.ReplaceAllString(text, "data:"+redacted)
	return text
}

// ErrChain renders an error and its wrapped causes once, already redacted.
// A domain error whose message is a public detail hides its wrapped cause
// (#274: "上传文件为空、过大或不可读取" without the size that tripped it), so
// every cause the text does not already contain is appended.
func ErrChain(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	for _, cause := range wrappedCauses(err) {
		if message := cause.Error(); message != "" && !strings.Contains(text, message) {
			text += ": " + message
		}
	}
	return Redact(text)
}

// wrappedCauses lists the errors under err, depth first; joined errors keep their order.
func wrappedCauses(err error) []error {
	var causes []error
	var walk func(error, int)
	walk = func(current error, depth int) {
		if current == nil || depth > 32 {
			return
		}
		switch wrapped := current.(type) {
		case interface{ Unwrap() []error }:
			for _, cause := range wrapped.Unwrap() {
				if cause != nil {
					causes = append(causes, cause)
					walk(cause, depth+1)
				}
			}
		default:
			if cause := errors.Unwrap(current); cause != nil {
				causes = append(causes, cause)
				walk(cause, depth+1)
			}
		}
	}
	walk(err, 0)
	return causes
}
