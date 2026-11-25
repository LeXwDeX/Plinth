package mock

import (
	"encoding/json"
	"strings"
)

// skillDraft answers a Skill draft request (#548) with the draft shape the
// Schema's authoring prompt asks for: a fixed slug and description, and the
// person's requirement (the text after the template's last line break before
// the closing delimiter) as the body. A real model writes all four itself.
func skillDraft(message string) string {
	body := message
	if start := strings.Index(body, "<<<SKILL_REQUEST>>>"); start >= 0 {
		body = body[start+len("<<<SKILL_REQUEST>>>"):]
	}
	body = strings.TrimSpace(strings.Replace(body, "<<<END_SKILL_REQUEST>>>", "", 1))
	encoded, _ := json.Marshal(map[string]string{
		"slug":        "mock-skill-draft",
		"description": "Mock 起草的 Skill：用户提到相关要求时使用。",
		"prompt":      body,
		"apply":       "instruction",
	})
	return string(encoded)
}
