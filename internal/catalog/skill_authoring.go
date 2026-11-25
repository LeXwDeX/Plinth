package catalog

import (
	"errors"
	"strings"
)

// ChatSkillAuthoringSchema is the server-only prompt of the Skill draft (#548):
// one chat model call turns a person's Skill name and requirement into a
// draft of the standard fields (slug, description, body and how it applies).
// The requirement is never written into the system prompt; it goes into the
// user message, inside the template's own delimiters.
type ChatSkillAuthoringSchema struct {
	// SystemPrompt tells the model the draft's shape: one JSON object with
	// the members slug, description, prompt and apply.
	SystemPrompt string `json:"system_prompt"`
	// UserTemplate is the user message, with {name} and {requirement}.
	UserTemplate string `json:"user_template"`
}

// validateSkillAuthoring checks the draft route and its prompt together: a
// Schema that offers the route must declare the prompt it runs.
func validateSkillAuthoring(skills ChatSkillsSchema) error {
	creation := skills.Creation
	if creation.DraftRoute == "" {
		return nil
	}
	if creation.Route == "" || creation.DraftRoute != creation.Route+"/draft" {
		return errors.New("skills.creation.draft_route 必须是 route 后接 /draft")
	}
	if creation.MaxRequirementCharacters <= 0 {
		return errors.New("skills.creation.max_requirement_characters 必须为正数")
	}
	authoring := skills.Authoring
	if strings.TrimSpace(authoring.SystemPrompt) == "" {
		return errors.New("skills.authoring.system_prompt 不能为空")
	}
	for _, placeholder := range []string{"{name}", "{requirement}"} {
		if !strings.Contains(authoring.UserTemplate, placeholder) {
			return errors.New("skills.authoring.user_template 必须包含 " + placeholder)
		}
	}
	return nil
}
