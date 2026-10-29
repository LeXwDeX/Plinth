package openaiimagesv1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
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

func generate(f *testkit.Fixture, body map[string]any, headers ...string) (int, map[string]any) {
	f.T.Helper()
	status, _, decoded := f.Do("POST", Prefix+"/images/generations", key, body, headers...)
	return status, decoded
}

func editBody(t *testing.T, partName string, withImage bool) testkit.Body {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("model", Model)
	_ = writer.WriteField("prompt", "make the cube blue")
	if withImage {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="`+partName+`"; filename="reference.png"`)
		header.Set("Content-Type", "image/png")
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte("png bytes"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()}
}

func edit(f *testkit.Fixture, body testkit.Body, headers ...string) (int, map[string]any) {
	f.T.Helper()
	status, _, decoded := f.Do("POST", Prefix+"/images/edits", key, body, headers...)
	return status, decoded
}

// picture decodes the inline picture of a success body and runs Plinth's
// delivery check over it.
func picture(t *testing.T, body map[string]any) []byte {
	t.Helper()
	encoded, _ := testkit.Field(t, body, "data", 0, "b64_json").(string)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		t.Fatalf("data[0].b64_json is not base64 picture bytes: %v", err)
	}
	return data
}

func TestGenerationAndEditDeliverAValidInlinePicture(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, body := generate(f, map[string]any{"model": Model, "prompt": "a cube", "size": "1024x1024", "quality": "low"})
	if status != http.StatusOK {
		t.Fatalf("generation: %d %v", status, body)
	}
	if err := testkit.DeliveryErr(t, picture(t, body), "image", "", "image/png"); err != nil {
		t.Fatalf("generated picture fails the delivery check: %v", err)
	}
	if body["size"] != "1024x1024" || body["quality"] != "low" {
		t.Fatalf("the response should echo the request's size and quality: %v", body)
	}
	// Plinth sends the reference picture as image[]; the committed sample says image.
	for _, part := range []string{"image[]", "image"} {
		status, body = edit(f, editBody(t, part, true))
		if status != http.StatusOK {
			t.Fatalf("edit with %s: %d %v", part, status, body)
		}
		if err := testkit.DeliveryErr(t, picture(t, body), "image", "", "image/png"); err != nil {
			t.Fatalf("edited picture fails the delivery check: %v", err)
		}
	}
}

func TestRequestValidationAndAuthentication(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, header, _ := f.Raw("POST", Prefix+"/images/generations", "", map[string]any{"model": Model, "prompt": "x"})
	if status != http.StatusUnauthorized || !strings.HasPrefix(header.Get("Content-Type"), "text/plain") {
		t.Fatalf("no key: %d %s", status, header.Get("Content-Type"))
	}
	if status, body := generate(f, map[string]any{"model": Model}); status != http.StatusBadRequest || testkit.Field(t, body, "error", "param") != "prompt" {
		t.Fatalf("missing prompt: %d %v", status, body)
	}
	if status, body := generate(f, map[string]any{"prompt": "x"}); status != http.StatusBadRequest || testkit.Field(t, body, "error", "param") != "model" {
		t.Fatalf("missing model: %d %v", status, body)
	}
	if status, body := edit(f, editBody(t, "image", false)); status != http.StatusBadRequest || testkit.Field(t, body, "error", "param") != "image" {
		t.Fatalf("edit without a picture: %d %v", status, body)
	}
}

func TestFaultScenarios(t *testing.T) {
	short := fakeupstream.Config{TimeoutHold: 50 * time.Millisecond}
	request := map[string]any{"model": Model, "prompt": "x"}
	for scenario, want := range map[string]struct {
		status int
		code   string
	}{
		"insufficient-balance": {http.StatusPaymentRequired, "insufficient_quota"},
		"key-quota":            {http.StatusPaymentRequired, "insufficient_quota"},
		"rate-limit":           {http.StatusTooManyRequests, "rate_limit_exceeded"},
		"server-error":         {http.StatusInternalServerError, "internal_error"},
		"task-failed":          {http.StatusBadRequest, "moderation_blocked"},
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t, short)
			status, body := generate(f, request, "X-Fake-Scenario", scenario)
			if status != want.status || testkit.Field(t, body, "error", "code") != want.code {
				t.Fatalf("%d %v", status, body)
			}
			// The edit endpoint answers the same faults.
			if status, _ = edit(f, editBody(t, "image", true), "X-Fake-Scenario", scenario); status != want.status {
				t.Fatalf("edit: %d", status)
			}
		})
	}
	t.Run("rate limit has Retry-After", func(t *testing.T) {
		_, header, _ := newFixture(t, short).Raw("POST", Prefix+"/images/generations", key, request, "X-Fake-Scenario", "rate-limit")
		if header.Get("Retry-After") != "1" {
			t.Fatalf("Retry-After = %q", header.Get("Retry-After"))
		}
	})
	t.Run("timeout and lost response drop the connection", func(t *testing.T) {
		for _, scenario := range []string{"timeout", "lost-response"} {
			f := newFixture(t, short)
			raw, _ := json.Marshal(request)
			req, _ := http.NewRequest("POST", f.Server.URL+Prefix+"/images/generations", bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("X-Fake-Scenario", scenario)
			if response, err := http.DefaultClient.Do(req); err == nil {
				response.Body.Close()
				t.Fatalf("%s: got a response %d", scenario, response.StatusCode)
			}
		}
	})
	t.Run("a corrupt picture no longer passes the delivery check", func(t *testing.T) {
		status, body := generate(newFixture(t, short), request, "X-Fake-Scenario", "artifact-corrupt")
		if status != http.StatusOK {
			t.Fatalf("%d", status)
		}
		if err := testkit.DeliveryErr(t, picture(t, body), "image", "", "image/png"); err == nil {
			t.Fatal("a corrupt picture passed the delivery check")
		}
	})
	t.Run("a key prefix selects the scenario", func(t *testing.T) {
		f := newFixture(t, short)
		status, _, _ := f.Raw("POST", Prefix+"/images/generations", "fake-rate-limit-1", request)
		if status != http.StatusTooManyRequests {
			t.Fatalf("status = %d", status)
		}
	})
	t.Run("URL scenarios do not apply to inline pictures", func(t *testing.T) {
		for _, scenario := range []string{"artifact-404", "artifact-expired"} {
			if status, _ := generate(newFixture(t, short), request, "X-Fake-Scenario", scenario); status != http.StatusOK {
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
	for endpoint, send := range map[string]func(valid bool) []byte{
		"/images/generations": func(valid bool) []byte {
			body := map[string]any{"model": Model}
			if valid {
				body["prompt"] = "a cube"
			}
			_, _, raw := f.Raw("POST", Prefix+"/images/generations", key, body)
			return raw
		},
		"/images/edits": func(valid bool) []byte {
			_, _, raw := f.Raw("POST", Prefix+"/images/edits", key, editBody(t, "image", valid))
			return raw
		},
	} {
		for _, exchange := range samples.Find("POST "+samplePrefix+endpoint, "") {
			switch exchange.Response.Status {
			case http.StatusOK:
				testkit.AssertSameShape(t, endpoint+" success", exchange.Response.Body.JSON, send(true))
				checked[endpoint+" ok"] = true
			case http.StatusBadRequest:
				testkit.AssertSameShape(t, endpoint+" 400", exchange.Response.Body.JSON, send(false))
				checked[endpoint+" 400"] = true
			}
		}
		for _, kind := range []string{" ok", " 400"} {
			if !checked[endpoint+kind] {
				t.Errorf("the sample has no %s%s exchange to compare", endpoint, kind)
			}
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
