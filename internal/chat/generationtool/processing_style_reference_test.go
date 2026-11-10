package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func styleLockedOptions(references ...string) chat.TurnOptions {
	return chat.TurnOptions{StyleLock: chat.StyleLock{Enabled: true, Text: "ink wash", References: references}}
}

// #250: a retexture step that accepts a reference picture carries the
// session's style picture when the step did not fill that style input; the
// style text stays out of the 3D prompt.
func TestRetextureCarriesTheStyleLockReferencePicture(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.runWith("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"model-1"}`, nil, styleLockedOptions("style-a"))
	if result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("retexture rejected: %+v submits=%d", result, h.gateway.submitCount)
	}
	input := h.frozenInput(0)
	if input["style_asset"] != "configured-input-asset" {
		t.Fatalf("style input = %v", input)
	}
	if _, carried := input["prompt"]; carried {
		t.Fatalf("the style text leaked into the 3D prompt: %v", input["prompt"])
	}
	if resolver, ok := h.config.References.(*fakeReferences); ok &&
		strings.Join(resolver.urls, ",") != "/api/v1/input-assets/style-a" {
		t.Fatalf("style reference URLs = %v", resolver.urls)
	}
}

// #250: when the Offering's own input rule gives a prompt and a reference
// picture one slot, a step that wrote the prompt keeps it and reports the
// skipped style picture instead of freezing an invalid input.
func TestRetexturePromptOccupiesTheStyleInput(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.runWith("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"model-1","prompt":"mossy stone"}`, nil, styleLockedOptions("style-a"))
	if result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("retexture rejected: %+v submits=%d", result, h.gateway.submitCount)
	}
	input := h.frozenInput(0)
	if _, carried := input["style_asset"]; carried {
		t.Fatalf("the style picture replaced the prompt's slot: %v", input)
	}
	if resolver, ok := h.config.References.(*fakeReferences); ok &&
		strings.Contains(strings.Join(resolver.urls, ","), "style-a") {
		t.Fatalf("the style picture was resolved although the prompt fills the slot: %v", resolver.urls)
	}
	content := h.content(result)
	warning, _ := content["warning"].(string)
	if !strings.Contains(warning, "这一步已经指定了参考图") {
		t.Fatalf("skip note missing from warning: %v", content)
	}
}

// #250: a step that already chose a picture for the single reference parameter
// is not silently replaced; the result says why the style picture was skipped.
func TestRetextureKeepsItsOwnReferenceAndReportsTheStyleSkip(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	history := []chat.Message{{Role: "user", ImageURLs: []string{"/api/v1/input-assets/turn"}}}
	result, _ := h.runWith("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"model-1","reference_image_indices":[1]}`, history, styleLockedOptions("style-a"))
	if result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("retexture rejected: %+v submits=%d", result, h.gateway.submitCount)
	}
	input := h.frozenInput(0)
	if input["style_asset"] != "configured-input-asset" {
		t.Fatalf("the step's own picture must stay: %v", input)
	}
	if resolver, ok := h.config.References.(*fakeReferences); ok {
		if strings.Contains(strings.Join(resolver.urls, ","), "style-a") {
			t.Fatalf("the style picture was resolved although the step named one: %v", resolver.urls)
		}
	}
	content := h.content(result)
	warning, _ := content["warning"].(string)
	if !strings.Contains(warning, "这一步已经指定了参考图") {
		t.Fatalf("skip note missing from warning: %v", content)
	}
}

// #250: a retexture service without a reference-image parameter reports that
// the style picture was not sent instead of failing the step.
func TestRetextureWithoutReferenceInputReportsTheUnsupportedStylePicture(t *testing.T) {
	h := newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		tasks := meshyCatalogTasks(h)
		tripo := modelTask("tripo-1", "owner-a", "session-a", h.tripo, "image-to-3d", "glb")
		tripo.Offering = generation.Offering{
			ID: "tripo/tripo-v3.1-full/image-to-3d", ProviderID: h.tripo,
			ProviderName: "Tripo3D", ModelName: "Tripo v3.1",
		}
		return append(tasks, tripo)
	})
	result, _ := h.runWith("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"tripo-1","prompt":"rusty","offering_id":"tripo/processing/texture"}`,
		nil, styleLockedOptions("style-a"))
	if result.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("retexture rejected: %+v submits=%d", result, h.gateway.submitCount)
	}
	input := h.frozenInput(0)
	if _, carried := input["reference_image"]; carried {
		t.Fatalf("an unsupported reference parameter was written: %v", input)
	}
	content := h.content(result)
	warning, _ := content["warning"].(string)
	if !strings.Contains(warning, "当前服务不接受参考图") {
		t.Fatalf("unsupported note missing from warning: %v", content)
	}
}

// #250: the person's one-turn exemption also withholds the style picture.
func TestRetextureStyleReferenceRespectsTheOneTurnExemption(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	options := styleLockedOptions("style-a")
	options.StyleLockSkipped = true
	h.runWith("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"model-1","prompt":"mossy stone"}`, nil, options)
	if _, carried := h.frozenInput(0)["style_asset"]; carried {
		t.Fatalf("the exempt step carried a style picture: %v", h.frozenInput(0))
	}
}
