package chat_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #709: a next_step option may carry continue_on_canvas. The server stores it
// like the other actions and never performs it: the page sends the person's
// previous message again as a canvas turn when they click the option. It
// names no function, so an operation_id is refused.
func TestNextStepCardStoresContinueOnCanvas(t *testing.T) {
	service, ctx := openNextStepService(t)
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-1",
		Call: agents.ToolCall{ID: "call-1", Name: "ask_choice"},
	}
	kind := nextStepChoiceKind()
	kind.Actions = append(kind.Actions, catalog.ChoiceActionContinueOnCanvas)
	refused, err := service.AskChoice(ctx, command, kind, chat.AskChoiceArguments{Kind: "next_step", Options: []chat.ChoiceOptionInput{
		{ID: "board", Label: "以画板为主继续", Action: &chat.ChoiceAction{Type: "continue_on_canvas", OperationID: "retexture"}},
		{ID: "keep", Label: "保持"},
	}})
	if err != nil || !refused.NoSubmission || !strings.Contains(refused.Error, "continue_on_canvas takes no operation_id") {
		t.Fatalf("refusal = %+v %v", refused, err)
	}
	// A kind that does not declare it refuses it like any other undeclared action.
	if refused, err := service.AskChoice(ctx, command, nextStepChoiceKind(), chat.AskChoiceArguments{Kind: "next_step", Options: []chat.ChoiceOptionInput{
		{ID: "board", Label: "以画板为主继续", Action: &chat.ChoiceAction{Type: "continue_on_canvas"}},
		{ID: "keep", Label: "保持"},
	}}); err != nil || !strings.Contains(refused.Error, "not allowed") {
		t.Fatalf("undeclared refusal = %+v %v", refused, err)
	}
	opened, err := service.AskChoice(ctx, command, kind, chat.AskChoiceArguments{Kind: "next_step", Options: []chat.ChoiceOptionInput{
		{ID: "board", Label: "以画板为主继续", Description: "把上一条话以画板为主重新发送", Action: &chat.ChoiceAction{Type: "continue_on_canvas"}},
		{ID: "keep", Label: "保持"},
	}})
	if err != nil || opened.Error != "" {
		t.Fatalf("card = %+v %v", opened, err)
	}
	choices, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 {
		t.Fatalf("stored cards = %+v %v", choices, err)
	}
	if action := choices[0].Options[0].Action; action == nil || action.Type != "continue_on_canvas" || action.OperationID != "" {
		t.Fatalf("stored action = %+v", choices[0].Options[0].Action)
	}
}
