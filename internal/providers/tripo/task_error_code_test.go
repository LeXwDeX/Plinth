package tripo_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// A Tripo task that ends "failed" carries error_code and error_message in its
// task object (official Python SDK: error_code / error_msg). The adapter reads
// the code through the connection's upstream_code_map (the same table as the
// HTTP envelope codes), keeps retryability with the status rule, and puts only
// the sanitized message into the internal evidence (#142).

func observeFailedTask(t *testing.T, body map[string]any, input string) generation.Observation {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"code": 0, "data": body})
	}))
	t.Cleanup(upstream.Close)
	adapter := newAdapter(t, upstream)
	observation, err := adapter.Observe(t.Context(), generation.ObserveRequest{
		AttemptID: "att-1",
		Offering:  tripoOffering(generation.TextTo3D),
		Job:       generation.UpstreamJob{ProviderID: "tripo", RemoteID: "uuid-1"},
		Input:     []byte(input),
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func TestFailedTaskErrorCodeIsClassifiedBySchemaTable(t *testing.T) {
	cases := []struct {
		name   string
		status string
		code   any
		want   generation.FailureClass
	}{
		{"2010 insufficient credits", "failed", 2010, generation.FailureInsufficientBalance},
		// Observed 2026-10-29 (#96): retargeting a rig built with a v2 rig model
		// ends failed with 1004 "Invalid input parameter".
		{"1004 unsupported source after a v2 rig", "failed", 1004, generation.FailureInvalidInput},
		{"2008 content moderation", "failed", 2008, generation.FailureRejected},
		{"2004 unsupported file type", "failed", 2004, generation.FailureInvalidInput},
		{"1001 internal error (real sample)", "failed", 1001, generation.FailureUpstreamUnavailable},
		{"1000 internal error", "failed", 1000, generation.FailureUpstreamUnavailable},
		{"code as quoted number", "failed", "2010", generation.FailureInsufficientBalance},
		{"unmapped code stays rejected", "failed", 3000, generation.FailureRejected},
		{"no code stays rejected", "failed", nil, generation.FailureRejected},
		{"non numeric code is dropped", "failed", "boom", generation.FailureRejected},
		// banned declares no class of its own, so the code applies; expired too.
		{"banned with a moderation code", "banned", 2008, generation.FailureRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"task_id": "uuid-1", "status": tc.status, "progress": 100, "output": map[string]any{}}
			if tc.code != nil {
				body["error_code"] = tc.code
				body["error_message"] = "upstream words"
			}
			observation := observeFailedTask(t, body, `{"prompt":"ship"}`)
			if observation.Phase != generation.UpstreamFailed || observation.Failure == nil {
				t.Fatalf("observation = %+v", observation)
			}
			failure := observation.Failure
			if failure.Class != tc.want {
				t.Errorf("class = %q, want %q", failure.Class, tc.want)
			}
			if failure.Retryable {
				t.Errorf("a failed task must not become retryable from its code (class %q)", failure.Class)
			}
			if failure.SafeDetail != "Tripo 任务状态为 "+tc.status {
				t.Errorf("public detail = %q: it must not carry upstream text", failure.SafeDetail)
			}
		})
	}
}

func TestFailedTaskMessageIsSanitizedIntoEvidenceOnly(t *testing.T) {
	const prompt = "a tiny dragon with golden scales"
	body := map[string]any{
		"task_id": "uuid-1", "status": "failed", "progress": 100, "output": map[string]any{},
		"error_code": 2008,
		"error_message": "Prompt \"" + prompt + "\" blocked, see https://cdn.tripo3d.ai/x.glb?token=abc123 " +
			"for tsk_ABCDEFGH12345678 and Bearer sk-secret, mail me@example.com",
	}
	offering := tripoOffering(generation.TextTo3D)
	offering.Parameters = []generation.Parameter{{Name: "prompt", Type: "text", Semantic: generation.SemanticPrompt}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"code": 0, "data": body})
	}))
	defer upstream.Close()
	observation, err := newAdapter(t, upstream).Observe(t.Context(), generation.ObserveRequest{
		AttemptID: "att-1", Offering: offering,
		Job:   generation.UpstreamJob{ProviderID: "tripo", RemoteID: "uuid-1"},
		Input: []byte(`{"prompt":"` + prompt + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := observation.Failure
	if failure == nil || failure.UpstreamCode != "2008" {
		t.Fatalf("failure = %+v", failure)
	}
	for _, leaked := range []string{prompt, "cdn.tripo3d.ai", "abc123", "tsk_ABCDEFGH", "sk-secret", "me@example.com"} {
		if strings.Contains(failure.UpstreamExcerpt, leaked) || strings.Contains(failure.SafeDetail, leaked) {
			t.Fatalf("leaked %q: excerpt %q detail %q", leaked, failure.UpstreamExcerpt, failure.SafeDetail)
		}
	}
	if !strings.HasPrefix(failure.UpstreamExcerpt, "code=2008 message=Prompt") || !strings.Contains(failure.UpstreamExcerpt, "[input]") {
		t.Fatalf("excerpt = %q", failure.UpstreamExcerpt)
	}
}

// The #96 fake injects the failed-task shape: task-failed ends the task with
// error_code 3000 (not in the Schema table), so it must stay a plain rejection
// whose evidence carries the fake's words. The observed 1004 of the retarget
// path and the real 1001 sample are replayed by the table test above and by
// TestOfficialFailedSmartSegmentationEndsTheAttemptWithoutArtifacts.
func TestFakeInjectedTaskFailureIsReadFromTheTaskObject(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{}, tripov3.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL: server.URL + tripov3.Prefix, APIKey: fakeupstream.KeyPrefix + string(fakeupstream.ScenarioTaskFailed),
		HTTPClient: server.Client(), AllowHTTPAssets: true, ErrorMapping: errorMapping, StatusMapping: statusMapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	offering := tripoOffering(generation.TextTo3D)
	started, err := adapter.Start(t.Context(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", OwnerID: "owner", SubmissionKey: "submission",
		Offering: offering, Input: []byte(`{"prompt":"ship"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var observation generation.Observation
	for range 20 {
		observation, err = adapter.Observe(t.Context(), generation.ObserveRequest{
			TaskID: "task", AttemptID: "attempt", Offering: offering, Job: *started.Job,
		})
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamFailed {
			break
		}
	}
	failure := observation.Failure
	if observation.Phase != generation.UpstreamFailed || failure == nil {
		t.Fatalf("observation = %+v", observation)
	}
	if failure.Class != generation.FailureRejected || failure.UpstreamCode != "3000" ||
		failure.UpstreamExcerpt != "code=3000 message=Fake injected task failure" {
		t.Fatalf("failure = %+v", failure)
	}
}

// Tripo's "unknown" status is not terminal (the official Python SDK's
// wait_for_task stops only on success, failed, cancelled, banned and expired),
// so the adapter keeps polling, but at most max_consecutive times in a row.
func TestUnknownStatusIsBoundedPolling(t *testing.T) {
	_, statusMapping := declaredTripoMappings(t)
	rule, ok := statusMapping.UpstreamStatus["unknown"]
	if !ok || rule.MaxConsecutive <= 0 || rule.Class == "" {
		t.Fatalf("unknown is not declared with a bound: %+v", rule)
	}
	status := "unknown"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"task_id": "uuid-1", "status": status, "progress": 0, "output": map[string]any{}}})
	}))
	defer upstream.Close()
	adapter := newAdapter(t, upstream)
	observe := func(remote string) generation.Observation {
		observation, err := adapter.Observe(t.Context(), generation.ObserveRequest{
			AttemptID: "att-1", Offering: tripoOffering(generation.TextTo3D),
			Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: remote},
		})
		if err != nil {
			t.Fatal(err)
		}
		return observation
	}
	lastRunningKey := ""
	for poll := 1; poll <= rule.MaxConsecutive; poll++ {
		observation := observe("uuid-1")
		if observation.Phase != generation.UpstreamRunning || observation.Failure != nil {
			t.Fatalf("poll %d = %+v, want running", poll, observation)
		}
		lastRunningKey = observation.DedupeKey
	}
	observation := observe("uuid-1")
	// The coordinator discards an observation whose DedupeKey equals the last
	// recorded one before applying its phase, so the terminal transition must
	// not reuse the running key.
	if observation.DedupeKey == "" || observation.DedupeKey == lastRunningKey {
		t.Fatalf("limit-exceeded key %q must differ from the running key %q", observation.DedupeKey, lastRunningKey)
	}
	if observation.Phase != generation.UpstreamFailed || observation.Failure == nil ||
		string(observation.Failure.Class) != rule.Class || observation.Failure.Retryable {
		t.Fatalf("poll past the bound = %+v", observation)
	}
	// Another task has its own allowance, and a known status resets the count.
	if observation := observe("uuid-2"); observation.Phase != generation.UpstreamRunning {
		t.Fatalf("other task = %+v", observation)
	}
	status = "running"
	observe("uuid-2")
	status = "unknown"
	for poll := 1; poll <= rule.MaxConsecutive; poll++ {
		if observation := observe("uuid-2"); observation.Phase != generation.UpstreamRunning {
			t.Fatalf("after reset, poll %d = %+v", poll, observation)
		}
	}
	if observation := observe("uuid-2"); observation.Phase != generation.UpstreamFailed {
		t.Fatalf("after reset, past the bound = %+v", observation)
	}
	// unknown that turns into success within the bound still completes.
	status = "unknown"
	observe("uuid-3")
	status = "success"
	if observation := observe("uuid-3"); observation.Phase != generation.UpstreamCompleted {
		t.Fatalf("success after unknown = %+v", observation)
	}
}
