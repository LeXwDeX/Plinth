package catalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// mutateDefaultCatalogLoad is mutateDefaultCatalog that also returns the
// Catalog it loaded.
func mutateDefaultCatalogLoad(t *testing.T, change func(document map[string]any), loaded **Catalog) error {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(defaultCatalog))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	change(document)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	*loaded, err = Load(bytes.NewReader(encoded))
	return err
}

// #401: a model profile names the wire API it is served on. The value is
// checked, the Responses wire refuses the members that only mean something on
// Chat Completions, and the endpoint path the wire needs is declared by the
// agent exactly when some profile uses it.
func TestLoadValidatesTheWireAPIOfAModelProfile(t *testing.T) {
	apply := func(profiles, path string) func(map[string]any) {
		return func(document map[string]any) {
			agent := document["application"].(map[string]any)["chat"].(map[string]any)["agent"].(map[string]any)
			var declared []any
			if err := json.Unmarshal([]byte(profiles), &declared); err != nil {
				t.Fatal(err)
			}
			agent["thinking"] = map[string]any{"enabled": true, "model_profiles": declared}
			if path == "" {
				delete(agent, "responses_endpoint_path")
			} else {
				agent["responses_endpoint_path"] = path
			}
		}
	}
	const responses = `"wire_api":"responses"`
	tests := []struct {
		name      string
		profiles  string
		path      string
		wantError string
	}{
		{name: "responses with levels", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `,"effort_field":"reasoning_effort","effort_levels":["low","high"],"default_effort":"high","omit_temperature":true}]`, path: "/responses"},
		{name: "the wire alone is a declaration", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `}]`, path: "/responses"},
		{name: "chat completions spelled out", profiles: `[{"model_prefixes":["gpt-4"],"wire_api":"chat_completions","omit_temperature":true}]`},
		{name: "both wires on one connection", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `},{"model_prefixes":["gpt-6"],"wire_api":"chat_completions","effort_field":"reasoning_effort","effort_levels":["low"],"default_effort":"low"}]`, path: "/responses"},
		{name: "responses with a chat completions tools flag", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `,"chat_completions_tools":false}]`, path: "/responses"},
		{name: "unknown wire", profiles: `[{"model_prefixes":["gpt-astra"],"wire_api":"completions","omit_temperature":true}]`, wantError: "wire_api"},
		{name: "wire in another case", profiles: `[{"model_prefixes":["gpt-astra"],"wire_api":"Responses","omit_temperature":true}]`, wantError: "wire_api"},
		{name: "vendor request fields", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `,"request_fields":{"thinking":{"type":"enabled"}}}]`, path: "/responses", wantError: "request_fields"},
		{name: "reasoning passback", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `,"reasoning_passback":true}]`, path: "/responses", wantError: "reasoning_passback"},
		{name: "stream usage option", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `,"stream_include_usage":false}]`, path: "/responses", wantError: "stream_include_usage"},
		{name: "responses without a path", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `}]`, wantError: "responses_endpoint_path"},
		{name: "a path that is not absolute", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `}]`, path: "responses", wantError: "responses_endpoint_path"},
		{name: "a path with spaces around it", profiles: `[{"model_prefixes":["gpt-astra"],` + responses + `}]`, path: " /responses", wantError: "responses_endpoint_path"},
		{name: "a path nobody uses", profiles: `[{"model_prefixes":["gpt-4"],"omit_temperature":true}]`, path: "/responses", wantError: "responses_endpoint_path"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, apply(testCase.profiles, testCase.path))
			if testCase.wantError == "" {
				if err != nil {
					t.Fatalf("valid declaration rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error = %v, want mention of %q", err, testCase.wantError)
			}
		})
	}
	// Thinking off: no profile, so no wire, and a path is unused.
	err := mutateDefaultCatalog(t, func(document map[string]any) {
		agent := document["application"].(map[string]any)["chat"].(map[string]any)["agent"].(map[string]any)
		agent["thinking"] = map[string]any{"enabled": false}
		agent["responses_endpoint_path"] = "/responses"
	})
	if err == nil || !strings.Contains(err.Error(), "responses_endpoint_path") {
		t.Fatalf("a path with thinking off: %v", err)
	}
}

// The wire a model is served on is its profile's, chat completions for a model
// without one; a family served on the Responses API runs Agent tools whatever
// the chat completions flag says; and nothing is a Responses family while
// thinking is off.
func TestWireOfAModelFollowsItsProfile(t *testing.T) {
	cannot := false
	thinking := &ChatThinkingSchema{Enabled: true, ModelProfiles: []ChatThinkingModelProfile{
		{ModelPrefixes: []string{"gpt-astra"}, WireAPI: ChatWireResponses, OmitTemperature: true, ChatCompletionsTools: &cannot},
		{ModelPrefixes: []string{"gpt-sol"}, ChatCompletionsTools: &cannot},
		{ModelPrefixes: []string{"gpt-terra"}, WireAPI: ChatWireCompletions},
		{ModelPrefixes: []string{"deepseek"}, OmitTemperature: true},
	}}
	for model, want := range map[string]struct {
		wire  string
		tools bool
	}{
		"gpt-astra":     {ChatWireResponses, true},
		"GPT-Astra":     {ChatWireResponses, true},
		"gpt-astra-9.9": {ChatWireResponses, true},
		"gpt-sol":       {ChatWireCompletions, false},
		"gpt-terra":     {ChatWireCompletions, true},
		"deepseek":      {ChatWireCompletions, true},
		"gpt-4o":        {ChatWireCompletions, true},
		"":              {ChatWireCompletions, true},
	} {
		profile, _ := thinking.ProfileFor(model)
		if got := thinking.WireFor(model); got != want.wire || profile.Wire() != want.wire || profile.RunsAgentTools() != want.tools {
			t.Errorf("%q: wire %q, tools %t; want %q, %t", model, got, profile.RunsAgentTools(), want.wire, want.tools)
		}
	}
	if !thinking.UsesResponses() {
		t.Error("a profile uses the responses wire")
	}
	thinking.Enabled = false
	if thinking.UsesResponses() || thinking.WireFor("gpt-astra") != ChatWireCompletions {
		t.Error("with thinking off no profile applies")
	}
	var absent *ChatThinkingSchema
	if absent.UsesResponses() || absent.WireFor("gpt-astra") != ChatWireCompletions {
		t.Error("a Catalog without thinking has no responses wire")
	}
	if (&ChatThinkingSchema{Enabled: true, ModelProfiles: thinking.ModelProfiles[1:]}).UsesResponses() {
		t.Error("no profile of the list uses the responses wire")
	}
}

// The wire and the endpoint load typed, are cloned by Application(), and stay
// out of the public Schema with the rest of the vendor profiles.
func TestWireAPILoadsTypedAndStaysPrivate(t *testing.T) {
	var loaded *Catalog
	err := mutateDefaultCatalogLoad(t, func(document map[string]any) {
		agent := document["application"].(map[string]any)["chat"].(map[string]any)["agent"].(map[string]any)
		agent["responses_endpoint_path"] = "/v1/responses"
		agent["thinking"] = map[string]any{"enabled": true, "model_profiles": []any{
			map[string]any{"model_prefixes": []any{"gpt-astra"}, "wire_api": "responses", "omit_temperature": true}}}
	}, &loaded)
	if err != nil {
		t.Fatal(err)
	}
	agent := loaded.Application().Chat.Agent
	if agent.ResponsesEndpointPath != "/v1/responses" || len(agent.Thinking.ModelProfiles) != 1 ||
		agent.Thinking.ModelProfiles[0].WireAPI != ChatWireResponses || agent.Thinking.WireFor("gpt-astra") != ChatWireResponses {
		t.Fatalf("agent = %#v", agent)
	}
	// Application() hands out a copy.
	agent.Thinking.ModelProfiles[0].WireAPI = ""
	if loaded.Application().Chat.Agent.Thinking.ModelProfiles[0].WireAPI != ChatWireResponses {
		t.Fatal("Application() did not clone the profiles")
	}
	encoded, err := json.Marshal(loaded.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{`"wire_api"`, `"responses_endpoint_path"`, `"/v1/responses"`} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("public Schema leaks %q", private)
		}
	}
}
