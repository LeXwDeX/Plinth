package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const gatewayWeeklyQuota = `{"error":{"code":"quota_exceeded","message":"Quota exceeded (weekly). Limit: $20.00, used: $20.24. Resets at 2026-11-30T00:00:00Z","type":"quota_exceeded"}}`

// The 503 bodies llms-proxy answers with itself once it muted a target whose
// upstream refused with a used-up quota (#640, formal-test log, read only).
const (
	gatewayMuted      = `{"error":{"message":"all targets muted until 2026-11-26T10:15:00Z","type":"upstream_unavailable"}}`
	gatewayRecovering = `{"error":{"message":"all targets recovering (half-open probe in flight)","type":"upstream_unavailable"}}`
)

// #640 end to end: the default Catalog and the real chat adapters. After a
// connection's 429 quota_exceeded, the gateway's "all targets muted" 503 is the
// same used-up quota: a streamed round is sent once, not three times, and
// carries ErrQuotaExhausted with the class of the 429. Another connection that
// saw no used-up quota keeps reading the same 503 as transient.
func TestChatGatewayMuteAfterUsedUpQuotaIsTheQuota(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	if application.Chat.Agent.QuotaMute == nil {
		t.Fatal("default Catalog declares no chat.agent.quota_mute")
	}
	var mu sync.Mutex
	answers := map[string][]string{}
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		key := request.Header.Get("Authorization")
		mu.Lock()
		requests[key]++
		body := gatewayMuted
		if queue := answers[key]; len(queue) > 0 {
			body, answers[key] = queue[0], queue[1:]
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body, "quota_exceeded") {
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	adapter, err := buildChatAgent(Config{
		AgentMode: "live",
		RuntimeSettings: map[string]string{
			"llm_base_url": server.URL + "/v1",
			"llm_api_key":  "default-key",
			"llm_model":    "gpt-astra",
		},
		ExtraChatConnections: []ChatConnectionConfig{{
			ID: "QWEN", BaseURL: server.URL + "/v1", APIKey: "qwen-key", Model: "qwen-max",
		}},
	}, application)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	answers["Bearer default-key"] = []string{gatewayWeeklyQuota, gatewayMuted, gatewayRecovering}
	mu.Unlock()
	request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{{Role: "user", Content: "hello"}}}
	if _, err := adapter.Complete(context.Background(), request); !errors.Is(err, agents.ErrQuotaExhausted) {
		t.Fatalf("429 quota_exceeded = %v, want ErrQuotaExhausted", err)
	}
	for _, answer := range []string{"muted", "recovering"} {
		_, err := adapter.Stream(context.Background(), request, nil)
		var exhaustion *agents.QuotaExhaustion
		if !errors.As(err, &exhaustion) || exhaustion.Class != "credential_quota" || errors.Is(err, agents.ErrTransient) {
			t.Fatalf("%s 503 after the quota = %v, want credential_quota, not transient", answer, err)
		}
	}
	mu.Lock()
	sent := requests["Bearer default-key"]
	mu.Unlock()
	if sent != 3 {
		t.Fatalf("default connection requests = %d, want 3: the mute answers are not retried", sent)
	}

	// The other connection saw no used-up quota: its mute answer stays transient.
	_, err = adapter.Complete(context.Background(), agents.Request{
		Connection: "QWEN", Model: "qwen-max", Messages: []agents.Message{{Role: "user", Content: "hello"}},
	})
	if errors.Is(err, agents.ErrQuotaExhausted) || !errors.Is(err, agents.ErrTransient) {
		t.Fatalf("QWEN 503 without a quota = %v, want transient", err)
	}
}

// The memory holds for the declared window only, and a 503 that names no
// declared marker or another status is never the quota.
func TestChatQuotaMemoryWindowAndMarkers(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	read := func(status int, body []byte) (string, bool) {
		if status == http.StatusTooManyRequests && strings.Contains(string(body), "quota_exceeded") {
			return "credential_quota", true
		}
		return "", false
	}
	mute := &catalog.ChatAgentQuotaMuteSchema{WindowSeconds: 600, HTTPStatus: 503, ContainsAny: []string{"All Targets Muted"}}
	memory := newChatQuotaMemory(read, mute, func() time.Time { return now })
	if _, exhausted := memory.exhausted(503, []byte(gatewayMuted)); exhausted {
		t.Fatal("mute answer without an earlier used-up quota read as the quota")
	}
	if class, exhausted := memory.exhausted(429, []byte(gatewayWeeklyQuota)); !exhausted || class != "credential_quota" {
		t.Fatalf("429 = %q %v", class, exhausted)
	}
	now = now.Add(5 * time.Minute)
	if class, exhausted := memory.exhausted(503, []byte(gatewayMuted)); !exhausted || class != "credential_quota" {
		t.Fatalf("mute within the window = %q %v", class, exhausted)
	}
	if _, exhausted := memory.exhausted(503, []byte(`{"error":{"message":"upstream overloaded"}}`)); exhausted {
		t.Fatal("503 without a declared marker read as the quota")
	}
	if _, exhausted := memory.exhausted(502, []byte(gatewayMuted)); exhausted {
		t.Fatal("undeclared status read as the quota")
	}
	now = now.Add(6 * time.Minute)
	if _, exhausted := memory.exhausted(503, []byte(gatewayMuted)); exhausted {
		t.Fatal("mute answer after the window read as the quota")
	}
	// Without quota_mute the memory reads as the declaration does.
	plain := newChatQuotaMemory(read, nil, nil)
	_, _ = plain.exhausted(429, []byte(gatewayWeeklyQuota))
	if _, exhausted := plain.exhausted(503, []byte(gatewayMuted)); exhausted {
		t.Fatal("mute read without quota_mute")
	}
	if newChatQuotaMemory(nil, mute, nil).reader() != nil {
		t.Fatal("a connection without error_mapping reads refusals")
	}
}
