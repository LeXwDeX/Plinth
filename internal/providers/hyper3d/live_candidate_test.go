package hyper3d_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func loadRodinLiveCandidate(t *testing.T) ([]wirefixture.Exchange, wirefixture.Policy) {
	t.Helper()
	raw, err := os.ReadFile("testdata/rodin-text-to-3d.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	policyRaw, err := os.ReadFile("testdata/rodin-text-to-3d.policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy wirefixture.Policy
	if err := json.Unmarshal(policyRaw, &policy); err != nil {
		t.Fatal(err)
	}
	return exchanges, policy
}

func TestRodinLiveCandidateReplaysWholeWire(t *testing.T) {
	exchanges, policy := loadRodinLiveCandidate(t)
	if len(exchanges) != 73 {
		t.Fatalf("live candidate exchanges = %d, want the reviewed 73", len(exchanges))
	}
	replay, err := wirefixture.NewReplayWithMedia(exchanges, policy, wirefixture.SyntheticMediaResolver{})
	if err != nil {
		t.Fatal(err)
	}
	for _, exchange := range exchanges {
		var body io.Reader
		contentType := exchange.Request.ContentType
		if exchange.Request.Body.Encoding == wirefixture.PayloadJSON {
			body = bytes.NewReader(exchange.Request.Body.JSON)
		}
		if exchange.Request.Body.Encoding == wirefixture.PayloadMultipart {
			var encoded bytes.Buffer
			writer := multipart.NewWriter(&encoded)
			for _, part := range exchange.Request.Body.Multipart.Parts {
				if part.Media != nil {
					t.Fatalf("exchange %d has an unsupported file part", exchange.Sequence)
				}
				if err := writer.WriteField(part.Name, part.Value); err != nil {
					t.Fatalf("exchange %d multipart: %v", exchange.Sequence, err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			body = &encoded
			contentType = writer.FormDataContentType()
		}
		request, err := http.NewRequest(exchange.Request.Method, "https://"+exchange.Request.Host+exchange.Request.Path, body)
		if err != nil {
			t.Fatalf("exchange %d request: %v", exchange.Sequence, err)
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
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
