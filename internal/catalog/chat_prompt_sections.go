package catalog

import (
	"fmt"
	"strings"
	"unicode/utf8"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// ChatAgentPromptSection is a piece of Agent-facing text (a paragraph of the
// system prompt, or a phrase in an Agent scope description) that teaches a
// capability. It applies only while at least one of Capabilities has an
// enabled Offering; otherwise it is removed from every projection and from
// what the model receives, and it comes back as soon as an Offering is enabled
// again (#115).
//
// Text is matched literally. A Text that fills its own line also takes the
// preceding line break with it. Published Agent configurations freeze the
// system prompt as text, so the same literal match is applied when such a
// version is read; the stored version itself is never rewritten.
type ChatAgentPromptSection struct {
	ID           string                      `json:"id"`
	Capabilities []schemacontract.Capability `json:"capabilities"`
	Text         string                      `json:"text"`
}

// ChatAgentCraftPrompt is a domain brief appended to the system context of a
// turn whose offered tools include at least one name in WhenToolsAny. It lets
// the shipped Catalog teach a craft (for example 3D modeling) only when the
// Agent can actually act in that domain, without growing the base system
// prompt on unrelated turns.
//
// Texts are plain prompt text; unlike ChatAgentPromptSection they are not
// tied to capability activation, because tool availability already reflects
// the enabled Offerings and the session's Agent scope.
type ChatAgentCraftPrompt struct {
	ID           string   `json:"id"`
	WhenToolsAny []string `json:"when_tools_any"`
	Text         string   `json:"text"`
	// OfferingCapabilities selects the Offerings whose knowledge fills
	// {offering_tradeoffs} (#249). Empty keeps a historical CraftPrompt
	// literal.
	OfferingCapabilities []schemacontract.Capability `json:"offering_capabilities,omitempty"`
	// TradeoffTemplate renders one Offering: {model}, {strengths} and
	// {weaknesses}. Entries are joined by TradeoffSeparator.
	TradeoffTemplate  string `json:"tradeoff_template,omitempty"`
	TradeoffSeparator string `json:"tradeoff_separator,omitempty"`
}

// CapabilityActive reports whether a capability currently has an enabled
// Offering.
type CapabilityActive func(schemacontract.Capability) bool

// ActiveCapabilities builds the CapabilityActive of a set of Offerings. A
// disabled Offering never activates its capability.
func ActiveCapabilities(offerings []schemacontract.Offering) CapabilityActive {
	active := make(map[schemacontract.Capability]bool, len(offerings))
	for _, offering := range offerings {
		if offering.Enabled {
			active[offering.Capability] = true
		}
	}
	return func(capability schemacontract.Capability) bool { return active[capability] }
}

// AnyCapabilityActive reports whether at least one capability is active. An
// empty list is not tied to a capability, so it is always active.
func AnyCapabilityActive(capabilities []schemacontract.Capability, active CapabilityActive) bool {
	if len(capabilities) == 0 {
		return true
	}
	for _, capability := range capabilities {
		if active(capability) {
			return true
		}
	}
	return false
}

// StripInactivePromptSections removes the text of every section whose
// capabilities have no enabled Offering.
func StripInactivePromptSections(text string, sections []ChatAgentPromptSection, active CapabilityActive) string {
	for _, section := range sections {
		if section.Text == "" || AnyCapabilityActive(section.Capabilities, active) {
			continue
		}
		text = strings.ReplaceAll(text, "\n"+section.Text, "")
		text = strings.ReplaceAll(text, section.Text, "")
	}
	return text
}

// WithoutInactivePromptText returns a copy of the chat schema whose Agent
// system prompt, domain contexts and Agent scope descriptions omit the sections
// whose capabilities have no enabled Offering. The input is not modified.
func (schema ChatSchema) WithoutInactivePromptText(active CapabilityActive) ChatSchema {
	sections := schema.Agent.PromptSections
	if len(sections) == 0 {
		return schema
	}
	schema.Agent.SystemPrompt = StripInactivePromptSections(schema.Agent.SystemPrompt, sections, active)
	contexts := make(map[string]string, len(schema.Agent.DomainContext))
	for key, value := range schema.Agent.DomainContext {
		contexts[key] = StripInactivePromptSections(value, sections, active)
	}
	schema.Agent.DomainContext = contexts
	modes := append([]ChatAgentScopeMode(nil), schema.AgentScope.Modes...)
	for index := range modes {
		modes[index].Description = StripInactivePromptSections(modes[index].Description, sections, active)
	}
	schema.AgentScope.Modes = modes
	return schema
}

// validateCraftPrompts checks the domain briefs: every id is unique, every
// trigger names a registered tool, and every text is non-empty and within the
// same character bound as the editable system prompt. toolNames holds both the
// Agent tools and the generation tools, because describe() offers both.
func validateCraftPrompts(prompts []ChatAgentCraftPrompt, toolNames map[string]struct{}, maxPromptCharacters int) error {
	seen := make(map[string]bool, len(prompts))
	for _, prompt := range prompts {
		id := strings.TrimSpace(prompt.ID)
		if id == "" || seen[id] {
			return fmt.Errorf("chat.agent.craft_prompts id 为空或重复: %q", prompt.ID)
		}
		seen[id] = true
		if len(prompt.WhenToolsAny) == 0 {
			return fmt.Errorf("chat.agent.craft_prompts %q 必须声明 when_tools_any", prompt.ID)
		}
		for _, tool := range prompt.WhenToolsAny {
			if _, known := toolNames[tool]; !known {
				return fmt.Errorf("chat.agent.craft_prompts %q 引用了未注册工具 %q", prompt.ID, tool)
			}
		}
		if strings.TrimSpace(prompt.Text) == "" {
			return fmt.Errorf("chat.agent.craft_prompts %q 的 text 不能为空", prompt.ID)
		}
		if maxPromptCharacters > 0 && utf8.RuneCountInString(prompt.Text) > maxPromptCharacters {
			return fmt.Errorf("chat.agent.craft_prompts %q 的 text 超过 %d 字符", prompt.ID, maxPromptCharacters)
		}
	}
	return nil
}

// validateCraftPromptOfferings checks the Offering knowledge projection of a
// craft prompt (#249). A prompt may fill {offering_tradeoffs} at most once;
// with it, the capabilities and the tradeoff template must be declared.
func validateCraftPromptOfferings(prompts []ChatAgentCraftPrompt, capabilityDomains map[schemacontract.Capability]schemacontract.CapabilityDomain) error {
	for _, prompt := range prompts {
		placeholder := "{offering_tradeoffs}"
		count := strings.Count(prompt.Text, placeholder)
		if count > 1 {
			return fmt.Errorf("chat.agent.craft_prompts %q 的 text 最多只能包含一次 %s", prompt.ID, placeholder)
		}
		if count == 0 {
			if len(prompt.OfferingCapabilities) > 0 || prompt.TradeoffTemplate != "" || prompt.TradeoffSeparator != "" {
				return fmt.Errorf("chat.agent.craft_prompts %q 声明了 offering_capabilities 或 tradeoff 模板却没有 %s", prompt.ID, placeholder)
			}
			continue
		}
		if len(prompt.OfferingCapabilities) == 0 {
			return fmt.Errorf("chat.agent.craft_prompts %q 的 %s 需要 offering_capabilities", prompt.ID, placeholder)
		}
		for _, capability := range prompt.OfferingCapabilities {
			if _, known := capabilityDomains[capability]; !known {
				return fmt.Errorf("chat.agent.craft_prompts %q 的 offering_capabilities 含未知能力 %q", prompt.ID, capability)
			}
		}
		if !strings.Contains(prompt.TradeoffTemplate, "{model}") || !strings.Contains(prompt.TradeoffTemplate, "{strengths}") {
			return fmt.Errorf("chat.agent.craft_prompts %q 的 tradeoff_template 必须包含 {model} 与 {strengths}", prompt.ID)
		}
		if prompt.TradeoffSeparator == "" {
			return fmt.Errorf("chat.agent.craft_prompts %q 的 tradeoff_separator 不能为空", prompt.ID)
		}
	}
	return nil
}

func validatePromptSections(schema ChatSchema, capabilityDomains map[schemacontract.Capability]schemacontract.CapabilityDomain) error {
	seen := make(map[string]bool, len(schema.Agent.PromptSections))
	for _, section := range schema.Agent.PromptSections {
		if strings.TrimSpace(section.ID) == "" || seen[section.ID] {
			return fmt.Errorf("chat.agent.prompt_sections id 为空或重复: %q", section.ID)
		}
		seen[section.ID] = true
		if strings.TrimSpace(section.Text) == "" || len(section.Capabilities) == 0 {
			return fmt.Errorf("chat.agent.prompt_sections %q 必须声明 text 和 capabilities", section.ID)
		}
		for _, capability := range section.Capabilities {
			if _, known := capabilityDomains[capability]; !known {
				return fmt.Errorf("chat.agent.prompt_sections %q 引用了未知能力 %q", section.ID, capability)
			}
		}
		found := strings.Contains(schema.Agent.SystemPrompt, section.Text)
		for _, value := range schema.Agent.DomainContext {
			found = found || strings.Contains(value, section.Text)
		}
		for _, mode := range schema.AgentScope.Modes {
			found = found || strings.Contains(mode.Description, section.Text)
		}
		if !found {
			return fmt.Errorf("chat.agent.prompt_sections %q 的 text 不在系统提示、领域上下文或 Agent 范围描述中", section.ID)
		}
	}
	return nil
}
