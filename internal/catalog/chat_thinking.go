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

// RunsAgentTools reports whether the model family can run the function tools
// an Agent turn carries on chat completions (chat_completions_tools); a model
// that matches no profile can.
func (profile ChatThinkingModelProfile) RunsAgentTools() bool {
	return profile.ChatCompletionsTools == nil || *profile.ChatCompletionsTools
}

// IncludeStreamUsage is whether a streamed request of this family asks for
// token usage; agentDefault is chat.agent.stream_include_usage.
func (profile ChatThinkingModelProfile) IncludeStreamUsage(agentDefault bool) bool {
	if profile.StreamIncludeUsage != nil {
		return *profile.StreamIncludeUsage
	}
	return agentDefault
}
