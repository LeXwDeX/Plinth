package generationtool_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func pictureHistory(h *processingHarness) []chat.Message {
	return []chat.Message{{Role: h.app.Chat.Roles.User, Content: "split it", ImageURLs: []string{"/api/v1/assets/asset-reference"},
		Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, ImageSlot: 1}}}}
}

// #187 D: Bang segmentation of a local model needs a reference image; the
// free process_model tool now takes one by handle, like retexture_model.
func TestProcessModelPassesANamedReferenceImageToBang(t *testing.T) {
	h := newProcessingHarness(t, "auto", hyper3DGLBTasks)
	result, _ := h.runAs("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"model-1","offering_id":"hyper3d/bang/local-model","reference_handles":["@img1"]}`, pictureHistory(h))
	if result.NoSubmission {
		t.Fatalf("Bang with a reference image refused: %+v", result)
	}
	input := h.frozenInput(0)
	if input["reference_image"] != "configured-input-asset" || input["source_asset"] != "model-1-glb" {
		t.Fatalf("frozen input %+v", input)
	}
}

// A step whose service takes no reference image refuses a named one, and a
// picture that is only selected in the UI is not forced onto such a step.
func TestProcessModelReferenceOnlyWhenTheStepDeclaresOne(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ := h.runAs("owner-a", "session-a", "process_model", `{"step":"remesh","source_task_id":"model-1","reference_handles":["@img1"]}`, pictureHistory(h))
	h.expectRejected(result, "reference")

	h = newProcessingHarness(t, "auto", meshyCatalogTasks)
	result, _ = h.runWith("owner-a", "session-a", "process_model", `{"step":"remesh","source_task_id":"model-1"}`, pictureHistory(h),
		chat.TurnOptions{ReferenceImageIndices: []int{1}})
	if result.NoSubmission {
		t.Fatalf("a selected picture blocked remesh: %+v", result)
	}
}
