package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// #640 against the fake gateway (openaiv1.GatewayMuteMarker) rather than a
// scripted handler: the default Catalog and the real chat adapters read the
// fake's 429 quota_exceeded as the used-up quota and the "all targets muted"
// and "all targets recovering" 503s that follow as the same quota, sent once
// each and not retried. The fake and the Catalog's chat.agent.quota_mute
// markers check each other: a fake answer the markers miss fails here.
func TestChatGatewayMuteOfTheFakeIsTheQuota(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	fake, err := fakeupstream.New(fakeupstream.Config{}, openaiv1.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	adapter, err := buildChatAgent(Config{
		AgentMode: "live",
		RuntimeSettings: map[string]string{
			"llm_base_url": server.URL + openaiv1.Prefix,
			"llm_api_key":  "default-key",
			"llm_model":    "gpt-astra",
		},
	}, taskCatalog.Application())
	if err != nil {
		t.Fatal(err)
	}
	request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{{Role: "user", Content: "hello " + openaiv1.GatewayMuteMarker}}}
	if _, err := adapter.Stream(context.Background(), request, nil); !errors.Is(err, agents.ErrQuotaExhausted) {
		t.Fatalf("429 quota_exceeded = %v, want ErrQuotaExhausted", err)
	}
	for _, answer := range []string{"muted 1 minute", "recovering", "muted 2 minutes"} {
		_, err := adapter.Stream(context.Background(), request, nil)
		var exhaustion *agents.QuotaExhaustion
		if !errors.As(err, &exhaustion) || exhaustion.Class != "credential_quota" || errors.Is(err, agents.ErrTransient) {
			t.Fatalf("%s 503 after the quota = %v, want credential_quota, not transient", answer, err)
		}
	}
	if sent := len(fake.Env().ChatRequests()); sent != 4 {
		t.Fatalf("requests = %d, want 4: no answer is retried", sent)
	}
}
