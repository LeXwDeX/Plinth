package chat_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #699: the proportion card showed the kind's default question ("接下来想怎么
// 做？") instead of the Agent's own question, because the question was only in
// the tool result. The card keeps the Agent's bounded question; a blank or
// over-long one leaves the kind's default.
func TestNextStepCardKeepsTheAgentQuestion_699(t *testing.T) {
	const asked = "风格图里看不到全身，想按哪种身体比例来画？"
	for name, test := range map[string]struct{ question, want, stored string }{
		"agent question": {question: "  " + asked + " ", want: asked, stored: asked},
		"blank":          {question: "  ", want: nextStepChoiceKind().Question},
		"over-long":      {question: strings.Repeat("比", 301), want: nextStepChoiceKind().Question},
		"at the bound":   {question: strings.Repeat("比", 300), want: strings.Repeat("比", 300), stored: strings.Repeat("比", 300)},
	} {
		t.Run(name, func(t *testing.T) {
			service, ctx := openNextStepService(t)
			result, err := service.AskChoice(ctx, chat.ToolCommand{
				OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-1",
				Call: agents.ToolCall{ID: "call-1", Name: "ask_choice"},
			}, nextStepChoiceKind(), chat.AskChoiceArguments{Kind: "next_step", Question: test.question, Options: []chat.ChoiceOptionInput{
				{ID: "four_heads", Label: "约 4 头身，正常头部大小"},
				{ID: "style_heads", Label: "完全按风格图的头身比"},
			}})
			if err != nil || result.Error != "" {
				t.Fatalf("next_step card = %+v err=%v", result, err)
			}
			var opened struct {
				Question string `json:"question"`
			}
			if err := json.Unmarshal([]byte(result.AgentContent), &opened); err != nil || opened.Question != test.want {
				t.Fatalf("agent content = %s %v", result.AgentContent, err)
			}
			choices, err := service.ListChoices(ctx, "owner-a", "session-a")
			if err != nil || len(choices) != 1 || choices[0].Question != test.stored {
				t.Fatalf("stored cards = %+v %v", choices, err)
			}
		})
	}
}
