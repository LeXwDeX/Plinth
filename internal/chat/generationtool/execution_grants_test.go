package generationtool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

type grantGateway struct {
	*fakeGateway
	repo *sqlite.Repository
	// mu guards the counters: calls of one turn run concurrently.
	mu            sync.Mutex
	preparedCount int
	submitCount   int
	// prepareErr, when set, is what freezing the input answers.
	prepareErr error
	// offering finds the Offering a frozen input is priced by; the default
	// looks in the fake gateway's own list.
	offering func(generation.OfferingID) (generation.Offering, bool)
}

// extraCreditsOf is what the kernel records on a frozen input: the extra
// credits its Offering's cost declaration charges for it.
func (g *grantGateway) extraCreditsOf(id generation.OfferingID, input json.RawMessage) int {
	if g.offering != nil {
		if offering, found := g.offering(id); found {
			return offering.ExtraCreditDetail(input).Credits
		}
		return 0
	}
	for _, offering := range g.offerings {
		if offering.ID == id {
			return offering.ExtraCreditDetail(input).Credits
		}
	}
	return 0
}

type persistedGrantGateway struct {
	*grantGateway
	getErrors int
}

type flakyGrantRead struct {
	executiongrant.Repository
	reads int
}

type flakyOperationRead struct {
	generationtool.OperationStore
	reads int
}

func (r *flakyOperationRead) LoadAgentOperation(ctx context.Context, owner, session, turn, call string) (generationtool.Operation, error) {
	if r.reads > 0 {
		r.reads--
		return generationtool.Operation{}, errors.New("temporary operation read failure")
	}
	return r.OperationStore.LoadAgentOperation(ctx, owner, session, turn, call)
}

func (r *flakyGrantRead) GetExecutionGrant(ctx context.Context, owner, requestID string) (executiongrant.Grant, error) {
	if r.reads > 0 {
		r.reads--
		return executiongrant.Grant{}, errors.New("temporary grant read failure")
	}
	return r.Repository.GetExecutionGrant(ctx, owner, requestID)
}

func (g *persistedGrantGateway) Get(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	if g.getErrors > 0 {
		g.getErrors--
		return generation.Task{}, errors.New("temporary task read failure")
	}
	return g.repo.Get(ctx, owner, id)
}

func (g *grantGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	if g.prepareErr != nil {
		return generation.PreparedInput{}, g.prepareErr
	}
	g.mu.Lock()
	g.preparedCount++
	count := g.preparedCount
	g.mu.Unlock()
	p := generation.PreparedInput{ID: generation.PreparedInputID(fmt.Sprintf("prepared-%d", count)),
		OwnerID: cmd.OwnerID, SessionID: cmd.SessionID, Capability: cmd.Capability,
		OfferingID: cmd.OfferingID, OfferingRevision: "rev-a", SchemaVersion: "1",
		NormalizedInput: cmd.Input, ExtraCredits: g.extraCreditsOf(cmd.OfferingID, cmd.Input),
		InputHash: fmt.Sprintf("frozen-%d", count), CreatedAt: time.Now().UTC()}
	if err := g.repo.CreatePreparedInput(ctx, p); err != nil {
		return p, err
	}
	return p, nil
}

func (g *grantGateway) GetPreparedInput(ctx context.Context, owner generation.OwnerID, id generation.PreparedInputID) (generation.PreparedInput, error) {
	return g.repo.GetPreparedInput(ctx, owner, id)
}

func (g *grantGateway) SubmitPrepared(_ context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	g.mu.Lock()
	g.submitCount++
	g.mu.Unlock()
	return generation.Accepted{TaskID: "accepted-agent-task"}, nil
}

func (g *grantGateway) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	return generation.Task{ID: id, OwnerID: owner, SessionID: "session-a", State: generation.TaskSucceeded,
		Offering: g.offerings[0]}, nil
}

func TestAgentGenerationPendingRestartDecisionAndStop(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner-a",
		chat.Message{ID: "user-a", SessionID: "session-a", Role: "user", Content: "paint", CreatedAt: now},
		chat.Message{ID: "turn-a", SessionID: "session-a", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session-a", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session-a", Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
			State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	tool.IdempotencyKeyTemplate = "agent:{session_id}:{call_id}"
	offering := generation.Offering{ID: "fake/offering", Revision: "rev-a", ModelID: "fake-model",
		Capability: tool.FallbackCapability, Enabled: true, Timeout: time.Second,
		Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt}, {Name: "quality", Type: "string"}}}
	gateway := &grantGateway{fakeGateway: &fakeGateway{offerings: []generation.Offering{offering}}, repo: repo}
	service := &executiongrant.Service{Grants: repo, Prepared: repo}
	scopedQuality := json.RawMessage(`"original"`)
	resolverCalls := 0
	newExecutor := func() *generationtool.Executor {
		e, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{},
			Operations: repo, Grants: service, Chat: application.Chat,
			OfferingParameters: func(owner, session, offering string) map[string]json.RawMessage {
				resolverCalls++
				if owner != "owner-a" || session != "session-a" || offering != "fake/offering" {
					t.Fatal("wrong offering parameter scope")
				}
				return map[string]json.RawMessage{"quality": scopedQuality}
			},
			StopRequested: func(_ context.Context, owner, session, turn string) (bool, error) {
				if owner != "owner-a" || session != "session-a" || turn != "turn-a" {
					t.Fatal("wrong turn scope")
				}
				return false, nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: []byte(`{"prompt":"paint"}`)},
		Tool: tool, Options: chat.TurnOptions{AIModel: "fake-model"}}
	// A step that needs a human ends the turn at once: the grant stays pending
	// and approvable, the operation is detached, and nothing is submitted.
	waiting := application.Chat.Agent.ConsentWaitingContent
	first, err := newExecutor().Execute(ctx, command, nil)
	if err != nil || first.Suspend || !first.NoSubmission || awaitedMessage(first.AgentContent) != waiting ||
		gateway.submitCount != 0 || gateway.preparedCount != 1 {
		t.Fatalf("first pending: %+v prepares=%d submits=%d err=%v", first, gateway.preparedCount, gateway.submitCount, err)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if err != nil || !op.Detached || op.GrantRequestID == "" {
		t.Fatalf("pending operation must be detached at once: %+v %v", op, err)
	}
	grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
	if err != nil || grant.Status != executiongrant.Pending {
		t.Fatalf("detached grant must stay pending: %+v %v", grant, err)
	}
	scopedQuality = json.RawMessage(`"changed-after-prepare"`)
	restarted, err := newExecutor().Execute(ctx, command, nil)
	if err != nil || restarted.Suspend || awaitedMessage(restarted.AgentContent) != waiting || gateway.preparedCount != 1 {
		t.Fatalf("restart pending: %+v %v", restarted, err)
	}
	changedHistory := command
	changedHistory.History = []chat.Message{{Role: application.Chat.Roles.User, ImageURLs: []string{"/new-history-image"}}}
	fromFrozen, err := newExecutor().Execute(ctx, changedHistory, nil)
	if err != nil || fromFrozen.Suspend || gateway.preparedCount != 1 {
		t.Fatalf("changed history recomputed frozen input: %+v prepares=%d err=%v", fromFrozen, gateway.preparedCount, err)
	}
	prepared, err := repo.GetPreparedInput(ctx, "owner-a", op.PreparedInputID)
	if err != nil {
		t.Fatal(err)
	}
	var frozen map[string]any
	if err := json.Unmarshal(prepared.NormalizedInput, &frozen); err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 || frozen["quality"] != "original" {
		t.Fatalf("scoped edit mutated frozen approval: calls=%d input=%v", resolverCalls, frozen)
	}
	if _, err := service.Decide(ctx, executiongrant.Principal{OwnerID: "owner-a", ActorID: "human-a", Source: executiongrant.AgentPending},
		op.GrantRequestID, executiongrant.Approve); err == nil {
		t.Fatal("agent source decided its own grant")
	}
	if _, err := service.Decide(ctx, executiongrant.Principal{OwnerID: "owner-a", ActorID: "human-a", Source: executiongrant.HumanBrowser},
		op.GrantRequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	// Even approved, the turn that left never submits it: the pending-approval
	// service owns the request from the moment it was detached.
	if again, err := newExecutor().Execute(ctx, command, nil); err != nil || again.TaskID != "" || gateway.submitCount != 0 {
		t.Fatalf("a detached operation was submitted by its old turn: %+v submits=%d err=%v", again, gateway.submitCount, err)
	}
	// A second step of the same turn is detached on its own; a denial leaves
	// nothing submitted either.
	deniedCommand := command
	deniedCommand.Call.ID = "call-denied"
	pending, err := newExecutor().Execute(ctx, deniedCommand, nil)
	if err != nil || pending.Suspend || awaitedMessage(pending.AgentContent) != waiting {
		t.Fatalf("second pending: %+v %v", pending, err)
	}
	deniedOp, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-denied")
	if err != nil || !deniedOp.Detached {
		t.Fatalf("denial target: %+v %v", deniedOp, err)
	}
	if _, err := service.Decide(ctx, executiongrant.Principal{OwnerID: "owner-a", ActorID: "human-a", Source: executiongrant.HumanBrowser},
		deniedOp.GrantRequestID, executiongrant.Deny); err != nil {
		t.Fatal(err)
	}
	denied, err := newExecutor().Execute(ctx, deniedCommand, nil)
	if err != nil || denied.Suspend || gateway.submitCount != 0 {
		t.Fatalf("denied operation resubmitted: %+v count=%d err=%v", denied, gateway.submitCount, err)
	}
}

func TestBoundAgentTaskRecoversAfterRestartWithChangedHarness(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bound-agent.db")
	repo, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner-a",
		chat.Message{ID: "bound-user-a", SessionID: "session-a", Role: "user", Content: "paint", CreatedAt: now},
		chat.Message{ID: "turn-a", SessionID: "session-a", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session-a", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session-a", Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
			State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	tool.IdempotencyKeyTemplate = "agent:{session_id}:{call_id}"
	offering := generation.Offering{ID: "fake/offering", Revision: "rev-a", ModelID: "fake-model",
		Capability: tool.FallbackCapability, Enabled: true, Timeout: time.Second, CreditsMetered: true,
		Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt}}}
	gateway := &grantGateway{fakeGateway: &fakeGateway{offerings: []generation.Offering{offering}}, repo: repo}
	// The automatic mode approves the step inside the turn, so the turn itself
	// submits and keeps the bound task.
	policy := approvalpolicy.New(
		application.Chat.ApprovalPolicy.DefaultMode, application.Chat.ApprovalPolicy.Modes,
		[]string{"text-to-3d", "image-to-3d", "retexture"},
		[]string{"text-to-image", "text-to-3d", "image-to-3d", "retexture"})
	service := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: policy, Modes: sessionMode{mode: "auto"}}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: []byte(`{"prompt":"paint"}`)},
		Tool: tool, Options: chat.TurnOptions{AIModel: "fake-model", Harness: &resolve.Snapshot{Selection: harness.DefaultSelection("owner-a", "session-a"), Status: resolve.StatusFree}}}
	first, err := generationtool.New(generationtool.Config{Harness: repo, Gateway: gateway, References: &fakeReferences{},
		Operations: repo, Grants: service, Chat: application.Chat,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := first.Execute(ctx, command, nil); err != nil || result.Suspend || result.TaskID == "" {
		t.Fatalf("prepare: %+v %v", result, err)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := repo.GetPreparedInput(ctx, "owner-a", op.PreparedInputID)
	if err != nil {
		t.Fatal(err)
	}
	task := generation.Task{ID: "accepted-agent-task", OwnerID: "owner-a", SessionID: "session-a",
		Capability: prepared.Capability, Offering: offering, Input: prepared.NormalizedInput,
		RequestHash:    generation.ExpectedRequestHash(offering.ID, prepared.NormalizedInput),
		IdempotencyKey: op.IdempotencyKey, State: generation.TaskSucceeded, Version: 1,
		CreatedAt: now, UpdatedAt: now,
		Admission: &generation.ExecutionAdmission{RequestID: op.GrantRequestID,
			PreparedInputID: op.PreparedInputID, PreparedInputHash: op.PreparedInputHash,
			OperationID: op.OperationID, Harness: op.Harness}}
	if _, replayed, err := repo.Create(ctx, task, generation.Event{TaskID: task.ID,
		Type: "task.submitted", Data: []byte(`{}`), CreatedAt: now}, 0); err != nil || replayed {
		t.Fatalf("persist accepted task: replay=%v err=%v", replayed, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	currentCatalogOffering := offering
	currentCatalogOffering.EstimatedCredits = 40
	currentCatalogOffering.CreditsMetered = false
	restartedGateway := &persistedGrantGateway{grantGateway: &grantGateway{fakeGateway: &fakeGateway{offerings: []generation.Offering{currentCatalogOffering}}, repo: repo}}
	grantReads := &flakyGrantRead{Repository: repo}
	opReads := &flakyOperationRead{OperationStore: repo}
	restarted, err := generationtool.New(generationtool.Config{Harness: repo, Gateway: restartedGateway, References: &fakeReferences{},
		Operations: opReads, Grants: &executiongrant.Service{Grants: grantReads, Prepared: repo}, Chat: application.Chat,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	changedSelection := command.Options.Harness.Selection
	changedSelection.OperationID = "new-choice"
	current, err := repo.CompareAndSwapHarnessSelection(ctx, changedSelection, 0)
	if err != nil {
		t.Fatal(err)
	}
	wrongBinding := command
	wrongSnapshot := *command.Options.Harness
	wrongSnapshot.Selection = current
	wrongBinding.Options.Harness = &wrongSnapshot
	if _, err := restarted.BoundTaskForCall(ctx, wrongBinding); !errors.Is(err, chat.ErrTurnCheckpointConflict) {
		t.Fatalf("bound replay accepted another selection: %v", err)
	}
	command.Options.Parameters = map[string]json.RawMessage{"size": json.RawMessage(`"changed"`)}
	boundID, err := restarted.BoundTaskForCall(ctx, command)
	if err != nil || boundID != string(task.ID) {
		t.Fatalf("recover bound identity: %q %v", boundID, err)
	}
	wrongIdentity := command
	wrongIdentity.FrozenIdempotencyKey = "different-paid-operation"
	if _, err := restarted.BoundTaskForCall(ctx, wrongIdentity); !errors.Is(err, chat.ErrTurnCheckpointConflict) {
		t.Fatalf("wrong operation identity was eligible for recovery: %v", err)
	}
	opReads.reads = 1
	unknown, err := restarted.Execute(ctx, command, nil)
	if err == nil || unknown.NoSubmission || unknown.TaskID != "" {
		t.Fatalf("temporary operation read wrongly proved no submission: %+v %v", unknown, err)
	}
	grantReads.reads = 1
	unknown, err = restarted.Execute(ctx, command, nil)
	if err == nil || unknown.NoSubmission || unknown.TaskID != "" {
		t.Fatalf("temporary grant read wrongly proved no submission: %+v %v", unknown, err)
	}
	restartedGateway.getErrors = 1
	transient, err := restarted.Execute(ctx, command, nil)
	if err == nil || !transient.AwaitTask || transient.TaskID != string(task.ID) {
		t.Fatalf("temporary task read lost original binding: %+v %v", transient, err)
	}
	callbackFailure, err := restarted.Execute(ctx, command, func(chat.ToolObservation) error {
		return errors.New("temporary observation failure")
	})
	if err == nil || !callbackFailure.AwaitTask || callbackFailure.TaskID != string(task.ID) {
		t.Fatalf("observation failure lost original binding: %+v %v", callbackFailure, err)
	}
	result, err := restarted.Execute(ctx, command, nil)
	if err != nil || result.TaskID != string(task.ID) || restartedGateway.submitCount != 0 || restartedGateway.preparedCount != 0 {
		t.Fatalf("bound recovery repeated paid call: result=%+v prepares=%d submits=%d err=%v",
			result, restartedGateway.preparedCount, restartedGateway.submitCount, err)
	}
	var resultContent map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &resultContent); err != nil {
		t.Fatalf("decode recovered task success JSON: %v", err)
	}
	if key := tool.ResultFields.EstimatedCredits; key == "" || resultContent[key] != float64(0) {
		t.Fatalf("recovered frozen metered zero estimate missing after current catalog changed: %s", result.AgentContent)
	}
}
