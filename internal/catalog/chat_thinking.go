package catalog

import "strings"

// ProfileFor returns the first vendor profile whose prefix the model name
// starts with, case-insensitively; none while thinking is off or nil.
func (thinking *ChatThinkingSchema) ProfileFor(model string) (ChatThinkingModelProfile, bool) {
	if thinking == nil || !thinking.Enabled {
		return ChatThinkingModelProfile{}, false
	}
	normalized := strings.ToLower(strings.TrimSpace(model))
	for _, profile := range thinking.ModelProfiles {
		for _, prefix := range profile.ModelPrefixes {
			if strings.HasPrefix(normalized, prefix) {
				return profile, true
			}
		}
	}
	return ChatThinkingModelProfile{}, false
}

// OffersEffort reports whether the profile lets a session choose a level.
func (profile ChatThinkingModelProfile) OffersEffort() bool {
	return profile.EffortField != "" && len(profile.EffortLevels) > 0
}

// ReasoningKind is how the model family carries its thinking (thinking_kind,
// #377): a profile that declares nothing carries none.
func (profile ChatThinkingModelProfile) ReasoningKind() string {
	switch profile.ThinkingKind {
	case ChatThinkingPlaintext, ChatThinkingEncrypted:
		return profile.ThinkingKind
	default:
		return ChatThinkingNone
	}
}

// EffortFor is the level a request sends for a chosen one: the choice when
// the profile offers it, else the profile default; empty when the profile
// offers no level choice.
func (profile ChatThinkingModelProfile) EffortFor(chosen string) string {
	if !profile.OffersEffort() {
		return ""
	}
	for _, level := range profile.EffortLevels {
		if level == chosen {
			return level
		}
	}
	return profile.DefaultEffort
}

// The kinds of request that are not a conversation turn and so take the level
// turn_efforts declares instead of the session's choice (#382).
const (
	// ChatTurnEffortTitle is the first turn's title request.
	ChatTurnEffortTitle = "title"
	// ChatTurnEffortSummary is a context compaction's summary request.
	ChatTurnEffortSummary = "summary"
	// ChatTurnEffortPromptEnhance is a prompt-enhancer request.
	ChatTurnEffortPromptEnhance = "prompt_enhance"
)

// EffortForTurn is the level a request of a kind that is not a conversation
// turn sends: the level turn_efforts declares for the kind when the profile
// offers it. A kind it does not name, or one it names with a level it does not
// offer, sends the profile default, exactly as a session's choice does. Empty
// when the profile offers no level choice.
func (profile ChatThinkingModelProfile) EffortForTurn(kind string) string {
	if !profile.OffersEffort() {
		return ""
	}
	if level, declared := profile.TurnEfforts[kind]; declared {
		for _, offered := range profile.EffortLevels {
			if offered == level {
				return level
			}
		}
	}
	return profile.DefaultEffort
}

// Wire is the HTTP API the model family is served on: chat_completions unless
// the profile declares wire_api responses.
func (profile ChatThinkingModelProfile) Wire() string {
	if profile.WireAPI == ChatWireResponses {
		return ChatWireResponses
	}
	return ChatWireCompletions
}

// RunsAgentTools reports whether the model family can run the function tools
// an Agent turn carries: on the responses wire it always can, on chat
// completions unless chat_completions_tools says it cannot. A model that
// matches no profile can (it is served on chat completions).
func (profile ChatThinkingModelProfile) RunsAgentTools() bool {
	return profile.Wire() == ChatWireResponses || profile.ChatCompletionsTools == nil || *profile.ChatCompletionsTools
}

// WireFor is the HTTP API a model is served on: its profile's wire_api, and
// chat completions for a model that matches no profile (#401).
func (thinking *ChatThinkingSchema) WireFor(model string) string {
	profile, _ := thinking.ProfileFor(model)
	return profile.Wire()
}

// UsesResponses reports whether any model profile is served on the responses
// wire, so a connection needs an adapter for it.
func (thinking *ChatThinkingSchema) UsesResponses() bool {
	if thinking == nil || !thinking.Enabled {
		return false
	}
	for _, profile := range thinking.ModelProfiles {
		if profile.Wire() == ChatWireResponses {
			return true
		}
	}
	return false
}

// IncludeStreamUsage is whether a streamed request of this family asks for
// token usage; agentDefault is chat.agent.stream_include_usage.
func (profile ChatThinkingModelProfile) IncludeStreamUsage(agentDefault bool) bool {
	if profile.StreamIncludeUsage != nil {
		return *profile.StreamIncludeUsage
	}
	return agentDefault
}
