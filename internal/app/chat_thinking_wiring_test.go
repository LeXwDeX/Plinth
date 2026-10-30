package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestChatAgentWiringEmitsSchemaDrivenThinkingOnWire(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	config, err := loadConfigFromSchema(application, func(string) string {
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	// The default model is qwen3.8-omni-flash (2026-10-30, #18), sent with
	// Qwen thinking at its highest effort.
	if config.RuntimeSettings["llm_model"] != "qwen3.8-omni-flash" {
		t.Fatalf("llm_model = %q", config.RuntimeSettings["llm_model"])
	}
	body := chatAgentWireBody(t, application, config.RuntimeSettings["llm_model"])
	if body["model"] != "qwen3.8-omni-flash" {
		t.Errorf("wire model = %#v", body["model"])
	}
	if body["enable_thinking"] != true || body["reasoning_effort"] != "xhigh" {
		t.Errorf("wire enable_thinking = %#v, reasoning_effort = %#v", body["enable_thinking"], body["reasoning_effort"])
	}
	if _, exists := body["thinking"]; exists {
		t.Errorf("thinking is DeepSeek's field: %#v", body)
	}
	if _, exists := body["thinking_budget"]; exists {
		t.Errorf("thinking_budget must never be sent: %#v", body)
	}
}

func TestChatAgentWiringSchemaMutationChangesThinkingWire(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		mutate      func(*catalog.ChatAgentSchema)
		wantEnabled any
		wantEffort  any
	}{
		{
			// #253: disabled thinking sends no vendor field at all.
			name: "explicitly disabled",
			mutate: func(agent *catalog.ChatAgentSchema) {
				agent.Thinking = &catalog.ChatThinkingSchema{Enabled: false}
			},
			wantEnabled: nil,
			wantEffort:  nil,
		},
		{
			// #253: a model no vendor profile names gets standard fields only.
			name:        "standard model without a profile",
			mutate:      func(*catalog.ChatAgentSchema) {},
			wantEnabled: nil,
			wantEffort:  nil,
		},
		{
			name: "legacy schema without thinking",
			mutate: func(agent *catalog.ChatAgentSchema) {
				agent.Thinking = nil
			},
			wantEnabled: nil,
			wantEffort:  nil,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			application := taskCatalog.Application()
			testCase.mutate(&application.Chat.Agent)
			body := chatAgentWireBody(
				t,
				application,
				"configured-model",
			)
			enabled, hasEnabled := body["enable_thinking"]
			if testCase.wantEnabled == nil {
				if hasEnabled {
					t.Errorf(
						"enable_thinking = %#v, want absent",
						enabled,
					)
				}
			} else if !hasEnabled || enabled != testCase.wantEnabled {
				t.Errorf(
					"enable_thinking = %#v (present=%v), want %#v",
					enabled,
					hasEnabled,
					testCase.wantEnabled,
				)
			}
			effort, hasEffort := body["reasoning_effort"]
			if testCase.wantEffort == nil {
				if hasEffort {
					t.Errorf(
						"reasoning_effort = %#v, want absent",
						effort,
					)
				}
			} else if !hasEffort || effort != testCase.wantEffort {
				t.Errorf(
					"reasoning_effort = %#v (present=%v), want %#v",
					effort,
					hasEffort,
					testCase.wantEffort,
				)
			}
		})
	}
}

func chatAgentWireBody(
	t *testing.T,
	application catalog.ApplicationSchema,
	model string,
) map[string]any {
	t.Helper()
	requestBody := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode wire request: %v", err)
		}
		requestBody <- body
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			writer,
			`{"choices":[{"message":{"content":"ok"}}]}`,
		)
	}))
	t.Cleanup(server.Close)
	adapter, err := buildChatAgent(Config{
		AgentMode: "live",
		RuntimeSettings: map[string]string{
			"llm_base_url": server.URL + "/v1",
			"llm_api_key":  "configured-test-key",
			// Profiles match the configured model, which production also sends.
			"llm_model": model,
		},
	}, application)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Complete(
		context.Background(),
		agents.Request{
			Model: model,
			Messages: []agents.Message{{
				Role:    "user",
				Content: "hello",
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	return <-requestBody
}

func TestChatAgentWiringSelectsVendorThinkingProfileByModel(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	for _, model := range []string{"deepseek", "DeepSeek-Flash"} {
		requestBody := make(chan map[string]any, 1)
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode wire request: %v", err)
			}
			requestBody <- body
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"ok"}}]}`)
		}))
		adapter, err := buildChatAgent(Config{
			AgentMode: "live",
			RuntimeSettings: map[string]string{
				"llm_base_url": server.URL + "/v1",
				"llm_api_key":  "configured-test-key",
				"llm_model":    model,
			},
		}, application)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.Complete(context.Background(), agents.Request{
			Model: model,
			Messages: []agents.Message{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "", ReasoningContent: "earlier plan"},
			},
		}); err != nil {
			t.Fatal(err)
		}
		body := <-requestBody
		server.Close()
		thinking, _ := body["thinking"].(map[string]any)
		if thinking["type"] != "enabled" || body["reasoning_effort"] != "low" {
			t.Fatalf("%s wire thinking = %#v", model, body)
		}
		if _, exists := body["enable_thinking"]; exists {
			t.Fatalf("%s sent enable_thinking: %#v", model, body)
		}
		messages, _ := body["messages"].([]any)
		if assistant, _ := messages[1].(map[string]any); assistant["reasoning_content"] != "earlier plan" {
			t.Fatalf("%s did not pass reasoning back: %#v", model, messages)
		}
	}
}
