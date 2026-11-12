package generationtool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #296: the parameters a tool result lists are the frozen ones, so an immediate
// result, one after the operation resumed and one recovered after a restart say
// the same thing, and an unset parameter never appears.

// parametersOf reads the parameters list of a tool result; present is false
// when the result carries no such key.
func parametersOf(t *testing.T, result chat.ToolResult) (list []any, present bool) {
	t.Helper()
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	raw, present := content["parameters"]
	if !present {
		return nil, false
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("parameters = %#v, want a list", raw)
	}
	return list, true
}

func reportedStep(label string) generation.Parameter {
	return generation.Parameter{Name: "steps", Label: label, Type: "number", Integer: true, Default: json.RawMessage(`20`)}
}

// reportedOffering has the prompt, a numeric parameter with a default and a seed
// without one, like the Rodin Offering the report was written for.
func reportedOffering(base generation.Offering) generation.Offering {
	base.Parameters = []generation.Parameter{
		{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt},
		reportedStep("Steps"),
		{Name: "seed", Label: "Seed", Type: "number", Integer: true},
	}
	return base
}

// echoGateway answers a read with the input the executor submitted, as the
// kernel's Task carries the frozen input.
type echoGateway struct{ *fakeGateway }

func (gateway *echoGateway) Get(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	task, err := gateway.fakeGateway.Get(ctx, owner, id)
	task.Input = gateway.submitted.Input
	return task, err
}

func TestToolResultListsTheFrozenParametersAndRecoveryReportsTheSame(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "refine_image" {
			tool = candidate
		}
	}
	if tool.ResultFields.Parameters == "" {
		t.Fatalf("refine_image declares no parameters key: %+v", tool.ResultFields)
	}
	offering := reportedOffering(generation.Offering{
		ID: "configured/offering", ModelID: "configured-model", ModelName: "Configured Model",
		Capability: tool.FallbackCapability, Enabled: true, Timeout: time.Second,
	})
	succeeded := generation.Task{ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded, Progress: 100, Offering: offering}
	gateway := &echoGateway{&fakeGateway{offerings: []generation.Offering{offering}, tasks: []generation.Task{succeeded, succeeded, succeeded}}}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat, TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: json.RawMessage(`{"prompt":"draw a cube"}`)},
		Tool: tool,
		// The person set the steps in the panel. Nobody set a seed.
		Options: chat.TurnOptions{AIModel: "configured-model", CreativeDomain: "image",
			Parameters: map[string]json.RawMessage{"steps": json.RawMessage(`30`)}},
	}
	first, err := executor.Execute(context.Background(), command, nil)
	if err != nil || first.TaskID == "" {
		t.Fatalf("first submission: %+v %v", first, err)
	}
	firstList, present := parametersOf(t, first)
	want := []any{map[string]any{"name": "steps", "label": "Steps", "value": float64(30), "source": "user"}}
	if !present || !reflect.DeepEqual(firstList, want) {
		t.Fatalf("parameters = %#v, want %#v (a seed that was never frozen must not appear)", firstList, want)
	}
	// The recovered observation reads the Task's frozen input, not the call.
	command.BoundTaskID = first.TaskID
	recovered, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	recoveredList, present := parametersOf(t, recovered)
	if !present || !reflect.DeepEqual(recoveredList, firstList) {
		t.Fatalf("recovered parameters = %#v, first = %#v", recoveredList, firstList)
	}
}

func TestToolResultOfATaskAtItsDefaultsListsNoParameters(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "refine_image" {
			tool = candidate
		}
	}
	offering := reportedOffering(generation.Offering{
		ID: "configured/offering", ModelID: "configured-model", ModelName: "Configured Model",
		Capability: tool.FallbackCapability, Enabled: true, Timeout: time.Second,
	})
	succeeded := generation.Task{ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded, Progress: 100, Offering: offering}
	gateway := &echoGateway{&fakeGateway{offerings: []generation.Offering{offering}, tasks: []generation.Task{succeeded}}}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat, TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", Tool: tool,
		Call:    agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: json.RawMessage(`{"prompt":"draw a cube"}`)},
		Options: chat.TurnOptions{AIModel: "configured-model", CreativeDomain: "image"},
	}, nil)
	if err != nil || result.TaskID == "" {
		t.Fatalf("submission: %+v %v", result, err)
	}
	list, present := parametersOf(t, result)
	if !present || len(list) != 0 {
		t.Fatalf("parameters = %#v (present=%v), want the empty list that says everything is at its default", list, present)
	}
}

func TestResumedAgentOperationReportsTheSameParametersAsTheFirstSubmission(t *testing.T) {
	executor, command, gateway, _ := policyHarness(t, sessionMode{mode: "auto"}, "refine_image", "text-to-image")
	gateway.offerings[0] = reportedOffering(gateway.offerings[0])
	command.Call.Arguments = []byte(`{"prompt":"draw a cube"}`)
	command.Options.Parameters = map[string]json.RawMessage{
		"steps": json.RawMessage(`30`), "seed": json.RawMessage(`7`),
	}
	first, err := executor.Execute(context.Background(), command, nil)
	if err != nil || first.TaskID == "" {
		t.Fatalf("first submission: %+v %v", first, err)
	}
	// The stored operation and its approved grant make the second call take the
	// resume path: no new input is prepared, the frozen one is read.
	second, err := executor.Execute(context.Background(), command, nil)
	if err != nil || second.TaskID == "" {
		t.Fatalf("resumed submission: %+v %v", second, err)
	}
	if gateway.preparedCount != 1 || gateway.submitCount != 2 {
		t.Fatalf("resume prepared=%d submitted=%d", gateway.preparedCount, gateway.submitCount)
	}
	want := []any{
		map[string]any{"name": "steps", "label": "Steps", "value": float64(30), "source": "user"},
		map[string]any{"name": "seed", "label": "Seed", "value": float64(7), "source": "user"},
	}
	for name, result := range map[string]chat.ToolResult{"first": first, "resumed": second} {
		list, present := parametersOf(t, result)
		if !present || !reflect.DeepEqual(list, want) {
			t.Fatalf("%s parameters = %#v, want %#v", name, list, want)
		}
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(first.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	if content["approval"] != "policy" {
		t.Fatalf("approval = %v: %s", content["approval"], first.AgentContent)
	}
	// A call that froze no seed lists none, whatever the Agent says it set.
	other := command
	other.Call.ID = "call-b"
	other.Options.Parameters = map[string]json.RawMessage{"steps": json.RawMessage(`30`)}
	result, err := executor.Execute(context.Background(), other, nil)
	if err != nil || result.TaskID == "" {
		t.Fatalf("second call: %+v %v", result, err)
	}
	list, present := parametersOf(t, result)
	if !present || !reflect.DeepEqual(list, want[:1]) {
		t.Fatalf("parameters without a seed = %#v, want only the steps", list)
	}
}

func TestBoundAgentTaskRecoveryReportsTheSameParametersAfterARestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bound-parameters.db")
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
		chat.Message{ID: "user-a", SessionID: "session-a", Role: "user", Content: "paint", CreatedAt: now},
		chat.Message{ID: "turn-a", SessionID: "session-a", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session-a", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session-a", Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
			State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "refine_image" {
			tool = candidate
		}
	}
	tool.IdempotencyKeyTemplate = "agent:{session_id}:{call_id}"
	tool.FallbackCapability = generation.TextToImage
	offering := reportedOffering(generation.Offering{ID: "fake/offering", Revision: "rev-a", ModelID: "fake-model",
		Capability: generation.TextToImage, Enabled: true, Timeout: time.Second})
	gateway := &grantGateway{fakeGateway: &fakeGateway{offerings: []generation.Offering{offering}}, repo: repo}
	policy := approvalpolicy.New(application.Chat.ApprovalPolicy.DefaultMode, application.Chat.ApprovalPolicy.Modes,
		[]string{"text-to-3d"}, []string{"text-to-image", "text-to-3d"})
	service := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: policy, Modes: sessionMode{mode: "auto"}}
	stopped := func(context.Context, string, string, string) (bool, error) { return false, nil }
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: []byte(`{"prompt":"draw a cube"}`)},
		Tool: tool, Options: chat.TurnOptions{AIModel: "fake-model",
			Parameters: map[string]json.RawMessage{"steps": json.RawMessage(`30`)}}}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{},
		Operations: repo, Grants: service, Chat: application.Chat, StopRequested: stopped})
	if err != nil {
		t.Fatal(err)
	}
	first, err := executor.Execute(ctx, command, nil)
	if err != nil || first.TaskID == "" {
		t.Fatalf("first submission: %+v %v", first, err)
	}
	firstList, present := parametersOf(t, first)
	want := []any{map[string]any{"name": "steps", "label": "Steps", "value": float64(30), "source": "user"}}
	if !present || !reflect.DeepEqual(firstList, want) {
		t.Fatalf("parameters = %#v, want %#v", firstList, want)
	}
	// The accepted Task is stored with the grant that admitted it; then the
	// process restarts and the Task is read back from the store.
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
	repo, err = sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	restartedGateway := &persistedGrantGateway{grantGateway: &grantGateway{
		fakeGateway: &fakeGateway{offerings: []generation.Offering{offering}}, repo: repo}}
	restarted, err := generationtool.New(generationtool.Config{Gateway: restartedGateway, References: &fakeReferences{},
		Operations: repo, Grants: &executiongrant.Service{Grants: repo, Prepared: repo}, Chat: application.Chat,
		StopRequested: stopped})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.Execute(ctx, command, nil)
	if err != nil || recovered.TaskID != string(task.ID) || restartedGateway.submitCount != 0 {
		t.Fatalf("bound recovery: result=%+v submits=%d err=%v", recovered, restartedGateway.submitCount, err)
	}
	recoveredList, present := parametersOf(t, recovered)
	if !present || !reflect.DeepEqual(recoveredList, firstList) {
		t.Fatalf("recovered parameters = %#v, first = %#v", recoveredList, firstList)
	}
}
