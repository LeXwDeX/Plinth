package app

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func loadAgentFlowLiveCandidate(t *testing.T) ([]wirefixture.Exchange, wirefixture.Policy) {
	t.Helper()
	raw, err := os.ReadFile("testdata/agent-flow/agent-flow.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil {
		t.Fatal(err)
	}
	for i := range exchanges {
		for _, body := range []*wirefixture.Payload{&exchanges[i].Request.Body, &exchanges[i].Response.Body} {
			if len(body.JSON) > 0 {
				var compact bytes.Buffer
				if err := json.Compact(&compact, body.JSON); err != nil {
					t.Fatalf("exchange %d JSON: %v", exchanges[i].Sequence, err)
				}
				body.JSON = compact.Bytes()
			}
		}
	}
	policyRaw, err := os.ReadFile("testdata/agent-flow/agent-flow.policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy wirefixture.Policy
	if err := json.Unmarshal(policyRaw, &policy); err != nil {
		t.Fatal(err)
	}
	return exchanges, policy
}

func TestAgentFlowLiveCandidateReplaysWholeWire(t *testing.T) {
	exchanges, policy := loadAgentFlowLiveCandidate(t)
	if len(exchanges) != 60 {
		t.Fatalf("live candidate exchanges = %d, want the reviewed 60", len(exchanges))
	}
	replay, err := wirefixture.NewReplayWithMedia(exchanges, policy, wirefixture.SyntheticMediaResolver{})
	if err != nil {
		t.Fatal(err)
	}
	sawToolCall := false
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
		if response.StatusCode != exchange.Response.Status {
			t.Fatalf("exchange %d status = %d, want %d", exchange.Sequence, response.StatusCode, exchange.Response.Status)
		}
		got, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("exchange %d body: %v", exchange.Sequence, err)
		}
		_ = response.Body.Close()
		if strings.Contains(string(got), "\"tool_calls\"") {
			sawToolCall = true
		}
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
	if !sawToolCall {
		t.Fatal("agent stream did not contain a tool call")
	}
}
