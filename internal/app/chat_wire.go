package app

import (
	"context"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// chatWire serves each request of one chat connection over the HTTP API its
// model's profile declares (#401): chat.agent.thinking.model_profiles[].wire_api
// is chat_completions (the default) or responses. Every kind of request goes
// through the connection's one adapter (the Agent turn, the first-turn title,
// the compaction summary, prompt enhancement and the administrator's connection
// test), so the choice is made here, per request and by the request's model,
// and nothing else in the application knows which wire a model speaks. The two
// adapters are built from the same address, key and transport, so a deployment
// that records its upstream calls records both.
type chatWire struct {
	chatCompletions agents.Adapter
	responses       agents.Adapter
	wireFor         func(model string) string
}

// wireOf is the wire a request is served on: the one the turn froze with its
// model (#377), else the live profile's (every request that is not a turn).
func (wire chatWire) wireOf(request agents.Request) string {
	if request.Thinking != nil {
		return request.Thinking.Wire
	}
	return wire.wireFor(request.Model)
}

func (wire chatWire) adapter(request agents.Request) agents.Adapter {
	if wire.wireOf(request) == catalog.ChatWireResponses {
		return wire.responses
	}
	return wire.chatCompletions
}

func (wire chatWire) Stream(ctx context.Context, request agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	return wire.adapter(request).Stream(ctx, request, emit)
}

func (wire chatWire) Complete(ctx context.Context, request agents.Request) (agents.Completion, error) {
	return wire.adapter(request).Complete(ctx, request)
}

// responsesOptions are the request extensions of a model served on the
// Responses API: the profile's thinking levels and its temperature rule. A
// model whose profile declares another wire has none (it is not served here).
func responsesOptions(agent catalog.ChatAgentSchema, model string) *openairesponses.Options {
	profile, matched := chatModelProfile(agent.Thinking, model)
	if !matched || profile.Wire() != catalog.ChatWireResponses {
		return nil
	}
	options := &openairesponses.Options{OmitTemperature: profile.OmitTemperature}
	if profile.OffersEffort() {
		options.EffortLevels, options.DefaultEffort = profile.EffortLevels, profile.DefaultEffort
	}
	return options
}
