package wirefixture_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func TestDeferredMediaSpoolsAndSubstitutes(t *testing.T) {
	store, dir := privateStore(t)
	limits := wirefixture.DeferredLimits{MaxBytes: 1 << 20, MaxExchanges: 8}
	recorder, err := wirefixture.NewDeferredRecorderWithEvidence(mediaMetadata(), limits, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Release() })
	data := bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47}, (9<<20)/4)
	_, body := capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/result", ""),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK,
				Header:        http.Header{"Content-Type": {"image/png"}},
				Body:          io.NopCloser(bytes.NewReader(data)),
				ContentLength: int64(len(data))}, nil
		}))
	if !bytes.Equal(body, data) {
		t.Fatal("business response changed")
	}
	if len(recorder.Exchanges()) != 0 || recorder.AssertComplete() == nil {
		t.Fatal("deferred media published before Finalize")
	}
	policy := testPolicy()
	policy.Allowed["result"] = true
	if err := recorder.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	exchanges := recorder.Exchanges()
	if len(exchanges) != 1 || exchanges[0].Response.Body.Media == nil {
		t.Fatalf("media exchange = %+v", exchanges)
	}
	media := exchanges[0].Response.Body.Media
	if media.MediaType != "image/png" || media.Length != int64(len(data)) || media.Recipe != "" {
		t.Fatalf("captured media = %+v", media)
	}
	evidence := recorder.MediaEvidence()
	if len(evidence) != 1 || evidence[0].Location.Sequence != 1 || evidence[0].Location.Part != -1 {
		t.Fatalf("evidence = %+v", evidence)
	}
	if got, err := evidence[0].Source.Bytes(); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("private evidence bytes = %d, %v", len(got), err)
	}
	if len(privateFiles(t, dir)) != 1 {
		t.Fatal("private evidence file missing")
	}

	public, err := wirefixture.SubstituteMediaWithExtensions(exchanges,
		map[string]string{"image/png": "png-1x1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if public[0].Metadata.Provenance != wirefixture.ProvenanceDerived ||
		public[0].Response.Body.Media == nil || public[0].Response.Body.Media.Recipe != "png-1x1" ||
		!public[0].Response.Body.Media.Substituted {
		t.Fatalf("derived media = %+v", public[0])
	}
	root, candidateDir := safetyCandidateDir(t)
	path := filepath.Join(candidateDir, "candidate.json")
	if err := wirefixture.WriteCandidate(root, path, public, policy); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, data[:32]) {
		t.Fatal("public candidate contains captured bytes")
	}
	var decoded []wirefixture.Exchange
	if json.Unmarshal(encoded, &decoded) != nil || decoded[0].Response.Body.Media.Recipe != "png-1x1" {
		t.Fatalf("public candidate = %s", encoded)
	}
}

func TestDeferredMediaTruncatedCloseInvalidatesEvidence(t *testing.T) {
	store, dir := privateStore(t)
	recorder, err := wirefixture.NewDeferredRecorderWithEvidence(mediaMetadata(),
		wirefixture.DeferredLimits{MaxBytes: 1 << 20, MaxExchanges: 8}, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Release() })
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/result", "")
	response, err := recorder.Wrap("hyper3d", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK,
			Header:        http.Header{"Content-Type": {"image/png"}},
			Body:          io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{1}, 1024))),
			ContentLength: 1024}, nil
	})).RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 16)
	if _, err := response.Body.Read(buffer); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	policy := testPolicy()
	policy.Allowed["result"] = true
	if err := recorder.Finalize(policy); err == nil {
		t.Fatal("truncated media finalized")
	}
	if !bytes.Equal(buffer, bytes.Repeat([]byte{1}, 16)) {
		t.Fatal("business bytes changed")
	}
	if len(privateFiles(t, dir)) != 0 {
		t.Fatal("truncated media left evidence")
	}
}
