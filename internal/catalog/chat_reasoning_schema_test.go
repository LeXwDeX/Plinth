package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// The levels and windows below are the vendors' own documented values (#322),
// checked on 2026-10-22, never derived from the Schema under test:
//   - DeepSeek: https://api-docs.deepseek.com/api/create-chat-completion
//     (reasoning_effort low/high/max) and
//     https://api-docs.deepseek.com/quick_start/pricing (context length 1M for
//     every current model). Default "low" is the user's 2026-10-20 decision.
//   - Qwen 3.8: https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions
//     (reasoning_effort low/medium/xhigh, default xhigh). Other Qwen models
//     document thinking_budget only, so the generic profile keeps the one level
//     already configured.
//   - GLM: https://docs.bigmodel.cn/api-reference/%E6%A8%A1%E5%9E%8B-api/%E5%AF%B9%E8%AF%9D%E8%A1%A5%E5%85%A8
//     (low/high/max accepted by GLM-5.2 and GLM-5.3, default max; the usage
//     comes in the last chunk and stream_options is not documented). Windows
//     differ per model under the "qwen" and "glm" prefixes, so none is declared.
//   - GPT-6 family: the commander checked the dev-test LLM proxy's /models
//     list on 2026-11-04 (gpt-6-astra, gpt-6-luna, gpt-6.1-sol, gpt-6.1-terra,
//     plus the bare gpt-terra/gpt-sol/gpt-luna aliases); a request with
//     reasoning_effort=high returned reasoning_tokens, and gpt-astra is the
//     alias of gpt-6-astra. The proxy carries no Claude model, so the "claude"
//     profile's reasoning_effort field is still to be verified against the
//     internal deployment proxy.
func TestDefaultCatalogDeclaresThinkingLevelsAndContextWindows(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	thinking := application.Chat.Agent.Thinking
	type want struct {
		prefix   string
		levels   []string
		fallback string
		window   int
		usage    *bool
	}
	no := false
	// gpt-6-astra (alias gpt-astra): OpenAI's model page lists low/medium/high/xhigh/max, and on
	// 2026-11-09 the dev-test proxy rejected "minimal" with "valid levels: low, medium, high, xhigh,
	// max" and accepted xhigh and max. The rest of the gpt-6 family is not re-verified (#213).
	// On 2026-11-20 /v1/responses answered "none" and "minimal" for gpt-astra with 400 "Unsupported
	// value ... Supported values are: 'low', 'medium', 'high', 'xhigh', and 'max'" and served xhigh
	// (#401): the profile offers exactly these five and never none or minimal.
	astra := []string{"low", "medium", "high", "xhigh", "max"}
	// #487: gpt-6.2 (the family gpt-6.2-astra belongs to) is served on the
	// Responses wire like the gpt-astra alias that resolves to it; gpt-6.1 and
	// the versioned gpt-6-astra stay on Chat Completions for the requests
	// without tools but cannot run an Agent turn there (OpenAI's Reasoning and
	// model pages, read 2026-11-23: Chat Completions does not support function
	// calling with GPT-6 Astra or GPT-6.1 Sol). Both keep the levels the gpt-6
	// family is verified to take.
	wants := []want{
		{prefix: "qwen-omni-flash", levels: []string{"low", "medium", "xhigh"}, fallback: "xhigh", window: 1000000},
		{prefix: "gpt-astra", levels: astra, fallback: "high"},
		{prefix: "gpt-6.2", levels: astra, fallback: "high"},
		{prefix: "gpt-6.1", levels: []string{"low", "medium", "high"}, fallback: "high"},
		{prefix: "gpt-6-astra", levels: astra, fallback: "high"},
		{prefix: "gpt-6", levels: []string{"low", "medium", "high"}, fallback: "high"},
		{prefix: "gpt-terra", levels: []string{"low", "medium", "high"}, fallback: "high"},
		{prefix: "gpt-sol", levels: []string{"low", "medium", "high"}, fallback: "high"},
		{prefix: "gpt-luna", levels: []string{"low", "medium", "high"}, fallback: "high"},
		{prefix: "claude", levels: []string{"low", "medium", "high"}, fallback: "high"},
		{prefix: "deepseek", levels: []string{"low", "high", "max"}, fallback: "low", window: 1000000},
		{prefix: "qwen3.8-omni-flash", levels: []string{"low", "medium", "xhigh"}, fallback: "xhigh", window: 1000000},
		{prefix: "qwen3.8", levels: []string{"low", "medium", "xhigh"}, fallback: "xhigh"},
		{prefix: "qwen", levels: []string{"xhigh"}, fallback: "xhigh"},
		{prefix: "glm", levels: []string{"low", "high", "max"}, fallback: "max", usage: &no},
	}
	if thinking == nil || len(thinking.ModelProfiles) != len(wants) {
		t.Fatalf("profiles = %#v", thinking)
	}
	for index, expected := range wants {
		profile := thinking.ModelProfiles[index]
		if profile.ModelPrefixes[0] != expected.prefix || profile.EffortField != "reasoning_effort" ||
			!slices.Equal(profile.EffortLevels, expected.levels) || profile.DefaultEffort != expected.fallback ||
			profile.ContextWindowTokens != expected.window {
			t.Fatalf("profile %d = %#v, want %#v", index, profile, expected)
		}
		if _, sent := profile.RequestFields["reasoning_effort"]; sent {
			t.Fatalf("profile %s still sends a fixed reasoning_effort: %s", expected.prefix, profile.RequestFields["reasoning_effort"])
		}
		if (expected.usage == nil) != (profile.StreamIncludeUsage == nil) ||
			expected.usage != nil && *expected.usage != *profile.StreamIncludeUsage {
			t.Fatalf("profile %s stream usage = %v", expected.prefix, profile.StreamIncludeUsage)
		}
	}
	// #401: the gateway resolves gpt-astra to gpt-6.2-astra, which refuses a
	// temperature (any value, on the Responses API; a value other than its
	// default on Chat Completions) and (OpenAI's latest-model guide and model
	// page, read 2026-11-20) runs function tools only on the Responses API.
	// So its profile omits the temperature and is served on the Responses wire
	// (step 2), where it runs Agent tools; it no longer says chat completions
	// cannot (chat_completions_tools is a field for another model).
	// #487: the gpt-6.2 family (gpt-6.2-astra and gpt-6.2-sol, which a versioned
	// model name resolves to) is the same model family, so its profile declares
	// the same wire, the same five levels and the same omitted temperature. The
	// rest of the family keeps Chat Completions for the requests without tools
	// (title, summary, prompt enhancement, connection test) but says in
	// chat_completions_tools that it cannot run an Agent turn there, so a
	// session is never offered a model whose every turn carries tools it cannot
	// run. No other profile declares a wire, omits the temperature or limits
	// its tools, and the Schema names the path of the Responses wire.
	responsesWire := map[string]bool{"gpt-astra": true, "gpt-6.2": true}
	withoutChatTools := map[string]bool{"gpt-6.1": true, "gpt-6-astra": true}
	for index, profile := range thinking.ModelProfiles {
		prefix := profile.ModelPrefixes[0]
		wantWire := ChatWireCompletions
		if responsesWire[prefix] {
			wantWire = ChatWireResponses
		}
		if profile.OmitTemperature != responsesWire[prefix] || profile.Wire() != wantWire ||
			(profile.WireAPI == ChatWireResponses) != responsesWire[prefix] {
			t.Fatalf("profile %d (%s): omit temperature = %t, wire = %q (declared %q)",
				index, prefix, profile.OmitTemperature, profile.Wire(), profile.WireAPI)
		}
		if profile.ChatCompletionsTools != nil && !withoutChatTools[prefix] {
			t.Fatalf("profile %d (%s) limits its chat completions tools unexpectedly: %v", index, prefix, *profile.ChatCompletionsTools)
		}
		if withoutChatTools[prefix] && (profile.ChatCompletionsTools == nil || *profile.ChatCompletionsTools) {
			t.Fatalf("profile %d (%s) must say chat completions cannot run tools", index, prefix)
		}
		if withoutChatTools[prefix] && profile.RunsAgentTools() {
			t.Fatalf("profile %d (%s) must not be offered for conversation turns", index, prefix)
		}
	}
	if !thinking.UsesResponses() || thinking.WireFor("GPT-Astra") != ChatWireResponses || thinking.WireFor("deepseek-flash") != ChatWireCompletions ||
		application.Chat.Agent.ResponsesEndpointPath != "/responses" || application.Chat.Agent.EndpointPath != "/chat/completions" {
		t.Fatalf("wires: responses used = %t, gpt-astra on %q, deepseek on %q, paths %q and %q", thinking.UsesResponses(),
			thinking.WireFor("GPT-Astra"), thinking.WireFor("deepseek-flash"), application.Chat.Agent.ResponsesEndpointPath, application.Chat.Agent.EndpointPath)
	}
	// Version-less aliases (2026-11-02, #64) resolve to their own profiles.
	if profile, ok := thinking.ProfileFor("qwen-omni-flash"); !ok || profile.DefaultEffort != "xhigh" ||
		!slices.Equal(profile.EffortLevels, []string{"low", "medium", "xhigh"}) {
		t.Fatalf("qwen-omni-flash profile = %#v %v", profile, ok)
	}
	if profile, ok := thinking.ProfileFor("gpt-astra"); !ok || profile.DefaultEffort != "high" ||
		!slices.Equal(profile.EffortLevels, astra) {
		t.Fatalf("gpt-astra profile = %#v %v", profile, ok)
	}
	// gpt-6 covers the versioned family; the bare aliases and Claude match
	// their own prefixes, and the first matching profile wins.
	for model, prefix := range map[string]string{
		"gpt-6-astra": "gpt-6-astra", "gpt-6-luna": "gpt-6",
		"gpt-6.1-sol": "gpt-6.1", "gpt-6.1-terra": "gpt-6.1", "gpt-6.2-astra": "gpt-6.2", "gpt-6.2-sol": "gpt-6.2",
		"gpt-terra": "gpt-terra", "gpt-sol": "gpt-sol", "gpt-luna": "gpt-luna",
		"claude-sonnet-4": "claude",
	} {
		if profile, ok := thinking.ProfileFor(model); !ok || profile.ModelPrefixes[0] != prefix ||
			profile.DefaultEffort != "high" {
			t.Fatalf("%s profile = %#v %v", model, profile, ok)
		}
	}
	// The dev-test model name routes to DeepSeek's flash model.
	if profile, ok := thinking.ProfileFor("DeepSeek-Flash"); !ok || profile.ModelPrefixes[0] != "deepseek" {
		t.Fatalf("deepseek-flash profile = %#v %v", profile, ok)
	}
	if profile, ok := thinking.ProfileFor("qwen3.8-max"); !ok || profile.ModelPrefixes[0] != "qwen3.8" {
		t.Fatalf("qwen3.8-max profile = %#v %v", profile, ok)
	}
	if profile, ok := thinking.ProfileFor("qwen-plus"); !ok || profile.ModelPrefixes[0] != "qwen" {
		t.Fatalf("qwen-plus profile = %#v %v", profile, ok)
	}
	if _, ok := thinking.ProfileFor("gpt-4o"); ok {
		t.Fatal("a model without a vendor profile matched one")
	}
	if !application.Chat.Agent.StreamIncludeUsage || application.Chat.Agent.ContextWindowSetting != "llm_context_window_tokens" {
		t.Fatalf("agent usage settings = %v %q", application.Chat.Agent.StreamIncludeUsage, application.Chat.Agent.ContextWindowSetting)
	}
	if _, declared := application.Runtime.Configuration.Settings["llm_context_window_tokens"]; !declared {
		t.Fatal("the context window override is not a declared runtime setting")
	}
	compaction := application.Chat.ContextCompaction
	// User 2026-10-22: compact automatically at 80% of the window and keep the
	// newest five rounds verbatim.
	if !compaction.Enabled || compaction.AutoThresholdRatio != 0.8 || compaction.KeepRecentRounds != 5 ||
		compaction.WarningRatio <= 0 || compaction.WarningRatio > compaction.AutoThresholdRatio {
		t.Fatalf("compaction = %#v", compaction)
	}
	routes := application.Runtime.HTTP.Routes
	for name, route := range map[string]string{
		"reasoning":  routes.ChatSessionReasoning,
		"context":    routes.ChatSessionContext,
		"compaction": routes.ChatSessionContextCompaction,
	} {
		if !strings.Contains(route, "{sessionID}") {
			t.Fatalf("route %s = %q", name, route)
		}
	}
	encoded, err := json.Marshal(application.Public())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"context_compaction":{"enabled":true,"auto_threshold_ratio":0.8`) {
		t.Fatalf("public chat lacks the compaction view")
	}
	// The prompts and the vendor profiles stay on the server.
	for _, private := range []string{compaction.SummaryPrompt, `"model_profiles"`, `"effort_field"`, `"thinking_kind"`} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("public Schema leaks %q", private)
		}
	}
}

func TestEffortForFallsBackToTheProfileDefault(t *testing.T) {
	profile := ChatThinkingModelProfile{EffortField: "reasoning_effort", EffortLevels: []string{"low", "high", "max"}, DefaultEffort: "low"}
	for chosen, sent := range map[string]string{"high": "high", "max": "max", "": "low", "xhigh": "low", "HIGH": "low"} {
		if got := profile.EffortFor(chosen); got != sent {
			t.Fatalf("EffortFor(%q) = %q, want %q", chosen, got, sent)
		}
	}
	if got := (ChatThinkingModelProfile{}).EffortFor("high"); got != "" {
		t.Fatalf("a profile without levels sent %q", got)
	}
}

// #382: a request that is not a conversation turn takes the level the family
// declares for its kind; a kind it declares nothing for keeps the family
// default, so the shipped default_catalog.json names low for all three.
func TestEffortForTurnUsesTheDeclaredLevelPerKind(t *testing.T) {
	profile := ChatThinkingModelProfile{
		EffortField:   "reasoning_effort",
		EffortLevels:  []string{"low", "high", "max"},
		DefaultEffort: "max",
		TurnEfforts:   map[string]string{ChatTurnEffortTitle: "low", ChatTurnEffortSummary: "low"},
	}
	if got := profile.EffortForTurn(ChatTurnEffortTitle); got != "low" {
		t.Fatalf("title effort = %q, want low", got)
	}
	if got := profile.EffortForTurn(ChatTurnEffortPromptEnhance); got != "max" {
		t.Fatalf("undeclared kind effort = %q, want the family default max", got)
	}
	// A level the family does not offer falls back, as a session's choice does.
	profile.TurnEfforts[ChatTurnEffortSummary] = "xhigh"
	if got := profile.EffortForTurn(ChatTurnEffortSummary); got != "max" {
		t.Fatalf("unoffered level effort = %q, want the family default max", got)
	}
	if got := (ChatThinkingModelProfile{}).EffortForTurn(ChatTurnEffortTitle); got != "" {
		t.Fatalf("a profile without levels sent %q", got)
	}
}

// The shipped Catalog declares a level for every kind on every family that
// offers a level, and none of those levels is missing from its own levels.
func TestDefaultCatalogDeclaresTurnEfforts(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	profiles := product.Application().Chat.Agent.Thinking.ModelProfiles
	if len(profiles) == 0 {
		t.Fatal("the default Catalog declares no thinking profile")
	}
	for _, profile := range profiles {
		for _, kind := range []string{ChatTurnEffortTitle, ChatTurnEffortSummary, ChatTurnEffortPromptEnhance} {
			if profile.EffortForTurn(kind) == "" {
				t.Fatalf("%v declares no level for %s", profile.ModelPrefixes, kind)
			}
		}
	}
}

func TestLoadValidatesThinkingLevelsAndWindows(t *testing.T) {
	profile := func(fields string) func(map[string]any) {
		return func(document map[string]any) {
			agent := document["application"].(map[string]any)["chat"].(map[string]any)["agent"].(map[string]any)
			var profiles []any
			if err := json.Unmarshal([]byte(fields), &profiles); err != nil {
				t.Fatal(err)
			}
			agent["thinking"] = map[string]any{"enabled": true, "model_profiles": profiles}
		}
	}
	tests := []struct {
		name      string
		profiles  string
		wantError string
	}{
		{name: "levels and window", profiles: `[{"model_prefixes":["deepseek"],"request_fields":{"thinking":{"type":"enabled"}},"effort_field":"reasoning_effort","effort_levels":["low","high","max"],"default_effort":"low","context_window_tokens":1000000}]`},
		{name: "window only", profiles: `[{"model_prefixes":["gpt"],"context_window_tokens":128000}]`},
		{name: "nothing declared", profiles: `[{"model_prefixes":["gpt"]}]`, wantError: "request_fields"},
		{name: "default not offered", profiles: `[{"model_prefixes":["d"],"effort_field":"reasoning_effort","effort_levels":["low","high"],"default_effort":"max"}]`, wantError: "default_effort"},
		{name: "levels without field", profiles: `[{"model_prefixes":["d"],"effort_levels":["low"],"default_effort":"low"}]`, wantError: "effort_field"},
		{name: "field without levels", profiles: `[{"model_prefixes":["d"],"effort_field":"reasoning_effort","default_effort":"low"}]`, wantError: "effort_levels"},
		{name: "duplicate level", profiles: `[{"model_prefixes":["d"],"effort_field":"reasoning_effort","effort_levels":["low","low"],"default_effort":"low"}]`, wantError: "low"},
		{name: "blank level", profiles: `[{"model_prefixes":["d"],"effort_field":"reasoning_effort","effort_levels":["low"," "],"default_effort":"low"}]`, wantError: "effort_levels"},
		{name: "reserved field", profiles: `[{"model_prefixes":["d"],"effort_field":"temperature","effort_levels":["low"],"default_effort":"low"}]`, wantError: "temperature"},
		{name: "stream options reserved", profiles: `[{"model_prefixes":["d"],"request_fields":{"stream_options":{"include_usage":true}}}]`, wantError: "stream_options"},
		{name: "field also fixed", profiles: `[{"model_prefixes":["d"],"request_fields":{"reasoning_effort":"low"},"effort_field":"reasoning_effort","effort_levels":["low"],"default_effort":"low"}]`, wantError: "reasoning_effort"},
		{name: "turn effort not offered", profiles: `[{"model_prefixes":["d"],"effort_field":"reasoning_effort","effort_levels":["low"],"default_effort":"low","turn_efforts":{"title":"xhigh"}}]`, wantError: "turn_efforts"},
		{name: "turn effort declared", profiles: `[{"model_prefixes":["d"],"effort_field":"reasoning_effort","effort_levels":["low"],"default_effort":"low","turn_efforts":{"title":"low"}}]`},
		{name: "negative window", profiles: `[{"model_prefixes":["d"],"context_window_tokens":-1}]`, wantError: "context_window_tokens"},
		{name: "shadowed prefix", profiles: `[{"model_prefixes":["qwen"],"context_window_tokens":1},{"model_prefixes":["qwen3.8"],"context_window_tokens":2}]`, wantError: "qwen3.8"},
		{name: "longer prefix first", profiles: `[{"model_prefixes":["qwen3.8"],"context_window_tokens":2},{"model_prefixes":["qwen"],"context_window_tokens":1}]`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, profile(testCase.profiles))
			if testCase.wantError == "" {
				if err != nil {
					t.Fatalf("valid profiles rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error = %v, want mention of %q", err, testCase.wantError)
			}
		})
	}
}

func TestLoadValidatesContextCompaction(t *testing.T) {
	set := func(key string, value any) func(map[string]any) {
		return func(document map[string]any) {
			compaction := document["application"].(map[string]any)["chat"].(map[string]any)["context_compaction"].(map[string]any)
			if value == nil {
				delete(compaction, key)
				return
			}
			compaction[key] = value
		}
	}
	tests := []struct {
		name      string
		change    func(map[string]any)
		wantError string
	}{
		{name: "threshold above one", change: set("auto_threshold_ratio", 1.2), wantError: "auto_threshold_ratio"},
		{name: "threshold zero", change: set("auto_threshold_ratio", 0), wantError: "auto_threshold_ratio"},
		{name: "warning above threshold", change: set("warning_ratio", 0.9), wantError: "warning_ratio"},
		{name: "no kept rounds", change: set("keep_recent_rounds", 0), wantError: "keep_recent_rounds"},
		{name: "no timeout", change: set("request_timeout_seconds", 0), wantError: "request_timeout_seconds"},
		{name: "source without transcript", change: set("source_template", "{previous_summary}"), wantError: "{transcript}"},
		{name: "context without summary", change: set("context_template", "{open}{close}"), wantError: "{summary}"},
		{name: "line without content", change: set("transcript_line_template", "{role}"), wantError: "{content}"},
		{name: "same markers", change: set("close_marker", "<<<SESSION_SUMMARY>>>"), wantError: "marker"},
		{name: "no handle template", change: set("compacted_handle_template", nil), wantError: "compacted_handle_template"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, testCase.change)
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error = %v, want mention of %q", err, testCase.wantError)
			}
		})
	}
	disabled := func(document map[string]any) {
		document["application"].(map[string]any)["chat"].(map[string]any)["context_compaction"] = map[string]any{"enabled": false}
	}
	if err := mutateDefaultCatalog(t, disabled); err != nil {
		t.Fatalf("compaction off rejected: %v", err)
	}
}

// The thinking kind is how a profile says which reasoning its model carries
// (#377). Only the families whose thinking the wire hands us are declared:
// DeepSeek, Qwen and GLM answer plaintext reasoning_content, the Responses
// wire carries the encrypted items of its own protocol; the profiles served on
// chat completions without reasoning passback get no kind, because nothing
// reaches the turn from them.
func TestDefaultCatalogDeclaresThinkingKinds(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	thinking := loaded.Application().Chat.Agent.Thinking
	for model, want := range map[string]string{
		"deepseek":       ChatThinkingPlaintext,
		"DeepSeek-Flash": ChatThinkingPlaintext,
		"qwen-plus":      ChatThinkingPlaintext,
		"qwen3.8-max":    ChatThinkingPlaintext,
		"glm-5.2":        ChatThinkingPlaintext,
		"gpt-astra":      ChatThinkingEncrypted,
		// Served on chat completions, and their reasoning never reaches a turn.
		"gpt-6-astra":   ChatThinkingNone,
		"claude-sonnet": ChatThinkingNone,
		"gpt-4o":        ChatThinkingNone,
	} {
		profile, _ := thinking.ProfileFor(model)
		if got := profile.ReasoningKind(); got != want {
			t.Errorf("%s: thinking kind %q, want %q", model, got, want)
		}
	}
	// A profile that passes reasoning back declares the kind that can go back,
	// and the Responses wire the kind only it can replay.
	for index, profile := range thinking.ModelProfiles {
		if profile.ReasoningPassback && profile.ReasoningKind() != ChatThinkingPlaintext {
			t.Errorf("profile %d (%s) passes reasoning back with kind %q",
				index, profile.ModelPrefixes[0], profile.ReasoningKind())
		}
		if profile.Wire() == ChatWireResponses && profile.ReasoningKind() != ChatThinkingEncrypted {
			t.Errorf("profile %d (%s) is on the responses wire with kind %q",
				index, profile.ModelPrefixes[0], profile.ReasoningKind())
		}
	}
}

// The kind values are the Agent port's: the Catalog declares them and the
// Agent marks its reasoning items with them, so the two cannot drift apart.
func TestThinkingKindValuesAreTheAgentPorts(t *testing.T) {
	if ChatThinkingNone != agents.ReasoningNone || ChatThinkingPlaintext != agents.ReasoningPlaintext ||
		ChatThinkingEncrypted != agents.ReasoningEncrypted {
		t.Fatalf("catalog kinds %q/%q/%q, agent kinds %q/%q/%q", ChatThinkingNone, ChatThinkingPlaintext,
			ChatThinkingEncrypted, agents.ReasoningNone, agents.ReasoningPlaintext, agents.ReasoningEncrypted)
	}
}

// The thinking kind is validated with the other profile members: a known value,
// plaintext for a profile that passes reasoning back, encrypted on the
// Responses wire. A profile that declares only a kind is a declaration.
func TestLoadValidatesThinkingKinds(t *testing.T) {
	profile := func(fields string) func(map[string]any) {
		return func(document map[string]any) {
			agent := document["application"].(map[string]any)["chat"].(map[string]any)["agent"].(map[string]any)
			var profiles []any
			if err := json.Unmarshal([]byte(fields), &profiles); err != nil {
				t.Fatal(err)
			}
			agent["thinking"] = map[string]any{"enabled": true, "model_profiles": profiles}
		}
	}
	tests := []struct {
		name      string
		profiles  string
		wantError string
	}{
		{name: "plaintext with passback", profiles: `[{"model_prefixes":["deepseek"],"reasoning_passback":true,"thinking_kind":"plaintext"}]`},
		{name: "kind alone", profiles: `[{"model_prefixes":["deepseek"],"thinking_kind":"plaintext"}]`},
		{name: "none spelled out", profiles: `[{"model_prefixes":["gpt-6"],"thinking_kind":"none"}]`},
		{name: "encrypted on responses", profiles: `[{"model_prefixes":["gpt-astra"],"wire_api":"responses","thinking_kind":"encrypted"}]`},
		{name: "unknown kind", profiles: `[{"model_prefixes":["deepseek"],"thinking_kind":"signed"}]`, wantError: "thinking_kind"},
		{name: "passback without a kind", profiles: `[{"model_prefixes":["deepseek"],"reasoning_passback":true}]`, wantError: "thinking_kind"},
		{name: "passback with the wrong kind", profiles: `[{"model_prefixes":["deepseek"],"reasoning_passback":true,"thinking_kind":"encrypted"}]`, wantError: "thinking_kind"},
		{name: "responses without the encrypted kind", profiles: `[{"model_prefixes":["gpt-astra"],"wire_api":"responses"}]`, wantError: "thinking_kind"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, profile(testCase.profiles))
			if testCase.wantError == "" {
				if err != nil {
					t.Fatalf("valid profiles rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error = %v, want mention of %q", err, testCase.wantError)
			}
		})
	}
}
