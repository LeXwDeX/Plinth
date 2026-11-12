package catalog_test

import (
	"strings"
	"testing"
)

// #294: a Hyper3D submission was refused with API_NO_ACTIVE_SUBSCRIPTION (the
// account has no API subscription) while its balance panel read fine; the
// product classed it as authentication and said "upstream authentication
// failed", which sends the administrator looking for a bad key. An account
// without a usable API subscription is an account-level refusal like a missing
// balance: the connection's error mapping declares the class, and the class's
// copy says what is wrong.

func TestHyper3DSubscriptionRefusalsAreAccountFailuresNotAuthentication(t *testing.T) {
	product := shippedProduct(t)
	application := product.Application()
	var classes map[string]string
	for _, connection := range application.Runtime.ProviderConnections {
		if connection.ID != "hyper3d" {
			continue
		}
		if connection.ErrorMapping == nil {
			t.Fatal("the hyper3d connection declares no error mapping")
		}
		classes = map[string]string{}
		for code, rule := range connection.ErrorMapping.UpstreamCodeMap {
			classes[code] = rule.Class
			if rule.Retryable && strings.HasPrefix(code, "API_") && strings.Contains(code, "SUBSCRIPTION") {
				t.Errorf("%s is declared retryable: waiting does not buy a subscription", code)
			}
		}
	}
	if classes == nil {
		t.Fatal("the Catalog declares no hyper3d connection")
	}
	for code, want := range map[string]string{
		// The two codes about the subscription: not an authentication failure.
		"API_NO_ACTIVE_SUBSCRIPTION":    "insufficient_balance",
		"API_SUBSCRIPTION_PLAN_TOO_LOW": "insufficient_balance",
		// The neighbours keep their classes: funds are funds, a refused permission
		// is the key's, a full pool is a rate limit.
		"API_INSUFFICIENT_FUNDS":        "insufficient_balance",
		"PERMISSION_DENIED":             "authentication",
		"API_PARALLELISM_LIMIT_REACHED": "rate_limited",
	} {
		if classes[code] != want {
			t.Errorf("%s is classed %q, want %q", code, classes[code], want)
		}
	}

	// The copy of the class says the API subscription may be what is missing, in
	// both languages and on both surfaces (the task's failure and the Agent's
	// report); it does not name the parameter or the upstream code.
	bundle := application.Localization.Bundles["en"]
	english := bundle.Messages
	for name, texts := range map[string]struct{ zh, en string }{
		"failure.insufficient_balance": {
			application.Content.Messages["failure.insufficient_balance"], english["failure.insufficient_balance"]},
		"the Agent's generation failure": {
			application.Chat.Presentation.Messages.GenerationFailedByClass["insufficient_balance"],
			bundle.Fields["chat.presentation.messages.generation_failed_by_class.insufficient_balance"]},
	} {
		for _, want := range []string{"API 订阅", "未开通", "余额不足"} {
			if !strings.Contains(texts.zh, want) {
				t.Errorf("%s: the default text %q does not say %q", name, texts.zh, want)
			}
		}
		for _, want := range []string{"API subscription", "not activated", "balance"} {
			if !strings.Contains(texts.en, want) {
				t.Errorf("%s: the English text %q does not say %q", name, texts.en, want)
			}
		}
		for _, internal := range []string{"API_NO_ACTIVE_SUBSCRIPTION", "authentication", "身份验证"} {
			if strings.Contains(texts.zh, internal) || strings.Contains(texts.en, internal) {
				t.Errorf("%s names %q, the problem is not the key", name, internal)
			}
		}
	}
}
