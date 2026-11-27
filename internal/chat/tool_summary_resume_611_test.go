package chat_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #611 regression: a paid call that waits for its Task and finishes after the
// turn resumes (the auto mode's three-view continuation, #663) keeps the
// summary its last result carries. The resumed reply used to drop it, so a
// hand-picked Skill's create_three_view call lost its used_skill mark.
func TestASummaryReturnedAfterTheTurnResumesStaysOnTheReply611(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	executor.boundSummary = chat.UsedSkillSummary(chat.UsedSkill{
		ID: "system-three-view", DisplayName: "三视图", Source: "user", Applied: true})
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "resumed summary")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	o := waitingOrchestrator(t, service, executor)
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	executor.set("task-"+session.ID, generation.TaskSucceeded)
	if err := o.ScanWaitingTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-done" {
		t.Fatalf("turn = %#v, %v", turn, err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID != assistant.ID {
			continue
		}
		used := chat.UsedSkills(message.ToolCalls)
		if len(used) != 1 || used[0].ID != "system-three-view" || used[0].Source != "user" {
			t.Fatalf("the resumed reply lost the call's summary: used=%+v calls=%+v", used, message.ToolCalls)
		}
		return
	}
	t.Fatal("reply missing")
}
