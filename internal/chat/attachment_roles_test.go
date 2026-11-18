package chat

import (
	"strings"
	"testing"
)

// #331: a list item's crop and its source scene carry their own labels, both
// when they ride as a picture and when they are only named in the selection,
// so the sub-session never reads either as an ordinary user upload.
func TestListCropAndSourceSceneLabelsNameTheirRoles(t *testing.T) {
	orchestrator, schema := canvasPromptOrchestrator(t)
	context := schema.Context
	replace := func(template, handle string) string {
		return strings.NewReplacer("{index}", "1", "{handle}", handle, "{task_id}", "", "{capture_id}", "").
			Replace(template)
	}

	pictured := Message{Attachments: []Attachment{
		{Handle: "@img3", Kind: AttachmentKindImage, Role: AttachmentRoleCrop, AssetID: "crop", ImageSlot: 1},
		{Handle: "@img4", Kind: AttachmentKindImage, Role: AttachmentRoleSourceScene, AssetID: "scene", ImageSlot: 2},
	}}
	for name, want := range map[string]string{
		"crop":         replace(context.AttachmentLabelTemplates["image.crop"], "@img3"),
		"source scene": replace(context.AttachmentLabelTemplates["image.source_scene"], "@img4"),
	} {
		if want == "" {
			t.Fatalf("%s label template is not declared", name)
		}
	}
	if label, structured := orchestrator.attachmentLabel(pictured, 1, 1); !structured ||
		label != replace(context.AttachmentLabelTemplates["image.crop"], "@img3") {
		t.Fatalf("crop picture label = %q", label)
	}
	if label, structured := orchestrator.attachmentLabel(pictured, 2, 1); !structured ||
		label != replace(context.AttachmentLabelTemplates["image.source_scene"], "@img4") {
		t.Fatalf("source scene picture label = %q", label)
	}

	selection := Message{Attachments: []Attachment{
		{Handle: "@img5", Kind: AttachmentKindImage, Role: AttachmentRoleCrop, AssetID: "crop"},
		{Handle: "@img6", Kind: AttachmentKindImage, Role: AttachmentRoleSourceScene, AssetID: "scene"},
	}}
	labels := orchestrator.unpicturedLabels(selection)
	for _, want := range []string{
		strings.ReplaceAll(context.UnpicturedAttachmentTemplates["image.crop"], "{handle}", "@img5"),
		strings.ReplaceAll(context.UnpicturedAttachmentTemplates["image.source_scene"], "{handle}", "@img6"),
	} {
		if want == "" || !strings.Contains(labels, want) {
			t.Fatalf("selection labels lack %q:\n%s", want, labels)
		}
	}
	upload := strings.ReplaceAll(context.UnpicturedAttachmentTemplates["image"], "{handle}", "@img5")
	if strings.Contains(labels, upload) {
		t.Fatalf("a crop is labelled as a selected upload:\n%s", labels)
	}
}
