package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

type harnessThreeRecipe struct{}

func (harnessThreeRecipe) ResolveRecipe(context.Context, string, generation.OfferingID) (skillrun.Recipe, bool, error) {
	return skillrun.Recipe{ID: threeViewRecipeID, Revision: "recipe-r1", Steps: []skillrun.StepSpec{{Key: "front", Label: "Front", Capability: generation.ImageToImage, OfferingID: "image-offering", OfferingRevision: "offering-r1", PromptTemplate: "front view", PromptParameter: "prompt", SourceParameter: "reference_assets", OutputRole: "image"}}}, true, nil
}

type harnessThreeReferences struct{ calls int }

func (r *harnessThreeReferences) Resolve(context.Context, generation.OwnerID, []string) ([]generation.AssetID, error) {
	r.calls++
	return []generation.AssetID{"source"}, nil
}

type harnessThreeGateway struct {
	repo                       *sqlite.Repository
	prepares, submits, replays int
}

func (g *harnessThreeGateway) Offerings(generation.Capability) []generation.Offering {
	return []generation.Offering{{ID: "image-offering", Revision: "offering-r1", ModelID: "model", Capability: generation.ImageToImage, Enabled: true}}
}
func (g *harnessThreeGateway) OfferingAvailable(generation.OfferingID) bool { return true }
func (g *harnessThreeGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	g.prepares++
	p := generation.PreparedInput{ID: "prepared", OwnerID: cmd.OwnerID, SessionID: cmd.SessionID, Capability: cmd.Capability, OfferingID: cmd.OfferingID, OfferingRevision: "offering-r1", NormalizedInput: cmd.Input, InputHash: "prepared-hash", Assets: []generation.PreparedInputAsset{{ID: "source"}}, CreatedAt: time.Now()}
	return p, g.repo.CreatePreparedInput(ctx, p)
}
func (g *harnessThreeGateway) GetPreparedInput(ctx context.Context, owner generation.OwnerID, id generation.PreparedInputID) (generation.PreparedInput, error) {
	return g.repo.GetPreparedInput(ctx, owner, id)
}
func (g *harnessThreeGateway) SubmitPrepared(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	g.submits++
	p, err := g.GetPreparedInput(ctx, cmd.OwnerID, cmd.PreparedInputID)
	if err != nil {
		return generation.Accepted{}, err
	}
	now := time.Now()
	task := generation.Task{ID: "accepted-three-view", OwnerID: cmd.OwnerID, SessionID: cmd.SessionID, Capability: p.Capability, Offering: g.Offerings(p.Capability)[0], Input: p.NormalizedInput, RequestHash: generation.ExpectedRequestHash(p.OfferingID, p.NormalizedInput), IdempotencyKey: cmd.IdempotencyKey, State: generation.TaskQueued, Version: 1, CreatedAt: now, UpdatedAt: now, Admission: &generation.ExecutionAdmission{Harness: cmd.Harness, RequestID: cmd.AuthorizationRef, PreparedInputID: p.ID, PreparedInputHash: p.InputHash, OperationID: cmd.OperationID, PlanStepID: cmd.PlanStepID}}
	stored, replayed, err := g.repo.Create(ctx, task, generation.Event{TaskID: task.ID, Type: "task.submitted", Data: json.RawMessage(`{}`), CreatedAt: now}, 0)
	return generation.Accepted{TaskID: stored.ID, Replayed: replayed}, err
}
func (g *harnessThreeGateway) VerifyPreparedTaskReplay(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Task, error) {
	g.replays++
	task, err := g.repo.GetByIdempotency(ctx, cmd.OwnerID, cmd.IdempotencyKey)
	if err != nil {
		return task, err
	}
	p, err := g.GetPreparedInput(ctx, cmd.OwnerID, cmd.PreparedInputID)
	if err != nil {
		return task, err
	}
	err = g.repo.VerifyTaskGrantReplay(ctx, task, &generation.ExecutionAdmission{Harness: cmd.Harness, RequestID: cmd.AuthorizationRef, PreparedInputID: p.ID, PreparedInputHash: p.InputHash, OperationID: cmd.OperationID, PlanStepID: cmd.PlanStepID})
	return task, err
}
func (g *harnessThreeGateway) Get(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	return g.repo.Get(ctx, owner, id)
}

func threeViewHarnessFixture(t *testing.T) (*Executor, *harnessThreeGateway, *harnessThreeReferences, chat.ToolCommand, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "three.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	now := time.Now()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", OwnerName: "Owner", Title: "three", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateInputAsset(ctx, generation.InputAsset{ID: "source", OwnerID: "owner", StorageKey: "saved/source", MediaType: "image/png", Size: 4, SHA256: "source-hash", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner", chat.Message{ID: "user", SessionID: "session", Role: "user", CreatedAt: now}, chat.Message{ID: "turn", SessionID: "session", Role: "assistant", CreatedAt: now}, chat.Event{SessionID: "session", Data: map[string]any{"type": "created"}, CreatedAt: now}, chat.Stream{SessionID: "session", Status: "active"}, chat.Turn{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	runs, err := skillrun.NewService(repo, harnessThreeRecipe{})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &harnessThreeGateway{repo: repo}
	grants := &executiongrant.Service{Grants: repo, Prepared: repo}
	runtime, err := skillrun.NewRuntime(runs, gateway, grants)
	if err != nil {
		t.Fatal(err)
	}
	references := &harnessThreeReferences{}
	executor := &Executor{skillRuntime: runtime, skillReferences: references, skillOfferings: gateway, skillRecipes: map[string]string{threeViewRecipeID: "recipe-r1"}, skillTurnStopped: func(context.Context, string, string, string) (bool, error) { return false, nil }, taskObservationTimeout: time.Minute}
	executor.SetThreeView(catalog.ChatThreeViewSchema{Messages: catalog.ChatThreeViewMessages{ReferenceRequired: "one image", AgentStarted: "{run}:{task}:{view}"}})
	snapshot := resolve.Snapshot{Selection: harness.DefaultSelection("owner", "session"), Status: resolve.StatusFree}
	command := chat.ToolCommand{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", Call: agents.ToolCall{ID: "call", Name: ToolCreateThreeView, Arguments: json.RawMessage(`{}`)}, Options: chat.TurnOptions{AIModel: "model", CreativeDomain: "image", Harness: &snapshot}, History: []chat.Message{{Role: "user", ImageURLs: []string{"source-image"}}}}
	return executor, gateway, references, command, path
}

func TestThreeViewNonzeroHarnessActualAdmissionAndRestartObservation(t *testing.T) {
	ctx := context.Background()
	executor, gateway, references, command, path := threeViewHarnessFixture(t)
	result, err := executor.Execute(ctx, command, nil)
	if err != nil || !result.Suspend || gateway.submits != 0 {
		t.Fatalf("prepare %+v %v", result, err)
	}
	run, execution, grant, err := executor.existingThreeView(ctx, command)
	if err != nil || run.Harness != command.Options.Harness.Selection.Binding || grant.Harness != run.Harness || run.Harness.IsZero() {
		t.Fatalf("free baseline identity %+v %+v %v", run, grant, err)
	}
	if _, err := gateway.repo.DecideExecutionGrant(ctx, "owner", grant.RequestID, "human", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	result, err = executor.Execute(ctx, command, nil)
	if err != nil || result.AwaitTask || result.TaskID != "accepted-three-view" {
		t.Fatalf("nonzero bound task could not be observed: %+v %v", result, err)
	}
	// Reopen the real ledger and change current selection. Recovery must observe
	// the original accepted identity with no new recipe/source/preparation work.
	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	current := harness.DefaultSelection("owner", "session")
	current.OperationID = "new-choice"
	if _, err := second.CompareAndSwapHarnessSelection(ctx, current, 0); err != nil {
		t.Fatal(err)
	}
	runs, err := skillrun.NewService(second, harnessThreeRecipe{})
	if err != nil {
		t.Fatal(err)
	}
	restartedGateway := &harnessThreeGateway{repo: second}
	runtime, err := skillrun.NewRuntime(runs, restartedGateway, &executiongrant.Service{Grants: second, Prepared: second})
	if err != nil {
		t.Fatal(err)
	}
	executor.skillRuntime = runtime
	result, err = executor.Execute(ctx, command, nil)
	if err != nil || result.TaskID != "accepted-three-view" || restartedGateway.submits != 0 || restartedGateway.prepares != 0 || references.calls != 1 {
		t.Fatalf("restart repeated work %+v %v", result, err)
	}
	recovery := command
	recovery.Options = chat.TurnOptions{}
	identity, err := executor.TaskIdentityForCall(ctx, recovery)
	if err != nil || identity.TaskID != result.TaskID || identity.IdempotencyKey != execution.IdempotencyKey {
		t.Fatalf("read-only recovery identity %+v %v", identity, err)
	}
	if _, err := executor.Execute(ctx, recovery, nil); !errors.Is(err, chat.ErrTurnCheckpointConflict) {
		t.Fatalf("read-only lookup bypassed execution binding: %v", err)
	}
	active := command
	snapshot := *command.Options.Harness
	snapshot.Selection = current
	snapshot.Selection.Revision = 1
	active.Options.Harness = &snapshot
	if _, err := executor.Execute(ctx, active, nil); !errors.Is(err, chat.ErrTurnCheckpointConflict) {
		t.Fatalf("active different binding recovered old execution: %v", err)
	}
	if restartedGateway.submits != 0 || restartedGateway.prepares != 0 {
		t.Fatal("rejected recovery created work")
	}
}

// The model may still emit an obsolete Skill name. The actual chat registry
// must withhold it and refuse dispatch before any SkillRun/Grant work begins.
func TestNewActiveHarnessTurnCannotDispatchThreeViewSkill(t *testing.T) {
	executor, gateway, references, _, _ := threeViewHarnessFixture(t)
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	service, err := chat.NewService(chat.ServiceConfig{
		Repository: gateway.repo, DefaultTitle: schema.Defaults.Title, UserRole: schema.Roles.User, AssistantRole: schema.Roles.Assistant,
		PendingContent: schema.State.PendingContent, StreamActive: schema.State.Active, StreamDone: schema.State.Done, StreamError: schema.State.Error,
		TurnQueued: schema.State.TurnQueued, TurnRunning: schema.State.TurnRunning, TurnWaitingTask: schema.State.TurnWaitingTask, TurnDone: schema.State.TurnDone, TurnError: schema.State.TurnError,
		PlaceholderHealing: chat.PlaceholderHealingPolicy{StaleAfter: time.Minute, PromptArgument: schema.Context.ToolPromptArgument,
			PromptPreviewCharacters: schema.State.FailedPromptPreviewChars, InterruptedMessage: schema.Presentation.Messages.Interrupted, InterruptedPromptTemplate: schema.Presentation.Messages.InterruptedPrompt},
		StreamCleanupTTL: time.Minute, SubscriberBufferSize: schema.Stream.SubscriberBufferSize,
		EventFields: chat.EventFieldPolicy{Type: schema.Stream.EventFields.Type, MessageID: schema.Stream.EventFields.MessageID},
		EventTypes:  chat.EventTypePolicy{MessageCreated: schema.Stream.EventTypes.MessageCreated},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner", "Owner", "active harness")
	if err != nil {
		t.Fatal(err)
	}
	selection := harness.DefaultSelection("owner", session.ID)
	selection.OperationID, selection.OfferingID, selection.OfferingRevision = "remesh", "chosen-remesh", "r1"
	selection.EnabledOperations = []string{"remesh"}
	selection, err = harness.NormalizeSelection(selection)
	if err != nil {
		t.Fatal(err)
	}
	service.SetHarnessSnapshotResolver(func(context.Context, string, string) (resolve.Snapshot, error) {
		return resolve.Snapshot{Selection: selection, OperationID: "remesh", ToolName: "process_model", ToolParameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), Status: resolve.StatusReady}, nil
	})
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner", SessionID: session.ID, Content: "make three views", Options: chat.TurnOptions{AIModel: "model", CreativeDomain: "image"}})
	if err != nil {
		t.Fatal(err)
	}
	agent := &obsoleteThreeViewAgent{}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{Service: service, Schema: schema, Model: "fixture-agent", Agent: agent, Tools: executor})
	if err != nil {
		t.Fatal(err)
	}
	_ = orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner", SessionID: session.ID, AssistantMessageID: assistant.ID})
	orchestrator.WaitForTitles()
	if len(agent.requests) == 0 {
		t.Fatal("agent never ran")
	}
	for _, tool := range agent.requests[0].Tools {
		if tool.Name == ToolCreateThreeView {
			t.Fatal("active Harness leaked the Skill tool into the actual model request")
		}
	}
	if references.calls != 0 || gateway.prepares != 0 || gateway.submits != 0 {
		t.Fatalf("unoffered Skill bypassed registry: references=%d prepares=%d submits=%d", references.calls, gateway.prepares, gateway.submits)
	}
	turn, err := service.GetTurn(ctx, "owner", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	rejected := false
	for _, message := range turn.Checkpoint.Completed {
		if message.Role == schema.Roles.Tool && message.ToolCallID != "" && strings.Contains(message.Content, `tool "create_three_view" is not available`) {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("obsolete Skill call has no durable registry refusal: state=%s completed=%+v", turn.State, turn.Checkpoint.Completed)
	}
}

type obsoleteThreeViewAgent struct{ requests []agents.Request }

func (a *obsoleteThreeViewAgent) Stream(_ context.Context, request agents.Request, _ func(agents.Delta) error) (agents.Completion, error) {
	a.requests = append(a.requests, request)
	if len(a.requests) == 1 {
		return agents.Completion{ToolCalls: []agents.ToolCall{{ID: "obsolete-skill", Name: ToolCreateThreeView, Arguments: json.RawMessage(`{}`)}}}, nil
	}
	return agents.Completion{Content: "The selected operation does not allow that Skill."}, nil
}
func (a *obsoleteThreeViewAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: "active harness"}, nil
}
