package meshy

import (
	"regexp"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

const taskErrorCodeRunes = 64

var taskErrorCode = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// taskErrorEvidence returns the internal forensic code and excerpt for a
// failed Meshy task from the official task_error object (type, code, message).
// Messages are upstream-controlled and may echo the submitted prompt, signed
// URLs, tokens or e-mail addresses, so the excerpt goes through the one shared
// sanitizer (generation.UpstreamExcerpt); echoes are the frozen task input's
// free-text values, stripped exactly. A code that is not a plain token is
// dropped entirely.
func taskErrorEvidence(response taskResponse, echoes ...string) (code, excerpt string) {
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
	if message := strings.TrimSpace(response.TaskError.Message); message != "" {
		parts = append(parts, "message="+message)
	}
	return code, generation.UpstreamExcerpt(strings.Join(parts, " "), generation.UpstreamExcerptRunes, echoes...)
}
