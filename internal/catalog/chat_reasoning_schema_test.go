package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
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
	wants := []want{
		{prefix: "deepseek", levels: []string{"low", "high", "max"}, fallback: "low", window: 1000000},
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
	for _, private := range []string{compaction.SummaryPrompt, `"model_profiles"`, `"effort_field"`} {
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
