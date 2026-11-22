package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #459: a finished 3D task can still owe the requested format as a
// post-success resource (#457's mixed post_success shape). The platform is
// already converting it, so the Agent's convert_model_format must not submit a
// second paid run; it says so in the Schema's own words and submits nothing.

// pendingResourceTask is a succeeded task whose converted_model/converted.fbx
// slot the kernel is still observing.
func pendingResourceTask() generation.Task {
	task := modelTask("tripo-1", "owner-a", "session-a", "tripo", "image-to-3d", "glb")
	task.Resources = map[string]generation.ResourceState{
		"converted_model|converted.fbx|": {State: generation.ResourcePending},
		"converted_model|converted.obj|": {State: generation.ResourceReady},
	}
	return task
}

func TestConvertModelFormatRefusesAFormatThePlatformIsConverting459(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := convertHarness(t, product)
	h.gateway.tasks["tripo-1"] = pendingResourceTask()

	result, _ := h.run("convert_model_format", `{"source_task_id":"tripo-1","format":"fbx"}`)
	if !result.NoSubmission {
		t.Fatalf("the call submitted work the platform is already doing: %+v", result)
	}
	if h.gateway.submitCount != 0 || len(h.gateway.frozen) != 0 {
		t.Fatalf("submits=%d frozen=%+v", h.gateway.submitCount, h.gateway.frozen)
	}
	if content := h.content(result); content["success"] != false {
		t.Fatalf("result = %v", content)
	}
	// The refusal is the Schema's own sentence for this case, with the format
	// filled in, rather than a sentence written in code.
	if want := strings.ReplaceAll(convertFormatConvertingText(t, h), "{format}", "fbx"); result.Error != want {
		t.Fatalf("refusal = %q, want the Schema copy %q", result.Error, want)
	}
}

// convertFormatConvertingText is the tool's declared refusal for a format the
// platform is already converting (model_delivery's Schema copy, not code).
func convertFormatConvertingText(t *testing.T, h *processingHarness) string {
	t.Helper()
	for _, tool := range h.app.Chat.Tools {
		if tool.Name == "convert_model_format" && tool.Processing != nil {
			if tool.Processing.Messages.FormatConverting == "" {
				t.Fatal("convert_model_format declares no format_converting copy")
			}
			return tool.Processing.Messages.FormatConverting
		}
	}
	t.Fatal("convert_model_format missing from the Schema")
	return ""
}

func TestConvertModelFormatRunsAFormatTheTaskOwesNothing459(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := convertHarness(t, product)
	// The OBJ slot settled, so the platform owes nothing for it: the ordinary
	// route order applies again.
	h.gateway.tasks["tripo-1"] = pendingResourceTask()
	result, _ := h.run("convert_model_format", `{"source_task_id":"tripo-1","format":"obj"}`)
	if result.NoSubmission || len(h.gateway.frozen) != 1 {
		t.Fatalf("result = %+v frozen = %+v", result, h.gateway.frozen)
	}
}
