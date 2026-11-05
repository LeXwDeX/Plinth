package chat_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// backgroundFacts adds to the waiting executor's Tasks what #137 reads: when a
// Task was submitted, whether the Provider queues it, and what stop and the
// consent settlement were asked to do.
type backgroundFacts struct {
	mu       sync.Mutex
	created  map[string]time.Time
	queued   map[string]bool
	canceled []string
	settled  []string
}

func newBackgroundFacts() *backgroundFacts {
	return &backgroundFacts{created: map[string]time.Time{}, queued: map[string]bool{}}
}

func (f *backgroundFacts) cancellations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.canceled...)
}

func (f *backgroundFacts) settlements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.settled...)
}

// backgroundContextAgent answers without tools and keeps every system context it got.
type backgroundContextAgent struct {
	mu      sync.Mutex
	systems []string
}

func (a *backgroundContextAgent) Stream(_ context.Context, request agents.Request, _ func(agents.Delta) error) (agents.Completion, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.systems = append(a.systems, request.Messages[0].Content)
	return agents.Completion{Content: "noted"}, nil
}

func (a *backgroundContextAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: "title"}, nil
}

func (a *backgroundContextAgent) lastSystem() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.systems) == 0 {
		return ""
	}
	return a.systems[len(a.systems)-1]
}

func backgroundSchema(t *testing.T) catalog.ChatSchema {
	t.Helper()
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount, schema.Agent.QueueSize = 4, 16
	if schema.Agent.BackgroundWait == nil || !schema.Agent.BackgroundWait.Allows("refine_image") {
		t.Fatal("the shipped Schema lets refine_image continue in the background")
	}
	return schema
}

func backgroundOrchestrator(t *testing.T, service *chat.Service, schema catalog.ChatSchema, e *waitingExecutor,
	facts *backgroundFacts, agent agents.Adapter) *chat.Orchestrator {
	t.Helper()
	if agent == nil {
		agent = waitingAgent{control: "set_params", generate: "refine_image"}
	}
	o, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema, Model: "model",
		Agent: agent, Tools: e,
		TaskGet: func(ctx context.Context, ownerID, taskID string) (generation.Task, error) {
			task, err := e.get(ctx, ownerID, taskID)
			if err != nil {
				return task, err
			}
			facts.mu.Lock()
			defer facts.mu.Unlock()
			task.CreatedAt = facts.created[taskID]
			if task.CreatedAt.IsZero() {
				task.CreatedAt = time.Now()
			}
			if facts.queued[taskID] {
				task.UpstreamQueue = &generation.UpstreamQueue{}
			}
			task.Capability = "text-to-image"
			task.Offering.ModelID = "configured-image-model"
			return task, nil
		},
		TaskCanceler: func(_ context.Context, ownerID, taskID string) error {
			facts.mu.Lock()
			facts.canceled = append(facts.canceled, taskID)
			facts.mu.Unlock()
			e.set(taskID, generation.TaskCanceled)
			return nil
		},
		StopConsent: func(_, _, callID, _, taskID string) error {
			facts.mu.Lock()
			defer facts.mu.Unlock()
			facts.settled = append(facts.settled, callID+"|"+taskID)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// waitingTurn starts a turn whose paid refine_image call waits on its Task.
func waitingTurn(t *testing.T, service *chat.Service, o *chat.Orchestrator, title string) (chat.ProcessTurn, string) {
	t.Helper()
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", title)
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnWaitingTaskState() {
		t.Fatalf("turn did not wait on its Task: %+v %v", turn, err)
	}
	return command, "task-" + session.ID
}

func assistantOf(t *testing.T, service *chat.Service, command chat.ProcessTurn) chat.Message {
	t.Helper()
	messages, err := service.ListMessages(context.Background(), command.OwnerID, command.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID == command.AssistantMessageID {
			return message
		}
	}
	t.Fatal("assistant message missing")
	return chat.Message{}
}

// #137: a single generation step queued at the Provider may stop being waited
// for. The turn ends as done with the Schema note, the Task keeps running (no
// cancel), its consent claim is settled as submitted, and the next turn tells
// the Agent the Task still runs so it looks the Task up instead of paying for
// the same generation again.
func TestContinueInBackgroundKeepsTheTaskRunningAndTellsTheNextTurn(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	facts := newBackgroundFacts()
	schema := backgroundSchema(t)
	o := backgroundOrchestrator(t, service, schema, executor, facts, nil)
	command, taskID := waitingTurn(t, service, o, "background")

	if available, err := o.TurnBackgroundAvailable(ctx, "owner-a", command.SessionID); err != nil || available {
		t.Fatalf("a fresh unqueued Task must not qualify yet: %v %v", available, err)
	}
	facts.mu.Lock()
	facts.queued[taskID] = true
	facts.mu.Unlock()
	if available, err := o.TurnBackgroundAvailable(ctx, "owner-b", command.SessionID); err != nil || available {
		t.Fatalf("another owner must read false: %v %v", available, err)
	}
	if available, err := o.TurnBackgroundAvailable(ctx, "owner-a", command.SessionID); err != nil || !available {
		t.Fatalf("an upstream-queued Task qualifies: %v %v", available, err)
	}
	if err := o.ContinueTurnInBackground(ctx, "owner-a", command.SessionID); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnQueuedState() || !turn.Checkpoint.Background {
		t.Fatalf("background choice not recorded: %+v %v", turn, err)
	}
	if err := o.ContinueTurnInBackground(ctx, "owner-a", command.SessionID); !errors.Is(err, chat.ErrTurnBackgroundUnavailable) {
		t.Fatalf("a second request must be refused: %v", err)
	}
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-done" {
		t.Fatalf("turn did not end as done: %+v %v", turn, err)
	}
	if canceled := facts.cancellations(); len(canceled) != 0 {
		t.Fatalf("background continue cancelled %v", canceled)
	}
	executor.mu.Lock()
	state := executor.states[taskID]
	executor.mu.Unlock()
	if state != generation.TaskRunning {
		t.Fatalf("task state = %s", state)
	}
	if settled := facts.settlements(); len(settled) != 1 || !strings.HasSuffix(settled[0], "|"+taskID) {
		t.Fatalf("consent must be settled as submitted with the Task: %v", settled)
	}
	if _, submissions := executor.counts(command.SessionID); submissions != 1 {
		t.Fatalf("submissions = %d", submissions)
	}
	assistant := assistantOf(t, service, command)
	if !strings.Contains(assistant.Content, schema.Agent.BackgroundWait.TurnContent) {
		t.Fatalf("content = %q", assistant.Content)
	}
	var paid chat.ToolCall
	for _, call := range assistant.ToolCalls {
		if call.TaskID == taskID {
			paid = call
		}
	}
	if !paid.Background {
		t.Fatalf("waited call not marked: %+v", assistant.ToolCalls)
	}
	events, err := service.ListEvents(ctx, command.OwnerID, command.SessionID, 0)
	if err != nil || len(events) == 0 || events[len(events)-1].Data["configured-type"] != "configured-done" {
		t.Fatalf("the stream did not end: %+v %v", events, err)
	}

	// A stop after the turn ended finds nothing to stop.
	if active, err := o.CancelTurn(ctx, "owner-a", command.SessionID); err != nil || active {
		t.Fatalf("stop after the end = %v %v", active, err)
	}

	// The next turn lists the running Task for the Agent.
	agent := &backgroundContextAgent{}
	next := backgroundOrchestrator(t, service, schema, executor, facts, agent)
	second, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: command.SessionID, Content: "is it done?"})
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: command.SessionID, AssistantMessageID: second.ID}); err != nil {
		t.Fatal(err)
	}
	system := agent.lastSystem()
	line := strings.NewReplacer("{task_id}", taskID, "{capability}", "text-to-image", "{model}", "configured-image-model",
		"{status}", string(generation.TaskRunning)+schema.Agent.BackgroundWait.UpstreamQueuedStatus).
		Replace(schema.Agent.BackgroundWait.TaskLineTemplate)
	if !strings.Contains(system, line) || !strings.Contains(system, "get_task") {
		t.Fatalf("the Agent was not told about the running Task:\n%s", system)
	}

	// Once the Task ended, the context no longer lists it.
	executor.set(taskID, generation.TaskSucceeded)
	third, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: command.SessionID, Content: "thanks"})
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: command.SessionID, AssistantMessageID: third.ID}); err != nil {
		t.Fatal(err)
	}
	if system := agent.lastSystem(); strings.Contains(system, taskID) {
		t.Fatalf("a finished Task is still listed:\n%s", system)
	}
}

// A Task that has run past the Schema threshold qualifies without a queue.
func TestContinueInBackgroundAfterTheThreshold(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	facts := newBackgroundFacts()
	schema := backgroundSchema(t)
	o := backgroundOrchestrator(t, service, schema, executor, facts, nil)
	command, taskID := waitingTurn(t, service, o, "threshold")
	threshold := time.Duration(schema.Agent.BackgroundWait.AfterSeconds) * time.Second
	facts.mu.Lock()
	facts.created[taskID] = time.Now().Add(-threshold + time.Minute)
	facts.mu.Unlock()
	if available, err := o.TurnBackgroundAvailable(ctx, "owner-a", command.SessionID); err != nil || available {
		t.Fatalf("before the threshold: %v %v", available, err)
	}
	facts.mu.Lock()
	facts.created[taskID] = time.Now().Add(-threshold - time.Second)
	facts.mu.Unlock()
	if err := o.ContinueTurnInBackground(ctx, "owner-a", command.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if canceled := facts.cancellations(); len(canceled) != 0 {
		t.Fatalf("cancelled %v", canceled)
	}
}

// The server refuses what does not qualify, and the turn keeps waiting with
// the existing stop behaviour: a SkillRun or a call the Schema does not name, a
// submission still queued for admission, a stop already requested, or no
// waiting turn at all.
func TestContinueInBackgroundRefusesWhatDoesNotQualify(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"no turn", "skill run", "tool not named", "admission retry", "stopped first", "no policy"} {
		t.Run(mode, func(t *testing.T) {
			service := newChatService(t, newMemoryChatRepository())
			executor := newWaitingExecutor()
			facts := newBackgroundFacts()
			schema := backgroundSchema(t)
			if mode == "tool not named" {
				policy := *schema.Agent.BackgroundWait
				policy.Tools = []string{"create_model_3d"}
				schema.Agent.BackgroundWait = &policy
			}
			if mode == "no policy" {
				schema.Agent.BackgroundWait = nil
			}
			o := backgroundOrchestrator(t, service, schema, executor, facts, nil)
			if mode == "no turn" {
				session, err := service.CreateSession(ctx, "owner-a", "Owner", mode)
				if err != nil {
					t.Fatal(err)
				}
				if err := o.ContinueTurnInBackground(ctx, "owner-a", session.ID); !errors.Is(err, chat.ErrTurnBackgroundUnavailable) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			command, taskID := waitingTurn(t, service, o, mode)
			facts.mu.Lock()
			facts.queued[taskID] = true
			facts.mu.Unlock()
			turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "skill run":
				// The waited call is the three-view SkillRun, not a single step.
				for index := range turn.Checkpoint.Assistant.ToolCalls {
					if turn.Checkpoint.Assistant.ToolCalls[index].TaskID == taskID || turn.Checkpoint.Assistant.ToolCalls[index].Name == "refine_image" {
						turn.Checkpoint.Assistant.ToolCalls[index].Name = "create_three_view"
					}
				}
				for index := range turn.Checkpoint.Completion.ToolCalls {
					if turn.Checkpoint.Completion.ToolCalls[index].Name == "refine_image" {
						turn.Checkpoint.Completion.ToolCalls[index].Name = "create_three_view"
					}
				}
			case "admission retry":
				waiting := turn.Checkpoint.WaitingCalls()
				waiting = append(waiting, chat.WaitingCall{CallID: "queued-call", Deadline: time.Now().Add(time.Hour),
					RetryAt: time.Now().Add(time.Hour), QueuedSince: time.Now()})
				turn.Checkpoint.SetWaiting(waiting)
			}
			if mode == "skill run" || mode == "admission retry" {
				if _, err := service.SaveTurnCheckpoint(ctx, turn, turn.State, turn.Checkpoint, false); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stopped first" {
				if active, err := o.CancelTurn(ctx, "owner-a", command.SessionID); err != nil || !active {
					t.Fatalf("stop = %v %v", active, err)
				}
			}
			if available, err := o.TurnBackgroundAvailable(ctx, "owner-a", command.SessionID); err != nil || available {
				t.Fatalf("available = %v %v", available, err)
			}
			if err := o.ContinueTurnInBackground(ctx, "owner-a", command.SessionID); !errors.Is(err, chat.ErrTurnBackgroundUnavailable) {
				t.Fatalf("err = %v", err)
			}
			after, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
			if err != nil || after.Checkpoint.Background || after.State != service.TurnWaitingTaskState() {
				t.Fatalf("refusal changed the turn: %+v %v", after, err)
			}
			if mode == "admission retry" || mode == "skill run" {
				return
			}
			// The existing stop still cancels the Task.
			if mode != "stopped first" {
				if active, err := o.CancelTurn(ctx, "owner-a", command.SessionID); err != nil || !active {
					t.Fatalf("stop = %v %v", active, err)
				}
			}
			if err := o.Process(ctx, command); err != nil {
				t.Fatal(err)
			}
			if canceled := facts.cancellations(); len(canceled) != 1 || canceled[0] != taskID {
				t.Fatalf("stop must cancel the Task: %v", canceled)
			}
			if assistant := assistantOf(t, service, command); strings.Contains(assistant.Content,
				backgroundSchema(t).Agent.BackgroundWait.TurnContent) {
				t.Fatalf("a stopped turn says it continues: %q", assistant.Content)
			}
		})
	}
}

// A stop that comes after the background choice ends the turn without
// cancelling: the choice committed first.
func TestStopAfterContinueInBackgroundDoesNotCancel(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	facts := newBackgroundFacts()
	o := backgroundOrchestrator(t, service, backgroundSchema(t), executor, facts, nil)
	command, taskID := waitingTurn(t, service, o, "stop later")
	facts.mu.Lock()
	facts.queued[taskID] = true
	facts.mu.Unlock()
	if err := o.ContinueTurnInBackground(ctx, "owner-a", command.SessionID); err != nil {
		t.Fatal(err)
	}
	if active, err := o.CancelTurn(ctx, "owner-a", command.SessionID); err != nil || !active {
		t.Fatalf("stop = %v %v", active, err)
	}
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-done" {
		t.Fatalf("turn = %+v %v", turn, err)
	}
	if canceled := facts.cancellations(); len(canceled) != 0 {
		t.Fatalf("a later stop cancelled %v", canceled)
	}
	if settled := facts.settlements(); len(settled) != 1 {
		t.Fatalf("settlements = %v", settled)
	}
	if !strings.Contains(assistantOf(t, service, command).Content, backgroundSchema(t).Agent.BackgroundWait.TurnContent) {
		t.Fatal("the turn must say the Task continues")
	}
}

// The waiting scanner and a restart both finish a queued background turn the
// same way: nothing is resubmitted and nothing is cancelled.
func TestBackgroundTurnFinishesThroughTheWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	facts := newBackgroundFacts()
	o := backgroundOrchestrator(t, service, backgroundSchema(t), executor, facts, nil)
	command, taskID := waitingTurn(t, service, o, "workers")
	facts.mu.Lock()
	facts.queued[taskID] = true
	facts.mu.Unlock()
	restarted := backgroundOrchestrator(t, service, backgroundSchema(t), executor, facts, nil)
	if err := o.ContinueTurnInBackground(ctx, "owner-a", command.SessionID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- restarted.Run(ctx) }()
	waitFor(t, func() bool {
		turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
		return err == nil && turn.State == "configured-turn-done"
	})
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if canceled := facts.cancellations(); len(canceled) != 0 {
		t.Fatalf("cancelled %v", canceled)
	}
	if _, submissions := executor.counts(command.SessionID); submissions != 1 {
		t.Fatalf("submissions = %d", submissions)
	}
}
