package chat

import (
	"context"
	"testing"
	"time"
)

func robustnessOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	orchestrator, err := NewOrchestrator(OrchestratorConfig{
		Service: newChatServiceForInternal(t),
		Schema:  suspendTestSchema(),
		Model:   "configured-model",
		Agent:   &noopStreamAgent{},
		Tools:   &noopToolExecutorForWake{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return orchestrator
}

// #665: a stop marker is written only while the stopped turn is registered,
// and it leaves with the registration. A stop that raced the end of the turn
// cannot stop the session's next turn.
func TestStopMarkerNeverOutlivesTheStoppedTurn(t *testing.T) {
	orchestrator := robustnessOrchestrator(t)
	key := orchestrator.turnKey("owner", "session")
	if cancel := orchestrator.markActiveTurnCanceled(key, "message-1"); cancel != nil || orchestrator.turnCanceled(key) {
		t.Fatal("a stop of a turn that is not running left a marker")
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	orchestrator.registerActiveTurn(key, "message-1", cancel)
	if orchestrator.markActiveTurnCanceled(key, "message-other") != nil || orchestrator.turnCanceled(key) {
		t.Fatal("a stop of another turn marked the running one")
	}
	// The turn ends: its deferred clearTurnCanceled ran before the stop took
	// the lock, the stop still finds it registered.
	orchestrator.clearTurnCanceled(key)
	if orchestrator.markActiveTurnCanceled(key, "message-1") == nil || !orchestrator.turnCanceled(key) {
		t.Fatal("the stop of the running turn did not mark it")
	}
	orchestrator.unregisterActiveTurn(key)
	if orchestrator.consumeTurnCanceled(key) {
		t.Fatal("the stop marker outlived its turn and would stop the next one")
	}
}

// #665: a consent decision that lands while the turn is still running, before
// it registered its wait, wakes the turn as soon as it registers instead of
// leaving it to consent_wait_timeout.
func TestConsentDecisionBeforeTheWaitWakesTheTurn(t *testing.T) {
	orchestrator := robustnessOrchestrator(t)
	command := ProcessTurn{OwnerID: "owner", SessionID: "session", AssistantMessageID: "message-early"}
	key := orchestrator.turnKey(command.OwnerID, command.SessionID)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	orchestrator.registerActiveTurn(key, command.AssistantMessageID, cancel)
	if orchestrator.ResumeConsentWait(command.OwnerID, command.SessionID) {
		t.Fatal("nothing was suspended yet")
	}
	if !orchestrator.suspendForConsent(command) {
		t.Fatal("suspendForConsent refused")
	}
	select {
	case woken := <-orchestrator.queue:
		if woken.AssistantMessageID != command.AssistantMessageID {
			t.Fatalf("woke %q", woken.AssistantMessageID)
		}
		orchestrator.unmarkQueued(woken.AssistantMessageID)
	case <-time.After(2 * time.Second):
		t.Fatal("the early decision was lost; the turn waits for its timeout")
	}
	if orchestrator.PendingConsentWaits() != 0 {
		t.Fatal("the woken wait is still registered")
	}
}

// #665: a panic while a worker dispatches a command (here the repository
// itself) is recovered: the worker survives, the command leaves the queue and
// the turn is never run again by this process.
func TestDispatchRecoversAPanicAndRetiresTheTurn(t *testing.T) {
	orchestrator := robustnessOrchestrator(t)
	command := ProcessTurn{OwnerID: "owner", SessionID: "session", AssistantMessageID: "message-panic"}
	if !orchestrator.markQueued(command.AssistantMessageID) {
		t.Fatal("markQueued")
	}
	// stubRepository panics on every call: the dispatch read of the turn
	// panics, and so does every write of its ending.
	orchestrator.dispatch(context.Background(), command)
	if !orchestrator.turnPanicked(command.AssistantMessageID) {
		t.Fatal("the panicked turn was not noted")
	}
	if orchestrator.isQueued(command.AssistantMessageID) {
		t.Fatal("the panicked command still holds its queued mark")
	}
	// Dispatched again (a reclaim or a restart copy), it is only ended.
	orchestrator.dispatch(context.Background(), command)
	func() {
		defer orchestrator.recoverGoroutine("test")
		panic("background bug")
	}()
}

// endedTurnRepository answers GetTurn with an ended turn; every other call
// panics (stubRepository), so a dispatch that ends the turn again shows.
type endedTurnRepository struct {
	stubRepository
	state string
}

func (repository endedTurnRepository) GetTurn(_ context.Context, ownerID, sessionID, assistantID string) (Turn, error) {
	return Turn{OwnerID: ownerID, SessionID: sessionID, AssistantMessageID: assistantID, State: repository.state}, nil
}

// #665: a command that reaches a panicked turn after its ending was written
// (a leftover consent timer, a duplicate stop) does not end it again: no
// reply rewrite, no error or done event on a stream that may be the next
// turn's. The panicked mark goes with it.
func TestDispatchLeavesAnEndedPanickedTurnAlone(t *testing.T) {
	orchestrator := robustnessOrchestrator(t)
	orchestrator.service.repository = endedTurnRepository{state: "internal-turn-error"}
	command := ProcessTurn{OwnerID: "owner", SessionID: "session", AssistantMessageID: "message-ended"}
	orchestrator.notePanicked(command.AssistantMessageID)
	if !orchestrator.markQueued(command.AssistantMessageID) {
		t.Fatal("markQueued")
	}
	orchestrator.dispatch(context.Background(), command)
	if orchestrator.turnPanicked(command.AssistantMessageID) {
		t.Fatal("the ended turn was ended again (its writes panicked) or kept its mark")
	}
	if orchestrator.isQueued(command.AssistantMessageID) {
		t.Fatal("the command still holds its queued mark")
	}
}
