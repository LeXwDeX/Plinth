package meshy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// loadLiveCandidate loads one committed capture by its file base name
// ("meshy-text-to-3d" is the 2026-10-26 capture, "meshy-text-to-3d-2026-11-22"
// the 2026-11-22 one).
func loadLiveCandidate(t *testing.T, base string) ([]wirefixture.Exchange, wirefixture.Policy) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + base + ".candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	policyRaw, err := os.ReadFile("testdata/" + base + ".policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy wirefixture.Policy
	if err := json.Unmarshal(policyRaw, &policy); err != nil {
		t.Fatal(err)
	}
	return exchanges, policy
}

// The 2026-10-26 capture stays as historical evidence of the wire this
// platform used before #460; its create requests still carry enable_pbr /
// pose_mode on the preview and ai_model on the refine.
func TestMeshyLiveCandidateReplaysWholeWire(t *testing.T) {
	exchanges, policy := loadLiveCandidate(t, "meshy-text-to-3d")
	if len(exchanges) != 67 {
		t.Fatalf("live candidate exchanges = %d, want the reviewed 67", len(exchanges))
	}
	replay, err := wirefixture.NewReplayWithMedia(exchanges, policy, wirefixture.SyntheticMediaResolver{})
	if err != nil {
		t.Fatal(err)
	}
	for _, exchange := range exchanges {
		var body io.Reader
		if exchange.Request.Body.Encoding == wirefixture.PayloadJSON {
			body = bytes.NewReader(exchange.Request.Body.JSON)
		}
		request, err := http.NewRequest(exchange.Request.Method, "https://"+exchange.Request.Host+exchange.Request.Path, body)
		if err != nil {
			t.Fatalf("exchange %d request: %v", exchange.Sequence, err)
		}
		if exchange.Request.ContentType != "" {
			request.Header.Set("Content-Type", exchange.Request.ContentType)
		}
		for name, value := range exchange.Request.Headers {
			request.Header.Set(name, value)
		}
		response, err := replay.RoundTrip(request)
		if err != nil {
			t.Fatalf("exchange %d replay: %v", exchange.Sequence, err)
		}
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("exchange %d body: %v", exchange.Sequence, err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatalf("exchange %d close: %v", exchange.Sequence, err)
		}
		if response.StatusCode != exchange.Response.Status {
			t.Fatalf("exchange %d status = %d", exchange.Sequence, response.StatusCode)
		}
		if exchange.Response.Body.Media != nil {
			_, want, err := wirefixture.SyntheticMedia(exchange.Response.Body.Media.Recipe)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, want) {
				t.Fatalf("exchange %d media bytes differ from recipe %q", exchange.Sequence, exchange.Response.Body.Media.Recipe)
			}
		}
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}
