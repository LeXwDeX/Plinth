package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #80: the pre-send note states capacity only; the source and order claims
// come from the frozen fact after sending. The two result texts keep their
// placeholders and both languages.
func TestReferenceSourceAndCapacityTextsFor80(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	messages := application.Runtime.GenerationMessages
	for name, text := range map[string]string{
		"reference_selection_note": messages.ReferenceSelectionNote,
		"reference_capacity":       messages.ReferenceCapacity,
	} {
		if text == "" || !strings.Contains(text, "{model}") || !strings.Contains(text, "{count}") {
			t.Fatalf("runtime.generation_messages.%s = %q", name, text)
		}
	}
	if !strings.Contains(messages.ReferenceCapacity, "{total}") {
		t.Fatalf("reference_capacity must name the attached count: %q", messages.ReferenceCapacity)
	}
	english := application.Localization.Bundles["en"].Fields
	for key, text := range map[string]string{
		"runtime.generation_messages.reference_selection_note": messages.ReferenceSelectionNote,
		"runtime.generation_messages.reference_capacity":       messages.ReferenceCapacity,
	} {
		if english[key] == "" || english[key] == text {
			t.Fatalf("English %s = %q", key, english[key])
		}
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	single := tool.ReferenceCapacityNote.Single
	if single == "" || !strings.Contains(single, "Do not predict") ||
		strings.Contains(single, "only the first is sent") || strings.Contains(single, "only the first is used") {
		t.Fatalf("single reference capacity note still predicts a picture: %q", single)
	}
	offering, ok := product.Get("meshy/meshy-7.1/image-to-3d")
	if !ok || offering.ExecutionMessages.ReferenceCapacity != messages.ReferenceCapacity ||
		offering.ExecutionMessages.ReferenceSelectionNote != messages.ReferenceSelectionNote {
		t.Fatalf("offering execution messages do not carry the texts: %+v", offering.ExecutionMessages)
	}
}
