package catalog

import (
	"slices"
	"strings"
)

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

// The kinds of request that take the level turn_efforts declares (#382): the
// requests beside a conversation turn, which never take the session's choice,
// and a turn's own tool follow-up rounds, which take it only when the person
// chose a level (#638).
const (
	// ChatTurnEffortTitle is the first turn's title request.
	ChatTurnEffortTitle = "title"
	// ChatTurnEffortSummary is a context compaction's summary request.
	ChatTurnEffortSummary = "summary"
	// ChatTurnEffortPromptEnhance is a prompt-enhancer request.
	ChatTurnEffortPromptEnhance = "prompt_enhance"
	// ChatTurnEffortToolFollowup is a conversation turn's round after its tool
	// results came back (#638): the model mostly reads what a read-only check
	// or an Offering list returned and picks the next call, so it needs far
	// less thinking than the round that answered the person's message.
	ChatTurnEffortToolFollowup = "tool_followup"
)

// chatTurnEffortKinds are the kinds turn_efforts may name.
var chatTurnEffortKinds = []string{
	ChatTurnEffortTitle, ChatTurnEffortSummary, ChatTurnEffortPromptEnhance, ChatTurnEffortToolFollowup,
}

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

// EffortForFollowUp is the level a tool follow-up round of a turn sends
// (#638), for a turn whose own level is turnLevel. A level the person chose
// for the session is kept for every round of the turn. Otherwise the round
// takes the level turn_efforts declares for tool_followup when the profile
// offers it (and offered, when not empty, the levels the administrator opened
// for the model, lists it); a profile that declares none keeps turnLevel.
func (profile ChatThinkingModelProfile) EffortForFollowUp(turnLevel string, chosen bool, offered []string) string {
	if chosen || !profile.OffersEffort() {
		return turnLevel
	}
	level, declared := profile.TurnEfforts[ChatTurnEffortToolFollowup]
	if !declared || !slices.Contains(profile.EffortLevels, level) ||
		(len(offered) > 0 && !slices.Contains(offered, level)) {
		return turnLevel
	}
	return level
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
