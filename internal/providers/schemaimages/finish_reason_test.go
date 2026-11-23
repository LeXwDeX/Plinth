package schemaimages_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
)

// Google answers a blocked prompt, or an image it refuses to produce, with HTTP
// 200 and no picture: the legacy generateContent envelope carries the reason as
// candidates[].finishReason (SAFETY, IMAGE_SAFETY, IMAGE_PROHIBITED_CONTENT,
// IMAGE_RECITATION, NO_IMAGE, …) and a prompt-level block as
// promptFeedback.blockReason. The gateway this Offering talks to serves the
// chat/completions shape, so the same reason word arrives on the choice
// (choices[].finish_reason) instead of a candidate, and the field name follows the
// chat/completions shape rather than the model's camelCase; the words are the
// model's.
// The Offering's protocol declares where to read them and the connection's
// error_mapping says what each word means, so such an answer is a content-safety
// refusal rather than the "finished but returned no picture" protocol failure it
// used to be. Official reference:
// https://aistudio.google.com/api-reference/generate-content/generate-content
// (finishReason) and https://aistudio.google.com/docs/safety-settings.md
// (blockReason, promptFeedback), read 2026-11-23.
func TestOfficialFinishReasonClassifiesAPicturelessAnswer(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := officialOffering(t, product, "gemini/gemini-3-pro-image/text-to-image")
	mapping := declaredImageMapping(t, "gemini-images")
	for name, testCase := range map[string]struct {
		body      string
		wantClass generation.FailureClass
		wantRetry bool
	}{
		"candidate blocked by safety": {
			body:      `{"choices":[{"finish_reason":"SAFETY","content":{"parts":[]}}]}`,
			wantClass: generation.FailureContentRejected,
		},
		"image safety": {
			body:      `{"choices":[{"finish_reason":"IMAGE_SAFETY","content":{"parts":[]}}]}`,
			wantClass: generation.FailureContentRejected,
		},
		"image prohibited content": {
			body:      `{"choices":[{"finish_reason":"IMAGE_PROHIBITED_CONTENT","content":{"parts":[]}}]}`,
			wantClass: generation.FailureContentRejected,
		},
		"recitation": {
			body:      `{"choices":[{"finish_reason":"RECITATION","content":{"parts":[]}}]}`,
			wantClass: generation.FailureContentRejected,
		},
		"prompt blocked before any candidate": {
			body:      `{"promptFeedback":{"blockReason":"IMAGE_SAFETY"},"candidates":[]}`,
			wantClass: generation.FailureContentRejected,
		},
		// The model was expected to draw and drew nothing: the same input may
		// succeed, so this is an upstream failure the task may retry, not a
		// refusal of the input.
		"no image produced": {
			body:      `{"choices":[{"finish_reason":"NO_IMAGE","content":{"parts":[]}}]}`,
			wantClass: generation.FailureUpstreamUnavailable,
			wantRetry: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(testCase.body))
			}))
			t.Cleanup(upstream.Close)
			adapter, err := schemaimages.New(schemaimages.Config{
				BaseURL: upstream.URL, APIKey: "stub-key", HTTPClient: http.DefaultClient,
				ErrorMapping: mapping, InputAssets: officialAssetStore{},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Start(t.Context(), generation.StartRequest{
				TaskID: "task", AttemptID: "attempt", OwnerID: "owner", SubmissionKey: "submission",
				Offering: offering, Input: []byte(`{"prompt":"a plain red cube"}`),
			})
			failure := requireFailure(t, err)
			if failure.Class != testCase.wantClass || failure.Retryable != testCase.wantRetry {
				t.Fatalf("failure = %+v, want class %s retryable %v", failure, testCase.wantClass, testCase.wantRetry)
			}
			if failure.UpstreamStatus != 0 {
				t.Fatalf("a 200 answer must not read as an HTTP error, got status %d", failure.UpstreamStatus)
			}
			if failure.SafeDetail == "" || !strings.Contains(failure.SafeDetail, "没有生成结果") {
				t.Fatalf("safe detail = %q", failure.SafeDetail)
			}
		})
	}
}

// A reason word the connection declares nothing for stays the protocol failure
// it was: the Adapter never invents a class the Schema does not declare.
func TestOfficialFinishReasonWithoutAMappingStaysAProtocolFailure(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := officialOffering(t, product, "gemini/gemini-3-pro-image/text-to-image")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"OTHER","content":{"parts":[]}}]}`))
	}))
	t.Cleanup(upstream.Close)
	adapter, err := schemaimages.New(schemaimages.Config{
		BaseURL: upstream.URL, APIKey: "stub-key", HTTPClient: http.DefaultClient,
		ErrorMapping: &generation.ErrorMapping{}, InputAssets: officialAssetStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Start(t.Context(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", OwnerID: "owner", SubmissionKey: "submission",
		Offering: offering, Input: []byte(`{"prompt":"a plain red cube"}`),
	})
	if failure := requireFailure(t, err); failure.Class != generation.FailureProtocol {
		t.Fatalf("failure = %+v, want protocol", failure)
	}
}

func requireFailure(t *testing.T, err error) *generation.Failure {
	t.Helper()
	if err == nil {
		t.Fatal("the picture-less answer must fail the task")
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("not a provider failure: %v", err)
	}
	return provider.Failure
}
