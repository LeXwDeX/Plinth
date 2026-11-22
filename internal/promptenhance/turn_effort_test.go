package promptenhance_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/promptenhance"
)

// #382: the enhancer rewrites one prompt beside the turn that will use it, so
// it sends the level the model family declares for a prompt enhancement
// (chat.agent.thinking.turn_efforts), not the family default it would otherwise
// fall back to. The model here is glm-5.3: low/high/max, default max.
func TestEnhancerUsesTheLevelTheModelFamilyDeclares(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Chat
	agent := &recordingAgent{completion: agents.Completion{Content: `{"prompt":"enhanced"}`}}
	service, err := promptenhance.New(promptenhance.Config{
		Offerings: offeringCatalog(nil),
		Schema:    schema,
		Model:     "glm-5.3",
		Agent:     agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enhance(context.Background(), promptenhance.Request{
		Prompt:     "original",
		Capability: generation.TextToImage,
	}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("requests = %d", len(agent.requests))
	}
	if effort := agent.requests[0].ReasoningEffort; effort != "low" {
		t.Fatalf("prompt enhancement effort = %q, want low", effort)
	}
}
