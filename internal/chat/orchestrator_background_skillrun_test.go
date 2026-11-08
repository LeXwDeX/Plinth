package chat_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// batchSkillExecutor plays the receipts the real executors return in one
// assistant completion: refine_image submits its Task and waits on it
// (AwaitTask), while create_three_view submits its own Task and returns the
// started-view receipt without AwaitTask (internal/chat/agenttools/three_view.go
// observeThreeView). The orchestrator keeps the SkillRun receipt ordered behind
// the waiting generation step, so that Task joins the turn's wait.
type batchSkillExecutor struct {
	mu       sync.Mutex
	states   map[string]generation.TaskState
	owners   map[string]string
	sessions map[string]string
	calls    map[string]int
}

func newBatchSkillExecutor() *batchSkillExecutor {
	return &batchSkillExecutor{states: map[string]generation.TaskState{}, owners: map[string]string{},
		sessions: map[string]string{}, calls: map[string]int{}}
}

func (e *batchSkillExecutor) Execute(_ context.Context, command chat.ToolCommand,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	e.mu.Lock()
	e.calls[command.Call.Name]++
	e.mu.Unlock()
	var taskID string
	switch command.Call.Name {
	case "refine_image":
		taskID = "task-generation-" + command.SessionID
	case "create_three_view":
		taskID = "task-three-view-" + command.SessionID
	default:
		return chat.ToolResult{NoSubmission: true}, nil
	}
	if command.BoundTaskID != "" {
		taskID = command.BoundTaskID
	}
	e.mu.Lock()
	e.states[taskID] = generation.TaskRunning
	e.owners[taskID], e.sessions[taskID] = command.OwnerID, command.SessionID
	e.mu.Unlock()
	if observe != nil {
		if err := observe(chat.ToolObservation{Registered: true, TaskID: taskID}); err != nil {
			return chat.ToolResult{}, err
		}
	}
	if command.Call.Name == "create_three_view" {
		return chat.ToolResult{TaskID: taskID, AgentContent: "three view started"}, nil
	}
	return chat.ToolResult{TaskID: taskID, AwaitTask: true, WaitUntil: time.Now().Add(time.Hour)}, nil
}

func (e *batchSkillExecutor) get(_ context.Context, ownerID, taskID string) (generation.Task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.owners[taskID] != ownerID {
		return generation.Task{}, errors.New("wrong task owner")
	}
	return generation.Task{ID: generation.TaskID(taskID), OwnerID: generation.OwnerID(ownerID),
		SessionID: e.sessions[taskID], State: e.states[taskID]}, nil
}

func (e *batchSkillExecutor) count(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[name]
}

// batchSkillAgent emits the given calls in one reply, the shape a model may
// produce for a request that starts a generation and three views together, and
// answers with text once the tool receipts are in.
type batchSkillAgent struct {
	calls   []agents.ToolCall
	mu      sync.Mutex
	systems []string
}

func (a *batchSkillAgent) Stream(_ context.Context, request agents.Request,
	_ func(agents.Delta) error,
) (agents.Completion, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.systems = append(a.systems, request.Messages[0].Content)
	for _, message := range request.Messages {
		if message.Role == "configured-tool" {
			return agents.Completion{Content: "finished"}, nil
		}
	}
	return agents.Completion{ToolCalls: a.calls}, nil
}

func (a *batchSkillAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: "title"}, nil
}

func (a *batchSkillAgent) lastSystem() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.systems) == 0 {
		return ""
	}
	return a.systems[len(a.systems)-1]
}

// #189: a SkillRun call shares the wait of a generation step submitted in the
// same batch. Its Task becomes background-eligible, and stopping the wait ends
// the turn without cancelling it; the next turn lists it for the Agent.
func TestContinueInBackgroundWaitsForABatchedSkillRunTask(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newBatchSkillExecutor()
	facts := newBackgroundFacts()
	schema := backgroundSchema(t)
	if !schema.Agent.BackgroundWait.Allows("create_three_view") {
		t.Fatal("the shipped Schema lets the three-view SkillRun wait continue in the background")
	}
	agent := &batchSkillAgent{calls: []agents.ToolCall{
		{ID: "generation", Name: "refine_image", Arguments: []byte(`{"prompt":"paint"}`)},
		{ID: "three-view", Name: "create_three_view", Arguments: []byte(`{}`)},
	}}
	o := backgroundOrchestratorWith(t, service, schema, executor, executor.get, func(context.Context, string, string) error {
		t.Fatal("background continue must not cancel a Task")
		return nil
	}, facts, agent)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "batched skill run")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "cube and three views"})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnWaitingTaskState() {
		t.Fatalf("the batch did not wait: %+v %v", turn, err)
	}
	waiting := turn.Checkpoint.WaitingCalls()
	generationTask, threeViewTask := "task-generation-"+session.ID, "task-three-view-"+session.ID
	if len(waiting) != 2 {
		t.Fatalf("waiting calls = %+v", waiting)
	}
	seen := map[string]bool{}
	for _, entry := range waiting {
		seen[entry.TaskID] = true
	}
	for _, taskID := range []string{generationTask, threeViewTask} {
		if !seen[taskID] {
			t.Fatalf("the batch did not wait on %s: %+v", taskID, waiting)
		}
	}
	if calls := executor.count("create_three_view"); calls != 1 {
		t.Fatalf("SkillRun submissions = %d", calls)
	}
	// A fresh Task that is neither queued upstream nor past the threshold does
	// not qualify, even though both calls are allowed.
	if o.TurnBackgroundAvailable(ctx, "owner-a", session.ID) {
		t.Fatal("fresh Tasks must not qualify yet")
	}
	facts.mu.Lock()
	facts.queued[generationTask] = true
	facts.mu.Unlock()
	if !o.TurnBackgroundAvailable(ctx, "owner-a", session.ID) {
		t.Fatal("an upstream-queued generation Task qualifies the batch")
	}
	if err := o.ContinueTurnInBackground(ctx, "owner-a", session.ID); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != service.TurnQueuedState() || !turn.Checkpoint.Background {
		t.Fatalf("background choice not recorded: %+v %v", turn, err)
	}
	if err := o.ContinueTurnInBackground(ctx, "owner-a", session.ID); !errors.Is(err, chat.ErrTurnBackgroundUnavailable) {
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
	assistantMessage := assistantOf(t, service, command)
	backgrounded := map[string]chat.ToolCall{}
	for _, call := range assistantMessage.ToolCalls {
		backgrounded[call.Name] = call
	}
	for name, taskID := range map[string]string{"refine_image": generationTask, "create_three_view": threeViewTask} {
		call := backgrounded[name]
		if !call.Background || call.TaskID != taskID {
			t.Fatalf("call %s not marked for the background: %+v", name, call)
		}
	}
	if !strings.Contains(assistantMessage.Content, schema.Agent.BackgroundWait.TurnContent) {
		t.Fatalf("content = %q", assistantMessage.Content)
	}
	events, err := service.ListEvents(ctx, command.OwnerID, command.SessionID, 0)
	if err != nil || len(events) == 0 || events[len(events)-1].Data["configured-type"] != "configured-done" {
		t.Fatalf("the stream did not end: %+v %v", events, err)
	}
	if calls := executor.count("create_three_view"); calls != 1 {
		t.Fatalf("backgrounding resubmitted the SkillRun: %d", calls)
	}

	// The next turn lists both background Tasks; only the generation Task is
	// upstream-queued.
	nextAgent := &backgroundContextAgent{}
	next := backgroundOrchestratorWith(t, service, schema, executor, executor.get, func(context.Context, string, string) error {
		t.Fatal("the next turn must not cancel a Task")
		return nil
	}, facts, nextAgent)
	second, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "is it done?"})
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: second.ID}); err != nil {
		t.Fatal(err)
	}
	system := nextAgent.lastSystem()
	for taskID, status := range map[string]string{
		generationTask: string(generation.TaskRunning) + schema.Agent.BackgroundWait.UpstreamQueuedStatus,
		threeViewTask:  string(generation.TaskRunning),
	} {
		line := strings.NewReplacer("{task_id}", taskID, "{capability}", "text-to-image", "{model}", "configured-image-model",
			"{status}", status).Replace(schema.Agent.BackgroundWait.TaskLineTemplate)
		if !strings.Contains(system, line) {
			t.Fatalf("the Agent was not told about %s:\n%s", taskID, system)
		}
	}
}

// The boundary this feature relies on: a lone create_three_view call returns
// its receipt without waiting (internal/chat/agenttools/three_view.go
// observeThreeView has AwaitTask only on its failure path, and
// internal/chat/agenttools/three_view_harness_test.go asserts AwaitTask is
// false). Such a turn ends normally, so it never offers the background choice.
func TestALoneSkillRunReceiptDoesNotWait(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	executor := newBatchSkillExecutor()
	facts := newBackgroundFacts()
	schema := backgroundSchema(t)
	agent := &batchSkillAgent{calls: []agents.ToolCall{
		{ID: "three-view", Name: "create_three_view", Arguments: []byte(`{}`)},
	}}
	o := backgroundOrchestratorWith(t, service, schema, executor, executor.get, func(context.Context, string, string) error {
		t.Fatal("nothing may be cancelled")
		return nil
	}, facts, agent)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "lone skill run")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "three views of this"})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}
	if err := o.Process(ctx, command); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil || turn.State != "configured-turn-done" {
		t.Fatalf("a lone SkillRun receipt must not wait: %+v %v", turn, err)
	}
	if waiting := turn.Checkpoint.WaitingCalls(); len(waiting) != 0 {
		t.Fatalf("waiting calls = %+v", waiting)
	}
	if calls := executor.count("create_three_view"); calls != 1 {
		t.Fatalf("SkillRun submissions = %d", calls)
	}
	if o.TurnBackgroundAvailable(ctx, "owner-a", session.ID) {
		t.Fatal("a finished turn has nothing to background")
	}
	if err := o.ContinueTurnInBackground(ctx, "owner-a", session.ID); !errors.Is(err, chat.ErrTurnBackgroundUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if canceled := facts.cancellations(); len(canceled) != 0 {
		t.Fatalf("cancelled %v", canceled)
	}
}