package openaiv1

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// The reply forms a real chat model takes that the fixed text and the tool
// call markers do not reach (#536). All are fake-only request conventions, read
// in the last message like the others, and every form is constructed from a
// vendor document or from a Plinth Schema declaration: no committed sample has
// one, and the endpoint table says so. A real round turns each into a sample.
const (
	// DSMLMarker in the last user message, together with tool markers, answers
	// with the calls written as DSML in the reply text instead of as
	// tool_calls, the way a model whose profile declares dsml_tool_calls does
	// (DeepSeek's V4 tool-call format; the spelling is the Schema's chat.dsml:
	// the ｜DSML｜ marker, a tool_calls block of invoke elements, string and
	// non-string parameters told apart by the string attribute). The reply ends
	// with finish_reason stop and has no tool_calls member.
	DSMLMarker = "[[fake:dsml]]"
	// InlineImageMarker answers with a picture the model drew itself: the PNG
	// arrives as base64 in delta.images (a stream) or message.images (a body)
	// next to the reply text, as gpt-astra did behind the gateway (the shape
	// the Adapter's inline-image tests fix).
	InlineImageMarker = "[[fake:inline-image]]"
	// StrictReasoningMarker anywhere in the conversation switches the DeepSeek
	// reasoning_content rule to its broad reading: with tools in the request,
	// every assistant message must carry the field, a text round included
	// (constraints.go, toolCallWithoutReasoning).
	StrictReasoningMarker = "[[fake:strict-reasoning]]"
	// QuotaMarkerPrefix, [[fake:quota:CODE]] in the last user message, answers
	// HTTP 429 with the error code CODE: one of the codes the chat connection's
	// Schema error_mapping declares as a used-up balance or key quota, or one of
	// the two rate limits, which are only a wait: slow_down (the error-code
	// guide's rate_limit_error code, checked 2026-11-25) and
	// rate_limit_exceeded (kept for the gateways that answer it; the guide does
	// not list it as a code). The envelope is the official
	// ErrorResponse (OpenAPI components.schemas.Error, error-code guide), so
	// the quota class and the rate-limit class can be told apart by code alone.
	QuotaMarkerPrefix = "[[fake:quota:"
	// GatewayFaultMarker, [[fake:gateway-502]] in the last user message,
	// answers the proxy's own failure (#555): HTTP 502 with a plain-text body
	// that echoes the request's last text, as the proxy in front of the gateway
	// did when a request body exceeded what it carried, and an X-Request-Id
	// header. A fake-only request convention, constructed from the 2026-11-24
	// gateway investigation (no committed sample).
	GatewayFaultMarker = "[[fake:gateway-502]]"
	// GatewayFaultRequestID is the X-Request-Id of that 502.
	GatewayFaultRequestID = "fake-gateway-502"
)

// QuotaCodes are the codes QuotaMarkerPrefix accepts, with the error type the
// official ErrorResponse gives them: the 429 of an exhausted balance is
// insufficient_quota (error-code guide), slow_down is rate_limit_error (the
// guide's row), and rate_limit_exceeded is "requests" or "tokens" (the type
// names the limit that was hit; not in the guide).
var QuotaCodes = map[string]string{
	"insufficient_quota":                "insufficient_quota",
	"credit_balance_exhausted":          "insufficient_quota",
	"organization_spend_limit_exceeded": "insufficient_quota",
	"project_spend_limit_exceeded":      "insufficient_quota",
	"organization_usage_limit_exceeded": "insufficient_quota",
	"quota_exceeded":                    "quota_exceeded",
	"rate_limit_exceeded":               "requests",
	"slow_down":                         "rate_limit_error",
}

// RateLimitCode reports whether a QuotaCodes code is a rate limit (a wait,
// answered with Retry-After) rather than a used-up balance or quota.
func RateLimitCode(code string) bool {
	return code == "rate_limit_exceeded" || code == "slow_down"
}

var quotaMarker = regexp.MustCompile(`\[\[fake:quota:([a-z_]+)\]\]`)

// quotaFault is the 429 a [[fake:quota:CODE]] marker asks for.
func (r chatRequest) quotaFault() (code string, ok bool) {
	for _, match := range quotaMarker.FindAllStringSubmatch(r.lastText(), -1) {
		if _, known := QuotaCodes[match[1]]; known {
			return match[1], true
		}
	}
	return "", false
}

// writeQuotaFault answers a quota marker. quota_exceeded keeps the gateway's
// own body (observed 2026-11-20, quotaExceededBody); the other codes are the
// official envelope with param and code.
func (h *handler) writeQuotaFault(w http.ResponseWriter, code string) {
	if code == "quota_exceeded" {
		writeJSON(w, http.StatusTooManyRequests, quotaExceededBody())
		return
	}
	message := "You exceeded your current quota, please check your plan and billing details."
	switch code {
	case "rate_limit_exceeded":
		message = "Rate limit reached for requests per min (RPM): Limit 3, Used 3, Requested 1. Please try again in 20s."
	case "slow_down":
		// The guide gives the cause, not the wording: the message is the fake's.
		message = "Your request rate increased too quickly. Please reduce your request rate and increase it gradually."
	}
	if RateLimitCode(code) {
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
	}
	writeJSON(w, http.StatusTooManyRequests, officialErrorBody(message, QuotaCodes[code], nil, code))
}

// StreamUsageTrailing reports whether the fake streams a model's usage on its
// own last chunk (empty choices) instead of on the finish_reason chunk.
func StreamUsageTrailing(model string) bool {
	rule, constrained := constraintFor(model)
	return constrained && rule.streamUsageTrailing
}

// dsmlBlock writes the calls as the DSML block of a reply.
func dsmlBlock(calls []toolCall) string {
	const marker = "｜DSML｜"
	var block strings.Builder
	block.WriteString("<" + marker + "tool_calls>\n")
	for _, call := range calls {
		fmt.Fprintf(&block, "<%sinvoke name=%q>\n", marker, call.name)
		var arguments map[string]json.RawMessage
		if json.Unmarshal([]byte(call.arguments), &arguments) == nil {
			names := make([]string, 0, len(arguments))
			for name := range arguments {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				value := string(arguments[name])
				isString := "false"
				var text string
				if json.Unmarshal(arguments[name], &text) == nil {
					value, isString = text, "true"
				}
				fmt.Fprintf(&block, "<%sparameter name=%q string=%q>%s</%sparameter>\n", marker, name, isString, value, marker)
			}
		}
		block.WriteString("</" + marker + "invoke>\n")
	}
	block.WriteString("</" + marker + "tool_calls>")
	return block.String()
}

// splitForStream cuts a reply into pieces a stream sends one by one, so that a
// reader that must hold a half tag back sees a marker split across chunks.
func splitForStream(text string) []string {
	runes := []rune(text)
	const pieces = 4
	size := (len(runes) + pieces - 1) / pieces
	var out []string
	for start := 0; start < len(runes); start += size {
		out = append(out, string(runes[start:min(start+size, len(runes))]))
	}
	return out
}

// inlinePNG is a tiny valid PNG (1x1, opaque); what the model drew.
const inlinePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// inlineImages is the delta.images / message.images member of a drawn picture.
func inlineImages() []any {
	return []any{map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": "data:image/png;base64," + inlinePNG},
	}}
}

// replyForm is how the reply of a request is shaped beyond the fixed text.
type replyForm struct {
	dsml        bool
	inlineImage bool
}

func (r chatRequest) replyForm() replyForm {
	last := r.lastText()
	return replyForm{
		dsml:        strings.Contains(last, DSMLMarker),
		inlineImage: strings.Contains(last, InlineImageMarker),
	}
}

// writeGatewayFault answers a GatewayFaultMarker: a 502 whose text body echoes
// the request's last text after a long filler, so a client that logs the body
// has to cut it and keep prompt text out of its log.
func (h *handler) writeGatewayFault(w http.ResponseWriter, request chatRequest) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Request-Id", GatewayFaultRequestID)
	w.WriteHeader(http.StatusBadGateway)
	_, _ = io.WriteString(w, "Bad Gateway: upstream refused "+request.lastText()+" "+strings.Repeat("x", 400)+"\n")
}
