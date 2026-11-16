package wirecapture

import (
	"fmt"
	"mime"
	"strings"
)

// ContentBase returns the media type of a Content-Type header without its
// parameters, after the same text checks every public value undergoes. It
// refuses types the fixtures cannot carry.
func ContentBase(raw string) (string, error) {
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
	if _, err := PayloadKind(base); err != nil {
		return "", err
	}
	return base, nil
}

// ProtocolFor names the Metadata.Protocol of a response content type.
func ProtocolFor(contentType string) string {
	switch kind, _ := PayloadKind(contentType); kind {
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
