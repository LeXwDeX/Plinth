package chat_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func directStyleLockStyle() catalog.ChatStyleLockSchema {
	return catalog.ChatStyleLockSchema{
		DirectTaskCapabilities: []generation.Capability{generation.TextToImage, generation.ImageToImage},
		PromptTemplate:         "\n\nStyle lock: {style}",
		EditScopeTemplate:      "\n\nEdit scope: change only the request.",
	}
}

func directStyleLockOffering(capability generation.Capability, promptMax int, referenceType string, referenceMax int) generation.Offering {
	parameters := []generation.Parameter{{Name: "prompt", Type: "text", Semantic: generation.SemanticPrompt, MaxLength: promptMax}}
	if referenceType != "" {
		parameters = append(parameters, generation.Parameter{
			Name: "reference_assets", Type: referenceType,
			Semantic: generation.SemanticReferenceImage, MaxItems: referenceMax,
		})
	}
	return generation.Offering{
		ID: generation.OfferingID("direct/offering-" + string(capability)), Capability: capability,
		Domain: generation.DomainImage, Parameters: parameters,
	}
}

func directStyleLockInput(t *testing.T, values map[string]any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func applyDirectStyleLock(
	t *testing.T,
	lock chat.StyleLock,
	skipped bool,
	capability generation.Capability,
	offering generation.Offering,
	style catalog.ChatStyleLockSchema,
	input json.RawMessage,
) (json.RawMessage, error) {
	t.Helper()
	return chat.ApplyStyleLockToDirectInput(lock, skipped, capability, offering, style, input)
}

func decodeDirectStyleLockInput(t *testing.T, input json.RawMessage) map[string]any {
	t.Helper()
	var values map[string]any
	if err := json.Unmarshal(input, &values); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestDirectStyleLockAppendsPromptByCapability(t *testing.T) {
	style := directStyleLockStyle()
	lock := chat.StyleLock{Enabled: true, Text: "ink and paper"}

	textOffering := directStyleLockOffering(generation.TextToImage, 0, "", 0)
	input := directStyleLockInput(t, map[string]any{"prompt": "a red cube"})
	merged, err := applyDirectStyleLock(t, lock, false, generation.TextToImage, textOffering, style, input)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _ := decodeDirectStyleLockInput(t, merged)["prompt"].(string)
	if !strings.HasSuffix(prompt, "\n\nStyle lock: ink and paper") || strings.Contains(prompt, "Edit scope") {
		t.Fatalf("text-to-image prompt = %q", prompt)
	}

	editOffering := directStyleLockOffering(generation.ImageToImage, 0, "asset-array", 3)
	input = directStyleLockInput(t, map[string]any{"prompt": "make it blue", "reference_assets": []string{"agent-a"}})
	merged, err = applyDirectStyleLock(t, lock, false, generation.ImageToImage, editOffering, style, input)
	if err != nil {
		t.Fatal(err)
	}
	values := decodeDirectStyleLockInput(t, merged)
	prompt, _ = values["prompt"].(string)
	if !strings.Contains(prompt, "\n\nStyle lock: ink and paper") || !strings.HasSuffix(prompt, "\n\nEdit scope: change only the request.") {
		t.Fatalf("image-to-image prompt = %q", prompt)
	}
}

func TestDirectStyleLockUsesTheProjectPromptAndResolvedPictures(t *testing.T) {
	style := directStyleLockStyle()
	style.ProjectPromptTemplate = "\n\nProject style: {style} of {project}"
	offering := directStyleLockOffering(generation.ImageToImage, 0, "asset-array", 5)
	lock := chat.StyleLock{
		Enabled: true, Source: chat.StyleLockSourceProject, Text: "warm ink",
		ProjectName: "Star Harbor", ProjectID: "prj_1",
		// The prepare boundary has already copied the project URLs into the
		// owner's input assets.
		References: []string{"style-a"},
	}
	input := directStyleLockInput(t, map[string]any{
		"prompt": "make it blue", "reference_assets": []string{"turn-a"},
	})
	merged, err := applyDirectStyleLock(t, lock, false, generation.ImageToImage, offering, style, input)
	if err != nil {
		t.Fatal(err)
	}
	values := decodeDirectStyleLockInput(t, merged)
	prompt, _ := values["prompt"].(string)
	if !strings.Contains(prompt, "Project style: warm ink of Star Harbor") || strings.Contains(prompt, "Style lock:") {
		t.Fatalf("project prompt = %q", prompt)
	}
	references, _ := values["reference_assets"].([]any)
	if len(references) != 2 || references[0] != "turn-a" || references[1] != "style-a" {
		t.Fatalf("project references = %v", references)
	}
}

func TestDirectStyleLockOrdersReferencesAfterTheTurnPictures(t *testing.T) {
	style := directStyleLockStyle()
	offering := directStyleLockOffering(generation.ImageToImage, 0, "asset-array", 5)
	lock := chat.StyleLock{Enabled: true, References: []string{"style-a", "agent-a", "style-b", "style-a"}}
	input := directStyleLockInput(t, map[string]any{
		"prompt": "make it blue", "reference_assets": []string{"agent-a", "agent-b"},
	})
	merged, err := applyDirectStyleLock(t, lock, false, generation.ImageToImage, offering, style, input)
	if err != nil {
		t.Fatal(err)
	}
	references := decodeDirectStyleLockInput(t, merged)["reference_assets"].([]any)
	want := []string{"agent-a", "agent-b", "style-a", "style-b"}
	if len(references) != len(want) {
		t.Fatalf("references = %#v", references)
	}
	for index, value := range want {
		if references[index] != value {
			t.Fatalf("references = %#v, want %v", references, want)
		}
	}
}

func TestDirectStyleLockTrimsThePromptToTheParameterLimit(t *testing.T) {
	style := directStyleLockStyle()
	offering := directStyleLockOffering(generation.TextToImage, 20, "", 0)
	lock := chat.StyleLock{Enabled: true, Text: "a style that is much longer than the limit"}
	input := directStyleLockInput(t, map[string]any{"prompt": "draw a cube"})
	merged, err := applyDirectStyleLock(t, lock, false, generation.TextToImage, offering, style, input)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _ := decodeDirectStyleLockInput(t, merged)["prompt"].(string)
	if len([]rune(prompt)) != 20 || !strings.HasPrefix(prompt, "draw a cube") {
		t.Fatalf("truncated prompt = %q (%d runes)", prompt, len([]rune(prompt)))
	}
}

func TestDirectStyleLockSinglePictureParameter(t *testing.T) {
	style := directStyleLockStyle()
	offering := directStyleLockOffering(generation.ImageToImage, 0, "asset", 0)

	merged, err := applyDirectStyleLock(t, chat.StyleLock{Enabled: true, References: []string{"style-a"}},
		false, generation.ImageToImage, offering, style, directStyleLockInput(t, map[string]any{"prompt": "edit"}))
	if err != nil {
		t.Fatal(err)
	}
	if picture, _ := decodeDirectStyleLockInput(t, merged)["reference_assets"].(string); picture != "style-a" {
		t.Fatalf("single picture = %q", picture)
	}

	_, err = applyDirectStyleLock(t, chat.StyleLock{Enabled: true, References: []string{"style-a"}},
		false, generation.ImageToImage, offering, style,
		directStyleLockInput(t, map[string]any{"prompt": "edit", "reference_assets": "agent-a"}))
	if !errors.Is(err, chat.ErrStyleLockReferenceUnavailable) {
		t.Fatalf("taken single picture error = %v", err)
	}

	_, err = applyDirectStyleLock(t, chat.StyleLock{Enabled: true, References: []string{"style-a", "style-b"}},
		false, generation.ImageToImage, offering, style, directStyleLockInput(t, map[string]any{"prompt": "edit"}))
	if !errors.Is(err, chat.ErrStyleLockReferenceUnavailable) {
		t.Fatalf("two single pictures error = %v", err)
	}
}

func TestDirectStyleLockRefusesAnOfferingWithoutReferenceParameter(t *testing.T) {
	style := directStyleLockStyle()
	offering := directStyleLockOffering(generation.ImageToImage, 0, "", 0)
	_, err := applyDirectStyleLock(t, chat.StyleLock{Enabled: true, References: []string{"style-a"}},
		false, generation.ImageToImage, offering, style, directStyleLockInput(t, map[string]any{"prompt": "edit"}))
	if !errors.Is(err, chat.ErrStyleLockReferenceUnavailable) {
		t.Fatalf("missing reference parameter error = %v", err)
	}
}

func TestDirectStyleLockLeavesExcludedAndInactiveInputsByteForByte(t *testing.T) {
	style := directStyleLockStyle()
	lock := chat.StyleLock{Enabled: true, Text: "ink", References: []string{"style-a"}}
	base := directStyleLockInput(t, map[string]any{
		"prompt": "make it blue", "reference_assets": []string{"agent-a"},
	})
	cases := []struct {
		name       string
		lock       chat.StyleLock
		skipped    bool
		capability generation.Capability
		offering   generation.Offering
	}{
		{name: "disabled", lock: chat.StyleLock{Text: "ink", References: []string{"style-a"}},
			capability: generation.ImageToImage, offering: directStyleLockOffering(generation.ImageToImage, 0, "asset-array", 3)},
		{name: "skipped", lock: lock, skipped: true,
			capability: generation.ImageToImage, offering: directStyleLockOffering(generation.ImageToImage, 0, "asset-array", 3)},
		{name: "no lock content", lock: chat.StyleLock{Enabled: true},
			capability: generation.ImageToImage, offering: directStyleLockOffering(generation.ImageToImage, 0, "asset-array", 3)},
		{name: "background removal", lock: lock,
			capability: generation.BackgroundRemove, offering: directStyleLockOffering(generation.BackgroundRemove, 0, "asset-array", 3)},
		{name: "image to 3d", lock: lock,
			capability: generation.ImageTo3D, offering: directStyleLockOffering(generation.ImageTo3D, 0, "asset-array", 3)},
		{name: "offering capability not declared", lock: lock,
			capability: generation.ImageToImage, offering: directStyleLockOffering(generation.ImageTo3D, 0, "asset-array", 3)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			merged, err := applyDirectStyleLock(t, test.lock, test.skipped, test.capability, test.offering, style, base)
			if err != nil {
				t.Fatal(err)
			}
			if string(merged) != string(base) {
				t.Fatalf("input changed: %s", merged)
			}
		})
	}
}

func TestStyleLockAppliesToDirectOnlyForDeclaredImageCapabilities(t *testing.T) {
	style := directStyleLockStyle()
	for _, capability := range []generation.Capability{generation.TextToImage, generation.ImageToImage} {
		if !chat.StyleLockAppliesToDirect(style, capability) {
			t.Fatalf("%s should be declared", capability)
		}
	}
	for _, capability := range []generation.Capability{
		generation.BackgroundRemove, generation.ImageTo3D, generation.TextTo3D,
		generation.ImageToMultiview, generation.EditMultiview,
	} {
		if chat.StyleLockAppliesToDirect(style, capability) {
			t.Fatalf("%s should stay excluded", capability)
		}
	}
	if chat.StyleLockAppliesToDirect(catalog.ChatStyleLockSchema{}, generation.TextToImage) {
		t.Fatal("an undeclared Schema must not inject")
	}
}
