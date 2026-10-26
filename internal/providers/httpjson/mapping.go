package httpjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ClassifyWithMapping 返回按连接级 error_mapping 声明驱动的通用分类器。
// 未命中任何声明规则时返回 nil，回退到 httpjson 默认 HTTP 状态码分类；
// 声明表本身不含任何 provider 身份分支。
func ClassifyWithMapping(mapping *generation.ErrorMapping) HTTPErrorClassifier {
	if mapping == nil {
		return nil
	}
	return func(response *http.Response, payload []byte) error {
		rule, ok := matchErrorMapping(mapping, response.StatusCode, payload)
		if !ok {
			return nil
		}
		header := mapping.RetryAfterHeader
		if strings.TrimSpace(header) == "" {
			header = "Retry-After"
		}
		failure := &generation.Failure{
			Class:      generation.FailureClass(rule.Class),
			Retryable:  rule.Retryable,
			SafeDetail: publicHTTPErrorMessage(response.StatusCode),
			RetryAfter: retryAfterWhen(response.Header.Get(header), rule.Retryable),
		}
		withUpstreamEvidence(failure, response.StatusCode, payload)
		return &generation.ProviderError{Failure: failure}
	}
}

func retryAfterWhen(value string, retryable bool) time.Duration {
	if !retryable {
		return 0
	}
	return retryAfter(value)
}

func matchErrorMapping(
	mapping *generation.ErrorMapping,
	status int,
	payload []byte,
) (generation.ErrorClassRule, bool) {
	// An out-of-balance refusal arrives under whatever status the provider
	// picks (402, 403, 429), and a broad status range such as 403 -> invalid
	// input would otherwise hide it. Evidence the connection declares as
	// "insufficient balance" therefore outranks the status ranges.
	if rule, ok := matchBalanceEvidence(mapping, status, payload); ok {
		return rule, true
	}
	for _, rangeRule := range mapping.HTTPStatusRanges {
		if status >= rangeRule.Min && status <= rangeRule.Max {
			return generation.ErrorClassRule{
				Class:     rangeRule.Class,
				Retryable: rangeRule.Retryable,
			}, true
		}
	}
	if rule, ok := mapping.UpstreamStatusMap[strconv.Itoa(status)]; ok {
		return rule, true
	}
	_, code := declaredErrorEvidence(mapping, payload)
	if code != "" {
		if rule, ok := mapping.UpstreamCodeMap[code]; ok {
			return rule, true
		}
	}
	if message := declaredErrorMessage(mapping, payload, status); message != "" {
		for _, matcher := range mapping.MessageMatchers {
			if messageMatcherHits(matcher, message) {
				return generation.ErrorClassRule{
					Class:     matcher.Class,
					Retryable: matcher.Retryable,
				}, true
			}
		}
	}
	return generation.ErrorClassRule{}, false
}

func matchBalanceEvidence(
	mapping *generation.ErrorMapping,
	status int,
	payload []byte,
) (generation.ErrorClassRule, bool) {
	balance := string(generation.FailureInsufficientBalance)
	_, code := declaredErrorEvidence(mapping, payload)
	if code != "" {
		if rule, ok := mapping.UpstreamCodeMap[code]; ok && rule.Class == balance {
			return rule, true
		}
	}
	if message := declaredErrorMessage(mapping, payload, status); message != "" {
		for _, matcher := range mapping.MessageMatchers {
			if matcher.Class == balance && messageMatcherHits(matcher, message) {
				return generation.ErrorClassRule{Class: matcher.Class, Retryable: matcher.Retryable}, true
			}
		}
	}
	return generation.ErrorClassRule{}, false
}

func messageMatcherHits(matcher generation.ErrorMessageMatcher, message string) bool {
	if matcher.Equals != "" {
		return strings.EqualFold(strings.TrimSpace(message), matcher.Equals)
	}
	lowered := strings.ToLower(message)
	for _, needle := range matcher.ContainsAll {
		if !strings.Contains(lowered, strings.ToLower(needle)) {
			return false
		}
	}
	if len(matcher.ContainsAny) > 0 {
		for _, needle := range matcher.ContainsAny {
			if strings.Contains(lowered, strings.ToLower(needle)) {
				return true
			}
		}
		return false
	}
	return len(matcher.ContainsAll) > 0
}

// declaredErrorEvidence 按声明路径（缺省用通用信封）提取错误码与消息。
func declaredErrorEvidence(
	mapping *generation.ErrorMapping,
	payload []byte,
) (message string, code string) {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return "", ""
	}
	code = stringAtJSONPath(document, mapping.ErrorFields.CodePath)
	if code == "" {
		code = stringAtJSONPath(document, mapping.ErrorFields.TypePath)
	}
	message = stringAtJSONPath(document, mapping.ErrorFields.MessagePath)
	if code == "" && message == "" && mapping.ErrorFields == (generation.ErrorFieldPaths{}) {
		genericMessage, genericCode := errorEvidence(payload)
		return genericMessage, genericCode
	}
	return message, code
}

func declaredErrorMessage(
	mapping *generation.ErrorMapping,
	payload []byte,
	status int,
) string {
	message, _ := declaredErrorEvidence(mapping, payload)
	if message == "" {
		message = safeMessage(payload)
	}
	if message == "" {
		message = fmt.Sprintf("Provider 返回 HTTP %d", status)
	}
	return message
}

// stringAtJSONPath 按点分段路径取字符串（数字编码为十进制字符串）。
func stringAtJSONPath(document map[string]any, path string) string {
	segments := strings.Split(strings.TrimSpace(path), ".")
	if len(segments) == 0 || segments[0] == "" {
		return ""
	}
	var current any = document
	for index, segment := range segments {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		value, exists := object[segment]
		if !exists {
			return ""
		}
		current = value
		if index == len(segments)-1 {
			return scalarString(current)
		}
	}
	return ""
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

// ClassifyPayloadWithMapping 按连接级 error_mapping 分类任务观察/业务
// 信封中的错误证据：分类词表键取 type_path 值（缺省 code_path 值），
// UpstreamCode 记录 code_path 值。未命中声明码表时返回 fallback 分类；
// 这是 meshy task_error / tripo 业务 code 共用的通用算法。
func ClassifyPayloadWithMapping(
	mapping *generation.ErrorMapping,
	payload []byte,
	fallbackClass generation.FailureClass,
	fallbackDetail string,
) *generation.Failure {
	classKey := ""
	upstreamCode := ""
	if mapping != nil {
		var document map[string]any
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		if err := decoder.Decode(&document); err == nil {
			classKey = stringAtJSONPath(document, mapping.ErrorFields.TypePath)
			upstreamCode = stringAtJSONPath(document, mapping.ErrorFields.CodePath)
		}
	}
	if classKey == "" {
		classKey = upstreamCode
	}
	failure := &generation.Failure{
		Class:      fallbackClass,
		Retryable:  false,
		SafeDetail: fallbackDetail,
	}
	if classKey != "" {
		if rule, ok := mapping.UpstreamCodeMap[classKey]; ok {
			failure.Class = generation.FailureClass(rule.Class)
			failure.Retryable = rule.Retryable
		}
	}
	if upstreamCode != "" {
		failure.UpstreamCode = upstreamCode
	}
	return failure
}
