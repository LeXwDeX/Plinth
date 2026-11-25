package openaiv1

import (
	"encoding/json"
	"strings"
)

// The Skill draft (#548) is a Plinth request shape, not an upstream contract:
// the user message wraps the person's Skill name and requirement in the
// delimiters of the Product Schema's chat.skills.authoring.user_template. A
// request whose last user message carries them is answered with a draft object
// in the shape the authoring prompt asks for, so the fake stack can draft a
// Skill without a real model. The official model has no such behavior.
const (
	skillRequestOpenMarker  = "<<<SKILL_REQUEST>>>"
	skillRequestCloseMarker = "<<<END_SKILL_REQUEST>>>"
	// SkillDraftMarker, [[fake:skill-draft {JSON}]] inside the requirement,
	// replaces members of the fixed draft (slug, description, prompt, apply),
	// so a test can script an invalid slug, another kind or an unusable draft
	// (an empty prompt). Without it the draft is FixedSkillDraftSlug, a fixed
	// description, apply instruction and the requirement text as the body.
	SkillDraftMarker = "[[fake:skill-draft"
	// FixedSkillDraftSlug is the slug of the unscripted draft.
	FixedSkillDraftSlug = "fake-skill-draft"
)

// skillDraft returns the draft reply when the request is a Skill draft.
func (r chatRequest) skillDraft() (string, bool) {
	last := r.lastText()
	start := strings.Index(last, skillRequestOpenMarker)
	if start < 0 {
		return "", false
	}
	body := last[start+len(skillRequestOpenMarker):]
	if end := strings.Index(body, skillRequestCloseMarker); end >= 0 {
		body = body[:end]
	}
	draft := map[string]any{
		"slug":        FixedSkillDraftSlug,
		"description": "Fake 起草的 Skill：用户的要求与它相关时使用。",
		"apply":       "instruction",
	}
	if marker := strings.Index(body, SkillDraftMarker); marker >= 0 {
		rest := body[marker+len(SkillDraftMarker):]
		closing := strings.Index(rest, "]]")
		if open := strings.Index(rest, "{"); open >= 0 && (closing < 0 || open < closing) {
			if end, ok := scanBalancedJSON(rest, open); ok {
				var overrides map[string]any
				if json.Unmarshal([]byte(rest[open:end]), &overrides) == nil {
					for key, value := range overrides {
						draft[key] = value
					}
				}
				closing = end + strings.Index(rest[end:], "]]")
			}
		}
		if closing >= 0 {
			body = body[:marker] + rest[closing+2:]
		} else {
			body = body[:marker]
		}
	}
	if _, scripted := draft["prompt"]; !scripted {
		// The template's first two lines are the name and the requirement's
		// label; the requirement follows.
		lines := strings.Split(strings.TrimSpace(body), "\n")
		if len(lines) > 2 {
			lines = lines[2:]
		}
		draft["prompt"] = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	encoded, _ := json.Marshal(draft)
	return string(encoded), true
}
