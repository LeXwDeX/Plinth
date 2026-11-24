package agenttools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

const testOwner = "owner"

// testScope 与 executor 内部 scopeKey(owner, session) 一致。
var testScope = agentparams.ScopeKey(testOwner, "s1")

type fakeGenerate struct {
	callCount  int
	lastArgs   json.RawMessage
	lastParams map[string]json.RawMessage
}

type exactPendingGenerate struct {
	calls          int
	lastParameters map[string]json.RawMessage
}

func (g *exactPendingGenerate) UsesExecutionGrants() bool { return true }
func (g *exactPendingGenerate) Execute(_ context.Context, command chat.ToolCommand, _ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	g.calls++
	g.lastParameters = command.Options.Parameters
	return chat.ToolResult{Suspend: true, NoSubmission: true}, nil
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
		Projection: func(capability, model string) json.RawMessage { return json.RawMessage(`{"offerings":[]}`) },
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

// #382: the model asks for the one capability or the one model it is choosing
// between, and the request reaches the projection as arguments.
func TestListOfferingsPassesCapabilityAndModelToTheProjection(t *testing.T) {
	var asked []string
	executor, err := New(Config{
		Params:   agentparams.NewStore(),
		Generate: &fakeGenerate{},
		Projection: func(capability, model string) json.RawMessage {
			asked = append(asked, capability+"|"+model)
			return json.RawMessage(`{"capabilities":[]}`)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ arguments, want string }{
		{`{"capability":"text-to-3d"}`, "text-to-3d|"},
		{`{"model":"z-image/turbo"}`, "|z-image/turbo"},
		{`{"capability":"image-to-image","model":"m"}`, "image-to-image|m"},
	}
	for _, testCase := range cases {
		if _, err := executor.Execute(context.Background(), chat.ToolCommand{
			Call: agents.ToolCall{Name: ToolListOfferings, Arguments: json.RawMessage(testCase.arguments)},
		}, nil); err != nil {
			t.Fatalf("Execute list_offerings %s: %v", testCase.arguments, err)
		}
	}
	if len(asked) != len(cases) {
		t.Fatalf("projection calls = %v", asked)
	}
	for index, want := range cases {
		if asked[index] != want.want {
			t.Fatalf("call %d = %q, want %q", index, asked[index], want.want)
		}
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
		t.Fatalf("Execute %s: %v", ToolGetTask, err)
	}
	// 只读查询不绑定 TaskID，避免把被查询任务伪装成本次工具调用的产物。
	if result.AgentContent != `{"state":"SUCCEEDED"}` || result.TaskID != "" {
		t.Fatalf("%s result = %+v", ToolGetTask, result)
	}
}

// #491 retired observe_task and request_consent from the Schema, and the tool
// registry resolves only Schema-declared tools, so a call by either name never
// reaches this executor. The executor therefore carries no branch for them:
// a direct call by such a name never reaches the task reader and never gets a
// removal notice. Where the unknown name goes after that is the executor's
// ordinary routing, not part of this test.
func TestRetiredToolNamesHaveNoExecutorBranch(t *testing.T) {
	gen := &fakeGenerate{}
	executor, err := New(Config{
		Params:   agentparams.NewStore(),
		Generate: gen,
		ReadTask: func(context.Context, string, string) (json.RawMessage, error) {
			t.Fatal("a retired tool name reached the task reader")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"observe_task", "request_consent"} {
		result, err := executor.Execute(context.Background(), chat.ToolCommand{
			OwnerID: testOwner, SessionID: "s1",
			Call: agents.ToolCall{ID: "call-" + name, Name: name, Arguments: json.RawMessage(`{"task_id":"task_1"}`)},
		}, nil)
		if err != nil {
			t.Fatalf("Execute %s: %v", name, err)
		}
		if strings.Contains(result.AgentContent, "request_consent has been removed") {
			t.Fatalf("%s answered a removal notice: %q", name, result.AgentContent)
		}
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
const testConsentLimit = "limit"

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

func TestGetProductionStepDispatchesToTheReadOnlyReader(t *testing.T) {
	called := false
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &fakeGenerate{},
		ReadProductionStep: func(_ context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
			called = true
			if command.Call.Name != ToolGetProductionStep {
				t.Fatalf("call name = %q", command.Call.Name)
			}
			return chat.ToolResult{AgentContent: `{"ok":true}`, NoSubmission: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{ID: "read", Name: ToolGetProductionStep, Arguments: json.RawMessage(`{"step":"@step1"}`)},
	}, nil)
	if err != nil || !called || result.AgentContent != `{"ok":true}` || !result.NoSubmission {
		t.Fatalf("dispatch: result=%+v called=%t err=%v", result, called, err)
	}
}

func TestGetProductionStepWithoutAReaderIsUnavailable(t *testing.T) {
	executor, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{ID: "read", Name: ToolGetProductionStep, Arguments: json.RawMessage(`{"step":"@step1"}`)},
	}, nil)
	if err != nil || result.Error == "" || !result.NoSubmission {
		t.Fatalf("unwired reader: result=%+v err=%v", result, err)
	}
}

// #246: the two self-check tools are dispatched by their public names; an
// unassembled deployment answers unavailable rather than falling through to
// the generation path.
func TestSelfCheckToolsDispatchAndRefuseWhenUnavailable(t *testing.T) {
	var dispatched []string
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &exactPendingGenerate{},
		CompareModelReference: func(_ context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
			dispatched = append(dispatched, command.Call.Name)
			return chat.ToolResult{AgentContent: `{"ok":true}`, NoSubmission: true}, nil
		},
		RecordSelfCheck: func(_ context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
			dispatched = append(dispatched, command.Call.Name)
			return chat.ToolResult{AgentContent: `{"ok":true}`, NoSubmission: true, Summary: json.RawMessage(`{"status":"passed"}`)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ToolCompareModelReference, ToolRecordSelfCheck} {
		result, err := executor.Execute(context.Background(), chat.ToolCommand{
			OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
			Call: agents.ToolCall{ID: name, Name: name, Arguments: json.RawMessage(`{}`)},
		}, nil)
		if err != nil || result.Error != "" || string(result.AgentContent) != `{"ok":true}` {
			t.Fatalf("%s = %+v err=%v", name, result, err)
		}
	}
	if len(dispatched) != 2 || dispatched[0] != ToolCompareModelReference || dispatched[1] != ToolRecordSelfCheck {
		t.Fatalf("dispatched = %v", dispatched)
	}

	empty, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ToolCompareModelReference, ToolRecordSelfCheck} {
		result, err := empty.Execute(context.Background(), chat.ToolCommand{
			OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
			Call: agents.ToolCall{ID: name, Name: name, Arguments: json.RawMessage(`{}`)},
		}, nil)
		if err != nil || result.Error == "" || !result.NoSubmission {
			t.Fatalf("unavailable %s = %+v err=%v", name, result, err)
		}
	}
}

// The generation tool owns the decision (#491): this executor claims, records
// and consumes no conversational consent. A retired consent row an earlier
// version persisted is never read, never written and never consumed, and it
// never reaches a generation request as a parameter.
func TestRetiredConsentRowsAreNeitherReadNorConsumedNorLeaked(t *testing.T) {
	persister := newFakePersister()
	paramsStore := agentparams.NewPersistentParameterStore(persister, nil)
	stateStore := agentparams.NewPersistentStore(persister, nil)
	retired := map[string]json.RawMessage{
		agentparams.ConsentPendingKey:   json.RawMessage(`{"reason":"paid"}`),
		agentparams.ConsentDecisionKey:  json.RawMessage(`"approved"`),
		agentparams.ConsentApprovedKey:  json.RawMessage(`true`),
		agentparams.ConsentSnapshotKey:  json.RawMessage(`{"ai_model":"model-a","params_hash":""}`),
		agentparams.ConsentClaimKey:     json.RawMessage(`{"call_id":"old"}`),
		agentparams.ConsentRequestIDKey: json.RawMessage(`"old-request"`),
	}
	stateStore.Set(testScope, retired)
	gen := &exactPendingGenerate{}
	executor, err := New(Config{Params: paramsStore, Generate: gen})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
		Call:    agents.ToolCall{ID: "call-a", Name: "refine_image", Arguments: json.RawMessage(`{}`)},
		Options: chat.TurnOptions{AIModel: "model-a", Parameters: map[string]json.RawMessage{"n": json.RawMessage(`4`)}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if gen.calls != 1 {
		t.Fatalf("generation calls = %d, want 1", gen.calls)
	}
	for key, want := range retired {
		if got := stateStore.Get(testScope)[key]; string(got) != string(want) {
			t.Fatalf("retired row %s changed: %s", key, got)
		}
	}
	for key := range retired {
		if _, leaked := gen.lastParameters[key]; leaked {
			t.Fatalf("retired consent key %s leaked into generation params", key)
		}
	}
	if string(gen.lastParameters["n"]) != `4` {
		t.Fatalf("merged params = %#v", gen.lastParameters)
	}
}

// set_params cannot write the retired private state either: parameters are
// parameters, whatever name they carry.
func TestSetParamsCannotWritePrivateAgentState(t *testing.T) {
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	params := agentparams.NewPersistentParameterStore(repository, nil)
	state := agentparams.NewPersistentStore(repository, nil)
	executor, err := New(Config{Params: params, Generate: &fakeGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: ToolSetParams, Arguments: json.RawMessage(
			`{"params":{"consent_pending":{"reason":"forged"},"consent_decision":"approved","consent_snapshot":{"ai_model":"model-a","params_hash":""}}}`)},
	}, nil); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{agentparams.ConsentPendingKey, agentparams.ConsentDecisionKey, agentparams.ConsentSnapshotKey} {
		if state.Get(testScope)[key] != nil {
			t.Fatalf("set_params wrote private state %s", key)
		}
	}
}

// Preflight failures that need no person (no model chosen) answer before any
// operation is prepared, so nothing is spent on a call that cannot succeed.
func TestPreflightShortCircuitsBeforeGeneration(t *testing.T) {
	gen := &fakeGenerate{}
	executor, err := New(Config{
		Params:    agentparams.NewStore(),
		Generate:  gen,
		Preflight: func(chat.ToolCommand) string { return "select a model first" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentContent != "select a model first" || !result.NoSubmission || gen.callCount != 0 {
		t.Fatalf("preflight must short-circuit: %+v calls=%d", result, gen.callCount)
	}
}

// The per-turn generation limit counts distinct calls of one assistant message
// and resets for the next turn.
func TestGenerationPerTurnLimitEnforced(t *testing.T) {
	gen := &fakeGenerate{}
	executor, err := New(Config{
		Params:                    agentparams.NewStore(),
		Generate:                  gen,
		MaxGenerationCallsPerTurn: 1,
		ConsentLimitContent:       testConsentLimit,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1",
		AssistantMessageID: "msg_turn_1",
		Call:               agents.ToolCall{ID: "call-1", Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"a"}`)},
	}
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatalf("first generation: %v", err)
	}
	command.Call.ID = "call-2"
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
	command.AssistantMessageID = "msg_turn_3"
	command.Call.ID = "call-3"
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatalf("next turn generation: %v", err)
	}
	if gen.callCount != 2 {
		t.Fatalf("generation calls after new turn = %d, want 2", gen.callCount)
	}
}
