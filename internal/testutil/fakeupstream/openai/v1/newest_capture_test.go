package openaiv1

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// dailyQuotaSample is the 2026-11-20 live-window refusal of the GPT connection:
// the gateway's model-group daily quota ran out and /v1/responses answered HTTP
// 429 with the quota_exceeded envelope and the same nested quota object the
// weekly refusal carries. The group name was replaced with a placeholder and the
// message stays the sanitizer's redaction; nothing else was edited.
func dailyQuotaSample(t *testing.T) (int, map[string]any) {
	t.Helper()
	raw, err := sampleFS.ReadFile("testdata/openai-v1-quota-2026-11-20.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	if len(exchanges) != 1 {
		t.Fatalf("the capture holds %d exchanges, want one", len(exchanges))
	}
	body, err := fakeupstream.JSONBody(exchanges[0])
	if err != nil {
		t.Fatal(err)
	}
	return exchanges[0].Response.Status, body
}

func shapeOfValue(t *testing.T, value any) fakeupstream.Shape {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := fakeupstream.ShapeOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

// The quota-exceeded scenario must answer exactly the envelope the gateway sent
// on /v1/responses when the daily quota ran out, so the Adapter's
// credential-quota classification is exercised on the shape it really sees.
func TestQuotaExceededMatchesTheDailyQuotaCapture(t *testing.T) {
	status, capture := dailyQuotaSample(t)
	if status != http.StatusTooManyRequests {
		t.Fatalf("capture status = %d", status)
	}
	f := newFixture(t, fakeupstream.Config{})
	response, raw := f.respond(map[string]any{"model": "m", "input": "hi"}, "X-Fake-Scenario", "quota-exceeded")
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("quota-exceeded = %d %s", response.StatusCode, raw)
	}
	var answered map[string]any
	if err := json.Unmarshal(raw, &answered); err != nil {
		t.Fatal(err)
	}
	if diff := fakeupstream.DiffShape(shapeOfValue(t, capture), shapeOfValue(t, answered)); !diff.Empty() {
		t.Errorf("quota-exceeded drifted from the 2026-11-20 capture: %s", diff)
	}
	errorObject, _ := answered["error"].(map[string]any)
	if errorObject["code"] != "quota_exceeded" || errorObject["type"] != "quota_exceeded" {
		t.Fatalf("error = %v", errorObject)
	}
	quota, _ := errorObject["quota"].(map[string]any)
	for _, field := range []string{"dimension", "group_name", "limit_usd", "resets_at", "scope", "used_usd"} {
		if _, has := quota[field]; !has {
			t.Errorf("the quota object lacks %s: %v", field, quota)
		}
	}
}
