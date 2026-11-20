package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// upstreamCall is one request the fake upstream received.
type upstreamCall struct {
	path          string
	body          string
	authorization string
}

type upstreamLog struct {
	mu    sync.Mutex
	calls []upstreamCall
}

func (log *upstreamLog) last() upstreamCall {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.calls[len(log.calls)-1]
}

func (log *upstreamLog) count() int {
	log.mu.Lock()
	defer log.mu.Unlock()
	return len(log.calls)
}

// loggedFake serves every fake upstream version behind a handler that notes
// each request, and returns the OpenAI-compatible gateway's /v1 address.
func loggedFake(t *testing.T) (string, *upstreamLog) {
	t.Helper()
	fake, err := registry.New(fakeupstream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	log := &upstreamLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		log.mu.Lock()
		log.calls = append(log.calls, upstreamCall{path: r.URL.Path, body: string(body), authorization: r.Header.Get("Authorization")})
		log.mu.Unlock()
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL + openaiv1.Prefix, log
}

// responsesSchema is the shipped Schema with gpt-astra served on the Responses
// API, as the shipped Catalog declares it from #401 step 2 on, and the path of
// that wire.
func responsesSchema(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Chat.Agent.ResponsesEndpointPath = "/responses"
	found := false
	for index, profile := range application.Chat.Agent.Thinking.ModelProfiles {
		if profile.ModelPrefixes[0] == "gpt-astra" {
			profile.WireAPI, profile.ChatCompletionsTools = catalog.ChatWireResponses, nil
			application.Chat.Agent.Thinking.ModelProfiles[index] = profile
			found = true
		}
	}
	if !found {
		t.Fatal("the shipped Schema has no gpt-astra profile")
	}
	return application
}

// #401 on the production wiring: a connection serves each request over the wire
// its model's profile declares, whatever kind of request it is, and a model on
// Chat Completions is sent exactly what it was before the Responses wire
// existed.
func TestChatConnectionServesEachModelOnItsProfilesWire(t *testing.T) {
	gateway, log := loggedFake(t)
	application := responsesSchema(t)
	key := fakeUpstreamKeyPrefix + "test-key"
	adapter, err := buildChatAgent(Config{AgentMode: "live",
		RuntimeSettings: map[string]string{"llm_base_url": gateway, "llm_api_key": key, "llm_model": "deepseek"},
		ExtraChatConnections: []ChatConnectionConfig{
			{ID: "GPT", BaseURL: gateway, APIKey: key + "-gpt", Model: "gpt-astra"},
		}}, application)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hello := []agents.Message{{Role: "user", Content: "hello"}}
	tools := []agents.Tool{{Name: "lookup_note", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}}
	temperature := application.Chat.Agent.Temperature
	picture := []agents.Message{{Role: "user", Parts: []agents.ContentPart{{Kind: "text", Text: "look"}, {Kind: "image_url", URL: "data:image/png;base64,AAAA", Detail: "low"}}}}

	// The four kinds of request of gpt-astra, on either connection: all
	// Responses, none with a temperature, each with the key of its connection.
	for _, connection := range []string{"", "GPT"} {
		for name, call := range map[string]func() error{
			"agent turn": func() error {
				_, err := adapter.Stream(ctx, agents.Request{Connection: connection, Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "xhigh", Tools: tools, Messages: hello}, nil)
				return err
			},
			"title": func() error {
				_, err := adapter.Complete(ctx, agents.Request{Connection: connection, Model: "gpt-astra", Temperature: temperature, Messages: hello})
				return err
			},
			"summary": func() error {
				_, err := adapter.Complete(ctx, agents.Request{Connection: connection, Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "high", Purpose: agents.PurposeSummary, Messages: hello})
				return err
			},
			"connection test": func() error {
				_, err := adapter.Complete(ctx, agents.Request{Connection: connection, Model: "gpt-astra", Temperature: temperature, ReasoningEffort: "low", Messages: picture})
				return err
			},
		} {
			if err := call(); err != nil {
				t.Fatalf("%q / %s: %v", connection, name, err)
			}
			sent := log.last()
			if sent.path != openaiv1.Prefix+"/responses" || strings.Contains(sent.body, "temperature") || !strings.Contains(sent.body, `"store":false`) {
				t.Errorf("%q / %s: %s %s", connection, name, sent.path, sent.body)
			}
			wantKey := "Bearer " + key
			if connection == "GPT" {
				wantKey += "-gpt"
			}
			if sent.authorization != wantKey {
				t.Errorf("%q / %s: authorization %q, want %q", connection, name, sent.authorization, wantKey)
			}
		}
	}
	// The Schema's own settings reach the Responses request: the profile's
	// level, the tool flags, parallel_tool_calls (chat.agent.parallel_tool_calls).
	if _, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Tools: tools, Messages: hello}, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"reasoning":{"effort":"high"}`, `"strict":false`, `"parallel_tool_calls":true`, `"include":["reasoning.encrypted_content"]`} {
		if !strings.Contains(log.last().body, want) {
			t.Errorf("the agent turn lacks %s: %s", want, log.last().body)
		}
	}

	// Another model of the same connections stays on Chat Completions, and its
	// request is the one the chat Adapter always built: the vendor fields make
	// it a re-encoded object (top-level keys sorted, nested values as they
	// were), with the temperature the Schema carries, the stream usage option
	// and parallel_tool_calls.
	const deepseekTurn = `{"messages":[{"role":"user","content":"hello"}],"model":"deepseek","parallel_tool_calls":true,` +
		`"reasoning_effort":"high","stream":true,"stream_options":{"include_usage":true},"temperature":0,"thinking":{"type":"enabled"},` +
		`"tools":[{"type":"function","function":{"name":"lookup_note","description":"d","parameters":{"type":"object"}}}]}`
	const plainTitle = `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"stream":false,"temperature":0}`
	for name, test := range map[string]struct {
		do   func() error
		want string
	}{
		"deepseek agent turn": {func() error {
			_, err := adapter.Stream(ctx, agents.Request{Model: "deepseek", Temperature: temperature, ReasoningEffort: "high", Tools: tools, Messages: hello}, nil)
			return err
		}, deepseekTurn},
		"a model without a profile": {func() error {
			_, err := adapter.Complete(ctx, agents.Request{Model: "gpt-4o", Temperature: temperature, Messages: hello})
			return err
		}, plainTitle},
	} {
		if err := test.do(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if sent := log.last(); sent.path != openaiv1.Prefix+"/chat/completions" || sent.body != test.want {
			t.Errorf("%s:\n got %s %s\nwant %s", name, sent.path, sent.body, test.want)
		}
	}
}

// Without a profile on the Responses wire the connection has no Responses
// adapter and the Schema needs no path for it: every request is Chat
// Completions, as before.
func TestChatConnectionWithoutAResponsesProfileSpeaksOnlyChatCompletions(t *testing.T) {
	gateway, log := loggedFake(t)
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Chat.Agent.ResponsesEndpointPath = ""
	for index, profile := range application.Chat.Agent.Thinking.ModelProfiles {
		profile.WireAPI = ""
		application.Chat.Agent.Thinking.ModelProfiles[index] = profile
	}
	adapter, err := buildChatAgent(Config{AgentMode: "live",
		RuntimeSettings: map[string]string{"llm_base_url": gateway, "llm_api_key": fakeUpstreamKeyPrefix + "test-key", "llm_model": "deepseek"}}, application)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"deepseek", "gpt-6-astra", "gpt-4o"} {
		if _, err := adapter.Complete(context.Background(), agents.Request{Model: model, Messages: []agents.Message{{Role: "user", Content: "hello"}}}); err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		if log.last().path != openaiv1.Prefix+"/chat/completions" {
			t.Errorf("%s went to %s", model, log.last().path)
		}
	}
}

// A connection that is not configured stays the not-configured stand-in, with
// or without a Responses profile.
func TestUnconfiguredConnectionStaysUnconfiguredWithAResponsesProfile(t *testing.T) {
	adapter, err := buildChatAgent(Config{AgentMode: "live", RuntimeSettings: map[string]string{"llm_model": "gpt-astra"}}, responsesSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, standIn := adapter.(agents.NotConfigured); !standIn {
		t.Fatalf("adapter = %T", adapter)
	}
}

// The request extensions of a Responses model are its profile's: the levels and
// the default, and the temperature rule; a model on another wire has none.
func TestResponsesOptionsFollowTheProfile(t *testing.T) {
	application := responsesSchema(t)
	options := responsesOptions(application.Chat.Agent, "GPT-Astra")
	if options == nil || !options.OmitTemperature || options.DefaultEffort != "high" ||
		strings.Join(options.EffortLevels, ",") != "low,medium,high,xhigh,max" {
		t.Fatalf("options = %#v", options)
	}
	for _, model := range []string{"deepseek", "gpt-6-astra", "gpt-4o", ""} {
		if got := responsesOptions(application.Chat.Agent, model); got != nil {
			t.Errorf("%q has Responses options %#v", model, got)
		}
	}
	// A profile on the wire that declares no levels and keeps the temperature.
	application.Chat.Agent.Thinking.ModelProfiles = append([]catalog.ChatThinkingModelProfile{{
		ModelPrefixes: []string{"plain-responses"}, WireAPI: catalog.ChatWireResponses}}, application.Chat.Agent.Thinking.ModelProfiles...)
	if got := responsesOptions(application.Chat.Agent, "plain-responses-1"); got == nil || got.OmitTemperature || len(got.EffortLevels) != 0 || got.DefaultEffort != "" {
		t.Errorf("plain = %#v", got)
	}
}
