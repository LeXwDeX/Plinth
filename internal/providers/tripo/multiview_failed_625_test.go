package tripo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// #625: a real edit of the front view ended "failed" with error_code 1001
// "Failed to generate image" (2026-11-25). Against the fake's task-failed scenario,
// which ends an edit the same way, the attempt fails as upstream_unavailable with
// the task's code and words in the evidence, and claims no view.
func TestFailedMultiviewEditIsUpstreamUnavailable(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{AccountConcurrencyLimit: -1}, tripov3.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	base := server.URL + tripov3.Prefix

	// The source: a succeeded image_to_multiview task of the fake, made with a plain key.
	call := func(method, path string, body any) map[string]any {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		request, err := http.NewRequest(method, base+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+fakeupstream.KeyPrefix+"plain")
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var decoded map[string]any
		if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	created := call(http.MethodPost, "/generation/image-to-multiview", map[string]any{"input": "file_x"})
	remote, _ := created["data"].(map[string]any)["task_id"].(string)
	for range 10 {
		if status := call(http.MethodGet, "/tasks/"+remote, nil)["data"].(map[string]any)["status"]; status == "success" {
			break
		}
	}

	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get("tripo/edit-multiview")
	if !ok {
		t.Fatal("missing tripo/edit-multiview")
	}
	em, sm := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL: base, APIKey: fakeupstream.KeyPrefix + string(fakeupstream.ScenarioTaskFailed),
		HTTPClient: server.Client(), AllowHTTPAssets: true, ErrorMapping: em, StatusMapping: sm,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"source_task":"source-public","front_prompt":"make the body plain red","left_prompt":"","back_prompt":"","right_prompt":"","authorize_extra_credits":true}`)
	source := generation.TaskDependency{TaskID: "source-public", Capability: generation.ImageToMultiview, OfferingID: "tripo/image-to-multiview", ProviderID: "tripo", SessionID: "session",
		UpstreamJob: generation.UpstreamJob{ProviderID: "tripo", RemoteID: remote}, ProviderBinding: json.RawMessage(`{"task_id":"source-public","attempt_id":"source-attempt","remote_id":"` + remote + `"}`)}
	request := generation.StartRequest{TaskID: "edit-public", AttemptID: "edit-attempt", OwnerID: "owner", SessionID: "session", SubmissionKey: "edit-submit",
		Offering: o, Input: input, TaskDependencies: map[string]generation.TaskDependency{"source_task": source}}
	started, err := adapter.Start(generation.WithDispatchCheckpointer(context.Background(), &recordingCheckpointer{}), request)
	if err != nil {
		t.Fatal(err)
	}
	var observation generation.Observation
	for range 20 {
		observation, err = adapter.Observe(context.Background(), generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: o, Input: input, Opaque: started.Opaque, Job: *started.Job})
		if err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamFailed {
			break
		}
		started.Opaque = observation.Opaque
	}
	failure := observation.Failure
	if observation.Phase != generation.UpstreamFailed || failure == nil || len(observation.Artifacts) != 0 {
		t.Fatalf("observation = %+v", observation)
	}
	if failure.Class != generation.FailureUpstreamUnavailable || failure.UpstreamCode != "1001" ||
		failure.UpstreamExcerpt != "code=1001 message=Failed to generate image" {
		t.Fatalf("failure = %+v", failure)
	}
}
