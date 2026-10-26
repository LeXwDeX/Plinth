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
