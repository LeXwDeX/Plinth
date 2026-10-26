package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// #326 N1: the 加工 panel mirrors the upstream-job age rule, so the public
// task says when the Task holding its job was made and whether the job is gone.
func TestTaskResponseCarriesTheUpstreamJobFacts(t *testing.T) {
	t.Parallel()
	route, err := routing.ParseTwo("/api/v1/tasks/{taskID}/assets/{assetID}", "taskID", "assetID")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{generation: &generation.Module{}, taskAssetRoute: route}
	job := time.Date(2026, 10, 18, 9, 43, 16, 0, time.UTC)
	task := generation.Task{ID: "recovered", State: generation.TaskSucceeded, CreatedAt: job.Add(56 * time.Hour),
		UpstreamJobCreatedAt: job, UpstreamJobGone: true}
	encoded, _ := json.Marshal(s.newTaskResponse("", task))
	for _, want := range []string{`"upstream_job_created_at":"2026-10-18T09:43:16Z"`, `"upstream_job_gone":true`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("task response lacks %s: %s", want, encoded)
		}
	}
	plain, _ := json.Marshal(s.newTaskResponse("", generation.Task{ID: "plain", CreatedAt: job}))
	if strings.Contains(string(plain), "upstream_job") {
		t.Fatalf("a task holding its own job says more: %s", plain)
	}
}

// #326: the owner's task detail says whether running a step that failed as
// source_expired again can succeed, so the chat card offers a paid
// regenerate only when it can. The value is a public code, never a field name.
func TestTaskResponseCarriesTheSourceRetryJudgment(t *testing.T) {
	t.Parallel()
	s := &Server{generation: &generation.Module{}}
	failed := generation.Task{ID: "failed", State: generation.TaskFailed, Failure: &generation.Failure{Class: generation.FailureSourceExpired}}
	for judgment, want := range map[generation.SourceRetry]string{
		generation.SourceRetrySavedFile:    `"source_retry":"saved_file"`,
		generation.SourceRetryExpired:      `"source_retry":"source_expired"`,
		generation.SourceRetryIncompatible: `"source_retry":"source_incompatible"`,
	} {
		response := s.newTaskResponse("", failed)
		withSourceRetry(&response, judgment)
		encoded, _ := json.Marshal(response)
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("%q: task response lacks %s: %s", judgment, want, encoded)
		}
	}
	response := s.newTaskResponse("", failed)
	withSourceRetry(&response, generation.SourceRetryAsBefore)
	if encoded, _ := json.Marshal(response); strings.Contains(string(encoded), "source_retry") {
		t.Fatalf("no judgment says more: %s", encoded)
	}
	succeeded := s.newTaskResponse("", generation.Task{ID: "done", State: generation.TaskSucceeded})
	withSourceRetry(&succeeded, generation.SourceRetrySavedFile)
	if encoded, _ := json.Marshal(succeeded); strings.Contains(string(encoded), "source_retry") {
		t.Fatalf("a task without a failure says more: %s", encoded)
	}
}
