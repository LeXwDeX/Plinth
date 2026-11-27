package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// endTurn marks a turn finished the way the orchestrator does after the
// checkpoint receipt ended it.
func endTurn(t *testing.T, r *Repository, id string) {
	t.Helper()
	if _, err := r.db.Exec(`UPDATE go_chat_turns SET state='DONE' WHERE assistant_message_id=?`, id); err != nil {
		t.Fatal(err)
	}
}

// turnEndedReader reads a turn's state from the fixture's database, as the app
// wires SetThreeViewTurnEnded.
func turnEndedReader(r *Repository) func(context.Context, string, string, string) (bool, error) {
	return func(ctx context.Context, _, session, turn string) (bool, error) {
		var state string
		if err := r.db.QueryRowContext(ctx, `SELECT state FROM go_chat_turns WHERE assistant_message_id=? AND session_id=?`,
			turn, session).Scan(&state); err != nil {
			return false, chat.ErrMessageNotFound
		}
		return state != "RUNNING", nil
	}
}

// #689, #693: a group the continuation stopped at the turn's generation
// checkpoint is not started again when the person answers "continue". Before
// the fix the next turn's create_three_view on the same source and recipe
// started a second group and drew and charged its front view again, while the
// receipt and the reply's note promised that replying "continue" goes on. Now
// the checkpoint receipt and note send the person to the card, and the next
// turn's call is refused without creating, submitting or counting anything.
// A later ask (not right after the pause) starts a new group as before.
func TestThreeViewCheckpointIsNotRedrawnOnTheNextTurn(t *testing.T) {
	f := newContinuationFixtureWithMode(t, false, "auto")
	f.toolConfig = func(config *agenttools.Config) {
		config.MaxGenerationCallsPerTurn = 20
		config.GenerationCheckpoint = map[string]int{"auto": 2}
		config.ConsentLimitContent = "checkpoint reached"
	}
	f.toolSchema = func(schema *catalog.ChatThreeViewSchema) {
		schema.Messages.DuplicateRun = "duplicate {run}"
		schema.Messages.PausedRun = "paused-run {run}"
		schema.Messages.CheckpointPaused = "checkpoint-paused {run} {view}"
		schema.Messages.CheckpointNote = "on the card {count}"
	}
	executor, command := f.toolFor(t, `{"subject_type":"humanoid"}`)
	executor.SetThreeViewTurnEnded(turnEndedReader(f.repo))
	command.ApprovalMode = "auto"
	ctx := context.Background()
	observe := func(chat.ToolObservation) error { return nil }
	result, err := executor.Execute(ctx, command, observe)
	if err != nil || !result.AwaitTask {
		t.Fatalf("front: %+v err=%v", result, err)
	}
	command.BoundTaskID = result.TaskID
	f.finish(t, command.BoundTaskID, "asset-resume-0")
	result, err = executor.Execute(ctx, command, observe)
	if err != nil || !result.AwaitTask {
		t.Fatalf("left: %+v err=%v", result, err)
	}
	command.BoundTaskID = result.TaskID
	f.finish(t, command.BoundTaskID, "asset-resume-1")
	result, err = executor.Execute(ctx, command, observe)
	if err != nil || result.AwaitTask || result.GenerationCheckpoint != 2 ||
		!strings.HasPrefix(result.AgentContent, "checkpoint-paused skill_") ||
		!strings.HasSuffix(result.AgentContent, " Back") || result.GenerationCheckpointNote != "on the card {count}" {
		t.Fatalf("checkpoint: %+v err=%v", result, err)
	}
	endTurn(t, f.repo, "turn-a")

	// The person answers "continue": a new turn calls create_three_view again.
	insertGrantTurn(t, f.repo, "turn-b", "owner-a", "session-a")
	next := command
	next.AssistantMessageID = "turn-b"
	next.Call = agents.ToolCall{ID: "auto-call-2", Name: "create_three_view", Arguments: []byte(`{"subject_type":"humanoid","reference_image_indices":[1]}`)}
	next.BoundTaskID = ""
	next.OwnGenerationUnits = 0
	next.History = append(append([]chat.Message(nil), command.History...),
		chat.Message{ID: "turn-a", Role: "assistant"})
	runs, err := f.runtime.List(ctx, "owner-a", "session-a", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	before, submits := len(runs), f.gateway.submits
	result, err = executor.Execute(ctx, next, observe)
	if err != nil || !result.NoSubmission || result.GenerationUnits != 0 || result.AwaitTask ||
		!strings.HasPrefix(result.AgentContent, "paused-run skill_") {
		t.Fatalf("continue call: %+v err=%v", result, err)
	}
	runs, err = f.runtime.List(ctx, "owner-a", "session-a", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != before || f.gateway.submits != submits {
		t.Fatalf("the continue call started a new group: runs %d -> %d, submits %d -> %d",
			before, len(runs), submits, f.gateway.submits)
	}

	// An ask a turn later is a new request: it starts a new group as before.
	insertGrantTurn(t, f.repo, "turn-c", "owner-a", "session-a")
	endTurn(t, f.repo, "turn-b")
	later := next
	later.AssistantMessageID = "turn-c"
	later.Call.ID = "auto-call-3"
	later.History = append(append([]chat.Message(nil), next.History...),
		chat.Message{ID: "turn-b", Role: "assistant"})
	result, err = executor.Execute(ctx, later, observe)
	if err != nil || !result.AwaitTask || f.gateway.submits != submits+1 {
		t.Fatalf("later ask: %+v err=%v submits %d -> %d", result, err, submits, f.gateway.submits)
	}
}

func stepIndexForTest(run skillrun.Run, key string) int {
	for i, step := range run.Steps {
		if step.Spec.Key == key {
			return i
		}
	}
	return -1
}
