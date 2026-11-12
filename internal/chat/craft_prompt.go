package chat

import (
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// craftPrompt is the domain-brief block of the system context. Every Schema
// craft prompt whose WhenToolsAny names at least one tool offered this turn,
// and whose optional WhenDomain matches the turn's workspace domain, is
// appended in Schema order, so the Agent receives the 3D craft only on a 3D
// turn and the 2D craft only on a 2D turn even when auto scope offers both tool
// sets (#239). The offered list is the same describe() list the model gets, so
// a tool withheld by the session's Agent scope, a disabled Offering or a bound
// harness never triggers its brief. A brief whose WhenBriefAny field is set in
// the frozen production brief ignores the domain filter: a session whose brief
// already chose a 3D purpose gets the 3D craft on a 2D stage turn too (#285).
func (orchestrator *Orchestrator) craftPrompt(registry *toolRegistry, scope, domain string, brief ProductionBrief) string {
	prompts := orchestrator.schema.Agent.CraftPrompts
	if len(prompts) == 0 || registry == nil {
		return ""
	}
	offered := make(map[string]struct{}, len(registry.ordered))
	for _, tool := range registry.describe(scope) {
		offered[tool.Name] = struct{}{}
	}
	var block strings.Builder
	for _, prompt := range prompts {
		if strings.TrimSpace(prompt.Text) == "" {
			continue
		}
		if prompt.WhenDomain != "" && domain != "" && prompt.WhenDomain != domain && !briefSetsAny(brief, prompt.WhenBriefAny) {
			continue
		}
		matched := false
		for _, name := range prompt.WhenToolsAny {
			if _, ok := offered[name]; ok {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		text := prompt.Text
		if strings.Contains(text, "{offering_tradeoffs}") {
			tradeoffs := registry.offeringTradeoffs(prompt)
			text = strings.ReplaceAll(text, "{offering_tradeoffs}",
				strings.Join(tradeoffs, prompt.TradeoffSeparator))
			if len(tradeoffs) == 0 {
				text = withoutEmptyLines(text)
			}
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		block.WriteString("\n\n")
		block.WriteString(text)
	}
	return block.String()
}

// offeringTradeoffs renders one line per known Offering of the craft prompt's
// capabilities from the Offering's own knowledge (#249). Only enabled and
// runtime-available Offerings appear, deduplicated by provider and model, so
// disabling an Offering changes the brief without editing the prompt.
func (registry *toolRegistry) offeringTradeoffs(prompt catalog.ChatAgentCraftPrompt) []string {
	if len(prompt.OfferingCapabilities) == 0 || prompt.TradeoffTemplate == "" || registry.offerings == nil {
		return nil
	}
	unknown := strings.TrimSpace(registry.craftPromptUnknownText())
	if unknown == "" {
		unknown = "unknown"
	}
	seen := map[string]bool{}
	lines := make([]string, 0)
	for _, capability := range prompt.OfferingCapabilities {
		for _, offering := range registry.offerings(string(capability)) {
			if offering.Capability != capability || !offering.Enabled || offering.Knowledge == nil {
				continue
			}
			if registry.offeringAllowed != nil && !registry.offeringAllowed(string(capability), offering.ModelID) {
				continue
			}
			if len(offering.Knowledge.Strengths) == 0 && len(offering.Knowledge.Weaknesses) == 0 {
				continue
			}
			key := offering.ProviderID + "\x00" + offering.ModelID
			if seen[key] {
				continue
			}
			seen[key] = true
			model := offering.ModelName
			if model == "" {
				model = offering.ModelID
			}
			strengths := strings.Join(offering.Knowledge.Strengths, "；")
			weaknesses := strings.Join(offering.Knowledge.Weaknesses, "；")
			if strengths == "" {
				strengths = unknown
			}
			if weaknesses == "" {
				weaknesses = unknown
			}
			lines = append(lines, strings.NewReplacer(
				"{model}", model,
				"{strengths}", strengths,
				"{weaknesses}", weaknesses,
			).Replace(prompt.TradeoffTemplate))
		}
	}
	return lines
}

func (registry *toolRegistry) craftPromptUnknownText() string {
	return registry.craftPromptUnknown
}

// withoutEmptyLines removes lines that became empty after replacing a
// comparison block, without touching the other lines' bytes.
func withoutEmptyLines(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// briefSetsAny reports whether the brief sets any named field: a non-empty
// text (profile ids are text) or a true flag.
func briefSetsAny(brief ProductionBrief, names []string) bool {
	for _, name := range names {
		if brief.Text(name) != "" || brief.Flag(name) {
			return true
		}
	}
	return false
}
