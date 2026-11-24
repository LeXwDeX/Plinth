package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// UpstreamHTTPErrorTexts is the one wording of an upstream HTTP error that a
// person reads (chat.presentation.messages.upstream_http_error): "{provider}
// 网关返回 {status}，原因：{reason}", with the reason the Schema declares for
// the status. A failed Task, the platform Agent's own turn and its tool
// results all render it here; the upstream's own text stays in the log.
type UpstreamHTTPErrorTexts struct {
	// Template names the provider, the HTTP status and the reason.
	Template string `json:"template"`
	// UnreachableTemplate names the provider and UnreachableReason for a
	// request that got no HTTP answer at all.
	UnreachableTemplate string `json:"unreachable_template"`
	// CombinedTemplate joins the upstream text ({upstream}) and a text that
	// must stay with it ({detail}), such as "the upstream may have charged".
	CombinedTemplate string `json:"combined_template"`
	// Reasons is keyed by an exact status ("429") or a status family
	// ("4xx", "5xx"); the exact status wins.
	Reasons           map[string]string `json:"reasons"`
	UnreachableReason string            `json:"unreachable_reason"`
	// FailureClasses are the Task failure classes whose text the upstream
	// text replaces; a class missing here (a content-safety refusal, a used-up
	// key quota) keeps its own, more specific text.
	FailureClasses []string `json:"failure_classes"`
}

var upstreamHTTPReasonKey = regexp.MustCompile(`^([1-5][0-9][0-9]|[45]xx)$`)

// Message is the text for an HTTP status the upstream answered, in the
// locale localize maps the Schema's default copy to (nil keeps it). "" when
// the status is no error status, no reason covers it or the provider is
// unnamed, so the caller keeps its own text.
func (texts UpstreamHTTPErrorTexts) Message(provider string, status int, localize func(string) string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" || status < 400 || status > 599 || texts.Template == "" {
		return ""
	}
	reason := texts.Reasons[strconv.Itoa(status)]
	if reason == "" {
		reason = texts.Reasons[strconv.Itoa(status/100)+"xx"]
	}
	if reason == "" {
		return ""
	}
	localize = keepText(localize)
	return strings.NewReplacer(
		"{provider}", localize(provider),
		"{status}", strconv.Itoa(status),
		"{reason}", localize(reason),
	).Replace(localize(texts.Template))
}

// UnreachableMessage is the text for a request that reached no upstream
// answer; "" when the Schema declares none or the provider is unnamed.
func (texts UpstreamHTTPErrorTexts) UnreachableMessage(provider string, localize func(string) string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" || texts.UnreachableTemplate == "" || texts.UnreachableReason == "" {
		return ""
	}
	localize = keepText(localize)
	return strings.NewReplacer(
		"{provider}", localize(provider),
		"{reason}", localize(texts.UnreachableReason),
	).Replace(localize(texts.UnreachableTemplate))
}

// Combined puts the upstream text in front of detail, which is already in
// the reader's locale; either alone when the other is empty.
func (texts UpstreamHTTPErrorTexts) Combined(upstream, detail string, localize func(string) string) string {
	if upstream == "" || detail == "" || texts.CombinedTemplate == "" {
		if upstream == "" {
			return detail
		}
		return upstream
	}
	return strings.NewReplacer("{upstream}", upstream, "{detail}", detail).
		Replace(keepText(localize)(texts.CombinedTemplate))
}

// Covers reports whether a Task failure of class shows the upstream text in
// place of the class's own text.
func (texts UpstreamHTTPErrorTexts) Covers(class string) bool {
	for _, covered := range texts.FailureClasses {
		if covered == class {
			return true
		}
	}
	return false
}

func keepText(localize func(string) string) func(string) string {
	if localize == nil {
		return func(text string) string { return text }
	}
	return localize
}

// validate checks an upstream_http_error declaration; an absent one (all
// fields empty) is allowed and renders nothing.
func (texts UpstreamHTTPErrorTexts) validate() error {
	if texts.Template == "" && texts.UnreachableTemplate == "" && len(texts.Reasons) == 0 &&
		texts.CombinedTemplate == "" && texts.UnreachableReason == "" && len(texts.FailureClasses) == 0 {
		return nil
	}
	for _, placeholder := range []string{"{provider}", "{status}", "{reason}"} {
		if !strings.Contains(texts.Template, placeholder) {
			return fmt.Errorf("presentation.upstream_http_error.template 须包含 %s", placeholder)
		}
	}
	if !strings.Contains(texts.UnreachableTemplate, "{provider}") || !strings.Contains(texts.UnreachableTemplate, "{reason}") ||
		strings.TrimSpace(texts.UnreachableReason) == "" {
		return errors.New("presentation.upstream_http_error.unreachable_template 须包含 {provider} 与 {reason}，并声明 unreachable_reason")
	}
	if !strings.Contains(texts.CombinedTemplate, "{upstream}") || !strings.Contains(texts.CombinedTemplate, "{detail}") {
		return errors.New("presentation.upstream_http_error.combined_template 须包含 {upstream} 与 {detail}")
	}
	if len(texts.Reasons) == 0 {
		return errors.New("presentation.upstream_http_error.reasons 不能为空")
	}
	for key, reason := range texts.Reasons {
		if !upstreamHTTPReasonKey.MatchString(key) || strings.TrimSpace(reason) == "" {
			return fmt.Errorf("presentation.upstream_http_error.reasons[%q] 须为 4xx/5xx 状态码或状态族并带文案", key)
		}
		if status, err := strconv.Atoi(key); err == nil && (status < 400 || status > 599) {
			return fmt.Errorf("presentation.upstream_http_error.reasons[%q] 须为 4xx/5xx 状态码", key)
		}
	}
	seen := map[string]struct{}{}
	for _, class := range texts.FailureClasses {
		if !schemacontract.FailureClass(class).Valid() {
			return fmt.Errorf("presentation.upstream_http_error.failure_classes 含未声明的失败分类 %q", class)
		}
		if _, duplicate := seen[class]; duplicate {
			return fmt.Errorf("presentation.upstream_http_error.failure_classes 重复 %q", class)
		}
		seen[class] = struct{}{}
	}
	return nil
}

// ProviderDisplayNames maps each connection's adapter key to the name a
// person reads for its upstream (display_name); a connection without one is
// left out.
func ProviderDisplayNames(connections []ProviderConnectionSchema) map[string]string {
	result := make(map[string]string, len(connections))
	for _, connection := range connections {
		if name := strings.TrimSpace(connection.DisplayName); name != "" && connection.AdapterKey != "" {
			result[connection.AdapterKey] = name
		}
	}
	return result
}

// ProviderDisplayName is the display name of the connection serving
// adapterKey; "" when none declares one.
func ProviderDisplayName(connections []ProviderConnectionSchema, adapterKey string) string {
	for _, connection := range connections {
		if connection.AdapterKey == adapterKey && adapterKey != "" {
			return strings.TrimSpace(connection.DisplayName)
		}
	}
	return ""
}
