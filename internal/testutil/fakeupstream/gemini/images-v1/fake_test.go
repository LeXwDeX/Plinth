package geminiimagesv1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

const key = "test-key"

func newFixture(t *testing.T, config fakeupstream.Config) *testkit.Fixture {
	t.Helper()
	return testkit.New(t, config, Version())
}

func textRequest() map[string]any {
	return requestBody([]any{map[string]any{"type": "text", "text": "a cube"}})
}

func editRequest() map[string]any {
	return requestBody([]any{
		map[string]any{"type": "text", "text": "make it blue"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
	})
}

func complete(f *testkit.Fixture, body map[string]any, headers ...string) (int, map[string]any) {
	f.T.Helper()
	status, _, decoded := f.Do("POST", Prefix+"/chat/completions", key, body, headers...)
	return status, decoded
}

// picture decodes the data URI of a success body.
func picture(t *testing.T, body map[string]any) (mediaType string, data []byte) {
	t.Helper()
	uri, _ := testkit.Field(t, body, "choices", 0, "message", "content", 0, "image_url", "url").(string)
	header, encoded, ok := strings.Cut(uri, ";base64,")
	if !ok || !strings.HasPrefix(header, "data:image/") {
		t.Fatalf("image_url.url is not an image data URI: %.40s", uri)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		t.Fatalf("the data URI holds no bytes: %v", err)
	}
	return strings.TrimPrefix(header, "data:"), data
}

func TestGenerationAndEditDeliverAValidDataURIPicture(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, request := range map[string]map[string]any{"text-to-image": textRequest(), "image-to-image": editRequest()} {
		status, body := complete(f, request)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %v", name, status, body)
		}
		mediaType, data := picture(t, body)
		if err := testkit.DeliveryErr(t, data, "image", "", mediaType); err != nil {
			t.Fatalf("%s: picture fails the delivery check: %v", name, err)
		}
		if body["model"] != Model || testkit.Field(t, body, "choices", 0, "message", "reasoning_content") != FixedReasoning {
			t.Fatalf("%s: model or reasoning not fixed: %v", name, body)
		}
	}
	// A request with a reference picture gets the edit structure (service tier, picture tokens).
	_, edited := complete(f, editRequest())
	if _, has := edited["service_tier"]; !has {
		t.Fatalf("an edit response should report service_tier: %v", edited)
	}
	_, plain := complete(f, textRequest())
	if _, has := plain["service_tier"]; has {
		t.Fatalf("a text-to-image response must not report service_tier: %v", plain)
	}
}

func TestRequestValidationAndAuthentication(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, header, _ := f.Raw("POST", Prefix+"/chat/completions", "", textRequest())
	if status != http.StatusUnauthorized || !strings.HasPrefix(header.Get("Content-Type"), "text/plain") {
		t.Fatalf("no key: %d %s", status, header.Get("Content-Type"))
	}
	if status, body := complete(f, map[string]any{"model": Model}); status != http.StatusBadRequest || testkit.Field(t, body, "error", "code") != "invalid_text_request" {
		t.Fatalf("missing messages: %d %v", status, body)
	}
	if status, _ := complete(f, map[string]any{"messages": []any{}}); status != http.StatusBadRequest {
		t.Fatalf("missing model: %d", status)
	}
}

func TestFaultScenarios(t *testing.T) {
	short := fakeupstream.Config{TimeoutHold: 50 * time.Millisecond}
	for scenario, want := range map[string]struct {
		status int
		code   string
	}{
		"insufficient-balance": {http.StatusPaymentRequired, "payment_required"},
		"key-quota":            {http.StatusPaymentRequired, "insufficient_quota"},
		"rate-limit":           {http.StatusTooManyRequests, "rate_limit_exceeded"},
		"server-error":         {http.StatusInternalServerError, "api_error"},
		"task-failed":          {http.StatusBadRequest, "content_policy_violation"},
		"forbidden":            {http.StatusForbidden, "forbidden"},
		"permission-denied":    {http.StatusForbidden, "permission_denied"},
		"model-not-found":      {http.StatusNotFound, "model_not_found"},
		"service-unavailable":  {http.StatusServiceUnavailable, "service_unavailable"},
		"quota-exceeded":       {http.StatusTooManyRequests, "quota_exceeded"},
	} {
		t.Run(scenario, func(t *testing.T) {
			status, body := complete(newFixture(t, short), textRequest(), "X-Fake-Scenario", scenario)
			if status != want.status || testkit.Field(t, body, "error", "code") != want.code {
				t.Fatalf("%d %v", status, body)
			}
		})
	}
	t.Run("rate limit has Retry-After", func(t *testing.T) {
		_, header, _ := newFixture(t, short).Raw("POST", Prefix+"/chat/completions", key, textRequest(), "X-Fake-Scenario", "rate-limit")
		if header.Get("Retry-After") != "1" {
			t.Fatalf("Retry-After = %q", header.Get("Retry-After"))
		}
	})
	t.Run("the quota answer is not a rate limit", func(t *testing.T) {
		status, header, body := newFixture(t, short).Do("POST", Prefix+"/chat/completions", key, textRequest(), "X-Fake-Scenario", "quota-exceeded")
		if status != http.StatusTooManyRequests || testkit.Field(t, body, "error", "type") != "quota_exceeded" {
			t.Fatalf("%d %v", status, body)
		}
		for _, field := range []string{"dimension", "group_name", "limit_usd", "resets_at", "scope", "used_usd"} {
			if _, ok := fakeupstream.Dig(body, "error", "quota", field); !ok {
				t.Errorf("the quota answer has no error.quota.%s: %v", field, body)
			}
		}
		if header.Get("Retry-After") != "" {
			t.Fatalf("a quota answer must not ask for a retry: %q", header.Get("Retry-After"))
		}
	})
	t.Run("invalid-key refuses a non-empty key", func(t *testing.T) {
		status, header, body := newFixture(t, short).Raw("POST", Prefix+"/chat/completions", key, textRequest(), "X-Fake-Scenario", "invalid-key")
		if status != http.StatusUnauthorized || !strings.HasPrefix(header.Get("Content-Type"), "text/plain") || string(body) != unauthorizedBody {
			t.Fatalf("%d %s %q", status, header.Get("Content-Type"), body)
		}
	})
	t.Run("content-filtered is a 200 without a picture", func(t *testing.T) {
		status, _, raw := newFixture(t, short).Raw("POST", Prefix+"/chat/completions", key, textRequest(), "X-Fake-Scenario", "content-filtered")
		if status != http.StatusOK {
			t.Fatalf("%d %s", status, raw)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if testkit.Field(t, body, "choices", 0, "finish_reason") != "content_filter" {
			t.Fatalf("finish_reason: %v", body)
		}
		content, ok := testkit.Field(t, body, "choices", 0, "message", "content").([]any)
		if !ok || len(content) != 0 {
			t.Fatalf("a filtered answer must have no content part: %v", body)
		}
		if bytes.Contains(raw, []byte(`"image_url"`)) {
			t.Fatalf("a filtered answer carries a picture: %s", raw)
		}
	})
	t.Run("timeout and lost response drop the connection", func(t *testing.T) {
		for _, scenario := range []string{"timeout", "lost-response"} {
			f := newFixture(t, short)
			raw, _ := json.Marshal(textRequest())
			req, _ := http.NewRequest("POST", f.Server.URL+Prefix+"/chat/completions", bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("X-Fake-Scenario", scenario)
			if response, err := http.DefaultClient.Do(req); err == nil {
				response.Body.Close()
				t.Fatalf("%s: got a response %d", scenario, response.StatusCode)
			}
		}
	})
	t.Run("a corrupt picture no longer passes the delivery check", func(t *testing.T) {
		status, body := complete(newFixture(t, short), textRequest(), "X-Fake-Scenario", "artifact-corrupt")
		if status != http.StatusOK {
			t.Fatalf("%d", status)
		}
		mediaType, data := picture(t, body)
		if err := testkit.DeliveryErr(t, data, "image", "", mediaType); err == nil {
			t.Fatal("a corrupt picture passed the delivery check")
		}
	})
	t.Run("URL scenarios do not apply to inline pictures", func(t *testing.T) {
		for _, scenario := range []string{"artifact-404", "artifact-expired"} {
			if status, _ := complete(newFixture(t, short), textRequest(), "X-Fake-Scenario", scenario); status != http.StatusOK {
				t.Fatalf("%s: %d", scenario, status)
			}
		}
	})
}

// TestDocumentedErrorCodeOverride covers the documented request-level codes a
// scenario does not name, and the statuses the class must not depend on. The
// codes and statuses follow https://ai.google.dev/gemini-api/docs/api-errors
// (checked 2026-11-08); the answers are constructed, not observed.
func TestDocumentedErrorCodeOverride(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	answers := []struct {
		code   string
		status int
	}{
		{"payment_required", http.StatusPaymentRequired},
		{"failed_precondition", http.StatusBadRequest},
		{"out_of_range", http.StatusRequestedRangeNotSatisfiable},
		{"not_found", http.StatusNotFound},
		{"model_not_found", http.StatusNotFound},
		{"already_exists", http.StatusConflict},
		{"aborted", http.StatusConflict},
		{"cancelled", 499},
		{"unimplemented", http.StatusNotImplemented},
		{"api_error", http.StatusInternalServerError},
		{"service_unavailable", http.StatusServiceUnavailable},
		{"deadline_exceeded", http.StatusGatewayTimeout},
	}
	for _, answer := range answers {
		t.Run(answer.code, func(t *testing.T) {
			status, body := complete(f, textRequest(), ErrorCodeHeader, answer.code, ErrorStatusHeader, strconv.Itoa(answer.status))
			if status != answer.status || testkit.Field(t, body, "error", "code") != answer.code {
				t.Fatalf("%d %v", status, body)
			}
		})
	}
	t.Run("without a status the answer is 400", func(t *testing.T) {
		status, body := complete(f, textRequest(), ErrorCodeHeader, "safety")
		if status != http.StatusBadRequest || testkit.Field(t, body, "error", "code") != "safety" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("an unusable status falls back to 400", func(t *testing.T) {
		status, body := complete(f, textRequest(), ErrorCodeHeader, "safety", ErrorStatusHeader, "nonsense")
		if status != http.StatusBadRequest || testkit.Field(t, body, "error", "code") != "safety" {
			t.Fatalf("%d %v", status, body)
		}
	})
}

// TestGenerationBlockedCodesAreInjectable covers every generation-blocked code
// the Gemini image connection declares, at the 400 and 409 statuses the
// connection's class must ignore (the public page gives them no status).
func TestGenerationBlockedCodesAreInjectable(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	codes := []string{"safety", "prohibited_content", "spii", "blocklist", "image_safety",
		"image_prohibited_content", "image_recitation", "image_other", "content_blocked",
		"recitation", "language", "content_policy_violation"}
	for _, code := range codes {
		for _, status := range []int{http.StatusBadRequest, http.StatusConflict} {
			t.Run(code+"/"+strconv.Itoa(status), func(t *testing.T) {
				got, body := complete(f, textRequest(), ErrorCodeHeader, code, ErrorStatusHeader, strconv.Itoa(status))
				if got != status || testkit.Field(t, body, "error", "code") != code {
					t.Fatalf("%d %v", got, body)
				}
			})
		}
	}
}

// TestErrorSelectorWorksThroughTheAPIKey covers the stacks whose requests
// cannot carry the selector headers (Plinth's Adapter only takes a base URL and
// a key).
func TestErrorSelectorWorksThroughTheAPIKey(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, want := range map[string]struct {
		status int
		code   string
	}{
		"fake-error-image_safety":     {http.StatusBadRequest, "image_safety"},
		"fake-error-409-image_safety": {http.StatusConflict, "image_safety"},
		"fake-error-416-out_of_range": {http.StatusRequestedRangeNotSatisfiable, "out_of_range"},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.Do("POST", Prefix+"/chat/completions", name, textRequest())
			if status != want.status || testkit.Field(t, body, "error", "code") != want.code {
				t.Fatalf("%d %v", status, body)
			}
		})
	}
}

func TestDriftFakeResponsesMatchTheRealSamples(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, fakeupstream.Config{})
	checked := map[string]bool{}
	for _, exchange := range samples.Find("POST "+samplePrefix+"/chat/completions", "") {
		switch exchange.Response.Status {
		case http.StatusOK:
			request, name := textRequest(), "text success"
			if strings.Contains(string(exchange.Request.Body.JSON), `"image_url"`) {
				request, name = editRequest(), "edit success"
			}
			_, _, raw := f.Raw("POST", Prefix+"/chat/completions", key, request)
			testkit.AssertSameShape(t, name, exchange.Response.Body.JSON, raw)
			checked[name] = true
		case http.StatusBadRequest:
			_, _, raw := f.Raw("POST", Prefix+"/chat/completions", key, map[string]any{"model": Model})
			testkit.AssertSameShape(t, "400", exchange.Response.Body.JSON, raw)
			checked["400"] = true
		}
	}
	for _, want := range []string{"text success", "edit success", "400"} {
		if !checked[want] {
			t.Errorf("the sample has no %s exchange to compare", want)
		}
	}
}

func TestEndpointTableIsCompleteAndHonest(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, fakeupstream.Config{})
	testkit.AssertEndpointTable(t, endpoints, samples, func(endpoint fakeupstream.Endpoint) int {
		status, _, _ := f.Raw(endpoint.Method, Prefix+endpoint.Path, key, map[string]any{})
		return status
	})
}

func TestAlignmentReportIsCommittedAndClean(t *testing.T) {
	testkit.AssertAlignmentReport(t, "testdata/alignment-report.json", OfficialBase, true)
}
