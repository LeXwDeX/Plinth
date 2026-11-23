package tripo_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// A Tripo task that ends "failed" carries error_code and error_message in its
// task object (official Python SDK: error_code / error_msg). The adapter reads
// the code through the connection's task_error_code_map only: the official
// error-handling page states that the request-level code and a failed task's
// error_code are two vocabularies and that "the same number can mean something
// else", and the task-query page publishes no complete task-level table. So a
// task code refines the class only when the Schema declares it, and anything
// else is kept as evidence (#142, #487).

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
		// The task-level table declares the two server errors as transient.
		{"1001 fatal server error", "failed", 1001, generation.FailureUpstreamUnavailable},
		{"1000 unknown server error", "failed", 1000, generation.FailureUpstreamUnavailable},
		{"code as quoted number", "failed", "1001", generation.FailureUpstreamUnavailable},
		// 2018 is a request-level code ("model too complex to remesh"); as a
		// task-level error_code the official pages publish no meaning, so the
		// adapter does not borrow the request-level one.
		{"2018 stays unclassified", "failed", 2018, generation.FailureRejected},
		{"2008 stays unclassified", "failed", 2008, generation.FailureRejected},
		{"2010 stays unclassified", "failed", 2010, generation.FailureRejected},
		{"unmapped code stays rejected", "failed", 3000, generation.FailureRejected},
		{"no code stays rejected", "failed", nil, generation.FailureRejected},
		{"non numeric code is dropped", "failed", "boom", generation.FailureRejected},
		// banned declares no class of its own, so the task-level table applies.
		{"banned with a task code", "banned", 1001, generation.FailureUpstreamUnavailable},
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
			if numeric, isNumeric := tc.code.(int); isNumeric && numeric > 0 && failure.UpstreamCode == "" {
				t.Errorf("the task code must stay in evidence, got %q", failure.UpstreamCode)
			}
		})
	}
}

// #487: the request-level table and the task-level table are separate, so a
// number that only exists in the request-level one never classifies a task.
func TestRequestLevelCodeNeverClassifiesATask(t *testing.T) {
	mapping, _ := declaredTripoMappings(t)
	if _, declared := mapping.TaskErrorCodeMap["2010"]; declared {
		t.Fatal("2010 is a request-level code and must not be declared at task level")
	}
	if _, declared := mapping.UpstreamCodeMap["1001"]; !declared {
		t.Fatal("the request-level table lost 1001")
	}
	observation := observeFailedTask(t, map[string]any{
		"task_id": "uuid-1", "status": "failed", "progress": 100,
		"output": map[string]any{}, "error_code": 2010,
	}, `{"prompt":"ship"}`)
	if observation.Failure == nil || observation.Failure.Class != generation.FailureRejected {
		t.Fatalf("failure = %+v, want the status rule's rejection", observation.Failure)
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
// the official transient code 1001, so the attempt keeps the upstream class and
// the evidence carries the fake's words. The observed 1004 of the retarget path
// is replayed by the table test above and by
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
	if failure.Class != generation.FailureUpstreamUnavailable || failure.UpstreamCode != "1001" ||
		failure.UpstreamExcerpt != "code=1001 message=Fake injected task failure" {
		t.Fatalf("failure = %+v", failure)
	}
}

// Tripo's "unknown" status is not terminal (the official task-query page says
// it is neither success nor failure and to continue polling with a timeout), so
// the adapter keeps polling, but only for as long as the connection's declared
// duration bound allows. When it passes, the task ends as an ambiguous failure:
// the upstream result is unknown, so nothing is resent and no retry is offered.
func TestUnknownStatusIsBoundedByDuration(t *testing.T) {
	_, statusMapping := declaredTripoMappings(t)
	rule, ok := statusMapping.UpstreamStatus["unknown"]
	if !ok || rule.MaxDurationSeconds <= 0 {
		t.Fatalf("unknown is not declared with a duration bound: %+v", rule)
	}
	status := "unknown"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"task_id": "uuid-1", "status": status, "progress": 0, "output": map[string]any{}}})
	}))
	defer upstream.Close()
	now := time.Now()
	adapter := newAdapterWithClock(t, upstream, func() time.Time { return now })
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
	// Inside the bound the task stays running, however many polls it takes
	// (the old poll-count bound failed after a hundred).
	for i := 0; i < 300; i++ {
		now = now.Add(time.Second)
		if observation := observe("uuid-1"); observation.Phase != generation.UpstreamRunning {
			t.Fatalf("poll %d inside the bound = %+v", i, observation)
		}
	}
	lastRunningKey := observe("uuid-1").DedupeKey
	// The window opened at the first unknown poll: 300 further seconds pass it.
	now = now.Add(time.Duration(rule.MaxDurationSeconds) * time.Second)
	observation := observe("uuid-1")
	// The coordinator discards an observation whose DedupeKey equals the last
	// recorded one before applying its phase, so the terminal transition must
	// not reuse the running key.
	if observation.DedupeKey == "" || observation.DedupeKey == lastRunningKey {
		t.Fatalf("limit-exceeded key %q must differ from the running key %q", observation.DedupeKey, lastRunningKey)
	}
	if observation.Phase != generation.UpstreamFailed || observation.Failure == nil ||
		observation.Failure.Class != generation.FailureSubmissionUnknown ||
		!observation.Failure.Ambiguous || observation.Failure.Retryable {
		t.Fatalf("poll past the duration bound = %+v", observation)
	}
	// Another task has its own window, and a known status closes it.
	if observation := observe("uuid-2"); observation.Phase != generation.UpstreamRunning {
		t.Fatalf("other task = %+v", observation)
	}
	status = "running"
	observe("uuid-2")
	status = "unknown"
	now = now.Add(time.Duration(rule.MaxDurationSeconds) * time.Second)
	if observation := observe("uuid-2"); observation.Phase != generation.UpstreamRunning {
		t.Fatalf("a known status must close the unknown window: %+v", observation)
	}
	// unknown that turns into success within the bound still completes.
	status = "unknown"
	observe("uuid-3")
	status = "success"
	if observation := observe("uuid-3"); observation.Phase != generation.UpstreamCompleted {
		t.Fatalf("success after unknown = %+v", observation)
	}
}
