package httpjson

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #299: a task that ends FAILED says why in its own error text, which is no HTTP
// error response; the connection's declared message matchers classify it, the
// first hit winning, and a matcher bound to an HTTP status never applies.
func TestClassifyMessageWithMapping(t *testing.T) {
	mapping := &generation.ErrorMapping{
		HTTPStatusRanges: []generation.HTTPStatusClass{{Min: 500, Max: 599, Class: string(generation.FailureUpstreamUnavailable), Retryable: true}},
		MessageMatchers: []generation.ErrorMessageMatcher{
			{ContainsAny: []string{"gateway"}, Class: string(generation.FailureTransport), HTTPStatus: &schema.StatusRange{Min: 502, Max: 504}},
			{Equals: "Out Of Quota", Class: string(generation.FailureCredentialQuota)},
			{ContainsAll: []string{"too long", "retry"}, Class: string(generation.FailureTimedOut), Retryable: true},
			{ContainsAny: []string{"显存不足", "out of memory"}, Class: string(generation.FailureUpstreamUnavailable)},
			{ContainsAny: []string{"memory"}, Class: string(generation.FailureInternal)},
		},
	}
	for name, test := range map[string]struct {
		message string
		class   generation.FailureClass
		retry   bool
		hit     bool
	}{
		"a contains-any needle":                        {"GPU 显存不足，无法处理该图片", generation.FailureUpstreamUnavailable, false, true},
		"another needle of the same matcher":           {"CUDA Out Of Memory while running", generation.FailureUpstreamUnavailable, false, true},
		"the first matcher that hits wins":             {"ran out of memory", generation.FailureUpstreamUnavailable, false, true},
		"a later matcher still applies on its own":     {"memory leak", generation.FailureInternal, false, true},
		"equals ignores case and edge space":           {"  out of quota ", generation.FailureCredentialQuota, false, true},
		"equals is not contains":                       {"out of quota today", "", false, false},
		"contains-all needs every needle":              {"took too long, please retry", generation.FailureTimedOut, true, true},
		"contains-all with one missing":                {"took too long", "", false, false},
		"a matcher bound to an HTTP status is skipped": {"bad gateway", "", false, false},
		"no matcher hits":                              {"bad image", "", false, false},
		"a blank message":                              {"   ", "", false, false},
	} {
		t.Run(name, func(t *testing.T) {
			rule, hit := ClassifyMessageWithMapping(mapping, test.message)
			if hit != test.hit || generation.FailureClass(rule.Class) != test.class || rule.Retryable != test.retry {
				t.Fatalf("ClassifyMessageWithMapping(%q) = %+v, %v; want class %q, retryable %v, hit %v",
					test.message, rule, hit, test.class, test.retry, test.hit)
			}
		})
	}
	// The status ranges of the vocabulary are for HTTP responses; a task's text is
	// not one, so only matchers speak for it.
	if rule, hit := ClassifyMessageWithMapping(&generation.ErrorMapping{HTTPStatusRanges: mapping.HTTPStatusRanges}, "anything"); hit {
		t.Fatalf("a status range classified a message: %+v", rule)
	}
	for name, empty := range map[string]*generation.ErrorMapping{"no vocabulary": nil, "an empty vocabulary": {}} {
		if rule, hit := ClassifyMessageWithMapping(empty, "GPU 显存不足"); hit {
			t.Fatalf("%s classified a message: %+v", name, rule)
		}
	}
}

// The matting connection's shipped matcher hits the words the live service used on
// 2026-11-13 for a picture its GPU could not hold, whatever size they name, and
// leaves other failures alone. The text is written out character for character.
func TestMattingMatcherHitsTheServicesOwnWords(t *testing.T) {
	mapping := connectionMapping(t, "matting")
	for _, message := range []string{
		"GPU 显存不足，无法处理该图片（图片尺寸: 2048×2048）。建议：使用更大显存的 GPU，或降低 BIREFNET_PROCESS_RES / SDMATTE_PROCESS_RES 配置。",
		"GPU 显存不足，无法处理该图片（图片尺寸: 4096×3000）。建议：使用更大显存的 GPU，或降低 BIREFNET_PROCESS_RES / SDMATTE_PROCESS_RES 配置。",
		"GPU 显存不足，无法处理该图片。建议：使用更大显存的 GPU，或降低 BIREFNET_PROCESS_RES / SDMATTE_PROCESS_RES 配置。",
	} {
		rule, hit := ClassifyMessageWithMapping(mapping, message)
		if !hit || generation.FailureClass(rule.Class) != generation.FailureUpstreamUnavailable || rule.Retryable {
			t.Errorf("%q = %+v, %v; want a capacity failure that is not retried", message, rule, hit)
		}
	}
	for _, message := range []string{"bad image", "图片格式无法解析", "GPU 驱动错误", "Fake matting failure.", "Matting 批次失败"} {
		if rule, hit := ClassifyMessageWithMapping(mapping, message); hit {
			t.Errorf("%q = %+v, want no classification", message, rule)
		}
	}
}
