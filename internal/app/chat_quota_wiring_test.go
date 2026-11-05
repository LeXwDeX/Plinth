package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #120: the chat model connections read a used-up quota through the chat
// connection's Schema error_mapping, the same declaration style and matching
// algorithm the image connections use. The gateway's real weekly-quota 429
// and OpenAI's insufficient_quota become agents.ErrQuotaExhausted on the
// default and on an extra connection; a plain 429 stays a status error.
func TestChatConnectionsReadDeclaredQuotaExhaustion(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		body  string
		class string
	}{
		{"gateway weekly quota", `{"error":{"code":"quota_exceeded","message":"Quota exceeded (weekly). Limit: $100.00, used: $101.21. Resets at 2026-11-09T00:00:00Z","quota":{"dimension":"weekly","group_name":"API","limit_usd":100,"resets_at":"2026-11-09T00:00:00Z","scope":"model_group","used_usd":101.20632699999999},"type":"quota_exceeded"}}`, "credential_quota"},
		{"openai insufficient_quota", `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`, "insufficient_balance"},
		{"plain rate limit", `{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`, ""},
		{"429 without a body", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(server.Close)
			adapter, err := buildChatAgent(Config{
				AgentMode: "live",
				RuntimeSettings: map[string]string{
					"llm_base_url": server.URL + "/v1",
					"llm_api_key":  "configured-test-key",
					"llm_model":    "configured-model",
				},
				ExtraChatConnections: []ChatConnectionConfig{{
					ID: "GPT", BaseURL: server.URL + "/v1", APIKey: "extra-key", Model: "extra-model",
				}},
			}, taskCatalog.Application())
			if err != nil {
				t.Fatal(err)
			}
			for _, connection := range []string{"", "GPT"} {
				_, err := adapter.Complete(context.Background(), agents.Request{
					Connection: connection, Model: "m",
					Messages: []agents.Message{{Role: "user", Content: "hello"}},
				})
				var status *agents.StatusError
				if !errors.As(err, &status) || status.Status != http.StatusTooManyRequests {
					t.Fatalf("connection %q: status lost: %v", connection, err)
				}
				var exhaustion *agents.QuotaExhaustion
				exhausted := errors.As(err, &exhaustion)
				if exhausted != (tc.class != "") || (exhausted && exhaustion.Class != tc.class) {
					t.Fatalf("connection %q: exhaustion = %#v, want class %q (%v)", connection, exhaustion, tc.class, err)
				}
			}
		})
	}
}
