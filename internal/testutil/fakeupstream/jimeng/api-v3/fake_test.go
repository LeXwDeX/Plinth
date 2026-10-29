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
		code   string
	}{
		"insufficient-balance": {http.StatusForbidden, "AccountOverdueError"},
		"key-quota":            {http.StatusTooManyRequests, "QuotaExceeded"},
		"rate-limit":           {http.StatusTooManyRequests, "RateLimitExceeded"},
		"server-error":         {http.StatusInternalServerError, "InternalServiceError"},
		"task-failed":          {http.StatusBadRequest, "OutputImageSensitiveContentDetected"},
	} {
		t.Run(scenario, func(t *testing.T) {
			status, decoded := generate(newFixture(t, short), "b64_json", "X-Fake-Scenario", scenario)
			if status != want.status || testkit.Field(t, decoded, "error", "code") != want.code {
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
		status, _, _ := newFixture(t, short).Raw("POST", Prefix+"/images/generations", "fake-rate-limit-1", body("x", "b64_json", nil))
		if status != http.StatusTooManyRequests {
			t.Fatalf("status = %d", status)
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
