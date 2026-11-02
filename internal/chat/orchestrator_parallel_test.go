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

// parallelAgent asks for n independent paid generations in one reply, then
// answers once it has seen tool receipts. It records the requests it got.
type parallelAgent struct {
	mu       sync.Mutex
	tool     string
	n        int
	requests []agents.Request
}

func (a *parallelAgent) Stream(_ context.Context, request agents.Request, _ func(agents.Delta) error) (agents.Completion, error) {
	a.mu.Lock()
	a.requests = append(a.requests, request)
	a.mu.Unlock()
	for _, message := range request.Messages {
		if message.Role == "configured-tool" {
			return agents.Completion{Content: "finished"}, nil
		}
	}
	var calls []agents.ToolCall
	for i := 0; i < a.n; i++ {
		calls = append(calls, agents.ToolCall{ID: fmt.Sprintf("part%02d", i), Name: a.tool,
			Arguments: []byte(fmt.Sprintf(`{"prompt":"part %d"}`, i))})
	}
	return agents.Completion{ToolCalls: calls}, nil
}
func (*parallelAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: "title"}, nil
}

// receipts are the tool messages of the last request that carried any.
func (a *parallelAgent) receipts() []agents.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	var found []agents.Message
	for _, request := range a.requests {
		var tools []agents.Message
		for _, message := range request.Messages {
			if message.Role == "configured-tool" {
				tools = append(tools, message)
			}
		}
		if len(tools) > 0 {
			found = tools
		}
	}
	return found
}

type parallelExecutor struct {
	mu        sync.Mutex
	states    map[string]generation.TaskState
	submitted []string // call IDs in submission order
	attempts  map[string]int
	full      bool // the kernel refuses every new submission (admission full)
	room      int  // submissions still accepted before the kernel refuses; negative: unlimited
	bare      bool // a refusal carries no receipt text, like a tool that leaves it to the orchestrator
	sessionID string
}

func newParallelExecutor() *parallelExecutor {
	return &parallelExecutor{states: map[string]generation.TaskState{}, attempts: map[string]int{}, room: -1}
}

func (e *parallelExecutor) Execute(_ context.Context, command chat.ToolCommand, observe func(chat.ToolObservation) error) (chat.ToolResult, error) {
	taskID := "task-" + command.Call.ID
	e.mu.Lock()
	e.sessionID = command.SessionID
	if command.BoundTaskID == "" {
		e.attempts[command.Call.ID]++
		if e.full || e.room == 0 {
			content := "failed:" + command.Call.ID
			if e.bare {
				content = ""
			}
			e.mu.Unlock()
			return chat.ToolResult{NoSubmission: true, RetryAfter: time.Second, AgentContent: content},
				errors.New("too many requests")
		}
		if e.room > 0 {
			e.room--
		}
		e.states[taskID] = generation.TaskRunning
		e.submitted = append(e.submitted, command.Call.ID)
		e.mu.Unlock()
		if err := observe(chat.ToolObservation{Registered: true, TaskID: taskID}); err != nil {
			return chat.ToolResult{}, err
		}
		return chat.ToolResult{TaskID: taskID, AwaitTask: true, WaitUntil: time.Now().Add(time.Hour)}, nil
	}
	state := e.states[command.BoundTaskID]
	e.mu.Unlock()
	switch state {
	case generation.TaskSucceeded:
		return chat.ToolResult{TaskID: command.BoundTaskID, AgentContent: "ok:" + command.Call.ID}, nil
	case generation.TaskFailed, generation.TaskCanceled:
		return chat.ToolResult{TaskID: command.BoundTaskID, AgentContent: "failed:" + command.Call.ID}, errors.New("task failed")
	}
	return chat.ToolResult{TaskID: command.BoundTaskID, AwaitTask: true, WaitUntil: command.TaskWaitDeadline}, nil
}

func (e *parallelExecutor) get(_ context.Context, ownerID, taskID string) (generation.Task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return generation.Task{ID: generation.TaskID(taskID), OwnerID: generation.OwnerID(ownerID),
		SessionID: e.sessionID, State: e.states[taskID]}, nil
}

func (e *parallelExecutor) setAll(state generation.TaskState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, current := range e.states {
		if current == generation.TaskRunning {
			e.states[id] = state
		}
	}
}

func (e *parallelExecutor) set(callID string, state generation.TaskState) {
	e.mu.Lock()
	e.states["task-"+callID] = state
	e.mu.Unlock()
}

func (e *parallelExecutor) submissions() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.submitted...)
}

type parallelFixture struct {
	t        *testing.T
	service  *chat.Service
	executor *parallelExecutor
	agent    *parallelAgent
	command  chat.ProcessTurn
}

func (f *parallelFixture) orchestrator() *chat.Orchestrator {
	f.t.Helper()
	schema := configuredChatSchema(f.t)
	schema.Agent.WorkerCount, schema.Agent.QueueSize = 4, 16
	o, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: f.service, Schema: schema, Model: "model",
		Agent: f.agent, Tools: f.executor, TaskGet: f.executor.get,
		TaskCanceler: func(_ context.Context, _, taskID string) error {
			f.executor.mu.Lock()
			f.executor.states[taskID] = generation.TaskCanceled
			f.executor.mu.Unlock()
			return nil
		},
		TaskLookup: func(context.Context, string, string) (generation.Task, error) {
			return generation.Task{}, &generation.Error{Code: generation.ErrNotFound}
		}})
	if err != nil {
		f.t.Fatal(err)
	}
	return o
}

func newParallelFixture(t *testing.T, calls int) *parallelFixture {
	t.Helper()
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	probe := configuredChatSchema(t)
	var generate string
	for _, tool := range probe.Tools {
		if tool.Name == "refine_image" {
			generate = tool.Name
		}
	}
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "parallel")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "split into parts"})
	if err != nil {
		t.Fatal(err)
	}
	return &parallelFixture{t: t, service: service, executor: newParallelExecutor(),
		agent:   &parallelAgent{tool: generate, n: calls},
		command: chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}}
}

func (f *parallelFixture) turn() chat.Turn {
	f.t.Helper()
	turn, err := f.service.GetTurn(context.Background(), f.command.OwnerID, f.command.SessionID, f.command.AssistantMessageID)
	if err != nil {
		f.t.Fatal(err)
	}
	return turn
}

// wake runs one scanner sweep and, when the turn was queued, processes it.
func (f *parallelFixture) wake(o *chat.Orchestrator) {
	f.t.Helper()
	ctx := context.Background()
	if err := o.ScanWaitingTasks(ctx); err != nil {
		f.t.Fatal(err)
	}
	if f.turn().State != f.service.TurnQueuedState() {
		return
	}
	if err := o.Process(ctx, f.command); err != nil {
		f.t.Fatal(err)
	}
}

func (f *parallelFixture) callIDs() []string {
	var ids []string
	for _, call := range f.turn().Checkpoint.Completion.ToolCalls {
		ids = append(ids, call.ID)
	}
	return ids
}

func TestParallelBatchSubmitsEveryCallBeforeWaiting(t *testing.T) {
	f := newParallelFixture(t, 6)
	if err := f.orchestrator().Process(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	turn := f.turn()
	if turn.State != f.service.TurnWaitingTaskState() || len(turn.Checkpoint.Completed) != 0 {
		t.Fatalf("turn = %s completed=%d", turn.State, len(turn.Checkpoint.Completed))
	}
	if got := f.executor.submissions(); len(got) != 6 {
		t.Fatalf("submitted before waiting = %v", got)
	}
	waiting := turn.Checkpoint.WaitingCalls()
	if len(waiting) != 6 || turn.Checkpoint.WaitingTaskID != "" {
		t.Fatalf("waiting = %+v legacy=%q", waiting, turn.Checkpoint.WaitingTaskID)
	}
	for i, entry := range waiting {
		if entry.CallID != f.callIDs()[i] || entry.TaskID != "task-"+entry.CallID || entry.Deadline.IsZero() {
			t.Fatalf("waiting[%d] = %+v", i, entry)
		}
	}
}

func TestParallelBatchWaitsForEveryTaskAndKeepsReceiptOrder(t *testing.T) {
	f := newParallelFixture(t, 6)
	o := f.orchestrator()
	ctx := context.Background()
	if err := o.Process(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	ids := f.callIDs()
	// Finish them in reverse order; the turn stays asleep until the last one.
	for i := len(ids) - 1; i >= 1; i-- {
		f.executor.set(ids[i], generation.TaskSucceeded)
		f.wake(o)
		if f.turn().State != f.service.TurnWaitingTaskState() {
			t.Fatalf("woke with task %d still running", 0)
		}
	}
	f.executor.set(ids[0], generation.TaskFailed) // one failure, the rest succeed
	f.wake(o)
	receipts := f.agent.receipts()
	if len(receipts) != 6 {
		t.Fatalf("receipts = %d", len(receipts))
	}
	for i, receipt := range receipts {
		want := "ok:" + ids[i]
		if i == 0 {
			want = "failed:" + ids[i]
		}
		if receipt.ToolCallID != ids[i] || receipt.Content != want {
			t.Fatalf("receipt %d = %q/%q want %s/%s", i, receipt.ToolCallID, receipt.Content, ids[i], want)
		}
	}
	// A failed step ends the turn in the error state, as it always did.
	if turn := f.turn(); turn.State != "configured-turn-error" {
		t.Fatalf("state = %s", turn.State)
	}
	if len(f.executor.submissions()) != 6 {
		t.Fatalf("resubmitted: %v", f.executor.submissions())
	}
}

func TestParallelBatchStopCancelsEveryTask(t *testing.T) {
	f := newParallelFixture(t, 6)
	o := f.orchestrator()
	ctx := context.Background()
	if err := o.Process(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	if active, err := o.CancelTurn(ctx, f.command.OwnerID, f.command.SessionID); err != nil || !active {
		t.Fatalf("stop = %v %v", active, err)
	}
	if err := o.Process(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	for _, id := range f.callIDs() {
		if state := f.executor.states["task-"+id]; state != generation.TaskCanceled {
			t.Fatalf("task of %s = %s", id, state)
		}
	}
	if turn := f.turn(); turn.State != "configured-turn-done" {
		t.Fatalf("state = %s", turn.State)
	}
}

func TestParallelBatchResumesAfterRestart(t *testing.T) {
	f := newParallelFixture(t, 6)
	if err := f.orchestrator().Process(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	f.executor.setAll(generation.TaskSucceeded)
	restarted := f.orchestrator() // a new process: nothing but the stored checkpoint
	f.wake(restarted)
	if turn := f.turn(); turn.State != "configured-turn-done" || len(f.agent.receipts()) != 6 {
		t.Fatalf("state = %s receipts=%d", turn.State, len(f.agent.receipts()))
	}
	if len(f.executor.submissions()) != 6 {
		t.Fatalf("restart submitted again: %v", f.executor.submissions())
	}
}

func TestLegacySingleFieldCheckpointStillResumes(t *testing.T) {
	f := newParallelFixture(t, 1)
	ctx := context.Background()
	if err := f.orchestrator().Process(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	turn := f.turn()
	entry := turn.Checkpoint.WaitingCalls()[0]
	turn.Checkpoint.Waiting = nil
	turn.Checkpoint.WaitingCallID, turn.Checkpoint.WaitingTaskID, turn.Checkpoint.WaitDeadline = entry.CallID, entry.TaskID, entry.Deadline
	if _, err := f.service.SaveTurnCheckpoint(ctx, turn, turn.State, turn.Checkpoint, false); err != nil {
		t.Fatal(err)
	}
	restarted := f.orchestrator()
	f.wake(restarted)
	if f.turn().State != f.service.TurnWaitingTaskState() {
		t.Fatal("legacy checkpoint woke while its task still ran")
	}
	f.executor.setAll(generation.TaskSucceeded)
	f.wake(restarted)
	if turn := f.turn(); turn.State != "configured-turn-done" || len(f.agent.receipts()) != 1 {
		t.Fatalf("state = %s", turn.State)
	}
	if len(f.executor.submissions()) != 1 {
		t.Fatalf("resubmitted: %v", f.executor.submissions())
	}
}

func (e *parallelExecutor) setRoom(room int) {
	e.mu.Lock()
	e.room = room
	e.mu.Unlock()
}

func (f *parallelFixture) saveWaiting(mutate func(waiting []chat.WaitingCall)) {
	f.t.Helper()
	turn := f.turn()
	waiting := turn.Checkpoint.WaitingCalls()
	mutate(waiting)
	turn.Checkpoint.SetWaiting(waiting)
	if _, err := f.service.SaveTurnCheckpoint(context.Background(), turn, turn.State, turn.Checkpoint, false); err != nil {
		f.t.Fatal(err)
	}
}

func TestAdmissionFullQueuesSubmissionAndRetriesTheSameCalls(t *testing.T) {
	f := newParallelFixture(t, 3)
	o := f.orchestrator()
	f.executor.setRoom(1) // the first call is accepted, then admission is full
	if err := o.Process(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	ids := f.callIDs()
	turn := f.turn()
	waiting := turn.Checkpoint.WaitingCalls()
	if turn.State != f.service.TurnWaitingTaskState() || len(waiting) != 2 || waiting[0].CallID != ids[0] ||
		waiting[0].TaskID == "" || waiting[1].CallID != ids[1] || waiting[1].TaskID != "" ||
		waiting[1].RetryAt.IsZero() || waiting[1].QueuedSince.IsZero() || waiting[1].Deadline.IsZero() {
		t.Fatalf("queued checkpoint = %s %+v", turn.State, waiting)
	}
	if got := f.executor.submissions(); len(got) != 1 {
		t.Fatalf("submitted = %v", got)
	}
	f.wake(o) // retry not due yet: the turn stays asleep
	if f.turn().State != f.service.TurnWaitingTaskState() {
		t.Fatal("woke before the retry was due")
	}
	f.saveWaiting(func(w []chat.WaitingCall) { w[1].RetryAt = time.Now().Add(-time.Second) })
	f.executor.setRoom(-1)
	f.wake(o)
	if got := f.executor.submissions(); len(got) != 3 || got[1] != ids[1] || got[2] != ids[2] {
		t.Fatalf("after retry submitted = %v", got)
	}
	if turn := f.turn(); turn.State != f.service.TurnWaitingTaskState() || len(turn.Checkpoint.WaitingCalls()) != 3 {
		t.Fatalf("after retry = %s %+v", turn.State, turn.Checkpoint.WaitingCalls())
	}
	f.executor.setAll(generation.TaskSucceeded)
	f.wake(o)
	if turn := f.turn(); turn.State != "configured-turn-done" || len(f.agent.receipts()) != 3 {
		t.Fatalf("state = %s receipts=%d", turn.State, len(f.agent.receipts()))
	}
}

func TestQueuedSubmissionFailsAfterTheRequestTimeout(t *testing.T) {
	f := newParallelFixture(t, 2)
	o := f.orchestrator()
	f.executor.setRoom(1)
	if err := o.Process(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	ids := f.callIDs()
	timeout := time.Duration(configuredChatSchema(t).Agent.RequestTimeoutSeconds) * time.Second
	f.saveWaiting(func(w []chat.WaitingCall) {
		w[1].QueuedSince = time.Now().Add(-timeout - time.Minute)
		w[1].RetryAt = time.Now().Add(-time.Second)
	})
	f.executor.set(ids[0], generation.TaskSucceeded)
	f.wake(o) // still no room: the retry is refused again, past the limit
	receipts := f.agent.receipts()
	if len(receipts) != 2 || receipts[0].Content != "ok:"+ids[0] || receipts[1].Content != "failed:"+ids[1] {
		t.Fatalf("receipts = %+v", receipts)
	}
	if turn := f.turn(); turn.State != "configured-turn-error" {
		t.Fatalf("state = %s", turn.State)
	}
}

// #59: a refusal past the queue deadline is the call's final receipt. A tool
// that gave no text must not hand the model an empty receipt; the orchestrator
// falls back to the Schema's generic generation failure text.
func TestQueuedSubmissionPastTheDeadlineNeverGivesAnEmptyReceipt(t *testing.T) {
	f := newParallelFixture(t, 2)
	o := f.orchestrator()
	f.executor.setRoom(1)
	f.executor.bare = true
	if err := o.Process(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	ids := f.callIDs()
	schema := configuredChatSchema(t)
	timeout := time.Duration(schema.Agent.RequestTimeoutSeconds) * time.Second
	f.saveWaiting(func(w []chat.WaitingCall) {
		w[1].QueuedSince = time.Now().Add(-timeout - time.Minute)
		w[1].RetryAt = time.Now().Add(-time.Second)
	})
	f.executor.set(ids[0], generation.TaskSucceeded)
	f.wake(o)
	receipts := f.agent.receipts()
	want := schema.Presentation.Messages.GenerationError
	if want == "" || len(receipts) != 2 || receipts[1].Content != want {
		t.Fatalf("receipts = %+v, want %q for the queued call", receipts, want)
	}
	if len(f.executor.submissions()) != 1 {
		t.Fatalf("submitted = %v", f.executor.submissions())
	}
}

func TestTwentyFirstGenerationIsRefusedByTheToolBudget(t *testing.T) {
	f := newParallelFixture(t, 21)
	o := f.orchestrator()
	ctx := context.Background()
	if err := o.Process(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	if got := len(f.executor.submissions()); got != 8 { // max_parallel_generation_submissions
		t.Fatalf("first batch submitted %d", got)
	}
	for round := 0; round < 10 && f.turn().State != "configured-turn-done"; round++ {
		f.executor.setAll(generation.TaskSucceeded)
		f.wake(o)
	}
	if turn := f.turn(); turn.State != "configured-turn-done" {
		t.Fatalf("state = %s", turn.State)
	}
	if got := len(f.executor.submissions()); got != 20 {
		t.Fatalf("submitted %d, want the budget of 20", got)
	}
	if len(f.executor.attempts) != 20 {
		t.Fatalf("21st call was attempted: %d", len(f.executor.attempts))
	}
}
