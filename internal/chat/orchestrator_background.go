package chat

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ErrTurnBackgroundUnavailable means the session has no waiting turn that may
// stop waiting while its Tasks keep running (#137).
var ErrTurnBackgroundUnavailable = errors.New("chat orchestrator: turn cannot continue in the background")

// TurnBackgroundAvailable reports whether the session's waiting turn may stop
// waiting while its Tasks keep running (#137). It changes nothing.
func (orchestrator *Orchestrator) TurnBackgroundAvailable(ctx context.Context, ownerID, sessionID string) (bool, error) {
	turn, waiting, err := orchestrator.service.WaitingTurn(ctx, ownerID, sessionID)
	if err != nil || !waiting {
		return false, err
	}
	return orchestrator.backgroundEligible(ctx, turn)
}

// ContinueTurnInBackground ends the session's waiting turn without cancelling
// its Tasks (#137). The choice is written onto the turn's checkpoint by the
// same compare-and-swap that wakes the turn, which fails once a stop was
// requested: a stop that came first still cancels, and a stop that comes
// later finds the choice and cancels nothing. Process then ends the turn
// (finalizeBackgroundTurn), also after a restart.
func (orchestrator *Orchestrator) ContinueTurnInBackground(ctx context.Context, ownerID, sessionID string) error {
	turn, waiting, err := orchestrator.service.WaitingTurn(ctx, ownerID, sessionID)
	if err != nil {
		return err
	}
	if !waiting {
		return ErrTurnBackgroundUnavailable
	}
	eligible, err := orchestrator.backgroundEligible(ctx, turn)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrTurnBackgroundUnavailable
	}
	checkpoint := turn.Checkpoint
	checkpoint.Background = true
	if _, err := orchestrator.service.SaveTurnCheckpoint(ctx, turn, orchestrator.service.TurnQueuedState(),
		checkpoint, false); err != nil {
		if errors.Is(err, ErrTurnCheckpointConflict) {
			// The Tasks finished and the turn woke, or a stop came first.
			return ErrTurnBackgroundUnavailable
		}
		return err
	}
	// A full queue leaves the queued turn to the waiting scanner.
	if err := orchestrator.Enqueue(ProcessTurn{OwnerID: ownerID, SessionID: sessionID,
		AssistantMessageID: turn.AssistantMessageID, Options: turn.Options}); err != nil {
		orchestrator.logger.Warn("后台继续的轮次入队暂失败，等待扫描重试",
			"assistant_message_id", turn.AssistantMessageID, "error", err)
	}
	return nil
}

// backgroundEligible reports whether a turn qualifies: it waits on Tasks, no
// stop was requested, every call it waits for is a single generation step the
// Schema allows (never a SkillRun or a Processing Plan) with its Task already
// submitted, and at least one of those Tasks still runs and either sits in the
// Provider's own queue or has run past the Schema threshold.
func (orchestrator *Orchestrator) backgroundEligible(ctx context.Context, turn Turn) (bool, error) {
	policy := orchestrator.schema.Agent.BackgroundWait
	if policy == nil || orchestrator.taskGet == nil || turn.State != orchestrator.service.TurnWaitingTaskState() ||
		turn.StopRequested || turn.Checkpoint.Background {
		return false, nil
	}
	entries := turn.Checkpoint.WaitingCalls()
	if len(entries) == 0 {
		return false, nil
	}
	names := map[string]string{}
	for _, call := range turn.Checkpoint.Completion.ToolCalls {
		names[call.ID] = call.Name
	}
	for _, call := range turn.Checkpoint.Assistant.ToolCalls {
		names[call.ID] = call.Name
	}
	threshold := time.Duration(policy.AfterSeconds) * time.Second
	now := time.Now()
	qualifies := false
	for _, entry := range entries {
		// A submission queued for an admission retry has no Task that could
		// keep running: the turn must stay to submit it.
		if entry.TaskID == "" || !policy.Allows(names[entry.CallID]) {
			return false, nil
		}
		task, err := orchestrator.taskGet(ctx, turn.OwnerID, entry.TaskID)
		if generation.IsCode(err, generation.ErrNotFound) {
			// The waiting scanner ends a wait on a deleted Task at its deadline.
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if task.ID != generation.TaskID(entry.TaskID) || task.OwnerID != generation.OwnerID(turn.OwnerID) ||
			task.SessionID != turn.SessionID {
			return false, nil
		}
		if task.State.Terminal() {
			continue
		}
		if task.UpstreamQueue != nil || !task.CreatedAt.IsZero() && now.Sub(task.CreatedAt) >= threshold {
			qualifies = true
		}
	}
	return qualifies, nil
}

// finalizeBackgroundTurn ends a turn whose person stopped waiting while its
// Tasks keep running (#137). Nothing is cancelled: each waited call keeps its
// Task binding, is marked Background for later turns, and its consent claim
// is settled as submitted (the same settlement a bound Task gets on stop), so
// the step is neither refused nor released and the production-plan
// reservation stays until the kernel settles the Task. The turn closes as done
// with the Schema note; the results reach the task tray and the task center,
// never this conversation.
func (orchestrator *Orchestrator) finalizeBackgroundTurn(ctx context.Context, command ProcessTurn, turn Turn,
	assistant *Message) error {
	ctx = catalog.WithLocale(context.WithoutCancel(ctx), command.Options.Locale)
	messages, err := orchestrator.service.ListMessages(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return err
	}
	for _, stored := range messages {
		if stored.ID != command.AssistantMessageID {
			continue
		}
		for index := range assistant.ToolCalls {
			for _, call := range stored.ToolCalls {
				if assistant.ToolCalls[index].ID == call.ID {
					mergeStoredToolCall(&assistant.ToolCalls[index], call)
				}
			}
		}
		break
	}
	for _, entry := range turn.Checkpoint.WaitingCalls() {
		if entry.TaskID == "" {
			continue
		}
		found := false
		for index := range assistant.ToolCalls {
			call := &assistant.ToolCalls[index]
			if call.ID != entry.CallID {
				continue
			}
			found = true
			if call.TaskID == "" {
				call.TaskID = entry.TaskID
			}
			if call.TaskID != entry.TaskID {
				return ErrTurnCheckpointConflict
			}
			call.Background = true
			if orchestrator.stopConsent != nil {
				if err := orchestrator.stopConsent(command.OwnerID, command.SessionID, call.ID,
					call.IdempotencyKey, call.TaskID); err != nil {
					return err
				}
			}
		}
		if !found {
			orchestrator.logger.Warn("后台继续的等待调用不在回复里",
				"assistant_message_id", command.AssistantMessageID, "call_id", entry.CallID)
		}
	}
	if policy := orchestrator.schema.Agent.BackgroundWait; policy != nil {
		note := orchestrator.text(ctx, policy.TurnContent)
		switch {
		case assistant.Content == "" || assistant.Content == orchestrator.schema.State.PendingContent:
			assistant.Content = note
		case !strings.HasSuffix(assistant.Content, note):
			assistant.Content += "\n" + note
		}
	}
	// Pictures that calls of this reply already returned stay with it.
	for _, url := range turn.Checkpoint.ImageURLs {
		known := false
		for _, existing := range assistant.ImageURLs {
			known = known || existing == url
		}
		if !known {
			assistant.ImageURLs = append(assistant.ImageURLs, url)
		}
	}
	orchestrator.attachAssistantResults(ctx, command.OwnerID, assistant)
	if err := orchestrator.service.UpdateMessage(ctx, command.OwnerID, *assistant); err != nil {
		return err
	}
	return orchestrator.appendEvent(ctx, command.SessionID, map[string]any{
		orchestrator.schema.Stream.EventFields.Type: orchestrator.schema.Stream.EventTypes.Done,
	})
}

// backgroundTasksPrompt lists, for the Agent, the Tasks of this session that
// the person stopped waiting for and that still run (#137), so a later turn
// looks them up instead of submitting the same paid generation again. Only the
// newest background calls are read (Schema max_listed_tasks), which bounds the
// Task reads of every turn; an unreadable Task is left out.
func (orchestrator *Orchestrator) backgroundTasksPrompt(ctx context.Context, ownerID, sessionID string,
	history []Message) string {
	policy := orchestrator.schema.Agent.BackgroundWait
	if policy == nil || orchestrator.taskGet == nil || ownerID == "" || sessionID == "" {
		return ""
	}
	var lines []string
	read := map[string]bool{}
	for index := len(history) - 1; index >= 0 && len(read) < policy.MaxListedTasks; index-- {
		calls := history[index].ToolCalls
		for callIndex := len(calls) - 1; callIndex >= 0 && len(read) < policy.MaxListedTasks; callIndex-- {
			call := calls[callIndex]
			if !call.Background || call.TaskID == "" || read[call.TaskID] {
				continue
			}
			read[call.TaskID] = true
			task, err := orchestrator.taskGet(ctx, ownerID, call.TaskID)
			if err != nil || task.ID != generation.TaskID(call.TaskID) ||
				task.OwnerID != generation.OwnerID(ownerID) || task.SessionID != sessionID || task.State.Terminal() {
				continue
			}
			status := string(task.State)
			if task.UpstreamQueue != nil {
				status += orchestrator.text(ctx, policy.UpstreamQueuedStatus)
			}
			model := task.Offering.ModelName
			if model == "" {
				model = task.Offering.ModelID
			}
			lines = append(lines, strings.NewReplacer(
				"{task_id}", string(task.ID),
				"{capability}", string(task.Capability),
				"{model}", model,
				"{status}", status,
			).Replace(orchestrator.text(ctx, policy.TaskLineTemplate)))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\n" + strings.ReplaceAll(orchestrator.text(ctx, policy.ContextTemplate), "{tasks}", strings.Join(lines, "\n"))
}
