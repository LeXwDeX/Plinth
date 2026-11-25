package agenttools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// #615: a second card in the same reply is refused with the Schema's words,
// which tell the Agent to wait for the open card instead of asking again.
func TestAskChoiceRefusesASecondCardOfTheSameReplyInSchemaWords_615(t *testing.T) {
	kind := catalog.ChatChoiceKindSchema{ID: "next_step", OptionSource: "agent"}
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &fakeGenerate{},
		ChoiceKinds:       []catalog.ChatChoiceKindSchema{kind},
		ChoiceOpenInReply: "本轮已经打开了一张选择卡，等用户在卡上回答。",
		AskChoice: func(context.Context, chat.ToolCommand, catalog.ChatChoiceKindSchema,
			chat.AskChoiceArguments,
		) (chat.ToolResult, error) {
			return chat.ToolResult{}, chat.ErrChoiceOpenInReply
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{ID: "call-second", Name: ToolAskChoice,
			Arguments: json.RawMessage(`{"kind":"next_step","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}]}`)}}, nil)
	if err != nil || !result.NoSubmission || result.Error != "本轮已经打开了一张选择卡，等用户在卡上回答。" {
		t.Fatalf("second card = %+v %v", result, err)
	}
}
