package mock_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// #548: the mock answers a Skill draft request with the draft object the
// authoring prompt asks for, the requirement as its body.
func TestMockAnswersASkillDraft(t *testing.T) {
	completion, err := newRoutingAdapter(t).Complete(context.Background(), agents.Request{
		Purpose: agents.PurposeSkillDraft,
		Messages: []agents.Message{{Role: "user",
			Content: "<<<SKILL_REQUEST>>>\n名称：水彩\n要求：\n用水彩\n<<<END_SKILL_REQUEST>>>"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var draft map[string]string
	if err := json.Unmarshal([]byte(completion.Content), &draft); err != nil {
		t.Fatal(err)
	}
	if draft["slug"] != "mock-skill-draft" || draft["apply"] != "instruction" || draft["description"] == "" || draft["prompt"] == "" {
		t.Fatalf("draft = %v", draft)
	}
}
