package app

import (
	"context"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #563: Rodin finished (and billed) the task, but every download of its
// result failed (the fake's artifact-404). The failure after the upstream
// finished is a charged failure: it ends as integrity_failure, the class the
// Schema's generation_failed_metered_by_class declares, so the #294 guard
// counts it and no Agent or MCP step repeats the charge without a person.
func TestDownloadFailureAfterUpstreamCompletionIsAChargedFailure(t *testing.T) {
	harness, product, _ := fakeStackHarness(t, func(origin string) map[string]string {
		return map[string]string{
			"hyper3d_base_url": origin + "/hyper3d/api/v2",
			"hyper3d_api_key":  fakeUpstreamKeyPrefix + string(fakeupstream.ScenarioArtifactNotFound),
		}
	})
	accepted := submitStep(t, harness, generation.TextTo3D, "hyper3d/rodin-gen-2.5/text-to-3d",
		`{"prompt":"a carved oak chair","authorize_extra_credits":true}`, "hyper3d-artifact-404", nil)
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskFailed || task.Failure == nil {
		t.Fatalf("state = %s, failure = %#v, want a failed Task", task.State, task.Failure)
	}
	if !task.Offering.CreditsMetered {
		t.Fatal("the Rodin Offering is not credits_metered: the guard would not apply")
	}
	failure := task.Failure
	if failure.Class != generation.FailureIntegrity || !failure.Retryable || failure.Ambiguous {
		t.Fatalf("failure = %#v, want a retryable, unambiguous integrity_failure", failure)
	}
	if failure.SafeDetail == "" {
		t.Fatalf("the download's detail was lost: %#v", failure)
	}
	chat := product.Application().Chat
	if _, declared := chat.Presentation.Messages.GenerationFailedMeteredByClass[string(failure.Class)]; !declared {
		t.Fatalf("generation_failed_metered_by_class does not declare %s", failure.Class)
	}
	unstored, err := generationtool.UnstoredChargedTasks(context.Background(), harness.module, chat,
		"owner-acme", "acme-session", []generation.Capability{generation.TextTo3D, generation.ImageTo3D})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(unstored, accepted.TaskID) {
		t.Fatalf("the #294 guard does not see the charged failure: %v", unstored)
	}
}
