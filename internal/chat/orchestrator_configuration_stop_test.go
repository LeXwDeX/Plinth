package chat_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

type configurationStopRepository struct {
	*sqlite.Repository
	beforeFreeze func()
}

func (r *configurationStopRepository) CompareAndSwapTurnCheckpoint(ctx context.Context,
	owner, session, assistant string, version int64, oldState, newState string,
	checkpoint chat.TurnCheckpoint, allowStopped bool, updated time.Time,
) (int64, error) {
	if checkpoint.Configuration != nil && !allowStopped && r.beforeFreeze != nil {
		hook := r.beforeFreeze
		r.beforeFreeze = nil
		hook()
	}
	return r.Repository.CompareAndSwapTurnCheckpoint(ctx, owner, session, assistant,
		version, oldState, newState, checkpoint, allowStopped, updated)
}

func TestStopBeforeConfigurationFreezeFinishesAcrossRestart(t *testing.T) {
	for _, mode := range []string{"queued before restart", "during snapshot", "snapshot error", "before snapshot CAS"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "chat.db")
			repository, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			r := &configurationStopRepository{Repository: repository}
			t.Cleanup(func() { _ = r.Close() })
			service := newChatService(t, r)
			session, err := service.CreateSession(ctx, "owner-a", "Owner", "stop configuration")
			if err != nil {
				t.Fatal(err)
			}
			assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			stop := func() {
				t.Helper()
				if _, active, err := service.RequestTurnStop(ctx, "owner-a", session.ID); err != nil || !active {
					t.Fatalf("stop: active=%v err=%v", active, err)
				}
			}
			if mode == "queued before restart" {
				stop()
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
				r.Repository, err = sqlite.Open(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "before snapshot CAS" {
				r.beforeFreeze = stop
			}
			reads := 0
			executor := &unexpectedStopExecutor{}
			o, err := chat.NewOrchestrator(chat.OrchestratorConfig{
				Service: service, Schema: configuredChatSchema(t), Model: "model",
				Agent: &scriptedStreamFuncs{}, Tools: executor,
				Configuration: func(context.Context, string) (chat.AgentConfigurationSnapshot, error) {
					reads++
					if mode == "during snapshot" || mode == "snapshot error" {
						stop()
					}
					if mode == "snapshot error" {
						return chat.AgentConfigurationSnapshot{}, errors.New("configuration read interrupted")
					}
					return chat.AgentConfigurationSnapshot{Revision: "1", SystemPrompt: "frozen prompt"}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := o.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
				t.Fatal(err)
			}
			turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
			if err != nil || turn.State != "configured-turn-done" || turn.Checkpoint.Configuration != nil {
				t.Fatalf("stopped turn: %+v err=%v", turn, err)
			}
			if mode == "queued before restart" && reads != 0 {
				t.Fatal("configuration was read after durable Stop")
			}
			if pending, err := service.RecoverTurns(ctx); err != nil || len(pending) != 0 {
				t.Fatalf("stopped turn still blocks recovery: %+v %v", pending, err)
			}
			if _, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "next turn"}); err != nil {
				t.Fatalf("stopped session remains busy: %v", err)
			}
		})
	}
}
