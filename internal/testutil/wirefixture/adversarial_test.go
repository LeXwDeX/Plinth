package wirefixture_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func allowPolicy(policy *wirefixture.Policy, keys, values []string) {
	for _, key := range keys {
		policy.Keys[key] = true
	}
	for _, value := range values {
		policy.Allowed[value] = true
	}
}

func sseStream(t *testing.T, chunks ...any) string {
	t.Helper()
	var stream strings.Builder
	for _, chunk := range chunks {
		data, err := json.Marshal(chunk)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&stream, "data: %s\n\n", data)
	}
	stream.WriteString("data: [DONE]\n\n")
	return stream.String()
}

func recordSSE(t *testing.T, policy wirefixture.Policy, stream string) ([]wirefixture.Exchange, []byte) {
	t.Helper()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/chat", "")
	_, body := capture(t, recorder, request, jsonResponse(200, "text/event-stream", stream))
	return recorder.Exchanges(), body
}

func toolCallChunk(index int, id, name, arguments string) map[string]any {
	call := map[string]any{"index": index}
	function := map[string]any{}
	if id != "" {
		call["id"] = id
	}
	if name != "" {
		function["name"] = name
	}
	if arguments != "" {
		function["arguments"] = arguments
	}
	call["function"] = function
	return map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
		"tool_calls": []any{call},
	}}}}
}

func TestOpenAIStreamFragmentedToolCallAndIDReplay(t *testing.T) {
	policy := testPolicy()
	allowPolicy(&policy, []string{
		"prompt", "choices", "delta", "content", "tool_calls", "index", "id",
		"type", "function", "name", "arguments", "model", "messages", "stream",
		"temperature", "role",
	}, []string{
		"configured-model", "user", "hello", "hello world", "call-a",
		"refine_image", "paint",
	})
	stream := "" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"world\",\"tool_calls\":[{\"index\":0,\"id\":\"call-\",\"function\":{\"name\":\"refine_\",\"arguments\":\"{\\\"prompt\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"a\",\"function\":{\"name\":\"image\",\"arguments\":\"\\\"paint\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"

	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: "https://api.meshy.ai/v1", EndpointPath: "/chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer", APIKey: "secret",
		RequestTimeout: time.Minute, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
		Client: &http.Client{Transport: recorder.Wrap("agent", jsonResponse(200, "text/event-stream", stream))},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := agents.Request{Model: "configured-model", Messages: []agents.Message{{Role: "user", Content: "hello"}}}
	completion, err := adapter.Stream(context.Background(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != "hello world" || len(completion.ToolCalls) != 1 ||
		completion.ToolCalls[0].ID != "call-a" ||
		completion.ToolCalls[0].Name != "refine_image" ||
		string(completion.ToolCalls[0].Arguments) != `{"prompt":"paint"}` {
		t.Fatalf("stream completion = %#v", completion)
	}
	exchanges := recorder.Exchanges()
	if len(exchanges) != 1 || !exchanges[0].Complete {
		t.Fatalf("stream capture = %#v", exchanges)
	}
	if exchanges[0].Response.Body.Transform != wirefixture.TransformNormalizedSSE {
		t.Fatalf("SSE transform = %q", exchanges[0].Response.Body.Transform)
	}
	if !strings.Contains(exchanges[0].Response.Body.SSE, `\"prompt\":\"paint\"`) {
		t.Fatalf("normalized SSE lost joined tool arguments: %s", exchanges[0].Response.Body.SSE)
	}
	if strings.Count(exchanges[0].Response.Body.SSE, `"call-a"`) != 1 {
		t.Fatalf("normalized SSE lost joined tool id: %s", exchanges[0].Response.Body.SSE)
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatalf("recorded SSE did not validate: %v", err)
	}

	replay, err := wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	replayAdapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL: "https://api.meshy.ai/v1", EndpointPath: "/chat/completions",
		AuthorizationName: "Authorization", AuthorizationType: "Bearer", APIKey: "secret",
		RequestTimeout: time.Minute, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 1 << 16,
		Client: &http.Client{Transport: replay},
	})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := replayAdapter.Stream(context.Background(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Content != "hello world" || len(replayed.ToolCalls) != 1 ||
		replayed.ToolCalls[0].ID != "call-a" ||
		replayed.ToolCalls[0].Name != "refine_image" ||
		string(replayed.ToolCalls[0].Arguments) != `{"prompt":"paint"}` {
		t.Fatalf("replayed completion = %#v", replayed)
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestSSEChoiceAndToolIndexesDoNotContaminate(t *testing.T) {
	policy := testPolicy()
	allowPolicy(&policy, []string{
		"prompt", "choices", "index", "delta", "content", "tool_calls", "id",
		"function", "name", "arguments",
	}, []string{
		"first choice", "second choice", "call-a", "call-b",
		"refine_image", "other_tool", "a", "b",
	})
	choice := func(index int, content, id, name, arguments string) map[string]any {
		return map[string]any{
			"index": index,
			"delta": map[string]any{
				"content": content,
				"tool_calls": []any{map[string]any{
					"index": 0, "id": id,
					"function": map[string]any{"name": name, "arguments": arguments},
				}},
			},
		}
	}
	stream := sseStream(t,
		map[string]any{"choices": []any{
			choice(0, "first ", "call-a", "refine_", `{"prompt":`),
			choice(1, "second ", "call-b", "other_", `{"prompt":`),
		}},
		map[string]any{"choices": []any{
			choice(0, "choice", "", "image", `"a"}`),
			choice(1, "choice", "", "tool", `"b"}`),
		}},
	)
	exchanges, _ := recordSSE(t, policy, stream)
	if len(exchanges) != 1 || !exchanges[0].Complete {
		t.Fatalf("multi-choice capture = %#v", exchanges)
	}
	text := exchanges[0].Response.Body.SSE
	for _, want := range []string{`"first choice"`, `"second choice"`, `"call-a"`, `"call-b"`, `"refine_image"`, `"other_tool"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	for _, wrong := range []string{`"refine_tool"`, `"other_image"`} {
		if strings.Contains(text, wrong) {
			t.Fatalf("choice/tool contamination %s in %s", wrong, text)
		}
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}
}

func TestSSEToolIndexesDoNotContaminate(t *testing.T) {
	policy := testPolicy()
	allowPolicy(&policy, []string{
		"prompt", "choices", "delta", "content", "tool_calls", "index", "id",
		"function", "name", "arguments",
	}, []string{"call-a", "other-b", "refine_image", "other_tool", "a", "b"})
	first := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
		"tool_calls": []any{
			map[string]any{"index": 0, "id": "call-", "function": map[string]any{"name": "refine_", "arguments": `{"prompt":`}},
			map[string]any{"index": 1, "id": "other-", "function": map[string]any{"name": "other_", "arguments": `{"prompt":`}},
		},
	}}}}
	second := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
		"tool_calls": []any{
			map[string]any{"index": 1, "id": "b", "function": map[string]any{"name": "tool", "arguments": `"b"}`}},
			map[string]any{"index": 0, "id": "a", "function": map[string]any{"name": "image", "arguments": `"a"}`}},
		},
	}}}}
	exchanges, _ := recordSSE(t, policy, sseStream(t, first, second))
	if len(exchanges) != 1 || !exchanges[0].Complete {
		t.Fatalf("tool-index capture = %#v", exchanges)
	}
	text := exchanges[0].Response.Body.SSE
	for _, want := range []string{`"call-a"`, `"other-b"`, `"refine_image"`, `"other_tool"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	for _, id := range []string{`"call-a"`, `"other-b"`} {
		if strings.Count(text, id) != 1 {
			t.Fatalf("tool id %s not normalized once: %s", id, text)
		}
	}
	for _, wrong := range []string{`"refine_tool"`, `"other_image"`} {
		if strings.Contains(text, wrong) {
			t.Fatalf("tool-index contamination %s in %s", wrong, text)
		}
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}
}

func TestSSEApprovedURLAliasPreserved(t *testing.T) {
	policy := testPolicy()
	allowPolicy(&policy, []string{"choices", "delta", "content"}, nil)
	rawURL := "https://api.meshy.ai/file?token=secret"
	policy.Aliases[rawURL] = "https://example.test/file"
	stream := sseStream(t,
		map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": rawURL}}}},
	)
	exchanges, _ := recordSSE(t, policy, stream)
	if len(exchanges) != 1 || !exchanges[0].Complete {
		t.Fatalf("SSE URL alias capture = %#v", exchanges)
	}
	text := exchanges[0].Response.Body.SSE
	if strings.Contains(text, rawURL) || !strings.Contains(text, "https://example.test/file") {
		t.Fatalf("SSE URL alias lost: %s", text)
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}
}

func TestSSEToolArgumentsSignedURLAlias(t *testing.T) {
	newPolicy := func() wirefixture.Policy {
		policy := testPolicy()
		allowPolicy(&policy, []string{
			"choices", "delta", "tool_calls", "index", "id", "function", "name", "arguments", "url",
		}, []string{"call-a", "refine_image"})
		return policy
	}
	rawURL := "https://api.meshy.ai/file?token=secret"
	splitURL := sseStream(t,
		toolCallChunk(0, "call-a", "refine_image", `{"url":"https://api.meshy.ai/file?`),
		toolCallChunk(0, "", "", `token=secret"}`),
	)

	t.Run("registered alias", func(t *testing.T) {
		policy := newPolicy()
		exchanges, _ := recordSSE(t, policy, splitURL)
		if len(exchanges) != 1 || !exchanges[0].Complete {
			t.Fatalf("signed URL alias capture: %#v", exchanges)
		}
		text := exchanges[0].Response.Body.SSE
		if !strings.Contains(text, "https://example.test/file") ||
			strings.Contains(text, "api.meshy.ai") || strings.Contains(text, "token=secret") {
			t.Fatalf("signed URL alias not applied: %s", text)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unregistered URL", func(t *testing.T) {
		policy := newPolicy()
		delete(policy.Aliases, rawURL)
		exchanges, _ := recordSSE(t, policy, splitURL)
		if len(exchanges) != 1 || exchanges[0].Complete || !strings.Contains(exchanges[0].Invalid, "not approved") {
			t.Fatalf("unregistered URL accepted: %#v", exchanges)
		}
		if strings.Contains(exchanges[0].Invalid, "api.meshy.ai") {
			t.Fatalf("unregistered URL exposed: %+v", exchanges[0])
		}
	})

	t.Run("unknown key", func(t *testing.T) {
		policy := newPolicy()
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			toolCallChunk(0, "call-a", "refine_image", `{"unexpected":"synthetic result"}`)))
		if len(exchanges) != 1 || exchanges[0].Complete || !strings.Contains(exchanges[0].Invalid, "unknown JSON key") {
			t.Fatalf("unknown argument key accepted: %#v", exchanges)
		}
	})

	t.Run("unapproved value", func(t *testing.T) {
		policy := newPolicy()
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			toolCallChunk(0, "call-a", "refine_image", `{"url":"private value"}`)))
		if len(exchanges) != 1 || exchanges[0].Complete || !strings.Contains(exchanges[0].Invalid, "not approved") {
			t.Fatalf("unapproved argument value accepted: %#v", exchanges)
		}
	})

	t.Run("types preserved", func(t *testing.T) {
		policy := newPolicy()
		allowPolicy(&policy, []string{"n", "b"}, nil)
		exchanges, _ := recordSSE(t, policy, sseStream(t,
			toolCallChunk(0, "call-a", "refine_image", `{"n":1.5,"b":true}`)))
		if len(exchanges) != 1 || !exchanges[0].Complete {
			t.Fatalf("typed arguments rejected: %#v", exchanges)
		}
		text := exchanges[0].Response.Body.SSE
		if !strings.Contains(text, `\"n\":1.5`) || !strings.Contains(text, `\"b\":true`) {
			t.Fatalf("argument types changed: %s", text)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSSEJoinedToolIDApproval(t *testing.T) {
	keys := []string{
		"choices", "delta", "tool_calls", "index", "id", "function", "name", "arguments",
	}
	splitID := sseStream(t,
		toolCallChunk(0, "call-", "refine_", `{"prompt":`),
		toolCallChunk(0, "a", "image", `"paint"}`),
	)

	t.Run("joined ID approved", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, keys, []string{"call-a", "refine_image", "paint"})
		exchanges, _ := recordSSE(t, policy, splitID)
		if len(exchanges) != 1 || !exchanges[0].Complete {
			t.Fatalf("joined tool id rejected: %#v", exchanges)
		}
		text := exchanges[0].Response.Body.SSE
		if strings.Count(text, `"call-a"`) != 1 || !strings.Contains(text, `"refine_image"`) {
			t.Fatalf("joined tool id not normalized once: %s", text)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("fragment approvals rejected", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, keys, []string{"call-", "a", "refine_image", "paint"})
		exchanges, _ := recordSSE(t, policy, splitID)
		if len(exchanges) != 1 || exchanges[0].Complete || !strings.Contains(exchanges[0].Invalid, "not approved") {
			t.Fatalf("fragment approvals authorized joined id: %#v", exchanges)
		}
		if err := wirefixture.Validate(exchanges, policy); err == nil {
			t.Fatal("unapproved joined tool id fixture validated")
		}
	})

	t.Run("ID alias after join", func(t *testing.T) {
		policy := testPolicy()
		allowPolicy(&policy, keys, []string{"refine_image", "paint"})
		aliasStream := sseStream(t,
			toolCallChunk(0, "call_", "refine_", `{"prompt":`),
			toolCallChunk(0, "live", "image", `"paint"}`),
		)
		exchanges, _ := recordSSE(t, policy, aliasStream)
		if len(exchanges) != 1 || !exchanges[0].Complete {
			t.Fatalf("joined ID alias rejected: %#v", exchanges)
		}
		text := exchanges[0].Response.Body.SSE
		if strings.Count(text, `"call-1"`) != 1 || strings.Contains(text, "call_live") {
			t.Fatalf("joined ID alias not applied: %s", text)
		}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSSECrossFragmentSecretsRejectOrRedact(t *testing.T) {
	toolKeys := []string{"choices", "delta", "tool_calls", "index", "id", "function", "name", "arguments"}
	cases := []struct {
		name        string
		policy      func() wirefixture.Policy
		stream      string
		complete    bool
		wantInvalid string
	}{
		{
			name: "content password",
			policy: func() wirefixture.Policy {
				p := testPolicy()
				allowPolicy(&p, []string{"choices", "delta", "content"}, []string{"password=", "hunter2"})
				return p
			},
			stream: sseStream(t,
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "password="}}}},
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "hunter2"}}}},
			),
			wantInvalid: "credential",
		},
		{
			name: "reasoning sk",
			policy: func() wirefixture.Policy {
				p := testPolicy()
				allowPolicy(&p, []string{"choices", "delta", "reasoning_content"}, []string{"sk-", "abcdefghijkl"})
				return p
			},
			stream: sseStream(t,
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": "sk-"}}}},
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": "abcdefghijkl"}}}},
			),
			wantInvalid: "credential",
		},
		{
			name: "tool arguments password",
			policy: func() wirefixture.Policy {
				p := testPolicy()
				allowPolicy(&p, append(toolKeys, "password"), []string{"call-a", "refine_image"})
				return p
			},
			stream: sseStream(t,
				toolCallChunk(0, "call-a", "refine_image", `{"password":`),
				toolCallChunk(0, "", "", `"hunter2"}`),
			),
			complete: true,
		},
		{
			name: "tool arguments query split",
			policy: func() wirefixture.Policy {
				p := testPolicy()
				allowPolicy(&p, append(toolKeys, "url"), []string{"call-a", "refine_image"})
				return p
			},
			stream: sseStream(t,
				toolCallChunk(0, "call-a", "refine_image", `{"url":"https://`),
				toolCallChunk(0, "", "", `provider.example/x?token=1"}`),
			),
			wantInvalid: "alias required",
		},
		{
			name: "tool arguments non-public IP split",
			policy: func() wirefixture.Policy {
				p := testPolicy()
				allowPolicy(&p, append(toolKeys, "url"), []string{"call-a", "refine_image"})
				return p
			},
			stream: sseStream(t,
				toolCallChunk(0, "call-a", "refine_image", `{"url":"100.`),
				toolCallChunk(0, "", "", `64.0.1"}`),
			),
			wantInvalid: "private host",
		},
		{
			name: "content query split",
			policy: func() wirefixture.Policy {
				p := testPolicy()
				allowPolicy(&p, []string{"choices", "delta", "content"}, []string{"https://", "provider.example/x?token=1"})
				return p
			},
			stream: sseStream(t,
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "https://"}}}},
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "provider.example/x?token=1"}}}},
			),
			wantInvalid: "URL",
		},
		{
			name: "content non-public IP split",
			policy: func() wirefixture.Policy {
				p := testPolicy()
				allowPolicy(&p, []string{"choices", "delta", "content"}, []string{"100.", "64.0.1"})
				return p
			},
			stream: sseStream(t,
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "100."}}}},
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "64.0.1"}}}},
			),
			wantInvalid: "private host",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := tc.policy()
			exchanges, _ := recordSSE(t, policy, tc.stream)
			if len(exchanges) != 1 {
				t.Fatalf("exchanges = %d", len(exchanges))
			}
			exchange := exchanges[0]
			if exchange.Complete != tc.complete {
				t.Fatalf("complete = %v, want %v: %+v", exchange.Complete, tc.complete, exchange)
			}
			if tc.complete {
				if err := wirefixture.Validate(exchanges, policy); err != nil {
					t.Fatalf("redacted fixture did not validate: %v", err)
				}
				if !strings.Contains(exchange.Response.Body.SSE, "[redacted]") {
					t.Fatalf("credential not redacted: %s", exchange.Response.Body.SSE)
				}
			} else {
				if exchange.Invalid == "" || !strings.Contains(exchange.Invalid, tc.wantInvalid) {
					t.Fatalf("unexpected failure reason: %+v", exchange)
				}
				if strings.Contains(exchange.Invalid, "tool id") || strings.Contains(exchange.Invalid, "tool name") {
					t.Fatalf("failure was incidental tool field concatenation: %+v", exchange)
				}
				if err := wirefixture.Validate(exchanges, policy); err == nil {
					t.Fatal("cross-fragment secret fixture validated")
				}
			}
			for _, raw := range []string{"hunter2", "sk-", "100.64", "provider.example", "token=1"} {
				if strings.Contains(exchange.Invalid, raw) || strings.Contains(string(exchange.Request.Body.JSON), raw) ||
					strings.Contains(exchange.Response.Body.SSE, raw) {
					t.Fatalf("raw secret %q exposed: %+v", raw, exchange)
				}
			}
		})
	}
}

func TestSSEFragmentsNeedJoinedApproval(t *testing.T) {
	policy := testPolicy()
	allowPolicy(&policy, []string{"choices", "delta", "content"}, []string{"synthetic ", "result"})
	delete(policy.Allowed, "synthetic result")
	stream := sseStream(t,
		map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "synthetic "}}}},
		map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "result"}}}},
	)
	exchanges, _ := recordSSE(t, policy, stream)
	if len(exchanges) != 1 || exchanges[0].Complete || exchanges[0].Invalid == "" {
		t.Fatalf("piece approvals accepted joined value: %#v", exchanges)
	}
}

func TestBracketTextNeedsExactApprovalAndDeepChecks(t *testing.T) {
	recordMessage := func(t *testing.T, policy wirefixture.Policy, message string) wirefixture.Exchange {
		t.Helper()
		body, err := json.Marshal(map[string]string{"message": message})
		if err != nil {
			t.Fatal(err)
		}
		recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
		request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", string(body))
		_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
		return recorder.Exchanges()[0]
	}

	t.Run("unapproved bracket text", func(t *testing.T) {
		exchange := recordMessage(t, testPolicy(), "[Image result]")
		if exchange.Complete || exchange.Invalid == "" {
			t.Fatalf("unapproved bracket text accepted: %+v", exchange)
		}
	})

	t.Run("allowed valid nested JSON", func(t *testing.T) {
		policy := testPolicy()
		nested := `{"unknown":"synthetic result"}`
		policy.Allowed[nested] = true
		exchange := recordMessage(t, policy, nested)
		if exchange.Complete || !strings.Contains(exchange.Invalid, "unknown JSON key") {
			t.Fatalf("allowed nested JSON bypassed deep checks: %+v", exchange)
		}
	})

	t.Run("allowed encoded credential", func(t *testing.T) {
		policy := testPolicy()
		policy.Keys["arguments"] = true
		policy.Keys["token"] = true
		nested := `{"arguments":"{\"token\":\"abc\"}"}`
		policy.Allowed[nested] = true
		exchange := recordMessage(t, policy, nested)
		if !exchange.Complete || strings.Contains(string(exchange.Request.Body.JSON), "abc") ||
			!strings.Contains(string(exchange.Request.Body.JSON), "[redacted]") {
			t.Fatalf("encoded credential not checked: %+v", exchange)
		}
		if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("allowed malformed JSON-like URL", func(t *testing.T) {
		policy := testPolicy()
		malformed := `{"url":"https://provider.example/x?token=1"`
		policy.Allowed[malformed] = true
		exchange := recordMessage(t, policy, malformed)
		if exchange.Complete || !strings.Contains(exchange.Invalid, "nested JSON") {
			t.Fatalf("allowed malformed URL accepted: %+v", exchange)
		}
		if strings.Contains(exchange.Invalid, "provider.example") {
			t.Fatalf("malformed URL exposed: %+v", exchange)
		}
	})
}

func TestAnySchemeURLAndCredentialFields(t *testing.T) {
	policy := testPolicy()
	policy.Keys["url"] = true
	rawS3 := "s3://bucket/key?X-Amz-Signature=abc"
	policy.Allowed[rawS3] = true
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"url":"`+rawS3+`"}`)
	_, body := capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
	if string(body) != "{}" {
		t.Fatalf("business response changed: %q", body)
	}
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || exchange.Invalid == "" {
		t.Fatalf("s3 URL accepted: %+v", exchange)
	}
	if strings.Contains(exchange.Invalid, rawS3) || strings.Contains(string(exchange.Request.Body.JSON), rawS3) {
		t.Fatalf("s3 URL exposed: %+v", exchange)
	}

	for _, tc := range []struct {
		name  string
		key   string
		body  string
		alias bool
	}{
		{"signed_url redacted", "signed_url", `{"signed_url":"s3://bucket/key"}`, false},
		{"auth_token redacted", "auth_token", `{"auth_token":"abc"}`, false},
		{"x-amz-signature redacted", "x-amz-signature", `{"x-amz-signature":"abc"}`, false},
		{"secret_access_key redacted", "secret_access_key", `{"secret_access_key":"abc"}`, false},
		{"signed_url alias", "signed_url", `{"signed_url":"https://signed.example/file?x=1"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := testPolicy()
			current.Keys[tc.key] = true
			if tc.alias {
				current.Aliases["https://signed.example/file?x=1"] = "https://example.test/file"
			}
			recorder := &wirefixture.Recorder{Policy: current, Default: testMetadata()}
			request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", tc.body)
			_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
			exchange := recorder.Exchanges()[0]
			if !exchange.Complete {
				t.Fatalf("sensitive field was not redacted safely: %+v", exchange)
			}
			text := string(exchange.Request.Body.JSON)
			if tc.alias {
				if !strings.Contains(text, "https://example.test/file") || strings.Contains(text, "signed.example") {
					t.Fatalf("approved signed URL alias lost: %s", text)
				}
				return
			}
			if !strings.Contains(text, "[redacted]") || strings.Contains(text, "s3://") ||
				strings.Contains(text, `"abc"`) {
				t.Fatalf("sensitive field leaked: %s", text)
			}
		})
	}
}

func TestNonPublicIPPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{"zero", "0.0.0.1", false},
		{"2001 aggregate", "2001::1", false},
		{"documentation v6", "2001:db8::1", false},
		{"6to4", "2002::1", false},
		{"documentation prefix", "3fff::1", false},
		{"NAT64", "64:ff9b::1", false},
		{"public IPv6", "2001:4860::8888", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := testPolicy()
			policy.Allowed[tc.value] = true
			recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
			request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/tasks", `{"message":"`+tc.value+`"}`)
			_, _ = capture(t, recorder, request, jsonResponse(200, "application/json", `{}`))
			exchange := recorder.Exchanges()[0]
			if exchange.Complete != tc.valid {
				t.Fatalf("IP complete=%v invalid=%s", exchange.Complete, exchange.Invalid)
			}
		})
	}
}

func TestRedirectResponseIsUnsupported(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
	responseBody := `{"result":"synthetic result"}`
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{
				"Content-Type": {"application/json"},
				"Location":     {"https://provider.example/next?token=secret"},
			},
			Body:          io.NopCloser(strings.NewReader(responseBody)),
			ContentLength: int64(len(responseBody)),
		}, nil
	})
	_, body := capture(t, recorder, request, base)
	if string(body) != responseBody {
		t.Fatalf("business response changed: %s", body)
	}
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || !strings.Contains(exchange.Invalid, "unsupported 3xx") {
		t.Fatalf("redirect accepted: %+v", exchange)
	}
	if exchange.Response.Headers["location"] != "" {
		t.Fatalf("redirect Location exposed: %+v", exchange.Response.Headers)
	}
	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); !errors.Is(err, wirefixture.ErrUnsupportedBody) {
		t.Fatalf("redirect validation error = %v", err)
	}
}
