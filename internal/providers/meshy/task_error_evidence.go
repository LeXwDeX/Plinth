package meshy

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	taskErrorMessageRunes = 300
	taskErrorCodeRunes    = 64
)

var (
	taskErrorURL    = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+`)
	taskErrorSecret = regexp.MustCompile(`(?i)\b(?:bearer\s+\S+|msy[-_][A-Za-z0-9_-]{8,}|(?:token|key|signature|secret)=\S+)`)
	taskErrorCode   = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
)

// taskErrorEvidence returns the internal forensic code and excerpt for a
// failed Meshy task from the official task_error object (type, code, message).
// Messages are upstream-controlled: URLs (signed asset links), bearer tokens
// and key-like strings are replaced, control characters are dropped and the
// text is bounded. A code that is not a plain token is dropped entirely.
func taskErrorEvidence(response taskResponse) (code, excerpt string) {
	if response.TaskError == nil {
		return "", ""
	}
	code = strings.TrimSpace(response.TaskError.Code)
	if len(code) > taskErrorCodeRunes || !taskErrorCode.MatchString(code) {
		code = ""
	}
	parts := make([]string, 0, 3)
	if kind := strings.TrimSpace(response.TaskError.Type); kind != "" && len(kind) <= taskErrorCodeRunes && taskErrorCode.MatchString(kind) {
		parts = append(parts, "type="+kind)
	}
	if code != "" {
		parts = append(parts, "code="+code)
	}
	if message := redactTaskErrorText(response.TaskError.Message); message != "" {
		parts = append(parts, "message="+message)
	}
	return code, strings.Join(parts, " ")
}

func redactTaskErrorText(text string) string {
	text = strings.ToValidUTF8(text, " ")
	text = taskErrorURL.ReplaceAllString(text, "[url]")
	text = taskErrorSecret.ReplaceAllString(text, "[redacted]")
	text = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > taskErrorMessageRunes {
		text = string(runes[:taskErrorMessageRunes])
	}
	return text
}
