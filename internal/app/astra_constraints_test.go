package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// #401 on the production wiring: the chat agent the application builds from
// the shipped Catalog talks to the fake upstream, whose gpt-astra refuses what
// the real gateway refused on 2026-11-20 (a temperature, function tools with a
// thinking level on Chat Completions, and the levels none and minimal, which
// the later probes showed it refuses on Responses). The shipped gpt-astra
// profile omits the temperature on every request
// path and is served on the Responses API (step 2), so every kind of request of
// Plinth, the Agent turn with its tools and level included, is served; the
// same Agent request shape on Chat Completions is still refused with the
// gateway's answer, which the adapter tells apart.
//
// Before step 2 this test asserted that the Agent turn of gpt-astra was
// refused; it now asserts the turn is served on /responses, and keeps the
// refusal as what a profile that stayed on Chat Completions would get.
func TestShippedAstraProfileServesEveryRequestOnTheResponsesWire(t *testing.T) {
	gateway, log := loggedFake(t)
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	key := fakeUpstreamKeyPrefix + "test-key"
	settings := map[string]string{"llm_base_url": gateway, "llm_api_key": key, "llm_model": "gpt-astra"}
	adapter, err := buildChatAgent(Config{AgentMode: "live", RuntimeSettings: settings}, application)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user := []agents.Message{{Role: application.Chat.Roles.User, Content: "hello"}}
	temperature := application.Chat.Agent.Temperature
	if temperature != 0 || application.Chat.API.PromptEnhance.Temperature != 0 {
		t.Fatalf("the Schema temperatures are %v and %v; the regression needs the 0 the gateway refused", temperature, application.Chat.API.PromptEnhance.Temperature)
	}
	tools := []agents.Tool{{Name: "lookup_note", Parameters: json.RawMessage(`{"type":"object"}`)}}
	served := func(name string) {
		t.Helper()
		sent := log.last()
		if sent.path != openaiv1.Prefix+"/responses" || strings.Contains(sent.body, "temperature") {
			t.Errorf("%s: %s %s", name, sent.path, sent.body)
		}
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
		if err != nil || reply.Content != openaiv1.FixedReply {
			t.Errorf("%s: %+v %v", name, reply, err)
		}
		served(name)
		if _, err := adapter.Stream(ctx, request, nil); err != nil {
			t.Errorf("%s streamed: %v", name, err)
		}
		served(name + " streamed")
	}

	// The administrator's connection test: a picture, the cheapest level and
	// the Schema temperature, answered by the same model.
	probe := connectionProber(adapter, application, slog.New(slog.NewTextHandler(io.Discard, nil)))
	result := probe(ctx, llmsettings.Connection{ID: "LLM", Model: "gpt-astra"})
	if !result.Reachable || result.Outcome != llmsettings.OutcomeOK {
		t.Fatalf("probe = %+v", result)
	}
	served("connection test")
	if !strings.Contains(log.last().body, `"input_image"`) || !strings.Contains(log.last().body, `"reasoning":{"effort":"low"}`) {
		t.Errorf("the connection test did not send a picture and the cheapest level: %s", log.last().body)
	}

	// The Agent's own request shape (tools, the profile's level, temperature 0
	// in the Schema): on Chat Completions the gateway refused it. Served on
	// Responses, it runs the two rounds of a tool loop.
	agentTurn := agents.Request{Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "high", Tools: tools, Messages: []agents.Message{
		{Role: application.Chat.Roles.User, Content: "note " + openaiv1.ToolMarkerFor("lookup_note", `{"topic":"cube"}`)}}}
	first, err := adapter.Stream(ctx, agentTurn, nil)
	if err != nil || len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "lookup_note" {
		t.Fatalf("agent turn: %+v %v", first, err)
	}
	served("agent turn")
	agentTurn.Messages = append(agentTurn.Messages,
		agents.Message{Role: application.Chat.Roles.Assistant, ToolCalls: first.ToolCalls, Reasoning: reasoningItemsOf(first)},
		agents.Message{Role: application.Chat.Roles.Tool, ToolCallID: first.ToolCalls[0].ID, Content: `{"note":"a cube"}`})
	second, err := adapter.Stream(ctx, agentTurn, nil)
	if err != nil || second.Content != openaiv1.FixedReply {
		t.Fatalf("agent turn round 2: %+v %v", second, err)
	}
	served("agent turn round 2")

	// Another model of the same wiring is on Chat Completions and is served as
	// before: the same shape, the Schema temperature included.
	other := agentTurn
	other.Model = "deepseek"
	if _, err := adapter.Stream(ctx, other, nil); err != nil {
		t.Fatalf("deepseek agent turn: %v", err)
	}
	if sent := log.last(); sent.path != openaiv1.Prefix+"/chat/completions" || !strings.Contains(sent.body, `"temperature":0`) {
		t.Errorf("deepseek: %s %s", sent.path, sent.body)
	}

	// A profile that stays on Chat Completions with tools and a level is what
	// the gateway refused: the fake answers the observed 400, the adapter names
	// the refused field, and the upstream's wording stays in the error.
	chatOnly := application
	chatOnly.Chat.Agent.Thinking = &catalog.ChatThinkingSchema{Enabled: true, ModelProfiles: append([]catalog.ChatThinkingModelProfile(nil), application.Chat.Agent.Thinking.ModelProfiles...)}
	for index, profile := range chatOnly.Chat.Agent.Thinking.ModelProfiles {
		if profile.ModelPrefixes[0] == "gpt-astra" {
			profile.WireAPI = ""
			chatOnly.Chat.Agent.Thinking.ModelProfiles[index] = profile
		}
	}
	refused, err := buildChatAgent(Config{AgentMode: "live", RuntimeSettings: settings}, chatOnly)
	if err != nil {
		t.Fatal(err)
	}
	fresh := agents.Request{Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "high", Tools: tools, Messages: user}
	_, err = refused.Stream(ctx, fresh, nil)
	var status *agents.StatusError
	param, rejected := agents.RequestRejectedParam(err)
	if err == nil || !rejected || param != "reasoning_effort" || !errors.As(err, &status) || status.Status != http.StatusBadRequest ||
		!strings.Contains(err.Error(), "Function tools with reasoning_effort are not supported for gpt-6.2-astra") {
		t.Fatalf("agent turn on chat completions: param=%q rejected=%t err=%v", param, rejected, err)
	}
	if sent := log.last(); sent.path != openaiv1.Prefix+"/chat/completions" {
		t.Errorf("the chat-only profile went to %s", sent.path)
	}
}

// writeCatalogWithProfile writes the shipped Catalog with one more model
// profile, for a test that needs a model of a kind the shipped one has none of.
func writeCatalogWithProfile(t *testing.T, root string, profile map[string]any) string {
	t.Helper()
	var document map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(catalog.DefaultDocument())))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	agent := document["application"].(map[string]any)["chat"].(map[string]any)["agent"].(map[string]any)
	thinking := agent["thinking"].(map[string]any)
	thinking["model_profiles"] = append([]any{profile}, thinking["model_profiles"].([]any)...)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "catalog-with-profile.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newAdminApplication(t *testing.T, mutate func(*Config)) *Application {
	t.Helper()
	root := t.TempDir()
	config := Config{
		Environment: "development", Address: "127.0.0.1:0",
		DatabasePath: filepath.Join(root, "art-ai.db"), StorageRoot: filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock", AgentMode: "mock",
		AuthMode: "development", DevelopmentUser: "owner-a", DevelopmentRoles: []string{"admin"},
	}
	mutate(&config)
	application, err := New(config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	return application
}

func appCall(t *testing.T, application *Application, method, target, body string, want int) map[string]any {
	t.Helper()
	response := appChatRequest(application.server.Handler, method, target, body)
	if response.Code != want {
		t.Fatalf("%s %s: %d %s", method, target, response.Code, response.Body.String())
	}
	decoded := map[string]any{}
	_ = json.Unmarshal(response.Body.Bytes(), &decoded)
	return decoded
}

// #401: a connection whose model cannot run the function tools of an Agent turn
// (the profile says so and the model is on Chat Completions) is never offered
// to sessions; the administration read says so, the connection test still
// reaches it, and a session that picked it cannot. The model is one the shipped
// Catalog does not have: gpt-astra itself now runs its tools on the Responses
// wire (the next test).
func TestAgentIncapableConnectionIsNotOfferedToSessions(t *testing.T) {
	cannot := false
	catalogFile := writeCatalogWithProfile(t, t.TempDir(), map[string]any{
		"model_prefixes": []any{"no-tools-model"}, "effort_field": "reasoning_effort", "effort_levels": []any{"low", "high"},
		"default_effort": "high", "chat_completions_tools": cannot,
	})
	application := newAdminApplication(t, func(config *Config) {
		config.CatalogFile = catalogFile
		config.RuntimeSettings = map[string]string{"llm_base_url": "https://gateway.test/v1", "llm_api_key": "sk-default", "llm_model": "deepseek"}
		config.ExtraChatConnections = []ChatConnectionConfig{{ID: "GPT", BaseURL: "https://gateway.test/v1", APIKey: "sk-gpt", Model: "no-tools-model"}}
	})
	routes := application.schema.LLMSettings.Routes
	managed := appCall(t, application, http.MethodGet, routes.Admin, "", http.StatusOK)
	tools := map[string]any{}
	for _, raw := range managed["connections"].([]any) {
		view := raw.(map[string]any)
		tools[view["id"].(string)] = view["agent_tools"]
	}
	if tools["LLM"] != true || tools["GPT"] != false || len(tools) != 2 {
		t.Fatalf("agent_tools = %v", tools)
	}

	created := appCall(t, application, http.MethodPost, "/api/chat/sessions", `{"title":"models"}`, http.StatusCreated)
	modelRoute := strings.ReplaceAll(application.schema.Runtime.HTTP.Routes.ChatSessionModel, "{sessionID}", created["id"].(string))
	view := appCall(t, application, http.MethodGet, modelRoute, "", http.StatusOK)
	if view["chatModel"] != "LLM" || len(view["chatModels"].([]any)) != 1 {
		t.Fatalf("session model = %v", view)
	}
	appCall(t, application, http.MethodPut, modelRoute, `{"chatModel":"GPT"}`, http.StatusBadRequest)
	// The connection test is not blocked: it sends no tools.
	appCall(t, application, http.MethodPost, routes.Test, `{"connection_id":"GPT"}`, http.StatusOK)
}

// #401 step 2 end to end through the real assembly: gpt-astra is offered to
// sessions again (agent_tools true), a session that picks it runs an Agent turn
// with a tool call against the fake upstream, and every request of that turn
// goes to /v1/responses: the first carries the tools in the flat form and the
// thinking level, the second the function_call with its function_call_output
// on the same call_id.
func TestSessionOnTheAstraConnectionRunsAnAgentTurnOnTheResponsesWire(t *testing.T) {
	gateway, log := loggedFake(t)
	key := fakeUpstreamKeyPrefix + "test-key"
	application := newAdminApplication(t, func(config *Config) {
		config.AgentMode = "live"
		config.RuntimeSettings = map[string]string{"llm_base_url": gateway, "llm_api_key": key, "llm_model": "deepseek"}
		config.ExtraChatConnections = []ChatConnectionConfig{{ID: "GPT", BaseURL: gateway, APIKey: key, Model: "gpt-astra"}}
	})
	runCtx, cancel := context.WithCancel(context.Background())
	generationDone, agentDone := make(chan error, 1), make(chan error, 1)
	go func() {
		generationDone <- application.generation.Run(runCtx, generation.CoordinatorConfig{
			PollEvery: 20 * time.Millisecond, LeaseFor: 30 * time.Second, BatchSize: 1, Concurrency: 1})
	}()
	go func() { agentDone <- application.chatAgent.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		<-generationDone
		<-agentDone
	})

	routes := application.schema.LLMSettings.Routes
	managed := appCall(t, application, http.MethodGet, routes.Admin, "", http.StatusOK)
	tools := map[string]any{}
	for _, raw := range managed["connections"].([]any) {
		view := raw.(map[string]any)
		tools[view["id"].(string)] = view["agent_tools"]
	}
	if tools["LLM"] != true || tools["GPT"] != true || len(tools) != 2 {
		t.Fatalf("agent_tools = %v", tools)
	}
	created := appCall(t, application, http.MethodPost, "/api/chat/sessions", `{}`, http.StatusCreated)
	sessionID := created["id"].(string)
	modelRoute := strings.ReplaceAll(application.schema.Runtime.HTTP.Routes.ChatSessionModel, "{sessionID}", sessionID)
	view := appCall(t, application, http.MethodGet, modelRoute, "", http.StatusOK)
	if len(view["chatModels"].([]any)) != 2 {
		t.Fatalf("the session is offered %v", view["chatModels"])
	}
	view = appCall(t, application, http.MethodPut, modelRoute, `{"chatModel":"GPT"}`, http.StatusOK)
	if view["chatModel"] != "GPT" {
		t.Fatalf("pick GPT = %v", view)
	}
	beforeTurn := log.count()
	appCall(t, application, http.MethodPost, "/api/chat/sessions/"+sessionID+"/messages",
		`{"content":"which models do I have? `+openaiv1.ToolMarkerFor("list_session_models", "")+`"}`, http.StatusAccepted)

	var content string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		listed := appChatRequest(application.server.Handler, http.MethodGet, "/api/chat/sessions/"+sessionID+"/messages", "")
		var current []map[string]any
		if json.Unmarshal(listed.Body.Bytes(), &current) == nil && len(current) == 2 {
			content, _ = current[1]["content"].(string)
			if content != "" && content != application.schema.Chat.State.PendingContent {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(content, openaiv1.FixedReply) {
		t.Fatalf("the assistant answered %q", content)
	}

	log.mu.Lock()
	calls := append([]upstreamCall(nil), log.calls[beforeTurn:]...)
	log.mu.Unlock()
	var turn []upstreamCall
	for _, call := range calls {
		if call.path != openaiv1.Prefix+"/responses" {
			t.Errorf("a request of the turn went to %s: %.300s", call.path, call.body)
		}
		if strings.Contains(call.body, `"model":"gpt-astra"`) {
			turn = append(turn, call)
		}
	}
	// The tool loop: the round that called the tool, and the round that answered.
	var rounds []map[string]any
	for _, call := range turn {
		var body map[string]any
		if err := json.Unmarshal([]byte(call.body), &body); err != nil {
			t.Fatal(err)
		}
		if _, withTools := body["tools"]; withTools {
			rounds = append(rounds, body)
		}
	}
	if len(rounds) < 2 {
		t.Fatalf("%d requests of the turn carried tools: %v", len(rounds), turn)
	}
	first, last := rounds[0], rounds[len(rounds)-1]
	firstTools, _ := first["tools"].([]any)
	if len(firstTools) == 0 || firstTools[0].(map[string]any)["type"] != "function" || firstTools[0].(map[string]any)["name"] == nil ||
		firstTools[0].(map[string]any)["strict"] != false || first["store"] != false || first["stream"] != true {
		t.Fatalf("first round = %.400v", first)
	}
	if reasoning, _ := first["reasoning"].(map[string]any); reasoning["effort"] != "high" {
		t.Fatalf("the thinking level of the first round = %v", first["reasoning"])
	}
	if _, sent := first["temperature"]; sent {
		t.Fatal("a temperature was sent")
	}
	var callID, outputID string
	for _, raw := range last["input"].([]any) {
		item := raw.(map[string]any)
		switch item["type"] {
		case "function_call":
			callID, _ = item["call_id"].(string)
			if item["name"] != "list_session_models" {
				t.Fatalf("function_call = %v", item)
			}
		case "function_call_output":
			outputID, _ = item["call_id"].(string)
			if output, _ := item["output"].(string); output == "" {
				t.Fatalf("function_call_output has no output: %v", item)
			}
		}
	}
	if callID == "" || callID != outputID {
		t.Fatalf("function_call %q and its output %q do not pair: %.600v", callID, outputID, last["input"])
	}
}

// reasoningItemsOf is what the orchestrator records of a reply on the Responses
// wire: one encrypted item on the assistant message of the same tool loop
// (#377).
func reasoningItemsOf(completion agents.Completion) []agents.ReasoningItem {
	if completion.ReasoningContent == "" {
		return nil
	}
	return []agents.ReasoningItem{{
		Model: "gpt-astra", Wire: "responses", Kind: agents.ReasoningEncrypted,
		Content: completion.ReasoningContent,
	}}
}
