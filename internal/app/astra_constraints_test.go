package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// #401 on the production wiring: the chat agent the application builds from
// the shipped Catalog talks to the fake upstream, whose gpt-astra refuses what
// the real gateway refused on 2026-11-20 (a temperature other than 1, function
// tools with a thinking level, and, from the OpenAI documents, the level
// none). The shipped gpt-astra profile omits the temperature on every request
// path, so each request without tools is served, and the Agent's own request
// shape is refused with the gateway's answer, which the adapter tells apart.
func TestShippedAstraProfileServesEveryRequestWithoutToolsOnTheConstrainedFake(t *testing.T) {
	fake, err := registry.New(fakeupstream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fakeURL, _ := url.Parse(server.URL)
	product, err := loadCatalogForShape("", upstreamShape{origin: fakeURL})
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	settings := map[string]string{
		"llm_base_url": server.URL + "/openai/v1", "llm_api_key": fakeUpstreamKeyPrefix + "test-key", "llm_model": "gpt-astra",
	}
	adapter, err := buildChatAgentWithTransport(Config{AgentMode: "live", RuntimeSettings: settings}, application, fakeShapeHTTPTransport(fakeURL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user := []agents.Message{{Role: application.Chat.Roles.User, Content: "hello"}}
	temperature := application.Chat.Agent.Temperature
	if temperature != 0 || application.Chat.API.PromptEnhance.Temperature != 0 {
		t.Fatalf("the Schema temperatures are %v and %v; the regression needs the 0 the gateway refused", temperature, application.Chat.API.PromptEnhance.Temperature)
	}

	// The requests of Plinth that carry no tools, each as its code builds it:
	// the first-turn title (no level), the compaction summary (the session's
	// level), the prompt enhancer and an Agent turn's tool-less summary round.
	for name, request := range map[string]agents.Request{
		"title":      {Model: "gpt-astra", Temperature: temperature, Messages: user},
		"summary":    {Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "high", Purpose: agents.PurposeSummary, Messages: user},
		"enhancer":   {Model: "gpt-astra", Temperature: application.Chat.API.PromptEnhance.Temperature, Messages: user},
		"last round": {Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "xhigh", Messages: user},
	} {
		reply, err := adapter.Complete(ctx, request)
		if err != nil || reply.Content != "Fake upstream reply." {
			t.Errorf("%s: %+v %v", name, reply, err)
		}
		if _, err := adapter.Stream(ctx, request, nil); err != nil {
			t.Errorf("%s streamed: %v", name, err)
		}
	}

	// The administrator's connection test: a picture, the cheapest level and
	// the Schema temperature, answered by the same model.
	probe := connectionProber(adapter, application, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// The deployment's own connection (the one this wiring has) serves gpt-astra.
	result := probe(ctx, llmsettings.Connection{ID: "LLM", Model: "gpt-astra"})
	if !result.Reachable || result.Outcome != llmsettings.OutcomeOK {
		t.Fatalf("probe = %+v", result)
	}

	// The Agent's own request shape (tools and the profile's level) is what
	// the gateway refused: the fake answers the observed 400, the adapter
	// names the refused field, and the upstream's wording stays in the error.
	agentTurn := agents.Request{Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "high", Messages: user,
		Tools: []agents.Tool{{Name: "lookup_note", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	_, err = adapter.Stream(ctx, agentTurn, nil)
	var status *agents.StatusError
	param, rejected := agents.RequestRejectedParam(err)
	if err == nil || !rejected || param != "reasoning_effort" || !errors.As(err, &status) || status.Status != http.StatusBadRequest ||
		!strings.Contains(err.Error(), "Function tools with reasoning_effort are not supported for gpt-6.2-astra") {
		t.Fatalf("agent turn: param=%q rejected=%t err=%v", param, rejected, err)
	}

	// Another model of the same wiring is not touched: the same shape, the
	// Schema temperature included, is served.
	other := agentTurn
	other.Model = "deepseek"
	if _, err := adapter.Stream(ctx, other, nil); err != nil {
		t.Fatalf("deepseek agent turn: %v", err)
	}
}

// #401: a connection whose model cannot run the function tools of an Agent turn
// on chat completions is never offered to sessions; the administration read
// says so, the connection test still reaches it, and a session that picked it
// cannot.
func TestAgentIncapableConnectionIsNotOfferedToSessions(t *testing.T) {
	root := t.TempDir()
	application, err := New(Config{
		Environment: "development", Address: "127.0.0.1:0",
		DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock", AgentMode: "mock",
		RuntimeSettings: map[string]string{"llm_base_url": "https://gateway.test/v1", "llm_api_key": "sk-default", "llm_model": "deepseek"},
		ExtraChatConnections: []ChatConnectionConfig{
			{ID: "GPT", BaseURL: "https://gateway.test/v1", APIKey: "sk-gpt", Model: "gpt-astra"},
		},
		AuthMode: "development", DevelopmentUser: "owner-a", DevelopmentRoles: []string{"admin"},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	handler := application.server.Handler
	call := func(method, target, body string, want int) map[string]any {
		t.Helper()
		response := appChatRequest(handler, method, target, body)
		if response.Code != want {
			t.Fatalf("%s %s: %d %s", method, target, response.Code, response.Body.String())
		}
		decoded := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		return decoded
	}
	routes := application.schema.LLMSettings.Routes
	managed := call(http.MethodGet, routes.Admin, "", http.StatusOK)
	tools := map[string]any{}
	for _, raw := range managed["connections"].([]any) {
		view := raw.(map[string]any)
		tools[view["id"].(string)] = view["agent_tools"]
	}
	if tools["LLM"] != true || tools["GPT"] != false || len(tools) != 2 {
		t.Fatalf("agent_tools = %v", tools)
	}

	created := call(http.MethodPost, "/api/chat/sessions", `{"title":"models"}`, http.StatusCreated)
	modelRoute := strings.ReplaceAll(application.schema.Runtime.HTTP.Routes.ChatSessionModel, "{sessionID}", created["id"].(string))
	view := call(http.MethodGet, modelRoute, "", http.StatusOK)
	if view["chatModel"] != "LLM" || len(view["chatModels"].([]any)) != 1 {
		t.Fatalf("session model = %v", view)
	}
	call(http.MethodPut, modelRoute, `{"chatModel":"GPT"}`, http.StatusBadRequest)
	// The connection test is not blocked: it sends no tools.
	call(http.MethodPost, routes.Test, `{"connection_id":"GPT"}`, http.StatusOK)
}
