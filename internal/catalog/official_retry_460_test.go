package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func connectionFacts(t *testing.T, adapterKey string) (id string, retrySubmit bool, status map[string]struct {
	Phase  string
	Queued bool
}, code map[string]bool) {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range c.Application().Runtime.ProviderConnections {
		if connection.AdapterKey != adapterKey {
			continue
		}
		status = map[string]struct {
			Phase  string
			Queued bool
		}{}
		for word, rule := range connection.StatusMapping.UpstreamStatus {
			status[word] = struct {
				Phase  string
				Queued bool
			}{rule.Phase, rule.Queued}
		}
		code = map[string]bool{}
		for key, rule := range connection.ErrorMapping.UpstreamCodeMap {
			code[key] = rule.Retryable
		}
		return connection.ID, connection.RetrySubmitServerError, status, code
	}
	t.Fatalf("connection %s is missing", adapterKey)
	return
}

// #460 B22: the Meshy status words are exactly the official five. No committed
// sample or historical status word outside them was found (the captures contain
// only PENDING, IN_PROGRESS, SUCCEEDED and FAILED; CANCELED is constructed).
func TestMeshyStatusMappingMatchesOfficialFiveWords(t *testing.T) {
	_, _, status, _ := connectionFacts(t, "meshy.v2")
	want := []string{"pending", "in_progress", "succeeded", "failed", "canceled"}
	if len(status) != len(want) {
		t.Fatalf("status words = %v, want exactly %v", status, want)
	}
	for _, word := range want {
		if _, ok := status[word]; !ok {
			t.Fatalf("status word %q is missing: %v", word, status)
		}
	}
	if !status["pending"].Queued {
		t.Fatal("pending must stay queued")
	}
}

// #460 C25: every Hyper3D Offering retries 429 and 5xx. The connection opts in
// to retrying a 5xx answer to the create; Tripo and Meshy do not.
func TestHyper3DRetryCoversRateLimitAndUpstreamUnavailable(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, offering := range c.List("") {
		if offering.AdapterKey != "hyper3d.rodin.v1" {
			continue
		}
		if offering.Retry.MaxAttempts < 2 {
			t.Errorf("%s max_attempts = %d, want at least 2", offering.ID, offering.Retry.MaxAttempts)
		}
		allowed := map[generation.FailureClass]bool{}
		for _, class := range offering.Retry.Retryable {
			allowed[generation.FailureClass(class)] = true
		}
		if !allowed[generation.FailureRateLimited] || !allowed[generation.FailureUpstreamUnavailable] {
			t.Errorf("%s retryable = %v, want rate_limited and upstream_unavailable", offering.ID, offering.Retry.Retryable)
		}
	}
	if _, retrySubmit, _, _ := connectionFacts(t, "hyper3d.rodin.v1"); !retrySubmit {
		t.Fatal("hyper3d must declare retry_submit_server_error")
	}
	for _, key := range []string{"tripo.v3", "meshy.v2"} {
		if _, retrySubmit, _, _ := connectionFacts(t, key); retrySubmit {
			t.Fatalf("%s must not declare retry_submit_server_error", key)
		}
	}
}

// #460 C26: API_PARALLELISM_LIMIT_REACHED is retryable.
func TestHyper3DParallelismLimitIsRetryable(t *testing.T) {
	_, _, _, code := connectionFacts(t, "hyper3d.rodin.v1")
	if retryable, ok := code["API_PARALLELISM_LIMIT_REACHED"]; !ok || !retryable {
		t.Fatalf("API_PARALLELISM_LIMIT_REACHED retryable = %v, present = %v", retryable, ok)
	}
}

// #460 C27 / C28 / C29: the Gen-2.5 image prompt is bounded at 1024, the
// sources point at the rodin-gen2-5 page read on 2026-11-22, and the retention
// policy is seven days.
func TestHyper3DGen25SourcesPromptAndRetention(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	image, ok := c.Get("hyper3d/rodin-gen-2.5/image-to-3d")
	if !ok {
		t.Fatal("hyper3d/rodin-gen-2.5/image-to-3d is missing")
	}
	found := false
	for _, parameter := range image.Parameters {
		if parameter.Name == "prompt" {
			found = true
			if parameter.MaxLength != 1024 {
				t.Fatalf("prompt max_length = %d, want 1024", parameter.MaxLength)
			}
		}
	}
	if !found {
		t.Fatal("the image Offering has no prompt")
	}
	for _, id := range []generation.OfferingID{"hyper3d/rodin-gen-2.5/text-to-3d", "hyper3d/rodin-gen-2.5/image-to-3d"} {
		offering, ok := c.Get(id)
		if !ok {
			t.Fatalf("%s is missing", id)
		}
		if len(offering.Knowledge.Sources) == 0 ||
			offering.Knowledge.Sources[0].URL != "https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5" ||
			offering.Knowledge.Sources[0].CheckedAt != "2026-11-22" {
			t.Errorf("%s sources = %+v", id, offering.Knowledge.Sources)
		}
	}
	policy, ok := c.ResultRefetchPolicy("hyper3d.rodin.v1")
	if !ok || policy.RetentionHours != 168 {
		t.Fatalf("hyper3d retention = %#v, %v", policy, ok)
	}
}
