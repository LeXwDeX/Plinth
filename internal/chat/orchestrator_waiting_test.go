package chat_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type waitingAgent struct{ control, generate string }

func (a waitingAgent) Stream(_ context.Context, request agents.Request, _ func(agents.Delta) error) (agents.Completion, error) {
	for _, message := range request.Messages {
		if message.Role == "configured-tool" {
			return agents.Completion{Content: "finished"}, nil
		}
	}
	return agents.Completion{ToolCalls: []agents.ToolCall{
		{ID: "control", Name: a.control, Arguments: []byte(`{"params":{}}`)},
		{ID: "paid", Name: a.generate, Arguments: []byte(`{"prompt":"paint"}`)},
	}}, nil
}
func (waitingAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: "title"}, nil
}

type waitingExecutor struct {
	mu                   sync.Mutex
	states               map[string]generation.TaskState
	owners               map[string]string
	sessions             map[string]string
	controlCount         map[string]int
	submitCount          map[string]int
	accepted             chan string
	observationFailsOnce bool
	omitBoundDeadline    bool
}

func newWaitingExecutor() *waitingExecutor {
	return &waitingExecutor{states: map[string]generation.TaskState{}, owners: map[string]string{},
		sessions: map[string]string{}, controlCount: map[string]int{}, submitCount: map[string]int{}, accepted: make(chan string, 32)}
}
func (e *waitingExecutor) Execute(_ context.Context, command chat.ToolCommand, observe func(chat.ToolObservation) error) (chat.ToolResult, error) {
	e.mu.Lock()
	if command.Call.Name == "set_params" {
		e.controlCount[command.SessionID]++
		e.mu.Unlock()
		return chat.ToolResult{AgentContent: "params saved"}, nil
	}
	taskID := "task-" + command.SessionID
	if command.BoundTaskID == "" {
		e.submitCount[command.SessionID]++
		e.states[taskID] = generation.TaskRunning
		e.owners[taskID], e.sessions[taskID] = command.OwnerID, command.SessionID
		e.mu.Unlock()
		if err := observe(chat.ToolObservation{Registered: true, TaskID: taskID}); err != nil {
			return chat.ToolResult{}, err
		}
		e.accepted <- taskID
		if e.observationFailsOnce {
			e.mu.Lock()
			e.observationFailsOnce = false
			e.mu.Unlock()
			return chat.ToolResult{TaskID: taskID, AwaitTask: true}, errors.New("observation temporarily unavailable")
		}
		return chat.ToolResult{TaskID: taskID, AwaitTask: true, WaitUntil: time.Now().Add(time.Hour)}, nil
	}
	state := e.states[command.BoundTaskID]
	e.mu.Unlock()
	if state == generation.TaskRunning || state == generation.TaskMaterializing {
		if e.omitBoundDeadline {
			return chat.ToolResult{TaskID: command.BoundTaskID, AwaitTask: true}, errors.New("observation temporarily unavailable")
		}
		return chat.ToolResult{TaskID: command.BoundTaskID, AwaitTask: true, WaitUntil: command.TaskWaitDeadline}, nil
	}
	return chat.ToolResult{TaskID: command.BoundTaskID, AgentContent: string(state)}, nil
}

func TestWaitingScannerHonorsDeadlineWhenTaskCannotBeRead(t *testing.T) {
	for _, mode := range []string{"transient before deadline", "deleted after deadline", "unavailable after deadline", "foreign task after deadline", "legacy missing deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			service := newChatService(t, newMemoryChatRepository())
			e := newWaitingExecutor()
			session, err := service.CreateSession(ctx, "owner-a", "Owner", mode)
			if err != nil {
				t.Fatal(err)
			}
			assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
			if err != nil {
				t.Fatal(err)
			}
			command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
			if err := waitingOrchestrator(t, service, e).Process(ctx, command); err != nil {
				t.Fatal(err)
			}
			turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(-time.Hour)
			if mode == "transient before deadline" {
				deadline = time.Now().Add(time.Hour)
			} else if mode == "legacy missing deadline" {
				deadline = time.Time{}
			}
			turn.Checkpoint.WaitDeadline = deadline
			if _, err := service.SaveTurnCheckpoint(ctx, turn, turn.State, turn.Checkpoint, false); err != nil {
				t.Fatal(err)
			}
			scanner, err := chat.NewOrchestrator(chat.OrchestratorConfig{
				Service: service, Schema: configuredChatSchema(t), Model: "model", Agent: waitingAgent{}, Tools: e,
				TaskGet: func(context.Context, string, string) (generation.Task, error) {
					if mode == "foreign task after deadline" {
						return generation.Task{ID: generation.TaskID("task-" + session.ID), OwnerID: "owner-b", SessionID: session.ID}, nil
					}
					code := generation.ErrUnavailable
					if mode == "deleted after deadline" {
						code = generation.ErrNotFound
					}
					return generation.Task{}, &generation.Error{Code: code}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := scanner.ScanWaitingTasks(ctx); err != nil {
				t.Fatal(err)
			}
			turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
			want := service.TurnQueuedState()
			if mode == "transient before deadline" || mode == "foreign task after deadline" {
				want = service.TurnWaitingTaskState()
			}
			if mode == "legacy missing deadline" {
				if turn.Checkpoint.WaitDeadline.IsZero() {
					t.Fatal("legacy wait deadline was not repaired")
				}
			} else if !turn.Checkpoint.WaitDeadline.Equal(deadline) {
				t.Fatal("scanner changed durable deadline")
			}
			if err != nil || turn.State != want {
				t.Fatalf("deadline recovery: %+v err=%v want=%s", turn, err, want)
			}
			if _, submissions := e.counts(session.ID); submissions != 1 {
				t.Fatalf("scanner submitted another task: %d", submissions)
			}
		})
	}
}

func TestObservationErrorCannotErasePersistedWaitDeadline(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	e := newWaitingExecutor()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "deadline survives")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	if err := waitingOrchestrator(t, service, e).Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := turn.Checkpoint.WaitDeadline
	e.omitBoundDeadline = true
	if err := waitingOrchestrator(t, service, e).Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || deadline.IsZero() || !turn.Checkpoint.WaitDeadline.Equal(deadline) {
		t.Fatalf("observation reset deadline: %+v err=%v original=%v", turn, err, deadline)
	}
	if _, submissions := e.counts(session.ID); submissions != 1 {
		t.Fatalf("observation retried submission: %d", submissions)
	}
}
func (e *waitingExecutor) get(_ context.Context, ownerID, taskID string) (generation.Task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.owners[taskID] != ownerID {
		return generation.Task{}, fmt.Errorf("wrong task owner")
	}
	return generation.Task{ID: generation.TaskID(taskID), OwnerID: generation.OwnerID(ownerID),
		SessionID: e.sessions[taskID], State: e.states[taskID]}, nil
}
func (e *waitingExecutor) set(taskID string, state generation.TaskState) {
	e.mu.Lock()
	e.states[taskID] = state
	e.mu.Unlock()
}
func (e *waitingExecutor) counts(sessionID string) (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.controlCount[sessionID], e.submitCount[sessionID]
}

func waitingOrchestrator(t *testing.T, service *chat.Service, e *waitingExecutor) *chat.Orchestrator {
	t.Helper()
	schema := configuredChatSchema(t)
	schema.Agent.WorkerCount, schema.Agent.QueueSize = 4, 16
	var generate string
	for _, tool := range schema.Tools {
		if tool.Name == "refine_image" {
			generate = tool.Name
			break
		}
	}
	if generate == "" {
		t.Fatal("generation tool missing")
	}
	o, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema, Model: "model",
		Agent: waitingAgent{control: "set_params", generate: generate}, Tools: e, TaskGet: e.get,
		TaskLookup: func(_ context.Context, ownerID, key string) (generation.Task, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			for id, session := range e.sessions {
				if e.owners[id] == ownerID && key != "" && session != "" {
					return generation.Task{ID: generation.TaskID(id), OwnerID: generation.OwnerID(ownerID), SessionID: session}, nil
				}
			}
			return generation.Task{}, &generation.Error{Code: generation.ErrNotFound}
		},
		TaskCanceler: func(_ context.Context, ownerID, taskID string) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.owners[taskID] != ownerID {
				return fmt.Errorf("wrong cancel owner")
			}
			e.states[taskID] = generation.TaskCanceled
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestKnownTaskObservationErrorWaitsAndScannerRecoversOriginal(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	executor.observationFailsOnce = true
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "observation retry")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	first := waitingOrchestrator(t, service, executor)
	if err := first.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnWaitingTaskState() ||
		turn.Checkpoint.WaitingTaskID != "task-"+session.ID || len(turn.Checkpoint.Completed) != 1 {
		t.Fatalf("known Task was completed after observation error: %+v %v", turn, err)
	}
	if _, submissions := executor.counts(session.ID); submissions != 1 {
		t.Fatalf("submissions=%d", submissions)
	}
	restarted := waitingOrchestrator(t, service, executor)
	executor.set("task-"+session.ID, generation.TaskSucceeded)
	if err := restarted.ScanWaitingTasks(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnQueuedState() {
		t.Fatalf("scanner did not queue original Task: %+v %v", turn, err)
	}
	if err := restarted.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if _, submissions := executor.counts(session.ID); submissions != 1 {
		t.Fatalf("recovery submitted a second paid Task: %d", submissions)
	}
}

func TestWaitingTasksReleaseWorkersAndResumeOnce(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	o := waitingOrchestrator(t, service, executor)
	var commands []chat.ProcessTurn
	for i := 0; i < 5; i++ {
		session, err := service.CreateSession(ctx, "owner-a", "Owner", fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
		if err != nil {
			t.Fatal(err)
		}
		commands = append(commands, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID})
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- o.Run(runCtx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool {
		for _, command := range commands {
			if _, count := executor.counts(command.SessionID); count != 1 {
				return false
			}
			// The executor returns before the orchestrator stores the waiting
			// checkpoint; wait for the stored state, not just the call.
			turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
			if err != nil || turn.State != service.TurnWaitingTaskState() {
				return false
			}
		}
		return true
	})
	for _, command := range commands {
		turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
		if err != nil || turn.State != service.TurnWaitingTaskState() || len(turn.Checkpoint.Completed) != 1 {
			t.Fatalf("waiting checkpoint = %#v, err=%v", turn, err)
		}
		if err := o.ScanWaitingTasks(ctx); err != nil {
			t.Fatal(err)
		}
		if control, submitted := executor.counts(command.SessionID); control != 1 || submitted != 1 {
			t.Fatalf("duplicate scan replayed tools: control=%d submit=%d", control, submitted)
		}
	}
	for i, command := range commands {
		state := generation.TaskSucceeded
		if i == 1 {
			state = generation.TaskFailed
		}
		if i == 2 {
			state = generation.TaskCanceled
		}
		executor.set("task-"+command.SessionID, state)
	}
	waitFor(t, func() bool {
		for _, command := range commands {
			turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
			if err != nil || turn.State == service.TurnWaitingTaskState() || turn.State == service.TurnRunningState() || turn.State == service.TurnQueuedState() {
				return false
			}
		}
		return true
	})
	for _, command := range commands {
		if control, submitted := executor.counts(command.SessionID); control != 1 || submitted != 1 {
			t.Fatalf("replay counts control=%d submit=%d", control, submitted)
		}
	}
}

func TestWaitingTaskCheckpointSurvivesRestartAndMaterializingIsNotTerminal(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "restart")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	first := waitingOrchestrator(t, service, executor)
	if err := first.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnWaitingTaskState() || turn.Checkpoint.Round != 0 || len(turn.Checkpoint.Completed) != 1 {
		t.Fatalf("initial checkpoint = %#v, err=%v", turn, err)
	}
	second := waitingOrchestrator(t, service, executor)
	executor.set("task-"+session.ID, generation.TaskMaterializing)
	if err := second.ScanWaitingTasks(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnWaitingTaskState() {
		t.Fatalf("materializing woke turn: %#v, %v", turn, err)
	}
	executor.set("task-"+session.ID, generation.TaskSucceeded)
	if err := second.ScanWaitingTasks(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnQueuedState() {
		t.Fatalf("terminal did not wake turn: %#v, %v", turn, err)
	}
	if err := second.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	if err := second.ScanWaitingTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if control, submitted := executor.counts(session.ID); control != 1 || submitted != 1 {
		t.Fatalf("restart replayed a completed control tool or paid call: control=%d submit=%d", control, submitted)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-done" {
		t.Fatalf("restart final turn = %#v, %v", turn, err)
	}
}

func TestWaitingTaskStopAndWrongOwnerFact(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newWaitingExecutor()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "stop")
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
	taskID := "task-" + session.ID
	executor.mu.Lock()
	executor.owners[taskID] = "owner-b"
	executor.mu.Unlock()
	executor.set(taskID, generation.TaskSucceeded)
	if err := o.ScanWaitingTasks(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnWaitingTaskState() {
		t.Fatalf("foreign task woke turn: %#v, %v", turn, err)
	}
	executor.mu.Lock()
	executor.owners[taskID] = "owner-a"
	executor.mu.Unlock()
	if active, err := o.CancelTurn(ctx, "owner-a", session.ID); err != nil || !active {
		t.Fatalf("stop = %v, %v", active, err)
	}
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-done" {
		t.Fatalf("stopped turn = %#v, %v", turn, err)
	}
	if control, submitted := executor.counts(session.ID); control != 1 || submitted != 1 {
		t.Fatalf("stop replayed tools: control=%d submit=%d", control, submitted)
	}
}
