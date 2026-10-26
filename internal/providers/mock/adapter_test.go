package mock

import (
	"context"
	"image/png"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestImageArtifactIsARealPNGPreview(t *testing.T) {
	adapter := New()
	minimum := 1
	offering := generation.Offering{
		ProviderID: "openai",
		Outputs: []generation.AssetRequirement{{
			Role:       "image",
			Minimum:    &minimum,
			MediaTypes: []string{"image/png"},
		}},
	}
	started, err := adapter.Start(
		context.Background(),
		generation.StartRequest{
			TaskID:        "task-image-preview",
			AttemptID:     "attempt-image-preview",
			SubmissionKey: "submission-image-preview",
			Offering:      offering,
			Input:         []byte(`{"prompt":"real PNG preview"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := generation.ObserveRequest{
		TaskID:    "task-image-preview",
		AttemptID: "attempt-image-preview",
		Offering:  offering,
		Job:       *started.Job,
	}
	if _, err := adapter.Observe(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	completed, err := adapter.Observe(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Artifacts) != 1 ||
		completed.Artifacts[0].MediaType != "image/png" {
		t.Fatalf("completed artifacts = %#v", completed.Artifacts)
	}
	body, metadata, err := adapter.OpenAsset(
		context.Background(),
		generation.OpenAssetRequest{
			TaskID:    request.TaskID,
			AttemptID: request.AttemptID,
			Offering:  offering,
			Job:       completed.Job,
			Claim:     completed.Artifacts[0],
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	preview, err := png.Decode(body)
	if err != nil {
		t.Fatalf("decode mock PNG preview: %v", err)
	}
	if metadata.MediaType != "image/png" ||
		preview.Bounds().Dx() <= 0 ||
		preview.Bounds().Dy() <= 0 {
		t.Fatalf("metadata=%#v bounds=%v", metadata, preview.Bounds())
	}
}

func TestJobIdentityDoesNotCollideAfterAdapterRestart(t *testing.T) {
	first := startTestJob(t, New(), "submission-a")
	second := startTestJob(t, New(), "submission-b")

	if first.RemoteID == second.RemoteID {
		t.Fatalf(
			"不同提交在适配器重启后复用了上游作业 ID %q",
			first.RemoteID,
		)
	}
}

func TestSameSubmissionKeepsJobIdentityAfterAdapterRestart(t *testing.T) {
	beforeRestart := New()
	startTestJob(t, beforeRestart, "unrelated-submission")
	first := startTestJob(t, beforeRestart, "stable-submission")

	afterRestart := New()
	second := startTestJob(t, afterRestart, "stable-submission")

	if first.RemoteID != second.RemoteID {
		t.Fatalf(
			"相同提交在适配器重启后作业 ID 从 %q 变为 %q",
			first.RemoteID,
			second.RemoteID,
		)
	}
}

func startTestJob(
	t *testing.T,
	adapter *Adapter,
	submissionKey string,
) generation.UpstreamJob {
	t.Helper()
	observation, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID:        generation.TaskID("task-" + submissionKey),
		AttemptID:     generation.AttemptID("attempt-" + submissionKey),
		SubmissionKey: submissionKey,
		Offering: generation.Offering{
			ProviderID: "openai",
		},
		Input: []byte(`{"prompt":"restart-safe identity"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Job == nil || observation.Job.RemoteID == "" {
		t.Fatalf("observation job = %#v", observation.Job)
	}
	return *observation.Job
}

// #321: a result made from a large mock result is large too, so a Bang result
// of a [mock:large_model] Rodin model is over the Bang local-model limit,
// like the 91.8 MB dev-test result.
func TestAJobMadeFromALargeJobIsLarge(t *testing.T) {
	adapter := New()
	observation, err := adapter.Start(context.Background(), generation.StartRequest{TaskID: "task-large", AttemptID: "attempt-large",
		SubmissionKey: "large", Offering: generation.Offering{ProviderID: "hyper3d"}, Input: []byte(`{"prompt":"totem ` + MarkerLargeModel + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	derived, err := adapter.Start(context.Background(), generation.StartRequest{TaskID: "task-derived", AttemptID: "attempt-derived",
		SubmissionKey: "derived", Offering: generation.Offering{ProviderID: "hyper3d"}, Input: []byte(`{"source_task":"task-large"}`),
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "task-large", UpstreamJob: *observation.Job}}})
	if err != nil {
		t.Fatal(err)
	}
	plain := startTestJob(t, adapter, "plain")
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if !adapter.jobs[derived.Job.RemoteID].large || adapter.jobs[plain.RemoteID].large {
		t.Fatalf("derived large = %v, plain large = %v", adapter.jobs[derived.Job.RemoteID].large, adapter.jobs[plain.RemoteID].large)
	}
}
