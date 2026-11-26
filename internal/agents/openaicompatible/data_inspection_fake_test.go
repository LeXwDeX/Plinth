package openaicompatible_test

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
)

// formal-test, 2026-11-26: DashScope's compatible endpoint refused a request's
// pictures inside the HTTP 200 stream it had started (data_inspection_failed,
// openaiv1.DataInspectionMarker). The Adapter reads the frame as a content
// review refusal, not a transient failure, so the round is not sent again as
// it is; the orchestrator's answer (streamAgent, pictureRefusalContent) is the
// same request without its pictures, which the fake answers as usual.
func TestTheFakesDataInspectionRefusalIsAContentRejection(t *testing.T) {
	adapter := resilienceAdapter(t, resilienceFake(t, fakeupstream.Config{}), "test-key", nil)
	picture, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "png", Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	text := "what is on it " + openaiv1.DataInspectionMarker
	withPicture := agents.Request{Model: "qwen3.6-plus", Messages: []agents.Message{{Role: "user", Parts: []agents.ContentPart{
		{Kind: "text", Text: text},
		{Kind: "image_url", URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(picture.Data)},
	}}}}
	retried := agents.NewRetrying(adapter, agents.RetryPolicy{
		MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	_, err = retried.Stream(context.Background(), withPicture, nil)
	var rejection *agents.ContentRejection
	if !errors.As(err, &rejection) || !errors.Is(err, agents.ErrContentRejected) || errors.Is(err, agents.ErrTransient) {
		t.Fatalf("with the picture = %v, want a content rejection, not transient", err)
	}
	if len(rejection.Reasons) == 0 {
		t.Fatalf("reasons = %v, want the code's tokens", rejection.Reasons)
	}
	withoutPicture := agents.Request{Model: "qwen3.6-plus", Messages: []agents.Message{{Role: "user", Content: text}}}
	completion, err := retried.Stream(context.Background(), withoutPicture, nil)
	if err != nil || completion.Content != openaiv1.FixedReply {
		t.Fatalf("without the picture = %q, %v; want the fixed reply", completion.Content, err)
	}
}
