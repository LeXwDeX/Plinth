package geminiimagesv1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
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
		"insufficient-balance": {http.StatusPaymentRequired, "insufficient_quota"},
		"key-quota":            {http.StatusPaymentRequired, "insufficient_quota"},
		"rate-limit":           {http.StatusTooManyRequests, "rate_limit_exceeded"},
		"server-error":         {http.StatusInternalServerError, "internal_error"},
		"task-failed":          {http.StatusBadRequest, "content_policy_violation"},
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
