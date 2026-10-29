package catalog

import (
	"fmt"
	"strings"

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
