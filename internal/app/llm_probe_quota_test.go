package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
)

// #120: the administrator's "test connection" reads a used-up quota through
// the chat connection's Schema error_mapping (the real adapter, the default
// Catalog): the gateway's weekly-quota 429 and OpenAI's insufficient_quota are
// quota_exhausted, while a plain 429 stays rate_limited.
func TestConnectionProbeTellsAUsedUpQuotaFromARateLimit(t *testing.T) {
	schema := llmConnectionSchema(t)
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"gateway weekly quota", `{"error":{"code":"quota_exceeded","message":"Quota exceeded (weekly). Limit: $100.00, used: $101.21. Resets at 2026-11-09T00:00:00Z","quota":{"dimension":"weekly","group_name":"API","limit_usd":100,"resets_at":"2026-11-09T00:00:00Z","scope":"model_group","used_usd":101.20632699999999},"type":"quota_exceeded"}}`, llmsettings.OutcomeQuotaExhausted},
		{"openai insufficient_quota", `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`, llmsettings.OutcomeQuotaExhausted},
		{"plain rate limit", `{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`, llmsettings.OutcomeRateLimited},
		{"429 without a body", ``, llmsettings.OutcomeRateLimited},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(server.Close)
			adapter, err := buildChatAgent(Config{
				AgentMode: "live",
				RuntimeSettings: map[string]string{
					"llm_base_url": server.URL + "/v1",
					"llm_api_key":  "configured-test-key",
					"llm_model":    "configured-model",
				},
			}, schema)
			if err != nil {
				t.Fatal(err)
			}
			got := connectionProber(adapter, schema, nil)(context.Background(), llmsettings.Connection{ID: "LLM", Model: "configured-model"})
			want := llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: test.want}
			if got != want || requests != 1 {
				t.Fatalf("got %+v after %d requests, want %+v after 1", got, requests, want)
			}
		})
	}
}

// The settings page shows llm_settings.ui.outcome_<outcome>: every outcome a
// test can report has Schema copy in the default locale and in English.
func TestEveryConnectionTestOutcomeHasSchemaCopy(t *testing.T) {
	schema := llmConnectionSchema(t)
	texts, err := catalog.NewTextLocalizer(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{
		llmsettings.OutcomeOK, llmsettings.OutcomeNotConfigured, llmsettings.OutcomeUnauthorized,
		llmsettings.OutcomeNotFound, llmsettings.OutcomeRateLimited, llmsettings.OutcomeQuotaExhausted,
		llmsettings.OutcomeTimeout, llmsettings.OutcomeUnreachable, llmsettings.OutcomeUpstreamError,
	} {
		text := schema.LLMSettings.UI["outcome_"+outcome]
		if text == "" {
			t.Fatalf("no llm_settings.ui.outcome_%s", outcome)
		}
		if english := texts.Text("en", text); english == "" || english == text {
			t.Fatalf("outcome_%s has no English copy", outcome)
		}
	}
	if schema.LLMSettings.UI["outcome_quota_exhausted"] == schema.LLMSettings.UI["outcome_rate_limited"] {
		t.Fatal("a used-up quota reads like a rate limit")
	}
}
