package generationtool

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #620: an Agent style Skill on a model without the Skill feature is refused
// with the models of this capability that can apply it, so the next call
// names one of them instead of trying model after model; with none available
// the refusal says so.
func TestAgentStyleSkillRefusalNamesTheModelsThatApplyIt(t *testing.T) {
	executor, refine, configuration := skillChoiceRig(t)
	feature := executor.chat.Skills.Feature
	executor.gateway = &choiceTestGateway{offerings: []generation.Offering{
		{ID: "meshy/nano-banana-pro/text-to-image", ModelID: "nano-banana-pro", Capability: generation.TextToImage, Enabled: true},
		{ID: "openai/gpt-image-2/text-to-image", ModelID: "gpt-image-2.6", Capability: generation.TextToImage, Enabled: true, Features: []string{feature}},
		{ID: "openai/gpt-image-2/image-to-image", ModelID: "gpt-image-2.6", Capability: generation.ImageToImage, Enabled: true, Features: []string{feature}},
	}}
	_, skill := executor.resolveCallSkill(skillCommand(refine, configuration, "", map[string]any{"prompt": "a cat", "skill": slugOf(t, configuration, inkID)}))
	directory := executor.chat.Skills.Directory
	refusal := executor.skillUnsupported(skill, generation.TextToImage)
	if !strings.Contains(refusal, "水墨") || !strings.Contains(refusal, directory.StyleModelsText([]string{"gpt-image-2.6"})) {
		t.Fatalf("refusal = %q", refusal)
	}
	executor.gateway = &choiceTestGateway{offerings: []generation.Offering{
		{ID: "meshy/nano-banana-pro/text-to-image", ModelID: "nano-banana-pro", Capability: generation.TextToImage, Enabled: true},
	}}
	if refusal := executor.skillUnsupported(skill, generation.TextToImage); !strings.Contains(refusal, directory.Messages.NoStyleModel) {
		t.Fatalf("no style model: refusal = %q", refusal)
	}
}
