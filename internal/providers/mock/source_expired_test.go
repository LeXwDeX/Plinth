package mock

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #326 N1/N2: the browser checks need a step the Provider refuses because it
// no longer keeps the source's original task (Meshy answers 404 Task not
// found). MarkerSourceExpired makes the mock answer that way.
func TestSourceExpiredMarkerAnswersAsAProviderThatLostTheSourceJob(t *testing.T) {
	_, err := New().Start(context.Background(), generation.StartRequest{
		TaskID: "t", AttemptID: "a", SubmissionKey: "expired",
		Offering: imageOffering("retexture"),
		Input:    []byte(`{"source_task":"task-1","prompt":"warm walnut ` + MarkerSourceExpired + `"}`),
	})
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure == nil {
		t.Fatalf("err = %v, want a ProviderError", err)
	}
	if failure := providerErr.Failure; failure.Class != generation.FailureSourceExpired || failure.Retryable || failure.UpstreamStatus != 404 {
		t.Fatalf("failure = %#v", failure)
	}
}
