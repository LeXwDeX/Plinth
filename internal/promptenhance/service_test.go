package promptenhance_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/promptenhance"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func TestServiceUsesSchemaProfileOfferingLimitAndConfiguredLLMFields(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Chat
	profile := schema.API.PromptEnhance.Profiles["image"]
	profile.SystemPrompt = "configured-system {length_rule}"
	profile.BoundedLengthRule = "configured-limit-{max_length}"
	profile.PromptField = "configuredPrompt"
	profile.NegativePromptField = "configuredNegative"
	profile.DefaultNegativePrompt = "configured-default-negative"
	schema.API.PromptEnhance.Profiles["image"] = profile

	agent := &recordingAgent{
		completion: agents.Completion{
			Content: `{
				"configuredPrompt":"123456789",
				"configuredNegative":"configured-negative"
			}`,
		},
	}
	service, err := promptenhance.New(promptenhance.Config{
		Offerings: offeringCatalog{
			{
				ModelID:    "configured-model",
				Capability: generation.TextToImage,
				Parameters: []generation.Parameter{
					{
						Semantic:  generation.SemanticPrompt,
						MaxLength: 5,
					},
				},
			},
		},
		Schema: schema,
		Model:  "configured-llm",
		Agent:  agent,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Enhance(context.Background(), promptenhance.Request{
		Prompt:     "original",
		Capability: generation.TextToImage,
		ModelID:    "configured-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Prompt != "12345" {
		t.Fatalf("prompt = %q", result.Prompt)
	}
	if result.NegativePrompt != "configured-negative" {
		t.Fatalf("negative prompt = %q", result.NegativePrompt)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("requests = %d", len(agent.requests))
	}
	request := agent.requests[0]
	if request.Model != "configured-llm" {
		t.Fatalf("model = %q", request.Model)
	}
	if request.Messages[0].Content !=
		"configured-system configured-limit-5" {
		t.Fatalf("system prompt = %q", request.Messages[0].Content)
	}
	if request.Messages[1].Content != "original" {
		t.Fatalf("user prompt = %q", request.Messages[1].Content)
	}
}

func TestServiceUsesSchemaDefaultNegativeAndRejectsEmptyCompletion(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Chat
	agent := &recordingAgent{
		completion: agents.Completion{Content: `{"prompt":"enhanced"}`},
	}
	service, err := promptenhance.New(promptenhance.Config{
		Offerings: offeringCatalog(nil),
		Schema:    schema,
		Model:     "configured-llm",
		Agent:     agent,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Enhance(context.Background(), promptenhance.Request{
		Prompt: "original",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NegativePrompt !=
		schema.API.PromptEnhance.Profiles["image"].DefaultNegativePrompt {
		t.Fatalf("result = %#v", result)
	}

	agent.completion.Content = " "
	if _, err := service.Enhance(
		context.Background(),
		promptenhance.Request{Prompt: "original"},
	); err == nil {
		t.Fatal("empty completion accepted")
	}
}

// #253: without Agent settings the enhancer still builds (no model needed)
// and reports itself unavailable instead of failing with an unknown error;
// a real Agent still needs a model.
func TestServiceReportsUnavailableWhileTheAgentIsNotConfigured(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Chat
	if _, err := promptenhance.New(promptenhance.Config{
		Offerings: offeringCatalog(nil), Schema: schema, Agent: &recordingAgent{},
	}); err == nil {
		t.Fatal("configured Agent without model accepted")
	}
	service, err := promptenhance.New(promptenhance.Config{
		Offerings: offeringCatalog(nil), Schema: schema,
		Agent: agents.NotConfigured{Missing: []string{"LLM_API_KEY"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Enhance(context.Background(), promptenhance.Request{Prompt: "original"})
	if !errors.Is(err, agents.ErrNotConfigured) || !strings.Contains(err.Error(), "LLM_API_KEY") {
		t.Fatalf("enhance error = %v", err)
	}
}

type offeringCatalog []generation.Offering

func (offerings offeringCatalog) List(
	capability generation.Capability,
) []generation.Offering {
	var result []generation.Offering
	for _, offering := range offerings {
		if capability == "" || offering.Capability == capability {
			result = append(result, offering)
		}
	}
	return result
}

type recordingAgent struct {
	completion agents.Completion
	requests   []agents.Request
}

func (agent *recordingAgent) Stream(
	context.Context,
	agents.Request,
	func(agents.Delta) error,
) (agents.Completion, error) {
	return agents.Completion{}, nil
}

func (agent *recordingAgent) Complete(
	_ context.Context,
	request agents.Request,
) (agents.Completion, error) {
	agent.requests = append(agent.requests, request)
	return agent.completion, nil
}

func boolPtr(value bool) *bool { return &value }

func TestEnhancerSharesOfferingPromptGuidanceWithTheAgent(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Chat
	agent := &recordingAgent{completion: agents.Completion{Content: `{"prompt":"enhanced","negative_prompt":"negative"}`}}
	english := generation.Offering{
		ID: "a", ModelID: "english-model", Capability: generation.TextToImage,
		Knowledge: &schemacontract.OfferingKnowledge{Prompt: &schemacontract.OfferingPrompt{
			Language: "en", Form: "descriptive", Unit: "words", WordsMin: 30, WordsMax: 60,
			ReadsText: boolPtr(true), NegativePrompt: boolPtr(false),
		}},
	}
	chinese := generation.Offering{
		ID: "b", ModelID: "chinese-model", Capability: generation.TextToImage,
		Knowledge: &schemacontract.OfferingKnowledge{Prompt: &schemacontract.OfferingPrompt{
			Language: "zh", Form: "keywords", Unit: "characters", WordsMin: 10, WordsMax: 40,
			ReadsText: boolPtr(true), NegativePrompt: boolPtr(true),
		}},
	}
	service, err := promptenhance.New(promptenhance.Config{
		Offerings: offeringCatalog{english, chinese}, Schema: schema, Model: "llm", Agent: agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	enhance := func(modelID string) promptenhance.Result {
		t.Helper()
		result, err := service.Enhance(context.Background(), promptenhance.Request{
			Prompt: "a cat", Capability: generation.TextToImage, ModelID: modelID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	englishResult := enhance("english-model")
	englishPrompt := agent.requests[0].Messages[0].Content
	chineseResult := enhance("chinese-model")
	chinesePrompt := agent.requests[1].Messages[0].Content
	if englishPrompt == chinesePrompt {
		t.Fatal("two Offerings produced the same system prompt")
	}
	for _, needle := range []string{"write in English", "30-60 words", "not supported as a separate negative prompt"} {
		if !strings.Contains(englishPrompt, needle) {
			t.Fatalf("English guidance misses %q: %s", needle, englishPrompt)
		}
	}
	for _, needle := range []string{"write in Chinese", "10-40 characters", "supported as a separate negative prompt"} {
		if !strings.Contains(chinesePrompt, needle) {
			t.Fatalf("Chinese guidance misses %q: %s", needle, chinesePrompt)
		}
	}
	// The Offering without a negative-prompt input returns none, even when
	// the model emitted one.
	if englishResult.NegativePrompt != "" {
		t.Fatalf("negative prompt = %q", englishResult.NegativePrompt)
	}
	if chineseResult.NegativePrompt != "negative" {
		t.Fatalf("negative prompt = %q", chineseResult.NegativePrompt)
	}

	// An unknown model keeps the generic rules.
	unknown, err := service.Enhance(context.Background(), promptenhance.Request{
		Prompt: "a dog", Capability: generation.TextToImage, ModelID: "unknown-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Prompt == "" || !strings.Contains(agent.requests[2].Messages[0].Content, schema.API.PromptEnhance.Profiles["image"].SystemPrompt[:20]) {
		t.Fatalf("unknown model prompt = %q", agent.requests[2].Messages[0].Content)
	}
}

func TestEnhancerExplainsAServiceThatDoesNotReadText(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Chat
	agent := &recordingAgent{completion: agents.Completion{Content: `{"prompt":"enhanced"}`}}
	offering := generation.Offering{
		ID: "image-only", ModelID: "image-only", Capability: generation.ImageToImage,
		Knowledge: &schemacontract.OfferingKnowledge{Prompt: &schemacontract.OfferingPrompt{
			ReadsText: boolPtr(false), NegativePrompt: boolPtr(false),
		}},
	}
	service, err := promptenhance.New(promptenhance.Config{
		Offerings: offeringCatalog{offering}, Schema: schema, Model: "llm", Agent: agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enhance(context.Background(), promptenhance.Request{
		Prompt: "change it", Capability: generation.ImageToImage, ModelID: "image-only",
	}); err != nil {
		t.Fatal(err)
	}
	system := agent.requests[0].Messages[0].Content
	if !strings.Contains(system, "does not read a text prompt") || !strings.Contains(system, "accepts no negative prompt") {
		t.Fatalf("system prompt = %q", system)
	}
}
