package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func imageCountFixtureOffering(id, model, countParameter string, maximum float64) generation.Offering {
	offering := generation.Offering{
		ID: generation.OfferingID(id), ProviderID: strings.Split(id, "/")[0], ModelID: model, ModelName: model,
		Capability: schema.TextToImage, Enabled: true,
	}
	if countParameter != "" {
		limit := maximum
		minimum := 1.0
		offering.EstimatedCreditsUnitParameter = countParameter
		offering.Parameters = []schema.Parameter{{
			Name: countParameter, Type: "number", Integer: true, Minimum: &minimum, Maximum: &limit,
			AdjustmentPolicy: "user", Default: json.RawMessage(`1`),
			Presentation: &schema.ParameterPresentation{Surface: schema.SurfaceProvider, Control: schema.ControlNumber},
		}}
	}
	return offering
}

// #564: the image count block states what the server really does. The count is
// written into every call, so the Agent is never told to make up a count over
// several calls, and the block names a model whose declared range is narrower
// than the session count (with the count it will really run) and the models
// that take no count at all.
func TestImageCountPromptStatesTheServerWrittenCountPerModel(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := loaded.Application().Chat
	registry, err := compileToolRegistry(chatSchema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	offerings := []generation.Offering{
		imageCountFixtureOffering("openai/gpt-image-2/text-to-image", "gpt-image-2", "n", 10),
		imageCountFixtureOffering("jimeng/seedream-4-5/text-to-image", "seedream-4-5", "max_images", 15),
		imageCountFixtureOffering("gemini/gemini-3-pro-image/text-to-image", "gemini-3-pro-image", "", 0),
	}
	registry.offerings = func(capability string) []generation.Offering {
		if capability == string(schema.TextToImage) {
			return offerings
		}
		return nil
	}
	registry.offeringAllowed = func(string, string) bool { return true }
	orchestrator := &Orchestrator{schema: chatSchema,
		imageCount: func(string, string) (int, bool) { return 15, true }}

	prompt := orchestrator.imageCountPrompt(registry, "owner", "session")
	for _, stale := range []string{"出不了就分几次补齐", "按用户这次说的执行"} {
		if strings.Contains(prompt, stale) {
			t.Errorf("the block still says %q, which the server's per-call count contradicts", stale)
		}
	}
	if !strings.Contains(prompt, "15 张") {
		t.Errorf("the block lacks the session count: %q", prompt)
	}
	if !strings.Contains(prompt, "gpt-image-2") || !strings.Contains(prompt, "10 张") {
		t.Errorf("the block does not say gpt-image-2 runs at its maximum 10: %q", prompt)
	}
	if strings.Contains(prompt, "seedream-4-5") {
		t.Errorf("a model whose range holds the count is named as clamped: %q", prompt)
	}
	if !strings.Contains(prompt, "gemini-3-pro-image") {
		t.Errorf("the block does not name the model without a count parameter: %q", prompt)
	}
	if strings.Contains(prompt, "{") {
		t.Errorf("the block kept a placeholder: %q", prompt)
	}

	// A count inside every range names no clamped model.
	orchestrator.imageCount = func(string, string) (int, bool) { return 4, true }
	if prompt := orchestrator.imageCountPrompt(registry, "owner", "session"); strings.Contains(prompt, "gpt-image-2") {
		t.Errorf("a count inside the range still names a clamp: %q", prompt)
	}
}

// #564 review: Seedream sends max_images only with image set generation on
// (omit_when sequential_image_generation=disabled, the default), so with the
// default settings the session count never reaches it. The block says so for
// the Offering's current parameter combination, read from the Offering's own
// omit_when declaration and the session's values, instead of claiming the
// model produces the count.
func TestImageCountPromptSaysWhenTheCountParameterIsOmitted(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := loaded.Application().Chat
	registry, err := compileToolRegistry(chatSchema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	seedream := imageCountFixtureOffering("jimeng/seedream-4-5/text-to-image", "seedream-4-5", "max_images", 15)
	seedream.Parameters[0].OmitWhen = []schema.ParameterCondition{{
		Parameter: "sequential_image_generation", Operator: "equals", Value: json.RawMessage(`"disabled"`),
	}}
	seedream.Parameters = append(seedream.Parameters, schema.Parameter{
		Name: "sequential_image_generation", Label: "组图生成", Type: "select", AdjustmentPolicy: "user",
		Default: json.RawMessage(`"disabled"`),
		Options: []schema.ParameterOption{{Value: "disabled", Label: "关闭"}, {Value: "auto", Label: "自动"}},
	})
	registry.offerings = func(capability string) []generation.Offering {
		if capability == string(schema.TextToImage) {
			return []generation.Offering{seedream}
		}
		return nil
	}
	registry.offeringAllowed = func(string, string) bool { return true }
	values := map[string]json.RawMessage{}
	orchestrator := &Orchestrator{schema: chatSchema,
		imageCount: func(string, string) (int, bool) { return 4, true },
		offeringParameters: func(ownerID, sessionID, offeringID string) map[string]json.RawMessage {
			if offeringID != string(seedream.ID) {
				return nil
			}
			return values
		}}

	prompt := orchestrator.imageCountPrompt(registry, "owner", "session")
	if !strings.Contains(prompt, "seedream-4-5") || !strings.Contains(prompt, "组图生成") || !strings.Contains(prompt, "关闭") {
		t.Fatalf("with image set generation off by default the block does not say the count is not sent: %q", prompt)
	}
	if strings.Contains(prompt, "{") {
		t.Fatalf("the block kept a placeholder: %q", prompt)
	}

	// The person turned image set generation on: max_images is sent, and the
	// block names nothing for Seedream.
	values["sequential_image_generation"] = json.RawMessage(`"auto"`)
	if prompt := orchestrator.imageCountPrompt(registry, "owner", "session"); strings.Contains(prompt, "seedream-4-5") {
		t.Fatalf("with image set generation on the block still names Seedream: %q", prompt)
	}
}
