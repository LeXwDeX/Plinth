// Package moderation reads the reasons an upstream content-safety refusal
// gives (#35), for the Agent adapter and the generation kernel alike. The
// tokens are vendor wire vocabulary; what a person reads for each one is the
// Product Schema's label.
package moderation

import (
	"regexp"
	"strings"
)

// maximumReasons bounds the reasons passed on from one refusal.
const maximumReasons = 6

var (
	// safetyViolations is Azure OpenAI's image refusal list:
	// "safety_violations=[sexual, violence]".
	safetyViolations = regexp.MustCompile(`safety_violations=\[([^\]]*)\]`)
	// filteredCategory is one category Azure OpenAI's content_filter_result
	// marks as filtered: "sexual":{"filtered":true,...}.
	filteredCategory = regexp.MustCompile(`"([A-Za-z_/-]+)"\s*:\s*\{\s*"filtered"\s*:\s*true`)
	// token is the shape of one reason passed on: a short vendor word, never
	// free text, since upstream bytes must not steer the Agent.
	token = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_./-]{0,39}$`)
)

// inspectionScopes are DashScope's data_inspection_failed messages, which
// name what was flagged but no category.
var inspectionScopes = []struct{ phrase, reason string }{
	{"input image data may contain inappropriate content", "input_image"},
	{"input text data may contain inappropriate content", "input_text"},
	{"input data may contain inappropriate content", "input"},
	{"output data may contain inappropriate content", "output"},
}

// Reasons are the reasons named in an upstream error body, in order and
// without repeats; nil when it names none.
func Reasons(body string) []string {
	var candidates []string
	if match := safetyViolations.FindStringSubmatch(body); match != nil {
		candidates = append(candidates, strings.Split(match[1], ",")...)
	}
	for _, match := range filteredCategory.FindAllStringSubmatch(body, -1) {
		candidates = append(candidates, match[1])
	}
	lowered := strings.ToLower(body)
	for _, scope := range inspectionScopes {
		if strings.Contains(lowered, scope.phrase) {
			candidates = append(candidates, scope.reason)
			break
		}
	}
	return Tokens(candidates...)
}

// Tokens keeps the candidates shaped like a reason, trimmed, in order and
// without repeats, at most maximumReasons.
func Tokens(candidates ...string) []string {
	var reasons []string
	for _, candidate := range candidates {
		candidate = strings.Trim(strings.TrimSpace(candidate), `"'`)
		if len(reasons) == maximumReasons || !token.MatchString(candidate) || contains(reasons, candidate) {
			continue
		}
		reasons = append(reasons, candidate)
	}
	return reasons
}

// Describe joins the Schema label of each reason with separator; a reason
// without a label is shown as is. Labels are keyed by the lower-case reason
// with "-" and "/" read as "_". localize turns each Schema text into the
// reader's locale; nil keeps the default.
func Describe(reasons []string, labels map[string]string, separator string, localize func(string) string) string {
	if localize == nil {
		localize = func(text string) string { return text }
	}
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		key := strings.NewReplacer("-", "_", "/", "_").Replace(strings.ToLower(reason))
		if label := labels[key]; label != "" {
			parts = append(parts, localize(label))
			continue
		}
		parts = append(parts, reason)
	}
	return strings.Join(parts, localize(separator))
}

// Explain fills template's {reasons} with Describe of reasons; "" without
// reasons or template. template, labels and separator are the Product
// Schema's content_rejected_reason* texts.
func Explain(template string, reasons []string, labels map[string]string, separator string, localize func(string) string) string {
	if template == "" || len(reasons) == 0 {
		return ""
	}
	if localize == nil {
		localize = func(text string) string { return text }
	}
	return strings.ReplaceAll(localize(template), "{reasons}", Describe(reasons, labels, separator, localize))
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}
