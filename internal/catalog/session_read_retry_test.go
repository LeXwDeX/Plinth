package catalog

import (
	"encoding/json"
	"testing"
)

// #314: the browser's bounded re-read of a session's chat model and thinking level after a failed read is
// declared by the Schema (runtime.frontend.session_read_retry) and reaches the page through the public
// projection; the picker's failed state is worded by Schema copy in both locales.
func TestSessionReadRetryIsDeclaredValidatedAndProjected(t *testing.T) {
	t.Parallel()
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	retry := application.Runtime.Frontend.SessionReadRetry
	if retry.MaxRetries <= 0 || retry.InitialDelayMilliseconds <= 0 ||
		retry.MaxBackoffMilliseconds < retry.InitialDelayMilliseconds ||
		retry.MaxRetryAfterMilliseconds < retry.MaxBackoffMilliseconds {
		t.Fatalf("default session_read_retry = %+v", retry)
	}

	for name, bad := range map[string]SessionReadRetrySchema{
		"zero":                {},
		"no retries":          {MaxRetries: 0, InitialDelayMilliseconds: 1000, MaxBackoffMilliseconds: 8000, MaxRetryAfterMilliseconds: 120000},
		"unbounded retries":   {MaxRetries: maxSessionReadRetries + 1, InitialDelayMilliseconds: 1000, MaxBackoffMilliseconds: 8000, MaxRetryAfterMilliseconds: 120000},
		"delay too short":     {MaxRetries: 4, InitialDelayMilliseconds: 50, MaxBackoffMilliseconds: 8000, MaxRetryAfterMilliseconds: 120000},
		"backoff below delay": {MaxRetries: 4, InitialDelayMilliseconds: 1000, MaxBackoffMilliseconds: 500, MaxRetryAfterMilliseconds: 120000},
		"retry-after low":     {MaxRetries: 4, InitialDelayMilliseconds: 1000, MaxBackoffMilliseconds: 8000, MaxRetryAfterMilliseconds: 2000},
	} {
		broken := application
		broken.Runtime.Frontend.SessionReadRetry = bad
		if err := ValidateApplicationSchema(broken); err == nil {
			t.Fatalf("%s: invalid session_read_retry accepted", name)
		}
	}

	public, err := json.Marshal(application.Public())
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Runtime struct {
			Frontend struct {
				SessionReadRetry SessionReadRetrySchema `json:"session_read_retry"`
			} `json:"frontend"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(public, &document); err != nil {
		t.Fatal(err)
	}
	if document.Runtime.Frontend.SessionReadRetry != retry {
		t.Fatalf("public session_read_retry = %+v, want %+v", document.Runtime.Frontend.SessionReadRetry, retry)
	}
}

// The failed state of the combined chat model and thinking picker is worded by Schema copy, so the page
// never falls back to an empty string; every key has an English counterpart (the bundle check enforces it
// on load, this pins the keys the picker reads).
func TestChatModelPickerFailedStateCopyIsDeclaredInBothLocales(t *testing.T) {
	t.Parallel()
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	english := application.Localization.Bundles["en"].Messages
	for _, key := range []string{
		"ui.studio.chat_model.load_failed",
		"ui.studio.chat_model.load_failed_short",
		"ui.studio.chat_model.retry",
		"ui.studio.reasoning.load_failed",
		"ui.unavailable.reason.picker.chat_model_load_failed",
		"ui.unavailable.reason.picker.reasoning_load_failed",
	} {
		if application.Content.Messages[key] == "" {
			t.Errorf("content.messages[%q] is missing", key)
		}
		if english[key] == "" {
			t.Errorf("bundles.en.messages[%q] is missing", key)
		}
	}
}
