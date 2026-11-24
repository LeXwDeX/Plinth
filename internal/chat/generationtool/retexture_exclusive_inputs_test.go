package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// A retexture service that takes a text prompt or a style picture, but not
// both (Meshy retexture's declared input rule), refuses a call that gives
// both with the rule itself: which inputs, how many it takes, which tool
// arguments carry them. The kernel's own normalization refuses it before
// anything is frozen, submitted or charged, and the Agent no longer receives
// only the generic invalid-parameters copy.
func TestRetextureWithPromptAndReferenceNamesTheExclusiveInputs(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.normalize = true
	history := []chat.Message{{Role: "user", ImageURLs: []string{"/api/v1/input-assets/wood"}}}
	result, _ := h.runAs("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"model-1","prompt":"oak wood grain","reference_image_indices":[1],"resolution":"2k"}`, history)
	if !result.NoSubmission || result.Suspend || h.gateway.submitCount != 0 || len(h.gateway.frozen) != 0 {
		t.Fatalf("both inputs reached the paid path: %+v submits=%d frozen=%d", result, h.gateway.submitCount, len(h.gateway.frozen))
	}
	content := h.content(result)
	message, _ := content["error"].(string)
	t.Logf("refusal: %s", message)
	generic := h.app.Chat.Presentation.Messages.InvalidParameters
	for _, want := range []string{"prompt", "style_asset", "reference_handles", "1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("refusal %q does not name %q", message, want)
		}
	}
	if generic != "" && strings.Contains(message, generic) {
		t.Fatalf("refusal still carries the generic copy: %q", message)
	}
	if strings.Contains(message, "oak wood") || strings.Contains(message, "input-assets") {
		t.Fatalf("refusal echoed the person's input: %q", message)
	}
}

// The same rule in English for an English turn.
func TestRetextureExclusiveInputsRefusalIsLocalized(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.normalize = true
	history := []chat.Message{{Role: "user", ImageURLs: []string{"/api/v1/input-assets/wood"}}}
	result, _ := h.runWith("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"model-1","prompt":"oak wood grain","reference_image_indices":[1]}`, history,
		chat.TurnOptions{Locale: "en"})
	if !result.NoSubmission || h.gateway.submitCount != 0 {
		t.Fatalf("both inputs reached the paid path: %+v", result)
	}
	message, _ := h.content(result)["error"].(string)
	t.Logf("refusal: %s", message)
	if !strings.Contains(message, "at most 1") || !strings.Contains(message, "reference_handles") {
		t.Fatalf("English refusal = %q", message)
	}
	for _, r := range message {
		if r >= 0x4e00 && r <= 0x9fff {
			t.Fatalf("English refusal carries Chinese text: %q", message)
		}
	}
}

// Either input alone still runs.
func TestRetextureWithOneStyleInputStillRuns(t *testing.T) {
	for name, arguments := range map[string]string{
		"prompt":    `{"source_task_id":"model-1","prompt":"oak wood grain"}`,
		"reference": `{"source_task_id":"model-1","reference_image_indices":[1]}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newProcessingHarness(t, "auto", meshyCatalogTasks)
			h.gateway.normalize = true
			history := []chat.Message{{Role: "user", ImageURLs: []string{"/api/v1/input-assets/wood"}}}
			result, _ := h.runAs("owner-a", "session-a", "retexture_model", arguments, history)
			if result.NoSubmission || h.gateway.submitCount != 1 {
				t.Fatalf("one style input was refused: %+v submits=%d", result, h.gateway.submitCount)
			}
		})
	}
}
