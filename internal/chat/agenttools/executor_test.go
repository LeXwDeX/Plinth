package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

const testOwner = "owner"

// testScope 与 executor 内部 scopeKey(owner, session) 一致。
var testScope = agentparams.ScopeKey(testOwner, "s1")

func testConsentRequestID(t *testing.T, store *agentparams.Store, scope string) string {
	t.Helper()
	var requestID string
	if err := json.Unmarshal(store.Get(scope)[agentparams.ConsentRequestIDKey], &requestID); err != nil || requestID == "" {
		t.Fatalf("missing consent request ID: %q, %v", requestID, err)
	}
	return requestID
}

type fakeGenerate struct {
	callCount  int
	lastArgs   json.RawMessage
	lastParams map[string]json.RawMessage
}

type exactPendingGenerate struct{ calls int }

func (g *exactPendingGenerate) UsesExecutionGrants() bool { return true }
func (g *exactPendingGenerate) Execute(_ context.Context, _ chat.ToolCommand, _ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	g.calls++
	return chat.ToolResult{Suspend: true, NoSubmission: true}, nil
}

func TestExactGrantGenerationNeverClaimsLegacyConsent(t *testing.T) {
	store := agentparams.NewStore()
	store.Set(testScope, map[string]json.RawMessage{
		ConsentDecisionKey: json.RawMessage(`"approved"`),
	})
	generate := &exactPendingGenerate{}
	executor, err := New(Config{Params: store, Generate: generate,
		ConsentState: func(string) map[string]json.RawMessage { return store.Get(testScope) },
		ConsentClear: func(string) {},
		ConsentClaim: func(string, string, string, json.RawMessage) (string, bool, error) {
			t.Fatal("legacy consent claim used for exact execution grant")
			return "", false, nil
		},
		ConsentAccepted: func(string, string, string) error { t.Fatal("legacy consent accepted"); return nil },
		ConsentRelease:  func(string, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	advisory, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "intent", Name: ToolRequestConsent, Arguments: json.RawMessage(`{"credits":10}`)},
	}, nil)
	if err != nil || advisory.Suspend || store.Get(testScope)[ConsentPendingKey] != nil {
		t.Fatalf("legacy request_consent became pending approval: %+v %v", advisory, err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: "refine_image", Arguments: json.RawMessage(`{}`)},
	}, nil)
	if err != nil || !result.Suspend || generate.calls != 1 {
		t.Fatalf("exact grant handoff: result=%+v calls=%d err=%v", result, generate.calls, err)
	}
}

func TestExactGrantTurnLimitCountsUniqueCallsAfterConsentWait(t *testing.T) {
	generate := &exactPendingGenerate{}
	executor, err := New(Config{Params: agentparams.NewStore(), Generate: generate,
		MaxGenerationCallsPerTurn: 4, ConsentLimitContent: "limit"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(id string) chat.ToolResult {
		result, err := executor.Execute(context.Background(), chat.ToolCommand{
			OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
			ConsentWaitDeadline: time.Now().Add(time.Minute),
			Call:                agents.ToolCall{ID: id, Name: "refine_image", Arguments: json.RawMessage(`{}`)},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, id := range []string{"call-1", "call-2", "call-3", "call-4"} {
		if result := call(id); !result.Suspend {
			t.Fatalf("%s blocked early: %+v", id, result)
		}
	}
	if result := call("call-5"); result.Suspend || result.AgentContent != "limit" || generate.calls != 4 {
		t.Fatalf("fifth distinct call passed: %+v calls=%d", result, generate.calls)
	}
	if result := call("call-4"); !result.Suspend || generate.calls != 5 {
		t.Fatalf("same call resume counted again: %+v calls=%d", result, generate.calls)
	}
}

type acceptedThenFailedGenerate struct {
	submits int
}

type preSubmitRejectThenAccept struct{ calls int }

func (f *preSubmitRejectThenAccept) Execute(_ context.Context, _ chat.ToolCommand, observe func(chat.ToolObservation) error) (chat.ToolResult, error) {
	f.calls++
	if f.calls == 1 {
		return chat.ToolResult{NoSubmission: true}, errors.New("input rejected before submit")
	}
	if err := observe(chat.ToolObservation{Registered: true, TaskID: "accepted-task"}); err != nil {
		return chat.ToolResult{}, err
	}
	return chat.ToolResult{TaskID: "accepted-task"}, nil
}

func (f *acceptedThenFailedGenerate) Execute(
	_ context.Context, command chat.ToolCommand, observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	if command.BoundTaskID != "" {
		return chat.ToolResult{TaskID: command.BoundTaskID}, nil
	}
	f.submits++
	if err := observe(chat.ToolObservation{Registered: true, TaskID: "accepted-task"}); err != nil {
		return chat.ToolResult{}, err
	}
	return chat.ToolResult{TaskID: "accepted-task"}, errors.New("task failed after acceptance")
}

func (f *fakeGenerate) Execute(
	_ context.Context,
	command chat.ToolCommand,
	_ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	f.callCount++
	f.lastArgs = command.Call.Arguments
	f.lastParams = command.Options.Parameters
	return chat.ToolResult{TaskID: "task_x"}, nil
}

func newExecutor(t *testing.T, gen chat.ToolExecutor) (*Executor, *agentparams.Store) {
	t.Helper()
	store := agentparams.NewStore()
	executor, err := New(Config{
		Params:     store,
		Generate:   gen,
		Projection: func() json.RawMessage { return json.RawMessage(`{"offerings":[]}`) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return executor, store
}

func TestSetParamsStores(t *testing.T) {
	executor, store := newExecutor(t, &fakeGenerate{})
	_, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: ToolSetParams, Arguments: json.RawMessage(`{"params":{"size":"1024x1024"}}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute set_params: %v", err)
	}
	if string(store.Get(testScope)["size"]) != `"1024x1024"` {
		t.Fatalf("store size = %s", store.Get(testScope)["size"])
	}
}

func TestListOfferingsReturnsProjection(t *testing.T) {
	executor, _ := newExecutor(t, &fakeGenerate{})
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		Call: agents.ToolCall{Name: ToolListOfferings},
	}, nil)
	if err != nil {
		t.Fatalf("Execute list_offerings: %v", err)
	}
	if result.AgentContent != `{"offerings":[]}` {
		t.Fatalf("projection = %s", result.AgentContent)
	}
}

func TestGetTaskReturnsState(t *testing.T) {
	store := agentparams.NewStore()
	executor, err := New(Config{
		Params:   store,
		Generate: &fakeGenerate{},
		ReadTask: func(_ context.Context, ownerID, taskID string) (json.RawMessage, error) {
			if ownerID != "owner" || taskID != "task_1" {
				t.Fatalf("ReadTask(%q, %q)", ownerID, taskID)
			}
			return json.RawMessage(`{"state":"SUCCEEDED"}`), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner",
		Call:    agents.ToolCall{Name: ToolGetTask, Arguments: json.RawMessage(`{"task_id":"task_1"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute get_task: %v", err)
	}
	// 只读查询不绑定 TaskID，避免把被查询任务伪装成本次工具调用的产物。
	if result.AgentContent != `{"state":"SUCCEEDED"}` || result.TaskID != "" {
		t.Fatalf("get_task result = %+v", result)
	}
}

func TestRequestConsentCallsConsent(t *testing.T) {
	store := agentparams.NewStore()
	var consentScope string
	executor, err := New(Config{
		Params:   store,
		Generate: &fakeGenerate{},
		Consent: func(command chat.ToolCommand, _ json.RawMessage, _ json.RawMessage) error {
			consentScope = agentparams.ScopeKey(command.OwnerID, command.SessionID)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"credits":10}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute request_consent: %v", err)
	}
	if consentScope != testScope {
		t.Fatalf("consent scope = %q", consentScope)
	}
	if result.AgentContent == "" {
		t.Fatalf("expected consent acknowledgement")
	}
}

func TestGeneratePassesHarnessParamsToGeneration(t *testing.T) {
	gen := &fakeGenerate{}
	executor, store := newExecutor(t, gen)
	store.Set(testScope, map[string]json.RawMessage{
		"size":    json.RawMessage(`"1024x1024"`),
		"quality": json.RawMessage(`"low"`),
	})
	_, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{
			Name:      "generate_image",
			Arguments: json.RawMessage(`{"prompt":"a cube","quality":"high"}`),
		},
		Options: chat.TurnOptions{Parameters: map[string]json.RawMessage{
			"quality": json.RawMessage(`"highest"`),
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Execute generate: %v", err)
	}
	if string(gen.lastParams["size"]) != `"1024x1024"` {
		t.Fatalf("harness size missing: %s", gen.lastParams["size"])
	}
	if string(gen.lastParams["quality"]) != `"highest"` {
		t.Fatalf("explicit turn quality must win: %s", gen.lastParams["quality"])
	}
	if string(gen.lastArgs) != `{"prompt":"a cube","quality":"high"}` {
		t.Fatalf("tool arguments changed: %s", gen.lastArgs)
	}
}

// consentTestContents 与 Product Schema chat.agent.consent_* 测试文案一致。
const (
	testConsentApproved = "approved"
	testConsentDenied   = "denied"
	testConsentWaiting  = "waiting"
	testConsentRequired = "required"
	testConsentLimit    = "limit"
)

func consentExecutor(t *testing.T, gen chat.ToolExecutor, state *agentparams.Store, wait time.Duration) *Executor {
	t.Helper()
	executor, err := New(Config{
		Params:                 agentparams.NewStore(),
		Generate:               gen,
		ConsentApprovedContent: testConsentApproved,
		ConsentDeniedContent:   testConsentDenied,
		ConsentWaitingContent:  testConsentWaiting,
		ConsentBlockedContent:  "blocked",
		ConsentRequiredContent: testConsentRequired,
		ConsentLimitContent:    testConsentLimit,
		ConsentState:           state.Get,
		ConsentClear: func(scope string) {
			state.Delete(scope, ConsentPendingKey, ConsentDecisionKey, ConsentApprovedKey)
		},
		ConsentWaitTimeout: wait,
		Consent: func(_ chat.ToolCommand, request json.RawMessage, _ json.RawMessage) error {
			state.Set(testScope, map[string]json.RawMessage{
				ConsentPendingKey:  request,
				ConsentDecisionKey: json.RawMessage(`"pending"`),
				ConsentApprovedKey: json.RawMessage(`false`),
			})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return executor
}

func pendingConsentState(scope string) *agentparams.Store {
	state := agentparams.NewStore()
	state.Set(scope, map[string]json.RawMessage{
		ConsentPendingKey:  json.RawMessage(`{"reason":"extra credits"}`),
		ConsentDecisionKey: json.RawMessage(`"pending"`),
		ConsentApprovedKey: json.RawMessage(`false`),
	})
	return state
}

func TestGenerateBlockedWhileConsentPending(t *testing.T) {
	gen := &fakeGenerate{}
	executor := consentExecutor(t, gen, pendingConsentState(testScope), 0)
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute generate: %v", err)
	}
	if gen.lastArgs != nil {
		t.Fatalf("generation must not proceed without approval, args = %s", gen.lastArgs)
	}
	if result.AgentContent == "" {
		t.Fatalf("expected blocked-consent content for the agent")
	}
}

func TestGenerateBlockedAfterConsentDenied(t *testing.T) {
	gen := &fakeGenerate{}
	state := pendingConsentState(testScope)
	state.Set(testScope, map[string]json.RawMessage{
		ConsentDecisionKey: json.RawMessage(`"denied"`),
	})
	executor := consentExecutor(t, gen, state, 0)
	_, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute generate: %v", err)
	}
	if gen.lastArgs != nil {
		t.Fatalf("denied consent must not generate, args = %s", gen.lastArgs)
	}
}

func TestGenerateProceedsAfterConsentApproved(t *testing.T) {
	gen := &fakeGenerate{}
	state := pendingConsentState(testScope)
	state.Set(testScope, map[string]json.RawMessage{
		ConsentDecisionKey: json.RawMessage(`"approved"`),
		ConsentApprovedKey: json.RawMessage(`true`),
	})
	executor := consentExecutor(t, gen, state, 0)
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute generate: %v", err)
	}
	if result.TaskID != "task_x" {
		t.Fatalf("approved generation should reach the generate tool, got %+v", result)
	}
}

// 付费生成必须先持有批准：从未请求同意时服务端直接拒绝，
// 不依赖 LLM 自觉调用 request_consent。
func TestGenerateRequiresConsentWithoutPendingRequest(t *testing.T) {
	gen := &fakeGenerate{}
	executor := consentExecutor(t, gen, agentparams.NewStore(), 0)
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute generate: %v", err)
	}
	if gen.lastArgs != nil {
		t.Fatalf("generation must not proceed without consent, args = %s", gen.lastArgs)
	}
	if result.AgentContent != testConsentRequired {
		t.Fatalf("content = %q", result.AgentContent)
	}
}

// 批准一次性消费：一次批准只放行一次付费生成，随后同意状态被清除，
// 后续生成必须重新请求同意，防止一次批准永久放行。
func TestApprovedConsentIsConsumedBySingleGeneration(t *testing.T) {
	gen := &fakeGenerate{}
	state := pendingConsentState(testScope)
	state.Set(testScope, map[string]json.RawMessage{
		ConsentDecisionKey: json.RawMessage(`"approved"`),
		ConsentApprovedKey: json.RawMessage(`true`),
	})
	executor := consentExecutor(t, gen, state, 0)
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
	}
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatalf("first generation: %v", err)
	}
	if remaining := state.Get(testScope); remaining != nil &&
		(remaining[ConsentPendingKey] != nil || remaining[ConsentDecisionKey] != nil) {
		t.Fatalf("consent must be consumed after one generation: %#v", remaining)
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("second generation: %v", err)
	}
	if gen.callCount != 1 {
		t.Fatalf("generation calls = %d, want 1", gen.callCount)
	}
	if result.AgentContent != testConsentRequired {
		t.Fatalf("content = %q", result.AgentContent)
	}
}

func TestAcceptedFailureCannotReuseConsentAndSameCallRecovers(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	state := agentparams.NewPersistentStore(repository, nil)
	gen := &acceptedThenFailedGenerate{}
	executor, err := New(Config{
		Params:   agentparams.NewStore(),
		Generate: gen,
		Preflight: func(command chat.ToolCommand) string {
			if command.Options.AIModel == "" {
				return "select model"
			}
			return ""
		},
		ConsentState:           state.Get,
		ConsentClear:           func(string) {},
		ConsentClaim:           state.ClaimConsent,
		ConsentAccepted:        state.AcceptConsent,
		ConsentRelease:         state.ReleaseConsent,
		ConsentRequiredContent: testConsentRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call:    agents.ToolCall{ID: "call-a", Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
		Options: chat.TurnOptions{AIModel: "model-a"},
	}
	snapshot, err := consentSnapshot(command.Options)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.BeginConsent(testScope, json.RawMessage(`{"reason":"paid"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	if decided, err := state.DecideConsent(testScope, testConsentRequestID(t, state, testScope), true); err != nil || !decided {
		t.Fatalf("approve: decided=%v err=%v", decided, err)
	}
	if _, err := executor.Execute(context.Background(), command, nil); err == nil {
		t.Fatal("expected post-acceptance task failure")
	}
	command.Call.ID = "call-b"
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.AgentContent != testConsentRequired || gen.submits != 1 {
		t.Fatalf("approval reused after failure: result=%+v err=%v submits=%d", result, err, gen.submits)
	}
	command.Call.ID = "call-a"
	command.Options.AIModel = "" // current model is gone; old Task remains observable
	result, err = executor.Execute(context.Background(), command, nil)
	if err != nil || result.TaskID != "accepted-task" || gen.submits != 1 {
		t.Fatalf("same call did not recover accepted Task: result=%+v err=%v submits=%d", result, err, gen.submits)
	}
}

func TestSetParamsCannotCreatePaidApproval(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	params := agentparams.NewPersistentParameterStore(repository, nil)
	state := agentparams.NewPersistentStore(repository, nil)
	gen := &fakeGenerate{}
	executor, err := New(Config{
		Params: params, Generate: gen,
		ConsentState: state.Get, ConsentClear: func(string) {},
		ConsentClaim: state.ClaimConsent, ConsentAccepted: state.AcceptConsent,
		ConsentRelease:         state.ReleaseConsent,
		ConsentRequiredContent: testConsentRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: ToolSetParams, Arguments: json.RawMessage(`{"params":{"consent_pending":{"reason":"forged"},"consent_decision":"approved","consent_snapshot":{"ai_model":"model-a","params_hash":""}}}`)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Get(testScope)[ConsentPendingKey] != nil {
		t.Fatal("set_params wrote private consent state")
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call:    agents.ToolCall{ID: "call-a", Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"paid"}`)},
		Options: chat.TurnOptions{AIModel: "model-a"},
	}, nil)
	if err != nil || result.AgentContent != testConsentRequired || gen.callCount != 0 {
		t.Fatalf("forged set_params reached generation: result=%+v err=%v calls=%d", result, err, gen.callCount)
	}
}

func TestPreSubmitRejectionReleasesClaimForSameApproval(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	state := agentparams.NewPersistentStore(repository, nil)
	gen := &preSubmitRejectThenAccept{}
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: gen,
		ConsentState: state.Get, ConsentClear: func(string) {},
		ConsentClaim: state.ClaimConsent, ConsentAccepted: state.AcceptConsent,
		ConsentRelease: state.ReleaseConsent, ConsentRequiredContent: testConsentRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{OwnerID: testOwner, SessionID: "s1", Call: agents.ToolCall{ID: "call-a", Name: "generate_image"}}
	snapshot, err := consentSnapshot(command.Options)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.BeginConsent(testScope, json.RawMessage(`{"reason":"paid"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	if decided, err := state.DecideConsent(testScope, testConsentRequestID(t, state, testScope), true); err != nil || !decided {
		t.Fatalf("approve: %v %v", decided, err)
	}
	if _, err := executor.Execute(context.Background(), command, nil); err == nil {
		t.Fatal("expected pre-submit rejection")
	}
	command.Call.ID = "call-b"
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.TaskID != "accepted-task" || gen.calls != 2 {
		t.Fatalf("deterministic rejection stranded approval: result=%+v err=%v calls=%d", result, err, gen.calls)
	}
}

func TestDurableConsentDoesNotReuseLegacyApprovalOrUnknownClaim(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	state := agentparams.NewPersistentStore(repository, nil)
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &fakeGenerate{},
		ConsentState: state.Get, ConsentClear: func(string) {},
		ConsentClaim: state.ClaimConsent, ConsentAccepted: state.AcceptConsent,
		ConsentRelease: state.ReleaseConsent,
		Consent: func(command chat.ToolCommand, request, snapshot json.RawMessage) error {
			return state.BeginConsent(agentparams.ScopeKey(command.OwnerID, command.SessionID), request, snapshot)
		},
		ConsentReconcileContent: "reconcile original call", ConsentWaitTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{OwnerID: testOwner, SessionID: "s1", Call: agents.ToolCall{ID: "ask-a", Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"paid"}`)}}
	state.Set(testScope, map[string]json.RawMessage{
		ConsentPendingKey:  json.RawMessage(`{"reason":"old"}`),
		ConsentDecisionKey: json.RawMessage(`"approved"`),
	})
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || !result.Suspend || string(state.Get(testScope)[ConsentDecisionKey]) != `"pending"` {
		t.Fatalf("legacy approval was reused: result=%+v err=%v", result, err)
	}
	snapshot, err := consentSnapshot(command.Options)
	if err != nil {
		t.Fatal(err)
	}
	if decided, err := state.DecideConsent(testScope, testConsentRequestID(t, state, testScope), true); err != nil || !decided {
		t.Fatalf("approve: %v %v", decided, err)
	}
	if _, allowed, err := state.ClaimConsent(testScope, "call-a", "", snapshot); err != nil || !allowed {
		t.Fatalf("claim: %v %v", allowed, err)
	}
	result, err = executor.Execute(context.Background(), command, nil)
	if err != nil || result.AgentContent != "reconcile original call" || result.Suspend {
		t.Fatalf("unknown claim overwritten: result=%+v err=%v", result, err)
	}
	if state.Get(testScope)[agentparams.ConsentClaimKey] == nil {
		t.Fatal("unknown claim lost")
	}
}

func TestGenerationLimitDoesNotReserveUnusedConsent(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	state := agentparams.NewPersistentStore(repository, nil)
	gen := &fakeGenerate{}
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: gen,
		ConsentState: state.Get, ConsentClear: func(string) {},
		ConsentClaim: state.ClaimConsent, ConsentAccepted: state.AcceptConsent,
		ConsentRelease:            state.ReleaseConsent,
		ConsentRequiredContent:    testConsentRequired,
		ConsentLimitContent:       testConsentLimit,
		MaxGenerationCallsPerTurn: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: "generate_image"},
	}
	snapshot, err := consentSnapshot(command.Options)
	if err != nil {
		t.Fatal(err)
	}
	approve := func() {
		t.Helper()
		if err := state.BeginConsent(testScope, json.RawMessage(`{"reason":"paid"}`), snapshot); err != nil {
			t.Fatal(err)
		}
		if decided, err := state.DecideConsent(testScope, testConsentRequestID(t, state, testScope), true); err != nil || !decided {
			t.Fatalf("approve: %v %v", decided, err)
		}
	}
	approve()
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatal(err)
	}
	approve()
	command.Call.ID = "call-b"
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.AgentContent != testConsentLimit || gen.callCount != 1 {
		t.Fatalf("limit result=%+v err=%v calls=%d", result, err, gen.callCount)
	}
	if state.Get(testScope)[agentparams.ConsentClaimKey] != nil {
		t.Fatal("limit reserved unused approval")
	}
	command.AssistantMessageID = "turn-b"
	if _, err := executor.Execute(context.Background(), command, nil); err != nil || gen.callCount != 2 {
		t.Fatalf("unused approval was stranded: err=%v calls=%d", err, gen.callCount)
	}
}

func TestUnboundAcceptedTaskRecoversBeforeCurrentModelPreflight(t *testing.T) {
	gen := &acceptedThenFailedGenerate{}
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: gen,
		Preflight:    func(chat.ToolCommand) string { return "current model unavailable" },
		ConsentState: func(string) map[string]json.RawMessage { return nil },
		ConsentClear: func(string) {},
		ConsentClaim: func(_, _, _ string, snapshot json.RawMessage) (string, bool, error) {
			if len(snapshot) != 0 {
				t.Fatal("recovery unexpectedly reserved a new claim")
			}
			return "accepted-task", true, nil
		},
		ConsentAccepted: func(string, string, string) error { return nil },
		ConsentRelease:  func(string, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", Call: agents.ToolCall{ID: "call-a", Name: "generate_image"},
	}, nil)
	if err != nil || result.TaskID != "accepted-task" || gen.submits != 0 {
		t.Fatalf("unbound recovery blocked or resubmitted: result=%+v err=%v submits=%d", result, err, gen.submits)
	}
}

// 单轮生成次数上限独立于同意状态生效：即使批准持续有效，
// 超出 Schema 上限的调用也必须被拒绝。
func TestGenerationPerTurnLimitEnforced(t *testing.T) {
	gen := &fakeGenerate{}
	approved := map[string]json.RawMessage{
		ConsentPendingKey:  json.RawMessage(`{"reason":"x"}`),
		ConsentDecisionKey: json.RawMessage(`"approved"`),
		ConsentApprovedKey: json.RawMessage(`true`),
	}
	executor, err := New(Config{
		Params:                    agentparams.NewStore(),
		Generate:                  gen,
		ConsentState:              func(string) map[string]json.RawMessage { return approved },
		ConsentClear:              func(string) {},
		MaxGenerationCallsPerTurn: 1,
		ConsentLimitContent:       testConsentLimit,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		AssistantMessageID: "msg_turn_1",
		Call:               agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a"}`)},
	}
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatalf("first generation: %v", err)
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("second generation: %v", err)
	}
	if gen.callCount != 1 {
		t.Fatalf("generation calls = %d, want 1", gen.callCount)
	}
	if result.AgentContent != testConsentLimit {
		t.Fatalf("content = %q", result.AgentContent)
	}
	// 新轮次（不同 assistant 消息）重置计数。
	command.AssistantMessageID = "msg_turn_2"
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatalf("next turn generation: %v", err)
	}
	if gen.callCount != 2 {
		t.Fatalf("generation calls after new turn = %d, want 2", gen.callCount)
	}
}

// ConsentState 与 ConsentClear 必须成对接线：只给其一属于装配缺陷，
// 会导致一次性消费语义静默失效。
func TestNewRejectsUnpairedConsentState(t *testing.T) {
	_, err := New(Config{
		Params:       agentparams.NewStore(),
		Generate:     &fakeGenerate{},
		ConsentState: func(string) map[string]json.RawMessage { return nil },
	})
	if err == nil {
		t.Fatal("expected error for ConsentState without ConsentClear")
	}
	_, err = New(Config{
		Params:       agentparams.NewStore(),
		Generate:     &fakeGenerate{},
		ConsentClear: func(string) {},
	})
	if err == nil {
		t.Fatal("expected error for ConsentClear without ConsentState")
	}
}

// AGT-12 挂起语义：首次调用登记挂起（Suspend=true，不阻塞 worker）；
// 决定写入后的恢复重放直接返回对应文案；超过最长挂起时长仍无决定则
// 返回等待文案收束本轮（生成在批准前不会执行）。
func TestRequestConsentSuspendsAndResumesOnApproval(t *testing.T) {
	gen := &fakeGenerate{}
	state := agentparams.NewStore()
	executor := consentExecutor(t, gen, state, 2*time.Second)
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"extra credits"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute request_consent: %v", err)
	}
	if !result.Suspend {
		t.Fatalf("expected suspend marker, got %+v", result)
	}
	state.Set(testScope, map[string]json.RawMessage{
		ConsentDecisionKey: json.RawMessage(`"approved"`),
		ConsentApprovedKey: json.RawMessage(`true`),
	})
	resumed, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		ConsentWaitDeadline: time.Now().Add(time.Minute),
		Call:                agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"extra credits"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute resumed request_consent: %v", err)
	}
	if resumed.Suspend {
		t.Fatalf("resumed call must not suspend again: %+v", resumed)
	}
	if resumed.AgentContent != testConsentApproved {
		t.Fatalf("expected approved content, got %q", resumed.AgentContent)
	}
}

// 真机回归（dev-test）：批准只对快照目标有效（AGT-05）。旧批准的快照与
// 当前生成目标不一致（如批准后切换了模型）时，重放守卫不得短路——必须
// 重写待决状态与快照，重新请求授权；否则旧批准与 AGT-05 门禁互锁，
// 生成永远报「付费授权未生效」且前端不再弹窗（decision 已是 approved）。
func TestRequestConsentRebindsStaleApprovalToChangedTarget(t *testing.T) {
	state := agentparams.NewStore()
	executor := consentExecutor(t, &fakeGenerate{}, state, 2*time.Second)
	state.Set(testScope, map[string]json.RawMessage{
		ConsentPendingKey:  json.RawMessage(`{"reason":"old target"}`),
		ConsentDecisionKey: json.RawMessage(`"approved"`),
		ConsentApprovedKey: json.RawMessage(`true`),
		ConsentSnapshotKey: json.RawMessage(`{"ai_model":"","params_hash":"old"}`),
	})
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call:    agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"new target"}`)},
		Options: chat.TurnOptions{AIModel: "model-b"},
	}, nil)
	if err != nil {
		t.Fatalf("Execute request_consent: %v", err)
	}
	if !result.Suspend {
		t.Fatalf("stale approval must re-request consent, got %+v", result)
	}
	var decision string
	if err := json.Unmarshal(state.Get(testScope)[ConsentDecisionKey], &decision); err != nil {
		t.Fatalf("read decision: %v", err)
	}
	if decision != ConsentDecisionPending {
		t.Fatalf("decision must reset to pending for a new authorization, got %q", decision)
	}
}

// 快照一致的既有批准仍走 AGT-12 重放短路：挂起恢复重放不重写待决状态。
func TestRequestConsentKeepsApprovalMatchingSnapshot(t *testing.T) {
	state := agentparams.NewStore()
	executor := consentExecutor(t, &fakeGenerate{}, state, 2*time.Second)
	state.Set(testScope, map[string]json.RawMessage{
		ConsentPendingKey:  json.RawMessage(`{"reason":"current target"}`),
		ConsentDecisionKey: json.RawMessage(`"approved"`),
		ConsentApprovedKey: json.RawMessage(`true`),
		ConsentSnapshotKey: json.RawMessage(`{"ai_model":"model-b","params_hash":""}`),
	})
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		ConsentWaitDeadline: time.Now().Add(time.Minute),
		Call:                agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"current target"}`)},
		Options:             chat.TurnOptions{AIModel: "model-b"},
	}, nil)
	if err != nil {
		t.Fatalf("Execute request_consent: %v", err)
	}
	if result.Suspend {
		t.Fatalf("approval matching snapshot must short-circuit, got %+v", result)
	}
	if result.AgentContent != testConsentApproved {
		t.Fatalf("expected approved content, got %q", result.AgentContent)
	}
}

func TestRequestConsentResumesOnDenial(t *testing.T) {
	state := agentparams.NewStore()
	executor := consentExecutor(t, &fakeGenerate{}, state, 2*time.Second)
	if _, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"extra credits"}`)},
	}, nil); err != nil {
		t.Fatalf("Execute request_consent: %v", err)
	}
	state.Set(testScope, map[string]json.RawMessage{
		ConsentDecisionKey: json.RawMessage(`"denied"`),
	})
	resumed, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		ConsentWaitDeadline: time.Now().Add(time.Minute),
		Call:                agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"extra credits"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute resumed request_consent: %v", err)
	}
	if resumed.AgentContent != testConsentDenied {
		t.Fatalf("expected denied content, got %q", resumed.AgentContent)
	}
}

func TestRequestConsentExpiredDeadlineReturnsWaiting(t *testing.T) {
	state := agentparams.NewStore()
	executor := consentExecutor(t, &fakeGenerate{}, state, 2*time.Second)
	if _, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"extra credits"}`)},
	}, nil); err != nil {
		t.Fatalf("Execute request_consent: %v", err)
	}
	resumed, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		ConsentWaitDeadline: time.Now().Add(-time.Second),
		Call:                agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"extra credits"}`)},
	}, nil)
	if err != nil {
		t.Fatalf("Execute expired-deadline request_consent: %v", err)
	}
	if resumed.Suspend {
		t.Fatalf("expired deadline must not suspend: %+v", resumed)
	}
	if resumed.AgentContent != testConsentWaiting {
		t.Fatalf("expected waiting content on timeout, got %q", resumed.AgentContent)
	}
}

// 批准与生成目标绑定（AGT-05）：模型或参数与批准快照不一致时必须
// 重新请求同意；一致时正常放行并在成功后消费批准。
func TestConsentSnapshotBindsGenerationTarget(t *testing.T) {
	gen := &fakeGenerate{}
	state := pendingConsentState(testScope)
	params := map[string]json.RawMessage{"n": json.RawMessage(`4`)}
	snapshot, err := json.Marshal(struct {
		AIModel    string `json:"ai_model"`
		ParamsHash string `json:"params_hash"`
	}{AIModel: "model-a", ParamsHash: paramsHash(params)})
	if err != nil {
		t.Fatal(err)
	}
	state.Set(testScope, map[string]json.RawMessage{
		ConsentDecisionKey: json.RawMessage(`"approved"`),
		ConsentApprovedKey: json.RawMessage(`true`),
		ConsentSnapshotKey: snapshot,
	})
	executor := consentExecutor(t, gen, state, 0)
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
		Options: chat.TurnOptions{
			AIModel:    "model-b",
			Parameters: map[string]json.RawMessage{"n": json.RawMessage(`4`)},
		},
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentContent != testConsentRequired || gen.callCount != 0 {
		t.Fatalf("mismatched target must be blocked: %+v calls=%d", result, gen.callCount)
	}
	if decision := state.Get(testScope)[ConsentDecisionKey]; string(decision) != `"approved"` {
		t.Fatalf("mismatched target must not consume approval: %s", decision)
	}

	command.Options.AIModel = "model-a"
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatalf("matching target: %v", err)
	}
	if gen.callCount != 1 {
		t.Fatalf("generation calls = %d, want 1", gen.callCount)
	}
	if remaining := state.Get(testScope); remaining != nil && remaining[ConsentDecisionKey] != nil {
		t.Fatalf("consent must be consumed after success: %#v", remaining)
	}
}

// 预检先于同意门禁（AGT-04）：即使批准有效，无需用户交互即可判定
// 的失败（未选模型）也直接返回，批准不被无效调用消耗。
func TestPreflightRunsBeforeConsentGate(t *testing.T) {
	gen := &fakeGenerate{}
	state := pendingConsentState(testScope)
	state.Set(testScope, map[string]json.RawMessage{
		ConsentDecisionKey: json.RawMessage(`"approved"`),
		ConsentApprovedKey: json.RawMessage(`true`),
	})
	executor, err := New(Config{
		Params:                 agentparams.NewStore(),
		Generate:               gen,
		Preflight:              func(chat.ToolCommand) string { return "select a model first" },
		ConsentApprovedContent: testConsentApproved,
		ConsentDeniedContent:   testConsentDenied,
		ConsentWaitingContent:  testConsentWaiting,
		ConsentBlockedContent:  "blocked",
		ConsentRequiredContent: testConsentRequired,
		ConsentLimitContent:    testConsentLimit,
		ConsentState:           state.Get,
		ConsentClear: func(scope string) {
			state.Delete(scope, ConsentPendingKey, ConsentDecisionKey, ConsentApprovedKey)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentContent != "select a model first" || gen.callCount != 0 {
		t.Fatalf("preflight must short-circuit: %+v calls=%d", result, gen.callCount)
	}
	if decision := state.Get(testScope)[ConsentDecisionKey]; string(decision) != `"approved"` {
		t.Fatalf("preflight failure must not consume approval: %s", decision)
	}
}

// fakePersister 复现 B10 装配：paramsStore 与 agent 状态 store 共享
// 同一持久层（go_agent_state，owner+scope+key 行）。
type fakePersister struct {
	rows map[string]map[string]json.RawMessage
}

func newFakePersister() *fakePersister {
	return &fakePersister{rows: map[string]map[string]json.RawMessage{}}
}

func (p *fakePersister) SaveAgentState(_, scope, key string, value json.RawMessage) error {
	bucket := p.rows[scope]
	if bucket == nil {
		bucket = map[string]json.RawMessage{}
		p.rows[scope] = bucket
	}
	bucket[key] = append(json.RawMessage(nil), value...)
	return nil
}

func (p *fakePersister) DeleteAgentState(_, scope string, keys ...string) error {
	bucket := p.rows[scope]
	for _, key := range keys {
		delete(bucket, key)
	}
	return nil
}

func (p *fakePersister) LoadAgentState(_, scope string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for key, value := range p.rows[scope] {
		out[key] = append(json.RawMessage(nil), value...)
	}
	return out, nil
}

// 同意键写入共享持久层后，params 侧不得把它们当作生成参数合并，
// 批准快照的参数哈希也必须保持一致：request_consent → 批准 →
// refine_image 必须放行并消费批准（生产验收回归）。
func TestConsentKeysDoNotPolluteSharedStateParams(t *testing.T) {
	persister := newFakePersister()
	paramsStore := agentparams.NewPersistentStore(persister, nil)
	stateStore := agentparams.NewPersistentStore(persister, nil)
	scope := testScope
	gen := &fakeGenerate{}
	var lastSnapshot json.RawMessage
	executor, err := New(Config{
		Params:                 paramsStore,
		Generate:               gen,
		Preflight:              func(chat.ToolCommand) string { return "" },
		ConsentApprovedContent: testConsentApproved,
		ConsentDeniedContent:   testConsentDenied,
		ConsentWaitingContent:  testConsentWaiting,
		ConsentBlockedContent:  "blocked",
		ConsentRequiredContent: testConsentRequired,
		ConsentLimitContent:    testConsentLimit,
		ConsentState:           stateStore.Get,
		ConsentClear: func(scope string) {
			stateStore.Delete(scope, ConsentPendingKey, ConsentDecisionKey, ConsentApprovedKey, ConsentSnapshotKey)
		},
		Consent: func(_ chat.ToolCommand, request json.RawMessage, snapshot json.RawMessage) error {
			lastSnapshot = snapshot
			stateStore.Set(scope, map[string]json.RawMessage{
				ConsentPendingKey:  request,
				ConsentDecisionKey: json.RawMessage(`"pending"`),
				ConsentApprovedKey: json.RawMessage(`false`),
			})
			if len(snapshot) > 0 {
				stateStore.Set(scope, map[string]json.RawMessage{ConsentSnapshotKey: snapshot})
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
		Options: chat.TurnOptions{
			AIModel:    "model-a",
			Parameters: map[string]json.RawMessage{"n": json.RawMessage(`4`)},
		},
	}
	if _, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: command.OwnerID, SessionID: command.SessionID,
		Call:    agents.ToolCall{Name: ToolRequestConsent, Arguments: json.RawMessage(`{"reason":"paid"}`)},
		Options: command.Options,
	}, nil); err != nil {
		t.Fatalf("request_consent: %v", err)
	}
	if len(lastSnapshot) == 0 {
		t.Fatal("snapshot must be captured")
	}
	stateStore.Set(scope, map[string]json.RawMessage{
		ConsentDecisionKey: json.RawMessage(`"approved"`),
		ConsentApprovedKey: json.RawMessage(`true`),
	})
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatalf("refine after approval: %v", err)
	}
	if gen.callCount != 1 {
		t.Fatalf("generation calls = %d, want 1", gen.callCount)
	}
	if gen.lastParams["n"] == nil || string(gen.lastParams["n"]) != `4` {
		t.Fatalf("merged params = %#v", gen.lastParams)
	}
	for key := range consentStateKeys {
		if _, leaked := gen.lastParams[key]; leaked {
			t.Fatalf("consent key %s leaked into generation params", key)
		}
	}
	if remaining := stateStore.Get(scope); remaining != nil && remaining[ConsentDecisionKey] != nil {
		t.Fatalf("consent must be consumed: %#v", remaining)
	}
}

func TestListSessionModelsUsesTheServerHeldScope(t *testing.T) {
	var gotOwner, gotSession string
	executor, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}, MaxGenerationCallsPerTurn: 4,
		ListModels: func(_ context.Context, owner, session string) (json.RawMessage, error) {
			gotOwner, gotSession = owner, session
			return json.RawMessage(`{"models":[]}`), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "list", Name: ToolListSessionModels,
			Arguments: json.RawMessage(`{"owner_id":"someone-else","session_id":"s2"}`)},
	}, nil)
	if err != nil || result.AgentContent != `{"models":[]}` || result.Error != "" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if gotOwner != testOwner || gotSession != "s1" {
		t.Fatalf("listing scope = %q/%q; model-supplied scope must be ignored", gotOwner, gotSession)
	}
	unwired, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}, MaxGenerationCallsPerTurn: 4})
	if err != nil {
		t.Fatal(err)
	}
	result, _ = unwired.Execute(context.Background(), chat.ToolCommand{OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{ID: "list", Name: ToolListSessionModels, Arguments: json.RawMessage(`{}`)}}, nil)
	if result.Error == "" {
		t.Fatal("an unwired listing must fail closed")
	}
}

func TestUpdateSessionNotesUsesTheServerHeldScopeAndReportsTheBound(t *testing.T) {
	var gotOwner, gotSession, gotNotes string
	var failure error
	schemaContext := catalog.ChatContextSchema{
		SessionNotesSavedMessage:    "configured-saved",
		SessionNotesTooLongTemplate: "configured {length}/{maximum}",
	}
	executor, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}, MaxGenerationCallsPerTurn: 4,
		Context: schemaContext,
		UpdateNotes: func(_ context.Context, owner, session, notes string) (string, error) {
			gotOwner, gotSession, gotNotes = owner, session, notes
			return notes, failure
		}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(arguments string) chat.ToolResult {
		result, err := executor.Execute(context.Background(), chat.ToolCommand{
			OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
			Call: agents.ToolCall{ID: "notes", Name: ToolUpdateSessionNotes, Arguments: json.RawMessage(arguments)},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := call(`{"notes":"hero: red scarf","owner_id":"someone-else","session_id":"s2"}`)
	if result.Error != "" || result.AgentContent != "configured-saved" {
		t.Fatalf("result = %+v", result)
	}
	if gotOwner != testOwner || gotSession != "s1" || gotNotes != "hero: red scarf" {
		t.Fatalf("scope = %q/%q notes=%q; the model-supplied scope must be ignored", gotOwner, gotSession, gotNotes)
	}
	failure = &chat.SessionNotesTooLongError{Length: 30, Maximum: 20}
	if result := call(`{"notes":"x"}`); result.Error != "configured 30/20" {
		t.Fatalf("over-long notes need the Schema message with the bound: %+v", result)
	}
	failure = nil
	for _, bad := range []string{`{}`, `{"notes":7}`, `not json`} {
		if result := call(bad); result.Error == "" {
			t.Fatalf("%s must be refused", bad)
		}
	}
	if result := call(`{"notes":""}`); result.Error != "" || gotNotes != "" {
		t.Fatalf("an empty string clears the notes: %+v", result)
	}
	unwired, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}, MaxGenerationCallsPerTurn: 4})
	if err != nil {
		t.Fatal(err)
	}
	result, _ = unwired.Execute(context.Background(), chat.ToolCommand{OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{ID: "notes", Name: ToolUpdateSessionNotes, Arguments: json.RawMessage(`{"notes":"x"}`)}}, nil)
	if result.Error == "" {
		t.Fatal("unwired notes must fail closed")
	}
}

type captureOfferingCommand struct{ command chat.ToolCommand }

func (g *captureOfferingCommand) UsesExecutionGrants() bool { return true }
func (g *captureOfferingCommand) Execute(_ context.Context, command chat.ToolCommand, _ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	g.command = command
	return chat.ToolResult{NoSubmission: true}, nil
}

func TestScopedSetParamsAndExplicitProvenance(t *testing.T) {
	store := agentparams.NewStore()
	store.Set(testScope, map[string]json.RawMessage{"quality": json.RawMessage(`"legacy"`), "legacy-only": json.RawMessage(`true`)})
	generate := &captureOfferingCommand{}
	executor, err := New(Config{Params: store, Generate: generate, MaxGenerationCallsPerTurn: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, offering := range []string{"provider/image", "provider/model"} {
		args, _ := json.Marshal(map[string]any{"offering_id": offering, "params": map[string]any{"quality": offering}})
		result, err := executor.Execute(context.Background(), chat.ToolCommand{OwnerID: testOwner, SessionID: "s1", Call: agents.ToolCall{Name: ToolSetParams, Arguments: args}}, nil)
		if err != nil || result.Error != "" {
			t.Fatalf("set: %+v %v", result, err)
		}
	}
	if got := string(store.GetOffering(testScope, "provider/image")["quality"]); got != `"provider/image"` {
		t.Fatalf("wrong offering: %s", got)
	}
	if got := string(store.Get(testScope)["quality"]); got != `"legacy"` {
		t.Fatalf("set_params overwrote legacy: %s", got)
	}
	explicit := map[string]json.RawMessage{"quality": json.RawMessage(`"explicit"`)}
	_, err = executor.Execute(context.Background(), chat.ToolCommand{OwnerID: testOwner, SessionID: "s1", Call: agents.ToolCall{ID: "call", Name: "generate"}, Options: chat.TurnOptions{Parameters: explicit}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(generate.command.ExplicitParameters) != 1 || string(generate.command.ExplicitParameters["quality"]) != `"explicit"` || string(generate.command.Options.Parameters["legacy-only"]) != "true" {
		t.Fatalf("provenance lost: %+v", generate.command)
	}
	if len(generate.command.Options.Parameters) != 2 {
		t.Fatal("scoped namespace leaked into frozen command hash")
	}
	explicit["quality"][1] = 'X'
	if string(generate.command.ExplicitParameters["quality"]) != `"explicit"` {
		t.Fatal("explicit snapshot aliases caller")
	}
}
