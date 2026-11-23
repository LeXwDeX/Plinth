package catalog_test

import (
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// OpenAI's error-code page (https://developers.openai.com/api/docs/guides/error-codes,
// read 2026-11-23) lists 429 codes that mean the account or one of its spend
// limits is used up: credit_balance_exhausted, organization_spend_limit_exceeded,
// project_spend_limit_exceeded and organization_usage_limit_exceeded. It also
// says to read error.code, because error.type may still be the broader
// insufficient_quota. Read as a plain rate limit, the Task waits and resends a
// request that no amount of waiting brings back, and the person is told to try
// again later instead of to top up or raise the limit.
func TestOpenAIConnectionsReadSpendLimitCodesAsAnExhaustedBalance(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{
		"credit_balance_exhausted",
		"organization_spend_limit_exceeded",
		"project_spend_limit_exceeded",
		"organization_usage_limit_exceeded",
	}
	seen := map[string]bool{}
	for _, connection := range loaded.Application().Runtime.ProviderConnections {
		if connection.ID != "openai-images" && connection.ID != "chat-llm" {
			continue
		}
		seen[connection.ID] = true
		if connection.ErrorMapping == nil {
			t.Fatalf("%s declares no error_mapping", connection.ID)
		}
		for _, code := range codes {
			rule, ok := connection.ErrorMapping.UpstreamCodeMap[code]
			if !ok || rule.Class != string(schema.FailureInsufficientBalance) {
				t.Errorf("%s: %s = %+v, want insufficient_balance", connection.ID, code, rule)
			}
			if rule.Retryable {
				t.Errorf("%s: %s must not be retryable", connection.ID, code)
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("connections declaring OpenAI codes = %v, want both image and chat", seen)
	}
}

// The image connections answer 429 to a request the upstream did not accept,
// which the client reports as a retryable rate limit; the synchronous image
// Offerings used to declare max_attempts 1 and so failed the step on the first
// one (#487). A 5xx still ends the attempt: a synchronous create that was not
// idempotent may have been accepted and billed, so retrying it could charge
// twice. Retrying 429 is safe in exactly the case the class describes: nothing
// was accepted.
func TestSyncImageOfferingsRetryARefusedRequestOnce(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []schema.OfferingID{
		"openai/gpt-image-2/text-to-image",
		"openai/gpt-image-2.5-sunburst/text-to-image",
		"openai/gpt-image-2/image-to-image",
		"openai/gpt-image-2.5-sunburst/image-to-image",
		"gemini/gemini-3-pro-image/text-to-image",
		"gemini/gemini-3-pro-image/image-to-image",
		"jimeng/seedream-4-5/text-to-image",
		"jimeng/seedream-4-5/image-to-image",
	} {
		offering, ok := loaded.Get(id)
		if !ok {
			t.Fatalf("offering %s is not declared", id)
		}
		policy := offering.Retry
		if policy.MaxAttempts < 2 {
			t.Errorf("%s: max_attempts = %d, want at least 2", id, policy.MaxAttempts)
		}
		if !slices.Contains(policy.Retryable, schema.FailureRateLimited) {
			t.Errorf("%s: retryable = %v, want rate_limited", id, policy.Retryable)
		}
		for _, class := range policy.Retryable {
			if class == schema.FailureSubmissionUnknown || class == schema.FailureUpstreamUnavailable {
				t.Errorf("%s: %s must not be retried on a synchronous create", id, class)
			}
		}
		if !policy.Allows(schema.FailureRateLimited, 2) {
			t.Errorf("%s: a rate limit must be retryable once", id)
		}
		if policy.Allows(schema.FailureUpstreamUnavailable, 2) {
			t.Errorf("%s: a 5xx must not be retried", id)
		}
	}
}
