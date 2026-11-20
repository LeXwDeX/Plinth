package openairesponses_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// both runs a request through Stream and Complete: a refusal reads the same on
// both.
func both(t *testing.T, adapter *openairesponses.Adapter, request agents.Request) map[string]error {
	t.Helper()
	_, streamErr := adapter.Stream(ctx, request, nil)
	_, completeErr := adapter.Complete(ctx, request)
	return map[string]error{"stream": streamErr, "complete": completeErr}
}

// #401: the two refusals the real gateway answered in the probe of 2026-11-20
// (the fake answers them byte for byte) name the refused field in param, and
// the Adapter tells them as agents.RequestRejection with that field, the status
// and the upstream's wording kept for the log. The test sends what the shipped
// profile never sends: the temperature, and a level the model does not know.
func TestAnObservedRefusalOfAFieldIsARejectedParameter(t *testing.T) {
	s := newStack(t)
	for name, test := range map[string]struct {
		options openairesponses.Options
		level   string
		param   string
		wording string
	}{
		"temperature": {openairesponses.Options{}, "", "temperature", "Unsupported parameter: 'temperature' is not supported with this model."},
		"unknown level": {openairesponses.Options{OmitTemperature: true, EffortLevels: []string{"bogus"}, DefaultEffort: "bogus"}, "bogus", "reasoning.effort",
			"Invalid value: 'bogus'. Supported values are: 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', and 'max'."},
		// Observed: the gateway knows none and minimal and the model does not
		// support them, which is not the Invalid value of an unknown level.
		"none": {openairesponses.Options{OmitTemperature: true, EffortLevels: []string{"none"}, DefaultEffort: "none"}, "none", "reasoning.effort",
			"Unsupported value: 'none' is not supported with the 'gpt-6.2-astra-2026-11-17' model. Supported values are: 'low', 'medium', 'high', 'xhigh', and 'max'."},
		"minimal": {openairesponses.Options{OmitTemperature: true, EffortLevels: []string{"minimal"}, DefaultEffort: "minimal"}, "minimal", "reasoning.effort",
			"Unsupported value: 'minimal' is not supported with the 'gpt-6.2-astra-2026-11-17' model. Supported values are: 'low', 'medium', 'high', 'xhigh', and 'max'."},
	} {
		options := test.options
		adapter := s.adapter(func(config *openairesponses.Config) {
			config.OptionsFor = func(string) *openairesponses.Options { return &options }
		})
		for path, err := range both(t, adapter, agents.Request{Model: "gpt-astra", ReasoningEffort: test.level, Messages: userHello}) {
			param, rejected := agents.RequestRejectedParam(err)
			var status *agents.StatusError
			if !rejected || param != test.param || !errors.Is(err, agents.ErrRequestRejected) ||
				!errors.As(err, &status) || status.Status != http.StatusBadRequest || !strings.Contains(err.Error(), test.wording) {
				t.Errorf("%s / %s: param=%q rejected=%t err=%v", name, path, param, rejected, err)
			}
			if errors.Is(err, agents.ErrContentRejected) || errors.Is(err, agents.ErrImagesRejected) || errors.Is(err, agents.ErrQuotaExhausted) {
				t.Errorf("%s / %s: misread refusal: %v", name, path, err)
			}
		}
	}
}

// Every other refusal keeps the class the Chat Completions Adapter gives it:
// the plain status error, a used-up quota the Schema declares, and a vendor
// content-safety refusal.
func TestOtherRefusalsKeepTheirClasses(t *testing.T) {
	s := newStack(t)
	classify := func(status int, body []byte) (string, bool) {
		return "credential_quota", status == http.StatusTooManyRequests && strings.Contains(string(body), `"code":"quota_exceeded"`)
	}
	for name, test := range map[string]struct {
		key       string
		classify  func(int, []byte) (string, bool)
		status    int
		exhausted bool
	}{
		"rate limit":            {"fake-rate-limit-1", classify, http.StatusTooManyRequests, false},
		"unavailable":           {"fake-service-unavailable-1", classify, http.StatusServiceUnavailable, false},
		"quota with a mapping":  {"fake-quota-exceeded-1", classify, http.StatusTooManyRequests, true},
		"quota without mapping": {"fake-quota-exceeded-1", nil, http.StatusTooManyRequests, false},
		"invalid key":           {"fake-invalid-key-1", classify, http.StatusUnauthorized, false},
		"no key":                {"", classify, http.StatusUnauthorized, false},
		"unknown model":         {"fake-model-not-found-1", classify, http.StatusNotFound, false},
	} {
		adapter := s.adapter(func(config *openairesponses.Config) {
			config.APIKey, config.ExhaustedQuota = test.key, test.classify
		})
		for path, err := range both(t, adapter, agents.Request{Model: "gpt-astra", Messages: userHello}) {
			var status *agents.StatusError
			if !errors.As(err, &status) || status.Status != test.status || errors.Is(err, agents.ErrQuotaExhausted) != test.exhausted ||
				errors.Is(err, agents.ErrRequestRejected) || errors.Is(err, agents.ErrContentRejected) {
				t.Errorf("%s / %s: %v", name, path, err)
			}
		}
	}
	adapter, _ := scripted(t, http.StatusBadRequest, "application/json",
		`{"error":{"code":"content_policy_violation","type":"invalid_request_error","param":"input","message":"safety_violations=[sexual]"}}`)
	for path, err := range both(t, adapter, agents.Request{Model: "m", Messages: userHello}) {
		if !errors.Is(err, agents.ErrContentRejected) || errors.Is(err, agents.ErrRequestRejected) ||
			len(agents.ContentRejectedReasons(err)) != 1 || agents.ContentRejectedReasons(err)[0] != "sexual" {
			t.Errorf("moderation / %s: %v", path, err)
		}
	}
}

// A response that failed on its own, in the status of a 200 stream, an error
// event or a failed response object, is an error without an HTTP status, and a
// content-safety or used-up-quota failure is told as on a refusal. Per the
// documents: the probes saw none of these.
func TestAFailedResponseIsAnError(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	for name, marker := range map[string]string{"response.failed": openaiv1.FailedMarker, "error event": openaiv1.StreamErrorMarker} {
		request := agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("go " + marker)}}
		_, err := adapter.Stream(ctx, request, nil)
		var status *agents.StatusError
		if err == nil || errors.As(err, &status) || !strings.Contains(err.Error(), "agent response failed") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The failed response object on the non-stream path.
	if _, err := adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("go " + openaiv1.FailedMarker)}}); err == nil ||
		!strings.Contains(err.Error(), "server_error") {
		t.Errorf("failed object: %v", err)
	}
	if _, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("go " + openaiv1.StreamErrorMarker)}}, nil); err == nil ||
		!strings.Contains(err.Error(), "server_is_overloaded") {
		t.Errorf("error event: %v", err)
	}

	classify := func(_ int, body []byte) (string, bool) {
		return "insufficient_balance", strings.Contains(string(body), `"code":"insufficient_quota"`)
	}
	for name, body := range map[string]string{
		"failed, content_filter": frames("response.failed",
			`{"type":"response.failed","response":{"status":"failed","error":{"code":"content_filter","message":"blocked: \"sexual\":{\"filtered\":true}"}}}`),
		"error event, content_policy_violation": frames("error",
			`{"type":"error","code":"content_policy_violation","message":"blocked","param":null}`),
		"nested error, content_filter": frames("error",
			`{"type":"error","error":{"type":"invalid_request_error","code":"content_filter","message":"blocked"}}`),
		"incomplete by the content filter": frames("response.incomplete",
			`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}}`),
	} {
		scriptedAdapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
		_, err := scriptedAdapter.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
		if !errors.Is(err, agents.ErrContentRejected) || len(agents.ContentRejectedReasons(err)) == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, body := range map[string]string{
		"failed": frames("response.failed", `{"type":"response.failed","response":{"status":"failed","error":{"code":"insufficient_quota","message":"no credit"}}}`),
		"error":  frames("error", `{"type":"error","code":"insufficient_quota","message":"no credit"}`),
	} {
		withClassifier := scriptedWith(t, body, classify)
		_, err := withClassifier.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
		var exhaustion *agents.QuotaExhaustion
		if !errors.As(err, &exhaustion) || exhaustion.Class != "insufficient_balance" || !errors.Is(err, agents.ErrQuotaExhausted) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The non-stream bodies of the same failures.
	for name, body := range map[string]string{
		"failed status": `{"status":"failed","error":{"code":"content_filter","message":"blocked"},"output":[]}`,
		"bare error":    `{"error":{"code":"content_filter","message":"blocked"}}`,
		"cut by filter": `{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}`,
	} {
		scriptedAdapter, _ := scripted(t, http.StatusOK, "application/json", body)
		if _, err := scriptedAdapter.Complete(ctx, agents.Request{Model: "m", Messages: userHello}); !errors.Is(err, agents.ErrContentRejected) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, body := range map[string]string{
		"queued":     `{"status":"queued","output":[]}`,
		"cancelled":  `{"status":"cancelled","output":[]}`,
		"no output":  `{}`,
		"not a body": `[]`,
	} {
		scriptedAdapter, _ := scripted(t, http.StatusOK, "application/json", body)
		if _, err := scriptedAdapter.Complete(ctx, agents.Request{Model: "m", Messages: userHello}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// scriptedWith is scripted with a quota classifier.
func scriptedWith(t *testing.T, sse string, classify func(int, []byte) (string, bool)) *openairesponses.Adapter {
	t.Helper()
	adapter, _ := scriptedConfigured(t, http.StatusOK, "text/event-stream", sse, func(config *openairesponses.Config) {
		config.ExhaustedQuota = classify
	})
	return adapter
}

// A cut answer (response.incomplete, max_output_tokens) returns what it has, as
// a Chat Completions reply that stopped on length does, with the usage; a call
// the model did not finish is dropped, because half its arguments must not run.
func TestACutResponseReturnsItsPartialAnswerAndDropsUnfinishedCalls(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	withTool := func(marker string) agents.Request {
		return agents.Request{Model: "gpt-astra", Tools: lookup, Messages: []agents.Message{userMessage("go " +
			openaiv1.ToolMarkerFor("lookup_note", `{"topic":"a long topic name"}`) + marker)}}
	}
	partial := openaiv1.FixedReply[:len(openaiv1.FixedReply)/2]
	for name, call := range map[string]func() (agents.Completion, error){
		"stream text": func() (agents.Completion, error) {
			return adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("go " + openaiv1.IncompleteMarker)}}, nil)
		},
		"complete text": func() (agents.Completion, error) {
			return adapter.Complete(ctx, agents.Request{Model: "gpt-astra", Messages: []agents.Message{userMessage("go " + openaiv1.IncompleteMarker)}})
		},
	} {
		completion, err := call()
		if err != nil || completion.Content != partial || len(completion.ToolCalls) != 0 || completion.Usage == nil {
			t.Errorf("%s: %#v %v", name, completion, err)
		}
	}
	for name, call := range map[string]func() (agents.Completion, error){
		"stream call": func() (agents.Completion, error) {
			return adapter.Stream(ctx, withTool(openaiv1.IncompleteMarker), nil)
		},
		"complete call": func() (agents.Completion, error) { return adapter.Complete(ctx, withTool(openaiv1.IncompleteMarker)) },
	} {
		completion, err := call()
		if err != nil || len(completion.ToolCalls) != 0 || completion.Content != "" {
			t.Errorf("%s: %#v %v", name, completion, err)
		}
	}
	// One call finished and one cut: the finished one is kept, in order.
	body := frames(
		"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"c0","name":"first","arguments":""}}`,
		"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"a\":1}"}`,
		"response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"a\":1}"}`,
		"response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"c1","name":"second","arguments":""}}`,
		"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"b\""}`,
		"response.incomplete", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":5,"output_tokens":7}}}`)
	scriptedAdapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
	completion, err := scriptedAdapter.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
	if err != nil || len(completion.ToolCalls) != 1 || completion.ToolCalls[0].ID != "c0" || string(completion.ToolCalls[0].Arguments) != `{"a":1}` ||
		completion.Usage == nil || completion.Usage.CompletionTokens != 7 {
		t.Fatalf("completion = %#v %v", completion, err)
	}
}

// An item whose own status says it was cut short is not a whole call even in a
// response that reports itself completed: its arguments are never run.
func TestACallThatIsIncompleteOnItsOwnIsDropped(t *testing.T) {
	body := frames("response.completed", `{"type":"response.completed","response":{"status":"completed","output":[`+
		`{"type":"function_call","call_id":"c0","name":"whole","arguments":"{\"a\":1}","status":"completed"},`+
		`{"type":"function_call","call_id":"c1","name":"cut","arguments":"{\"b\"","status":"incomplete"}]}}`)
	adapter, _ := scripted(t, http.StatusOK, "text/event-stream", body)
	completion, err := adapter.Stream(ctx, agents.Request{Model: "m", Messages: userHello}, nil)
	if err != nil || len(completion.ToolCalls) != 1 || completion.ToolCalls[0].ID != "c0" {
		t.Fatalf("stream: %#v %v", completion, err)
	}
	complete, _ := scripted(t, http.StatusOK, "application/json", `{"status":"completed","output":[`+
		`{"type":"function_call","call_id":"c1","name":"cut","arguments":"{\"b\"","status":"incomplete"}]}`)
	if completion, err := complete.Complete(ctx, agents.Request{Model: "m", Messages: userHello}); err != nil || len(completion.ToolCalls) != 0 {
		t.Fatalf("complete: %#v %v", completion, err)
	}
}

// A request is refused before it is sent for a role the wire has no item for,
// and emit's own error stops the stream and is the error returned.
func TestRequestAndCallbackErrors(t *testing.T) {
	s := newStack(t)
	adapter := s.adapter(nil)
	if _, err := adapter.Complete(ctx, agents.Request{Model: "m", Messages: []agents.Message{{Role: "narrator", Content: "x"}}}); err == nil ||
		!strings.Contains(err.Error(), "unsupported message role") || s.recorder.count() != 0 {
		t.Fatalf("role: %v (requests %d)", err, s.recorder.count())
	}
	boom := errors.New("client went away")
	_, err := adapter.Stream(ctx, agents.Request{Model: "gpt-astra", Messages: userHello}, func(agents.Delta) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("emit error = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := adapter.Stream(canceled, agents.Request{Model: "gpt-astra", Messages: userHello}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %v", err)
	}
	for name, config := range map[string]openairesponses.Config{
		"no base url": {EndpointPath: "/responses", AuthorizationName: "A", AuthorizationType: "B", RequestTimeout: 1, ScannerMaxBytes: 1, ErrorBodyMaxBytes: 1},
		"no endpoint": {BaseURL: "http://x", AuthorizationName: "A", AuthorizationType: "B", RequestTimeout: 1, ScannerMaxBytes: 1, ErrorBodyMaxBytes: 1},
		"no limits":   {BaseURL: "http://x", EndpointPath: "/responses", AuthorizationName: "A", AuthorizationType: "B"},
		"no scheme":   {BaseURL: "http://x", EndpointPath: "/responses", AuthorizationName: "A", RequestTimeout: 1, ScannerMaxBytes: 1, ErrorBodyMaxBytes: 1},
		"no timeout":  {BaseURL: "http://x", EndpointPath: "/responses", AuthorizationName: "A", AuthorizationType: "B", ScannerMaxBytes: 1, ErrorBodyMaxBytes: 1},
	} {
		if _, err := openairesponses.New(config); err == nil {
			t.Errorf("%s: an incomplete config was accepted", name)
		}
	}
}
