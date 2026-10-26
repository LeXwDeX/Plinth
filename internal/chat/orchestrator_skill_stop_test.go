package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type stoppedSkillExecutor struct {
	executed int
	readErr  error
	turnID   string
	session  string
}

func (e *stoppedSkillExecutor) Execute(context.Context, chat.ToolCommand, func(chat.ToolObservation) error) (chat.ToolResult, error) {
	e.executed++
	return chat.ToolResult{}, errors.New("paid response lost before Chat binding")
}

func (e *stoppedSkillExecutor) TaskIdentityForCall(_ context.Context, command chat.ToolCommand) (chat.ToolTaskIdentity, error) {
	if command.OwnerID != "owner-a" || command.SessionID != e.session || command.AssistantMessageID != e.turnID || command.Call.Name != "create_three_view" {
		return chat.ToolTaskIdentity{}, chat.ErrTurnCheckpointConflict
	}
	if e.readErr != nil {
		return chat.ToolTaskIdentity{}, e.readErr
	}
	return chat.ToolTaskIdentity{TaskID: "skill-task", IdempotencyKey: "skill_submit_original"}, nil
}

func TestStopRecoversThreeViewActualStepIdentity(t *testing.T) {
	for _, mode := range []string{"accepted without Chat binding", "transient identity read", "deleted accepted Task"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			service := newChatService(t, newMemoryChatRepository())
			schema := configuredChatSchema(t)
			session, err := service.CreateSession(ctx, "owner-a", "Owner", "stop Skill")
			if err != nil {
				t.Fatal(err)
			}
			assistantID := startTurnFor(t, service, "owner-a", session.ID, "three views")
			executor := &stoppedSkillExecutor{turnID: assistantID, session: session.ID}
			agent := &scriptedSuspendAgent{initial: agents.Completion{ToolCalls: []agents.ToolCall{{
				ID: "skill-call", Name: "create_three_view", Arguments: []byte(`{}`),
			}}}}
			canceled := 0
			makeOrchestrator := func() *chat.Orchestrator {
				o, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema, Model: "model", Agent: agent, Tools: executor,
					TaskLookup: func(_ context.Context, owner, key string) (generation.Task, error) {
						if owner != "owner-a" || key != "skill_submit_original" {
							t.Fatalf("Stop guessed Skill identity: owner=%s key=%s", owner, key)
						}
						if mode == "deleted accepted Task" {
							return generation.Task{}, &generation.Error{Code: generation.ErrConflict}
						}
						return generation.Task{ID: "skill-task", OwnerID: "owner-a", SessionID: session.ID}, nil
					},
					TaskGet: func(_ context.Context, owner, id string) (generation.Task, error) {
						return generation.Task{ID: generation.TaskID(id), OwnerID: generation.OwnerID(owner), SessionID: session.ID}, nil
					},
					TaskCanceler: func(_ context.Context, owner, id string) error {
						if owner != "owner-a" || id != "skill-task" {
							t.Fatalf("Stop canceled foreign task: %s/%s", owner, id)
						}
						canceled++
						return nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				return o
			}
			command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistantID}
			if err := makeOrchestrator().Process(ctx, command); err != nil {
				t.Fatal(err)
			}
			if _, active, err := service.RequestTurnStop(ctx, "owner-a", session.ID); err != nil || !active {
				t.Fatalf("Stop did not persist: %v %v", active, err)
			}
			if mode == "transient identity read" {
				executor.readErr = errors.New("Skill ledger temporarily unavailable")
				if err := makeOrchestrator().Process(ctx, command); err == nil {
					t.Fatal("unverified paid call incorrectly finalized Stop")
				}
				if pending, err := service.RecoverTurns(ctx); err != nil || len(pending) != 1 || !pending[0].StopRequested || canceled != 0 {
					t.Fatalf("transient Stop lost recovery: %+v canceled=%d err=%v", pending, canceled, err)
				}
				executor.readErr = nil
			}
			if err := makeOrchestrator().Process(ctx, command); err != nil {
				t.Fatal(err)
			}
			turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistantID)
			wantCanceled := 1
			if mode == "deleted accepted Task" {
				wantCanceled = 0
			}
			if err != nil || turn.State != "configured-turn-done" || executor.executed != 1 || canceled != wantCanceled {
				t.Fatalf("Skill Stop failed: %+v executed=%d canceled=%d err=%v", turn, executor.executed, canceled, err)
			}
			messages, err := service.ListMessages(ctx, "owner-a", session.ID)
			if err != nil || messages[1].ToolCalls[0].TaskID != "skill-task" || messages[1].ToolCalls[0].IdempotencyKey != "skill_submit_original" {
				t.Fatalf("stopped history lost accepted Skill step: %+v %v", messages, err)
			}
		})
	}
}
