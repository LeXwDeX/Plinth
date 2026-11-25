package chat_test

import (
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #620: the models that can apply a style Skill are the enabled, available
// Offerings that declare the Schema's Skill feature, each model named once.
func TestStyleSkillModelsReadTheSchemaFeature(t *testing.T) {
	offerings := []generation.Offering{
		{ID: "openai/gpt-image-2/text-to-image", ModelID: "gpt-image-2.6", Enabled: true, Features: []string{"skills"}},
		{ID: "openai/gpt-image-2/image-to-image", ModelID: "gpt-image-2.6", Enabled: true, Features: []string{"skills"}},
		{ID: "meshy/nano-banana-pro/text-to-image", ModelID: "nano-banana-pro", Enabled: true},
		{ID: "openai/gpt-image-old/text-to-image", ModelID: "gpt-image-old", Enabled: false, Features: []string{"skills"}},
		{ID: "openai/gpt-image-off/text-to-image", ModelID: "gpt-image-off", Enabled: true, Features: []string{"skills"}},
	}
	available := func(id generation.OfferingID) bool { return id != "openai/gpt-image-off/text-to-image" }
	if got := chat.StyleSkillModels(offerings, "skills", available); !slices.Equal(got, []string{"gpt-image-2.6"}) {
		t.Fatalf("models = %v", got)
	}
	if got := chat.StyleSkillModels(offerings, "", available); len(got) != 0 {
		t.Fatalf("no feature declared: %v", got)
	}
}
