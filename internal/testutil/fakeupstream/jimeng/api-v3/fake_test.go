package jimengapiv3

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

func generate(f *testkit.Fixture, format string, headers ...string) (int, map[string]any) {
	f.T.Helper()
	status, _, decoded := f.Do("POST", Prefix+"/images/generations", key, body("a cube", format, nil), headers...)
	return status, decoded
}

func TestInlineAndURLResponsesDeliverAValidPicture(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, inline := generate(f, "b64_json")
	if status != http.StatusOK {
		t.Fatalf("inline: %d %v", status, inline)
	}
	data, err := base64.StdEncoding.DecodeString(testkit.Field(t, inline, "data", 0, "b64_json").(string))
	if err != nil || testkit.DeliveryErr(t, data, "image", "", "image/jpeg") != nil {
		t.Fatalf("inline picture invalid: %v", err)
	}
	if testkit.Field(t, inline, "model") != Model || testkit.Field(t, inline, "data", 0, "size") != "2048x2048" {
		t.Fatalf("model or size not echoed: %v", inline)
	}

	status, remote := generate(f, "url")
	if status != http.StatusOK {
		t.Fatalf("url: %d %v", status, remote)
	}
	link := testkit.Field(t, remote, "data", 0, "url").(string)
	code, contentType, bytesBack := f.Download(link)
	if code != http.StatusOK || contentType != "image/jpeg" {
		t.Fatalf("download: %d %s", code, contentType)
	}
	if err := testkit.DeliveryErr(t, bytesBack, "image", "", contentType); err != nil {
		t.Fatalf("downloaded picture invalid: %v", err)
	}
	if _, has := remote["data"].([]any)[0].(map[string]any)["b64_json"]; has {
		t.Fatalf("a url response must not carry b64_json: %v", remote)
	}
}

func TestRequestValidationAndAuthentication(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, _, raw := f.Raw("POST", Prefix+"/images/generations", "", body("x", "b64_json", nil))
	if status != http.StatusUnauthorized || !strings.Contains(string(raw), "AuthenticationError") {
		t.Fatalf("no key: %d %s", status, raw)
	}
	status, _, decoded := f.Do("POST", Prefix+"/images/generations", key, map[string]any{"model": Model})
	if status != http.StatusBadRequest || testkit.Field(t, decoded, "error", "code") != "MissingParameter" {
		t.Fatalf("missing prompt: %d %v", status, decoded)
	}
}

func TestFaultScenarios(t *testing.T) {
	short := fakeupstream.Config{TimeoutHold: 50 * time.Millisecond}
	for scenario, want := range map[string]struct {
		status int
		kind   string
		code   string
	}{
		"insufficient-balance": {http.StatusForbidden, "Forbidden", "AccountOverdueError"},
		"key-quota":            {http.StatusTooManyRequests, "TooManyRequests", "QuotaExceeded"},
		"quota-exceeded":       {http.StatusTooManyRequests, "TooManyRequests", "SetLimitExceeded"},
		"rate-limit":           {http.StatusTooManyRequests, "TooManyRequests", "RateLimitExceeded.EndpointRPMExceeded"},
		"server-error":         {http.StatusInternalServerError, "InternalServerError", "InternalServiceError"},
		"forbidden":            {http.StatusForbidden, "Forbidden", "AccessDenied"},
		"model-not-found":      {http.StatusNotFound, "NotFound", "InvalidEndpointOrModel.NotFound"},
		"service-unavailable":  {http.StatusServiceUnavailable, "ServiceUnavailable", "NoAvailableService"},
		"invalid-key":          {http.StatusUnauthorized, "Unauthorized", "AuthenticationError"},
		"task-failed":          {http.StatusBadRequest, "BadRequest", "OutputImageSensitiveContentDetected"},
	} {
		t.Run(scenario, func(t *testing.T) {
			status, decoded := generate(newFixture(t, short), "b64_json", "X-Fake-Scenario", scenario)
			if status != want.status || testkit.Field(t, decoded, "error", "code") != want.code || testkit.Field(t, decoded, "error", "type") != want.kind {
				t.Fatalf("%d %v", status, decoded)
			}
		})
	}
	t.Run("rate limit has Retry-After", func(t *testing.T) {
		_, header, _ := newFixture(t, short).Raw("POST", Prefix+"/images/generations", key, body("x", "b64_json", nil), "X-Fake-Scenario", "rate-limit")
		if header.Get("Retry-After") != "1" {
			t.Fatalf("Retry-After = %q", header.Get("Retry-After"))
		}
	})
	t.Run("timeout and lost response drop the connection", func(t *testing.T) {
		for _, scenario := range []string{"timeout", "lost-response"} {
			f := newFixture(t, short)
			raw, _ := json.Marshal(body("x", "b64_json", nil))
			req, _ := http.NewRequest("POST", f.Server.URL+Prefix+"/images/generations", bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("X-Fake-Scenario", scenario)
			if response, err := http.DefaultClient.Do(req); err == nil {
				response.Body.Close()
				t.Fatalf("%s: got a response %d", scenario, response.StatusCode)
			}
		}
	})
	t.Run("corrupt pictures fail the delivery check, inline and by URL", func(t *testing.T) {
		f := newFixture(t, short)
		_, inline := generate(f, "b64_json", "X-Fake-Scenario", "artifact-corrupt")
		data, _ := base64.StdEncoding.DecodeString(testkit.Field(t, inline, "data", 0, "b64_json").(string))
		if testkit.DeliveryErr(t, data, "image", "", "image/jpeg") == nil {
			t.Fatal("a corrupt inline picture passed the delivery check")
		}
		_, remote := generate(f, "url", "X-Fake-Scenario", "artifact-corrupt")
		_, _, bytesBack := f.Download(testkit.Field(t, remote, "data", 0, "url").(string))
		if testkit.DeliveryErr(t, bytesBack, "image", "", "image/jpeg") == nil {
			t.Fatal("a corrupt downloaded picture passed the delivery check")
		}
	})
	t.Run("a missing picture URL answers 404", func(t *testing.T) {
		f := newFixture(t, short)
		_, remote := generate(f, "url", "X-Fake-Scenario", "artifact-404")
		if code, _, _ := f.Download(testkit.Field(t, remote, "data", 0, "url").(string)); code != http.StatusNotFound {
			t.Fatalf("status = %d", code)
		}
	})
	t.Run("an expired signed URL answers the TOS 403 body", func(t *testing.T) {
		f := newFixture(t, short)
		_, remote := generate(f, "url", "X-Fake-Scenario", "artifact-expired")
		link := testkit.Field(t, remote, "data", 0, "url").(string)
		code, contentType, raw := f.Download(link)
		if code != http.StatusForbidden || !strings.Contains(contentType, "json") || !strings.Contains(string(raw), `"Message":"Request has expired"`) {
			t.Fatalf("expired URL: %d %s %s", code, contentType, raw)
		}
		// An unaffected request keeps a working URL.
		_, fresh := generate(f, "url")
		if code, _, _ := f.Download(testkit.Field(t, fresh, "data", 0, "url").(string)); code != http.StatusOK {
			t.Fatalf("a normal URL answered %d", code)
		}
	})
	t.Run("a key prefix selects the scenario", func(t *testing.T) {
		f := newFixture(t, short)
		if status, _, _ := f.Raw("POST", Prefix+"/images/generations", "fake-rate-limit-1", body("x", "b64_json", nil)); status != http.StatusTooManyRequests {
			t.Fatalf("rate-limit status = %d", status)
		}
		if status, _, _ := f.Raw("POST", Prefix+"/images/generations", "fake-invalid-key-1", body("x", "b64_json", nil)); status != http.StatusUnauthorized {
			t.Fatalf("invalid-key status = %d", status)
		}
	})
}

// generateRequest sends request with the fixture's test key.
func generateRequest(f *testkit.Fixture, request map[string]any, headers ...string) (int, map[string]any) {
	f.T.Helper()
	status, _, decoded := f.Do("POST", Prefix+"/images/generations", key, request, headers...)
	return status, decoded
}

// sequential builds a request that asks for sequential generation.
func sequential(prompt, format string, maxImages int) map[string]any {
	request := body(prompt, format, nil)
	request["sequential_image_generation"] = "auto"
	if maxImages > 0 {
		request["sequential_image_generation_options"] = map[string]any{"max_images": maxImages}
	}
	return request
}

func TestSequentialGenerationAnswersEveryRequestedImage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		maxImages int
		want      int
	}{
		{"max_images", 3, 3},
		{"the documented default", 0, 15},
		{"the documented limit", 99, 15},
		{"one picture", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{})
			status, decoded := generateRequest(f, sequential("a cube", "b64_json", tc.maxImages))
			if status != http.StatusOK {
				t.Fatalf("%d %v", status, decoded)
			}
			items, _ := decoded["data"].([]any)
			if len(items) != tc.want {
				t.Fatalf("data has %d pictures, want %d: %v", len(items), tc.want, decoded)
			}
			for index := range items {
				data, err := base64.StdEncoding.DecodeString(testkit.Field(t, decoded, "data", index, "b64_json").(string))
				if err != nil || testkit.DeliveryErr(t, data, "image", "", "image/jpeg") != nil {
					t.Fatalf("picture %d invalid: %v", index, err)
				}
				if size := testkit.Field(t, decoded, "data", index, "size"); size != "2048x2048" {
					t.Fatalf("picture %d size = %v", index, size)
				}
			}
			if generated := testkit.Field(t, decoded, "usage", "generated_images"); generated != float64(tc.want) {
				t.Fatalf("usage.generated_images = %v, want %d", generated, tc.want)
			}
		})
	}
	t.Run("url answers a distinct working link per picture", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{})
		status, decoded := generateRequest(f, sequential("a cube", "url", 3))
		if status != http.StatusOK {
			t.Fatalf("%d %v", status, decoded)
		}
		seen := map[string]bool{}
		for index := 0; index < 3; index++ {
			link := testkit.Field(t, decoded, "data", index, "url").(string)
			if seen[link] {
				t.Fatalf("picture %d repeats the URL %q", index, link)
			}
			seen[link] = true
			code, contentType, bytesBack := f.Download(link)
			if code != http.StatusOK || contentType != "image/jpeg" {
				t.Fatalf("picture %d download: %d %s", index, code, contentType)
			}
			if err := testkit.DeliveryErr(t, bytesBack, "image", "", contentType); err != nil {
				t.Fatalf("picture %d downloaded invalid: %v", index, err)
			}
		}
	})
	t.Run("disabled ignores max_images", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{})
		request := body("a cube", "b64_json", map[string]any{
			"sequential_image_generation_options": map[string]any{"max_images": 3},
		})
		status, decoded := generateRequest(f, request)
		if status != http.StatusOK {
			t.Fatalf("%d %v", status, decoded)
		}
		if items, _ := decoded["data"].([]any); len(items) != 1 {
			t.Fatalf("a disabled request answered %d pictures: %v", len(items), decoded)
		}
	})
}

func TestPartialImagesKeepTheSuccessfulPictures(t *testing.T) {
	t.Run("inline", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{})
		status, decoded := generateRequest(f, sequential("a cube", "b64_json", 3), "X-Fake-Scenario", "partial-images")
		if status != http.StatusOK {
			t.Fatalf("%d %v", status, decoded)
		}
		items, _ := decoded["data"].([]any)
		if len(items) != 3 {
			t.Fatalf("data has %d pictures, want 3: %v", len(items), decoded)
		}
		for _, index := range []int{0, 2} {
			data, err := base64.StdEncoding.DecodeString(testkit.Field(t, decoded, "data", index, "b64_json").(string))
			if err != nil || testkit.DeliveryErr(t, data, "image", "", "image/jpeg") != nil {
				t.Fatalf("picture %d invalid: %v", index, err)
			}
		}
		failed, _ := items[1].(map[string]any)
		if failed["b64_json"] != nil || failed["url"] != nil {
			t.Fatalf("the failed picture carries a picture: %v", failed)
		}
		if testkit.Field(t, decoded, "data", 1, "error", "code") != "OutputImageSensitiveContentDetected" {
			t.Fatalf("the failed picture names no documented code: %v", failed)
		}
		if message, _ := testkit.Field(t, decoded, "data", 1, "error", "message").(string); message == "" {
			t.Fatalf("the failed picture names no message: %v", failed)
		}
		if _, has := decoded["error"]; has {
			t.Fatalf("a partly successful request must not carry a top-level error: %v", decoded)
		}
		if generated := testkit.Field(t, decoded, "usage", "generated_images"); generated != float64(2) {
			t.Fatalf("usage.generated_images = %v, want 2", generated)
		}
	})
	t.Run("url", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{})
		status, decoded := generateRequest(f, sequential("a cube", "url", 3), "X-Fake-Scenario", "partial-images")
		if status != http.StatusOK {
			t.Fatalf("%d %v", status, decoded)
		}
		failed, _ := decoded["data"].([]any)[1].(map[string]any)
		if failed["url"] != nil || failed["b64_json"] != nil {
			t.Fatalf("the failed picture carries a picture: %v", failed)
		}
		for _, index := range []int{0, 2} {
			code, contentType, bytesBack := f.Download(testkit.Field(t, decoded, "data", index, "url").(string))
			if code != http.StatusOK || contentType != "image/jpeg" {
				t.Fatalf("picture %d download: %d %s", index, code, contentType)
			}
			if err := testkit.DeliveryErr(t, bytesBack, "image", "", contentType); err != nil {
				t.Fatalf("picture %d downloaded invalid: %v", index, err)
			}
		}
	})
	t.Run("a single-picture request stays whole", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{})
		status, decoded := generateRequest(f, body("a cube", "b64_json", nil), "X-Fake-Scenario", "partial-images")
		if status != http.StatusOK {
			t.Fatalf("%d %v", status, decoded)
		}
		items, _ := decoded["data"].([]any)
		if len(items) != 1 {
			t.Fatalf("a disabled request answered %d pictures: %v", len(items), decoded)
		}
		if _, has := items[0].(map[string]any)["error"]; has {
			t.Fatalf("a disabled request answered a failed picture: %v", items[0])
		}
	})
}

// TestDocumentedUpstreamCodesAreReachable drives the X-Fake-Upstream-Code
// channel: every code default_catalog.json declares for the jimeng-images
// connection, plus the other answers of the official error table, must be
// producible with the documented status and type.
func TestDocumentedUpstreamCodesAreReachable(t *testing.T) {
	codes := map[string]struct {
		status int
		kind   string
	}{
		// The catalog's eight documented codes (internal/catalog/default_catalog.json, jimeng-images).
		"AccountOverdueError":                 {http.StatusForbidden, "Forbidden"},
		"QuotaExceeded":                       {http.StatusTooManyRequests, "TooManyRequests"},
		"SetLimitExceeded":                    {http.StatusTooManyRequests, "TooManyRequests"},
		"InputTextSensitiveContentDetected":   {http.StatusBadRequest, "BadRequest"},
		"InputImageSensitiveContentDetected":  {http.StatusBadRequest, "BadRequest"},
		"OutputTextSensitiveContentDetected":  {http.StatusBadRequest, "BadRequest"},
		"OutputImageSensitiveContentDetected": {http.StatusBadRequest, "BadRequest"},
		"SensitiveContentDetected":            {http.StatusBadRequest, "BadRequest"},
		// The authentication, refusal, timeout and unavailable answers.
		"AuthenticationError":                       {http.StatusUnauthorized, "Unauthorized"},
		"InvalidParameter":                          {http.StatusBadRequest, "BadRequest"},
		"InvalidEndpointOrModel.NotFound":           {http.StatusNotFound, "NotFound"},
		"ModelNotOpen":                              {http.StatusNotFound, "NotFound"},
		"RequestTimeout":                            {http.StatusRequestTimeout, "RequestTimeout"},
		"OperationDenied.ServiceNotOpen":            {http.StatusForbidden, "Forbidden"},
		"AccessDenied":                              {http.StatusForbidden, "Forbidden"},
		"ServiceConnectionRefused":                  {http.StatusServiceUnavailable, "ServiceUnavailable"},
		"NoAvailableService":                        {http.StatusServiceUnavailable, "ServiceUnavailable"},
		"InternalServiceError":                      {http.StatusInternalServerError, "InternalServerError"},
		"RateLimitExceeded.EndpointRPMExceeded":     {http.StatusTooManyRequests, "TooManyRequests"},
		"RateLimitExceeded.EndpointTPMExceeded":     {http.StatusTooManyRequests, "TooManyRequests"},
		"RateLimitExceeded.EndpointFlexTPMExceeded": {http.StatusTooManyRequests, "TooManyRequests"},
		"AccountRateLimitExceeded":                  {http.StatusTooManyRequests, "TooManyRequests"},
		"ModelAccountRpmRateLimitExceeded":          {http.StatusTooManyRequests, "TooManyRequests"},
		"ServerOverloaded":                          {http.StatusTooManyRequests, "TooManyRequests"},
	}
	for code, want := range codes {
		t.Run(code, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{})
			status, decoded := generateRequest(f, body("a cube", "b64_json", nil), UpstreamCodeHeader, code)
			if status != want.status || testkit.Field(t, decoded, "error", "code") != code || testkit.Field(t, decoded, "error", "type") != want.kind {
				t.Fatalf("%d %v", status, decoded)
			}
			if message, _ := testkit.Field(t, decoded, "error", "message").(string); message == "" {
				t.Fatalf("no error message: %v", decoded)
			}
		})
	}
	t.Run("an unknown code is rejected", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{})
		status, decoded := generateRequest(f, body("a cube", "b64_json", nil), UpstreamCodeHeader, "NoSuchError")
		if status != http.StatusBadRequest || testkit.Field(t, decoded, "error", "code") != "InvalidParameter" {
			t.Fatalf("%d %v", status, decoded)
		}
	})
	t.Run("the channel wins over a scenario", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{})
		status, decoded := generateRequest(f, body("a cube", "b64_json", nil), "X-Fake-Scenario", "rate-limit", UpstreamCodeHeader, "RequestTimeout")
		if status != http.StatusRequestTimeout || testkit.Field(t, decoded, "error", "code") != "RequestTimeout" {
			t.Fatalf("%d %v", status, decoded)
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
	for _, exchange := range samples.Find("POST "+samplePrefix+"/images/generations", "") {
		switch exchange.Response.Status {
		case http.StatusOK:
			format, name := "b64_json", "inline"
			if strings.Contains(string(exchange.Response.Body.JSON), `"url"`) {
				format, name = "url", "url"
			}
			_, _, raw := f.Raw("POST", Prefix+"/images/generations", key, body("a cube", format, nil))
			testkit.AssertSameShape(t, name+" success", exchange.Response.Body.JSON, raw)
			checked[name] = true
		case http.StatusBadRequest:
			_, _, raw := f.Raw("POST", Prefix+"/images/generations", key, map[string]any{"model": Model})
			testkit.AssertSameShape(t, "400", exchange.Response.Body.JSON, raw)
			checked["400"] = true
		case http.StatusUnauthorized:
			_, _, raw := f.Raw("POST", Prefix+"/images/generations", "", body("a cube", "b64_json", nil))
			testkit.AssertSameShape(t, "401", exchange.Response.Body.JSON, raw)
			checked["401"] = true
		}
	}
	for _, want := range []string{"inline", "url", "400", "401"} {
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
		path := strings.ReplaceAll(endpoint.Path, "{name}", "fake-image-0.jpeg")
		status, _, _ := f.Raw(endpoint.Method, Prefix+path, key, map[string]any{})
		if endpoint.Method == "GET" && status == http.StatusNotFound {
			return http.StatusOK // a GET route is routed even when the file is unknown
		}
		return status
	})
}

func TestAlignmentReportIsCommittedAndClean(t *testing.T) {
	testkit.AssertAlignmentReport(t, "testdata/alignment-report.json", OfficialBase, true)
}
