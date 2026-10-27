package wirefixture_test

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func recordSecurityJSON(t *testing.T, policy wirefixture.Policy, body string) wirefixture.Exchange {
	t.Helper()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", body)
	_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
	exchanges := recorder.Exchanges()
	if len(exchanges) != 1 {
		t.Fatalf("exchanges = %d", len(exchanges))
	}
	return exchanges[0]
}

func TestJSONKeyRegistrationPrecedesRedaction(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"ordinary", `{"unregistered":"synthetic result"}`},
		{"sensitive", `{"provider_auth_token":"fake-private-value"}`},
		{"sensitive already redacted", `{"provider_auth_token":"[redacted]"}`},
		{"sensitive suffix", `{"DB_PASSWORD":"[redacted]"}`},
		{"original key spelling", `{"Password":"[redacted]"}`},
		{"signed URL with exact alias", `{"signed_url":"https://api.meshy.ai/file?token=secret"}`},
		{"nested ordinary", `{"message":{"unregistered":null}}`},
		{"nested sensitive", `{"message":{"provider_auth_token":"[redacted]"}}`},
		{"array sensitive", `{"message":[{"provider_auth_token":"[redacted]"}]}`},
		{"JSON string", `{"message":"{\"provider_auth_token\":\"[redacted]\"}"}`},
		{"JSON array string", `{"message":"[{\"provider_auth_token\":\"[redacted]\"}]"}`},
		{"repeated JSON string", `{"message":"{\"message\":\"{\\\"provider_auth_token\\\":\\\"[redacted]\\\"}\"}"}`},
		{"escaped nested key", `{"message":"{\"provider_\\u0061uth_token\":\"[redacted]\"}"}`},
		{"nested alias target", `{"message":"nested-alias"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := testPolicy()
			policy.Keys["password"] = true
			policy.Allowed[`{"provider_auth_token":"[redacted]"}`] = true
			policy.Aliases["nested-alias"] = `{"provider_auth_token":"[redacted]"}`
			exchange := recordSecurityJSON(t, policy, tc.body)
			if exchange.Complete || !strings.Contains(exchange.Invalid, "unknown JSON key") || len(exchange.Request.Body.JSON) != 0 {
				t.Fatalf("unregistered key did not fail closed: %+v", exchange)
			}
			if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); !errors.Is(err, wirefixture.ErrUnknownKey) {
				t.Fatalf("captured unknown key validation = %v", err)
			}

			// Revalidate a hand-built candidate, not only an incomplete capture.
			candidate := safetyExchange()
			candidate.Response.Body.JSON = json.RawMessage(tc.body)
			exchanges := []wirefixture.Exchange{candidate}
			if err := wirefixture.Validate(exchanges, policy); !errors.Is(err, wirefixture.ErrUnknownKey) {
				t.Fatalf("candidate unknown key validation = %v", err)
			}
			root, dir := safetyCandidateDir(t)
			path := filepath.Join(dir, "candidate.json")
			if err := wirefixture.WriteCandidate(root, path, exchanges, policy); !errors.Is(err, wirefixture.ErrUnknownKey) {
				t.Fatalf("unknown key export = %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("rejected candidate created a file: %v", err)
			}
		})
	}
}

func TestRegisteredSensitiveJSONKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"credential", `{"password":"fake-private-value"}`, `{"password":"[redacted]"}`},
		{"nested credential", `{"message":"{\"password\":\"fake-private-value\"}"}`, `{"message":"{\"password\":\"[redacted]\"}"}`},
		{"signed URL exact alias", `{"signed_url":"https://api.meshy.ai/file?token=secret"}`, `{"signed_url":"https://example.test/file"}`},
		{"signed URL unmatched", `{"signed_url":"https://api.meshy.ai/file?token=other"}`, `{"signed_url":"[redacted]"}`},
		{"signed URL redaction alias", `{"signed_url":"https://provider.example/private?sig=fake"}`, `{"signed_url":"[redacted]"}`},
		{"signed URL public", `{"signed_url":"https://example.test/file"}`, `{"signed_url":"https://example.test/file"}`},
		{"signed URL non-URL text", `{"signed_url":"synthetic result"}`, `{"signed_url":"[redacted]"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := testPolicy()
			allowPolicy(&policy, []string{"password", "signed_url"}, nil)
			policy.Aliases["https://provider.example/private?sig=fake"] = "[redacted]"
			exchange := recordSecurityJSON(t, policy, tc.body)
			if !exchange.Complete || string(exchange.Request.Body.JSON) != tc.want {
				t.Fatalf("registered sensitive key result = %+v, want %s", exchange, tc.want)
			}
			if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSSENestedSensitiveTextIsReplaced(t *testing.T) {
	for _, field := range []string{"content", "reasoning_content"} {
		t.Run(field, func(t *testing.T) {
			policy := testPolicy()
			allowPolicy(&policy, []string{"choices", "delta", "content", "reasoning_content", "tool_calls", "index", "id", "function", "name", "arguments"}, []string{"call-a", "refine_image"})
			var stream string
			if field == "arguments" {
				stream = sseStream(t,
					toolCallChunk(0, "call-a", "refine_image", `{"provider_auth_token":`),
					toolCallChunk(0, "", "", `"fake-private-value"}`))
			} else {
				stream = sseStream(t,
					map[string]any{"choices": []any{map[string]any{"delta": map[string]any{field: `{"provider_auth_token":`}}}},
					map[string]any{"choices": []any{map[string]any{"delta": map[string]any{field: `"fake-private-value"}`}}}})
			}
			exchanges, _ := recordSSE(t, policy, stream)
			if len(exchanges) != 1 || exchanges[0].Complete || exchanges[0].Response.Body.SSE != "" {
				t.Fatalf("unregistered nested SSE text accepted: %+v", exchanges)
			}
			if err := wirefixture.Validate(exchanges, policy); err == nil {
				t.Fatal("unregistered nested SSE text validated")
			}
		})
	}
}

func TestSSEDeltaSubsetRejectedEvenWhenAllowed(t *testing.T) {
	for _, field := range []string{"refusal", "function_call", "audio"} {
		t.Run(field, func(t *testing.T) {
			policy := testPolicy()
			allowPolicy(&policy, []string{"choices", "delta", field}, []string{"sk-", "abcdefghijkl"})
			stream := sseStream(t,
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{field: "sk-"}}}},
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{field: "abcdefghijkl"}}}},
			)
			exchanges, _ := recordSSE(t, policy, stream)
			if len(exchanges) != 1 || exchanges[0].Complete || !strings.Contains(exchanges[0].Invalid, "SSE delta field") {
				t.Fatalf("unsupported delta field accepted: %#v", exchanges)
			}
			if strings.Contains(exchanges[0].Invalid, "sk-") || strings.Contains(exchanges[0].Response.Body.SSE, "sk-") {
				t.Fatalf("unsupported delta content exposed: %+v", exchanges[0])
			}
			if err := wirefixture.Validate(exchanges, policy); err == nil {
				t.Fatal("unsupported delta field fixture validated")
			}
		})
	}

	t.Run("role remains supported", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, []string{"choices", "delta", "role"}, []string{"assistant"})
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"role": "assistant"}}}}))
		if len(exchanges) != 1 || !exchanges[0].Complete {
			t.Fatalf("role delta rejected: %#v", exchanges)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSSENestedContentSanitizedBeforeTextCheck(t *testing.T) {
	t.Run("content URL alias", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, []string{"choices", "delta", "content", "url"}, nil)
		raw := `{"url":"https://api.meshy.ai/file?token=secret"}`
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": raw}}}}))
		if len(exchanges) != 1 || !exchanges[0].Complete {
			t.Fatalf("nested content alias capture: %#v", exchanges)
		}
		text := exchanges[0].Response.Body.SSE
		if !strings.Contains(text, "https://example.test/file") ||
			strings.Contains(text, "api.meshy.ai") || strings.Contains(text, "token=secret") {
			t.Fatalf("nested content alias lost: %s", text)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("content credential field", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, []string{"choices", "delta", "content", "password"}, nil)
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": `{"password":"fake-nested-secret"}`}}}}))
		if len(exchanges) != 1 || !exchanges[0].Complete {
			t.Fatalf("nested content credential capture: %#v", exchanges)
		}
		text := exchanges[0].Response.Body.SSE
		if !strings.Contains(text, "[redacted]") || strings.Contains(text, "fake-nested-secret") {
			t.Fatalf("nested content credential leaked: %s", text)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("reasoning URL alias", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, []string{"choices", "delta", "reasoning_content", "url"}, nil)
		raw := `{"url":"https://api.meshy.ai/file?token=secret"}`
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": raw}}}}))
		if len(exchanges) != 1 || !exchanges[0].Complete {
			t.Fatalf("nested reasoning alias capture: %#v", exchanges)
		}
		text := exchanges[0].Response.Body.SSE
		if !strings.Contains(text, "https://example.test/file") ||
			strings.Contains(text, "api.meshy.ai") || strings.Contains(text, "token=secret") {
			t.Fatalf("nested reasoning alias lost: %s", text)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAliasTargetsAreTerminal(t *testing.T) {
	base := func() wirefixture.Policy {
		policy := testPolicy()
		policy.Keys["message"] = true
		policy.Keys["arguments"] = true
		return policy
	}
	record := func(t *testing.T, policy wirefixture.Policy, alias string) wirefixture.Exchange {
		t.Helper()
		return recordSecurityJSON(t, policy, `{"message":"`+alias+`"}`)
	}

	t.Run("chain rejected", func(t *testing.T) {
		policy := base()
		policy.Aliases["alias-a"] = "alias-b"
		policy.Aliases["alias-b"] = "[redacted]"
		exchange := record(t, policy, "alias-a")
		if exchange.Complete || !strings.Contains(exchange.Invalid, "alias chain") {
			t.Fatalf("alias chain accepted: %+v", exchange)
		}
	})

	t.Run("cycle rejected", func(t *testing.T) {
		policy := base()
		policy.Aliases["cycle-a"] = "cycle-b"
		policy.Aliases["cycle-b"] = "cycle-a"
		exchange := record(t, policy, "cycle-a")
		if exchange.Complete || !strings.Contains(exchange.Invalid, "alias chain") {
			t.Fatalf("alias cycle accepted: %+v", exchange)
		}
	})

	t.Run("unknown target rejected", func(t *testing.T) {
		policy := base()
		policy.Aliases["alias-unknown"] = "private value"
		exchange := record(t, policy, "alias-unknown")
		if exchange.Complete || !strings.Contains(exchange.Invalid, "alias target") {
			t.Fatalf("unknown alias target accepted: %+v", exchange)
		}
	})

	t.Run("allowed text accepted", func(t *testing.T) {
		policy := base()
		policy.Aliases["alias-text"] = "synthetic result"
		exchange := record(t, policy, "alias-text")
		if !exchange.Complete || !strings.Contains(string(exchange.Request.Body.JSON), "synthetic result") {
			t.Fatalf("allowed alias text rejected: %+v", exchange)
		}
		if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("canonical URL accepted", func(t *testing.T) {
		policy := base()
		policy.Aliases["alias-url"] = "https://example.test/file"
		exchange := record(t, policy, "alias-url")
		if !exchange.Complete || !strings.Contains(string(exchange.Request.Body.JSON), "https://example.test/file") {
			t.Fatalf("canonical alias URL rejected: %+v", exchange)
		}
		if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("nested JSON accepted", func(t *testing.T) {
		policy := base()
		policy.Aliases["alias-nested"] = `{"arguments":"synthetic result"}`
		exchange := record(t, policy, "alias-nested")
		if !exchange.Complete {
			t.Fatalf("nested alias target rejected: %+v", exchange)
		}
		if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("nested alias cycle bounded", func(t *testing.T) {
		policy := base()
		policy.Aliases["cycle-json"] = `{"arguments":"cycle-json"}`
		exchange := record(t, policy, "cycle-json")
		if exchange.Complete || !strings.Contains(exchange.Invalid, "nesting") {
			t.Fatalf("nested alias cycle accepted: %+v", exchange)
		}
	})

	t.Run("path alias chain rejected", func(t *testing.T) {
		policy := base()
		policy.Aliases["path-a"] = "path-b"
		policy.Aliases["path-b"] = "tasks"
		request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/path-a", "")
		recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
		_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
		exchange := recorder.Exchanges()[0]
		if exchange.Complete || !strings.Contains(exchange.Invalid, "request path") {
			t.Fatalf("path alias chain accepted: %+v", exchange)
		}
	})

	t.Run("host alias chain rejected", func(t *testing.T) {
		policy := base()
		policy.Aliases["example.test"] = "example.test"
		request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
		recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
		_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
		exchange := recorder.Exchanges()[0]
		if exchange.Complete || !strings.Contains(exchange.Invalid, "request host") {
			t.Fatalf("host alias chain accepted: %+v", exchange)
		}
	})
}

func TestCredentialKeySuffixesAndEmbeddedAssignments(t *testing.T) {
	t.Run("suffix keys redacted", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, []string{"my_api_key", "provider_auth_token", "provider_access_key", "DB_PASSWORD"}, nil)
		exchange := recordSecurityJSON(t, policy, `{"my_api_key":"fake-api-value","provider_auth_token":"fake-auth-value","provider_access_key":"fake-access-value","DB_PASSWORD":"fake-db-password"}`)
		if !exchange.Complete {
			t.Fatalf("suffix credential keys rejected: %+v", exchange)
		}
		text := string(exchange.Request.Body.JSON)
		if strings.Contains(text, "fake-api-value") || strings.Contains(text, "fake-auth-value") ||
			strings.Contains(text, "fake-access-value") || strings.Contains(text, "fake-db-password") ||
			strings.Count(text, "[redacted]") != 4 {
			t.Fatalf("suffix credential keys leaked: %s", text)
		}
		if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("max_tokens stays structural", func(t *testing.T) {
		policy := testPolicy()
		policy.Keys["max_tokens"] = true
		policy.Allowed["100"] = true
		exchange := recordSecurityJSON(t, policy, `{"max_tokens":100}`)
		if !exchange.Complete || !strings.Contains(string(exchange.Request.Body.JSON), `"max_tokens":100`) ||
			strings.Contains(string(exchange.Request.Body.JSON), "[redacted]") {
			t.Fatalf("ordinary token counter changed: %+v", exchange)
		}
	})

	cases := []struct {
		name      string
		message   string
		complete  bool
		wantMark  string
		wantError string
	}{
		{"uppercase assignment", `MY_API_KEY=fake-api-value`, false, "fake-api-value", "credential"},
		{"prefixed underscore assignment", `provider_auth_token=fake-auth-value`, false, "fake-auth-value", "credential"},
		{"dollar assignment", `DB_PASSWORD=$ecret`, false, "$ecret", "credential"},
		{"brace assignment", `DB_PASSWORD={secret}`, false, "{secret}", "credential"},
		{"bang assignment", `DB_PASSWORD=!secret`, false, "!secret", "credential"},
		{"unicode assignment", `DB_PASSWORD=秘密`, false, "秘密", "credential"},
		{"data URI without media type", `data:,fake-data-value`, false, "fake-data-value", "alias required"},
		{"data URI without media type base64", `data:;base64,ZmFrZS1kYXRh`, false, "ZmFrZS1kYXRh", "alias required"},
		{"prose embedded credential", `prose {"password":"fake-prose-secret"} tail`, false, "fake-prose-secret", "credential"},
		{"escaped prose embedded credential", `prose {\"provider_auth_token\":\"fake-escaped-secret\"} tail`, false, "fake-escaped-secret", "credential"},
		{"ordinary counter assignment", `max_tokens=100`, true, "", ""},
		{"approved ordinary text", `[Image result]`, true, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := testPolicy()
			policy.Allowed[tc.message] = true
			body, err := json.Marshal(map[string]string{"message": tc.message})
			if err != nil {
				t.Fatal(err)
			}
			exchange := recordSecurityJSON(t, policy, string(body))
			if exchange.Complete != tc.complete {
				t.Fatalf("complete = %v, want %v: %+v", exchange.Complete, tc.complete, exchange)
			}
			text := string(exchange.Request.Body.JSON) + exchange.Invalid + exchange.Response.Body.SSE
			if tc.wantMark != "" && strings.Contains(text, tc.wantMark) {
				t.Fatalf("raw marker exposed: %+v", exchange)
			}
			if tc.wantError != "" && !strings.Contains(exchange.Invalid, tc.wantError) {
				t.Fatalf("unexpected failure reason: %+v", exchange)
			}
			if exchange.Complete {
				if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
					t.Fatal(err)
				}
			} else if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err == nil {
				t.Fatal("unsafe fixture validated")
			}
		})
	}
}

func TestNumericScalarsRequireExplicitApproval(t *testing.T) {
	body := `{"message":987654321}`
	t.Run("unapproved numeric user id", func(t *testing.T) {
		policy := testPolicy()
		policy.Keys["message"] = true
		exchange := recordSecurityJSON(t, policy, body)
		if exchange.Complete || !strings.Contains(exchange.Invalid, "number not approved") ||
			strings.Contains(string(exchange.Request.Body.JSON), "987654321") {
			t.Fatalf("numeric user id accepted: %+v", exchange)
		}
	})

	t.Run("numeric alias preserves number type", func(t *testing.T) {
		policy := testPolicy()
		policy.Keys["message"] = true
		policy.Aliases["987654321"] = "1"
		exchange := recordSecurityJSON(t, policy, body)
		if !exchange.Complete {
			t.Fatalf("numeric alias rejected: %+v", exchange)
		}
		text := string(exchange.Request.Body.JSON)
		if !strings.Contains(text, `"message":1`) || strings.Contains(text, `"message":"1"`) {
			t.Fatalf("numeric alias changed type: %s", text)
		}
		if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("non-number alias rejected", func(t *testing.T) {
		policy := testPolicy()
		policy.Keys["message"] = true
		policy.Aliases["987654321"] = `"1"`
		exchange := recordSecurityJSON(t, policy, body)
		if exchange.Complete || !strings.Contains(exchange.Invalid, "numeric alias") {
			t.Fatalf("non-number alias accepted: %+v", exchange)
		}
	})

	t.Run("unapproved numeric alias target rejected", func(t *testing.T) {
		policy := testPolicy()
		policy.Aliases["987654321"] = "123"
		exchange := recordSecurityJSON(t, policy, body)
		if exchange.Complete || !strings.Contains(exchange.Invalid, "numeric alias") {
			t.Fatalf("unapproved numeric alias target accepted: %+v", exchange)
		}
	})

	t.Run("numeric alias chain rejected", func(t *testing.T) {
		policy := testPolicy()
		policy.Keys["message"] = true
		policy.Aliases["987654321"] = "123"
		policy.Aliases["123"] = "1"
		exchange := recordSecurityJSON(t, policy, body)
		if exchange.Complete || !strings.Contains(exchange.Invalid, "numeric alias") {
			t.Fatalf("numeric alias chain accepted: %+v", exchange)
		}
	})
}

func TestRetainedHeadersRejectControls(t *testing.T) {
	policy := testPolicy()
	value := "application/json\r\nX-Injected: 1"
	policy.Allowed[value] = true
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
	request.Header.Set("Accept", value)
	_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || !strings.Contains(exchange.Invalid, "control character") {
		t.Fatalf("control header accepted: %+v", exchange)
	}
	if exchange.Request.Headers["accept"] != "" || strings.Contains(exchange.Invalid, "X-Injected") {
		t.Fatalf("control header exposed: %+v", exchange)
	}
	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err == nil {
		t.Fatal("control header fixture validated")
	}
}

func TestSSEStrictChunkAndChoiceFields(t *testing.T) {
	policy := testPolicy()
	allowPolicy(&policy, []string{
		"id", "object", "created", "model", "choices", "usage", "system_fingerprint", "service_tier",
		"index", "delta", "content", "finish_reason", "prompt_tokens", "completion_tokens",
	}, []string{
		"chat.completion.chunk", "configured-model", "fp-synthetic", "default",
	})
	chunk := map[string]any{
		"id": "call_live", "object": "chat.completion.chunk", "created": 1, "model": "configured-model",
		"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"content": "synthetic result"}, "finish_reason": "stop",
		}},
		"usage":              map[string]any{"prompt_tokens": 1, "completion_tokens": 2},
		"system_fingerprint": "fp-synthetic", "service_tier": "default",
	}
	exchanges, _ := recordSSE(t, policy, sseStream(t, chunk))
	if len(exchanges) != 1 || !exchanges[0].Complete {
		t.Fatalf("strict chat chunk rejected: %#v", exchanges)
	}
	text := exchanges[0].Response.Body.SSE
	if !strings.Contains(text, `"usage":{"completion_tokens":2,"prompt_tokens":1}`) {
		t.Fatalf("usage metadata lost: %s", text)
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}

	usageOnly := map[string]any{
		"choices": []any{},
		"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 2},
	}
	exchanges, _ = recordSSE(t, policy, sseStream(t, usageOnly))
	if len(exchanges) != 1 || !exchanges[0].Complete {
		t.Fatalf("usage-only chunk rejected: %#v", exchanges)
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}

	unsupported := []struct {
		name   string
		stream string
	}{
		{"choices text", `data: {"choices":[{"index":0,"text":"synthetic result"}]}` + "\n\ndata: [DONE]\n\n"},
		{"top-level text", `data: {"text":"synthetic result"}` + "\n\ndata: [DONE]\n\n"},
		{"top-level message", `data: {"message":{"content":"synthetic result"}}` + "\n\ndata: [DONE]\n\n"},
	}
	for _, tc := range unsupported {
		t.Run(tc.name, func(t *testing.T) {
			current := policy
			current.Keys = maps.Clone(policy.Keys)
			current.Allowed = maps.Clone(policy.Allowed)
			allowPolicy(&current, []string{"text", "message"}, []string{"synthetic result"})
			exchanges, _ := recordSSE(t, current, tc.stream)
			if len(exchanges) != 1 || exchanges[0].Complete {
				t.Fatalf("unsupported fragment field accepted: %#v", exchanges)
			}
			if !strings.Contains(exchanges[0].Invalid, "SSE choice field") &&
				!strings.Contains(exchanges[0].Invalid, "SSE chunk field") {
				t.Fatalf("unexpected failure reason: %+v", exchanges[0])
			}
			if err := wirefixture.Validate(exchanges, current); err == nil {
				t.Fatal("unsupported fragment field fixture validated")
			}
		})
	}
}

func TestSSEDuplicateToolIndexRejected(t *testing.T) {
	policy := testPolicy()
	allowPolicy(&policy, []string{
		"choices", "delta", "tool_calls", "index", "id", "function", "name", "arguments",
	}, []string{"call-a", "refine_image", "paint"})
	stream := sseStream(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
		"tool_calls": []any{
			map[string]any{"index": 0, "id": "call-a", "function": map[string]any{"name": "refine_image", "arguments": `{"prompt":"paint"}`}},
			map[string]any{"index": 0, "id": "call-a", "function": map[string]any{"name": "refine_image", "arguments": `{"prompt":"paint"}`}},
		},
	}}}})
	exchanges, _ := recordSSE(t, policy, stream)
	if len(exchanges) != 1 || exchanges[0].Complete || !strings.Contains(exchanges[0].Invalid, "duplicate SSE tool index") {
		t.Fatalf("duplicate tool index accepted: %#v", exchanges)
	}
	if err := wirefixture.Validate(exchanges, policy); err == nil {
		t.Fatal("duplicate tool index fixture validated")
	}
}

func TestRelativeURLReferencesFailClosedOrAlias(t *testing.T) {
	unsafe := []string{
		`//admin:secret@example.test/file`,
		`//example.test/file?sig=fake`,
		`/admin:secret@example.test/file`,
		`/file?sig=fake`,
		`/file#fragment`,
	}
	for _, raw := range unsafe {
		t.Run(raw, func(t *testing.T) {
			policy := testPolicy()
			policy.Keys["message"] = true
			policy.Allowed[raw] = true
			body, err := json.Marshal(map[string]string{"message": raw})
			if err != nil {
				t.Fatal(err)
			}
			exchange := recordSecurityJSON(t, policy, string(body))
			if exchange.Complete || exchange.Invalid == "" {
				t.Fatalf("relative URL reference accepted: %+v", exchange)
			}
			if strings.Contains(exchange.Invalid, raw) || strings.Contains(string(exchange.Request.Body.JSON), raw) {
				t.Fatalf("relative URL reference exposed: %+v", exchange)
			}
			if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err == nil {
				t.Fatal("relative URL reference fixture validated")
			}
		})
	}

	t.Run("JSON key", func(t *testing.T) {
		policy := testPolicy()
		policy.Keys["/file?sig=fake"] = true
		exchange := recordSecurityJSON(t, policy, `{"/file?sig=fake":"synthetic result"}`)
		if exchange.Complete || exchange.Invalid == "" {
			t.Fatalf("URL-shaped JSON key accepted: %+v", exchange)
		}
	})

	t.Run("safe relative path", func(t *testing.T) {
		policy := testPolicy()
		policy.Keys["message"] = true
		policy.Allowed["/v1/tasks"] = true
		exchange := recordSecurityJSON(t, policy, `{"message":"/v1/tasks"}`)
		if !exchange.Complete {
			t.Fatalf("safe relative path rejected: %+v", exchange)
		}
		if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("aliases", func(t *testing.T) {
		cases := []struct {
			raw   string
			alias string
			want  string
		}{
			{`//example.test/file?sig=fake`, "https://example.test/file", "https://example.test/file"},
			{`/file?sig=fake`, "[redacted]", "[redacted]"},
			{`https://api.meshy.ai/other?token=secret`, "[redacted]", "[redacted]"},
		}
		for _, tc := range cases {
			policy := testPolicy()
			policy.Keys["message"] = true
			policy.Allowed[tc.raw] = true
			policy.Aliases[tc.raw] = tc.alias
			body, err := json.Marshal(map[string]string{"message": tc.raw})
			if err != nil {
				t.Fatal(err)
			}
			exchange := recordSecurityJSON(t, policy, string(body))
			if !exchange.Complete || strings.Contains(string(exchange.Request.Body.JSON), tc.raw) ||
				!strings.Contains(string(exchange.Request.Body.JSON), tc.want) {
				t.Fatalf("alias %q not retained: %+v", tc.raw, exchange)
			}
			if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("nested JSON alias", func(t *testing.T) {
		policy := testPolicy()
		policy.Keys["message"] = true
		policy.Keys["arguments"] = true
		policy.Allowed[`/file?sig=fake`] = true
		policy.Aliases[`/file?sig=fake`] = "https://example.test/file"
		exchange := recordSecurityJSON(t, policy, `{"message":"{\"arguments\":\"/file?sig=fake\"}"}`)
		if !exchange.Complete || !strings.Contains(string(exchange.Request.Body.JSON), "https://example.test/file") {
			t.Fatalf("nested relative alias not retained: %+v", exchange)
		}
		if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("split SSE unaliased", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, []string{"choices", "delta", "content"}, nil)
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "/file?"}}}},
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "sig=fake"}}}},
		))
		if len(exchanges) != 1 || exchanges[0].Complete || exchanges[0].Invalid == "" {
			t.Fatalf("unaliased split relative URL accepted: %#v", exchanges)
		}
		if strings.Contains(exchanges[0].Invalid, "sig=fake") ||
			strings.Contains(exchanges[0].Response.Body.SSE, "sig=fake") {
			t.Fatalf("unaliased split relative URL exposed: %+v", exchanges[0])
		}
	})

	t.Run("split SSE alias", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, []string{"choices", "delta", "content"}, nil)
		policy.Allowed[`/file?sig=fake`] = true
		policy.Aliases[`/file?sig=fake`] = "https://example.test/file"
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "/file?"}}}},
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "sig=fake"}}}},
		))
		if len(exchanges) != 1 || !exchanges[0].Complete {
			t.Fatalf("split relative alias rejected: %#v", exchanges)
		}
		if !strings.Contains(exchanges[0].Response.Body.SSE, "https://example.test/file") ||
			strings.Contains(exchanges[0].Response.Body.SSE, "sig=fake") {
			t.Fatalf("split relative alias lost: %s", exchanges[0].Response.Body.SSE)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("nested SSE alias", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, []string{"choices", "delta", "content", "url"}, nil)
		policy.Allowed[`/file?sig=fake`] = true
		policy.Aliases[`/file?sig=fake`] = "https://example.test/file"
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": `{"url":"/file?sig=fake"}`}}}},
		))
		if len(exchanges) != 1 || !exchanges[0].Complete ||
			!strings.Contains(exchanges[0].Response.Body.SSE, "https://example.test/file") {
			t.Fatalf("nested SSE relative alias lost: %#v", exchanges)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})
}

func TestJSONNumericExactTextAliases(t *testing.T) {
	policy := testPolicy()
	policy.Keys["message"] = true
	exchange := recordSecurityJSON(t, policy, `{"message":1.5}`)
	if !exchange.Complete || !strings.Contains(string(exchange.Request.Body.JSON), `"message":1.5`) {
		t.Fatalf("approved numeric exact text rejected: %+v", exchange)
	}
	payload := struct {
		Message json.Number `json:"message"`
	}{}
	if err := json.Unmarshal(exchange.Request.Body.JSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message.String() != "1.5" {
		t.Fatalf("numeric exact text changed: %s", payload.Message)
	}
}

func TestExplicitAliasesPrecedePublicDefaults(t *testing.T) {
	const raw = "https://example.test/private-marker"
	for _, target := range []string{"[redacted]", "https://example.test/file"} {
		t.Run(target, func(t *testing.T) {
			policy := testPolicy()
			policy.Keys["signed_url"] = true
			policy.Aliases[raw] = target
			for _, body := range []string{
				`{"message":"` + raw + `"}`,
				`{"signed_url":"` + raw + `"}`,
				`{"message":"{\"url\":\"` + raw + `\"}"}`,
			} {
				exchange := recordSecurityJSON(t, policy, body)
				if !exchange.Complete || strings.Contains(string(exchange.Request.Body.JSON), raw) ||
					!strings.Contains(string(exchange.Request.Body.JSON), target) {
					t.Fatalf("explicit alias ignored: %+v", exchange)
				}
				if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
					t.Fatal(err)
				}
			}
			exchanges, _ := recordSSE(t, policy, sseStream(t,
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": raw}}}},
			))
			if len(exchanges) != 1 || !exchanges[0].Complete || strings.Contains(exchanges[0].Response.Body.SSE, raw) ||
				!strings.Contains(exchanges[0].Response.Body.SSE, target) {
				t.Fatalf("explicit SSE alias ignored: %#v", exchanges)
			}
			if err := wirefixture.Validate(exchanges, policy); err != nil {
				t.Fatal(err)
			}
		})
	}
	policy := testPolicy()
	policy.Aliases["1"] = "2"
	exchange := recordSecurityJSON(t, policy, `{"message":1}`)
	if !exchange.Complete || string(exchange.Request.Body.JSON) != `{"message":2}` {
		t.Fatalf("explicit numeric alias ignored: %+v", exchange)
	}
	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
		t.Fatal(err)
	}
}
