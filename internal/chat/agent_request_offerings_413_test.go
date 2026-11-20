package chat

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// oneAgentRequestListLimit bounds how often one Agent request may read the
// Offering directory (#413). A turn declares a handful of capabilities; the
// request must resolve each one once, not once per prompt block, tool-list
// build or model_choices evaluation (that was ~618 Catalog.List calls).
const oneAgentRequestListLimit = 8

// countingOfferings is the server's wiring of the Offering source, with the
// Catalog.List calls counted: the tool registry reads it for model_choices and
// the craft brief.
func countingOfferings(product *catalog.Catalog) (func(string) []generation.Offering, *atomic.Int64) {
	var calls atomic.Int64
	return func(capability string) []generation.Offering {
		calls.Add(1)
		return product.List(generation.Capability(capability))
	}, &calls
}

// offeringAvailability is the server's availability source: it reads the same
// immutable Catalog view, so it adds no Catalog.List call of its own.
func offeringAvailability(product *catalog.Catalog) func(capability, modelID string) bool {
	return func(capability, modelID string) bool {
		for _, offering := range product.ListShared(generation.Capability(capability)) {
			if offering.ModelID == modelID {
				return true
			}
		}
		return false
	}
}

// TestAgentRequestReadsOfferingsOncePerCapability is the #413 regression: one
// Agent request resolves the runtime Offerings (and so Catalog.List) a bounded
// number of times.
func TestAgentRequestReadsOfferingsOncePerCapability(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := product.Application().Chat
	registry, err := compileToolRegistry(chatSchema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	source, listCalls := countingOfferings(product)
	registry.offerings = source
	registry.offeringAllowed = offeringAvailability(product)
	orchestrator := &Orchestrator{schema: chatSchema, registry: registry, agentScope: registry.scope}

	request := orchestrator.agentRequest(context.Background(), nil,
		TurnOptions{AgentScope: "auto", CreativeDomain: "model-3d"}, "owner", "")

	if len(request.Tools) == 0 {
		t.Fatal("the turn offers no tool")
	}
	if len(request.Messages) == 0 || request.Messages[0].Content == "" {
		t.Fatal("the turn built no system prompt")
	}
	if calls := listCalls.Load(); calls == 0 || calls > oneAgentRequestListLimit {
		t.Fatalf("one Agent request read the Offering directory %d times; want 1–%d "+
			"(once per capability, shared by every prompt and tool-list builder)",
			calls, oneAgentRequestListLimit)
	} else {
		t.Logf("one Agent request read the Offering directory %d times", calls)
	}
}

// TestTurnRegistryIsPerRequest guards where the #413 cache lives: each request
// resolves its own Offering source again, and the Orchestrator's long-lived
// registry never carries a cache.
func TestTurnRegistryIsPerRequest(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := product.Application().Chat
	registry, err := compileToolRegistry(chatSchema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	source, listCalls := countingOfferings(product)
	registry.offerings = source
	registry.offeringAllowed = offeringAvailability(product)
	orchestrator := &Orchestrator{schema: chatSchema, registry: registry, agentScope: registry.scope}
	options := TurnOptions{AgentScope: "auto", CreativeDomain: "model-3d"}

	before := listCalls.Load()
	orchestrator.agentRequest(context.Background(), nil, options, "owner", "")
	first := listCalls.Load() - before
	before = listCalls.Load()
	orchestrator.agentRequest(context.Background(), nil, options, "owner", "")
	second := listCalls.Load() - before

	if first == 0 || second != first {
		t.Fatalf("two requests read the Offering directory %d and %d times; want the same bounded count twice", first, second)
	}
	if registry.cache != nil {
		t.Fatal("the Orchestrator's long-lived registry carries a request cache")
	}
}

// TestAgentRequestToolNotesStayOutOfTheSharedToolList guards the second half
// of #413: the request's tool list is a copy of the request-scoped view, so
// the per-turn description notes cannot leak into what another builder of the
// same request reads.
func TestAgentRequestToolNotesStayOutOfTheSharedToolList(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := product.Application().Chat
	registry, err := compileToolRegistry(chatSchema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	source, _ := countingOfferings(product)
	registry.offerings = source
	registry.offeringAllowed = offeringAvailability(product)
	orchestrator := &Orchestrator{
		schema: chatSchema, registry: registry, agentScope: registry.scope,
		toolNote: func(catalog.ChatToolSchema, TurnOptions) string { return "turn-note" },
	}
	// Attach the turn's fork the way Process does, so the test can inspect the
	// view the request and its prompt builders share.
	fork := forkTurnRegistry(registry)
	ctx := context.WithValue(context.Background(), turnRegistryContextKey{}, fork)

	request := orchestrator.agentRequest(ctx, nil,
		TurnOptions{AgentScope: "auto", CreativeDomain: "model-3d"}, "owner", "")

	noted := false
	for _, tool := range request.Tools {
		if strings.Contains(tool.Description, "turn-note") {
			noted = true
		}
	}
	if !noted {
		t.Fatal("the request carries none of the turn's tool notes")
	}
	for _, tool := range fork.describe(orchestrator.agentScope.Normalize("auto")) {
		if strings.Contains(tool.Description, "turn-note") {
			t.Fatalf("tool %q of the shared view carries the request's note", tool.Name)
		}
	}
}
