package openaiv1

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #548: a request in the Skill draft shape (the authoring template's
// delimiters) is answered with a draft object; [[fake:skill-draft {JSON}]]
// scripts its members and is not copied into the body.
func TestSkillDraftShapeIsAnsweredWithADraft(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	draft := func(requirement string) map[string]any {
		t.Helper()
		status, body := f.chat(map[string]any{"model": "m", "messages": []map[string]any{
			{"role": "system", "content": "draft a Skill"},
			{"role": "user", "content": "<<<SKILL_REQUEST>>>\n名称：水彩\n要求：\n" + requirement + "\n<<<END_SKILL_REQUEST>>>"},
		}})
		if status != http.StatusOK {
			t.Fatalf("status %d: %v", status, body)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(dig(t, body, "choices", 0, "message", "content").(string)), &parsed); err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	plain := draft("角色设定图用柔和水彩。")
	if plain["slug"] != FixedSkillDraftSlug || plain["apply"] != "instruction" || plain["prompt"] != "角色设定图用柔和水彩。" || plain["description"] == "" {
		t.Fatalf("draft = %v", plain)
	}
	scripted := draft(`用水彩 [[fake:skill-draft {"slug":"Bad Slug","apply":"generation-prompt"}]]`)
	if scripted["slug"] != "Bad Slug" || scripted["apply"] != "generation-prompt" || scripted["prompt"] != "用水彩" {
		t.Fatalf("scripted draft = %v", scripted)
	}
	// Any other request keeps the fixed reply.
	_, other := f.chat(map[string]any{"model": "m", "messages": user("hi")})
	if dig(t, other, "choices", 0, "message", "content") != FixedReply {
		t.Fatalf("plain reply = %v", other)
	}
}
