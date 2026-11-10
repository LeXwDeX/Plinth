package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func workLedgerPolicy(t *testing.T) catalog.ChatWorkLedgerSchema {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return loaded.Application().Chat.WorkLedger
}

// workVerifier resolves the attachments the tests put in history. It refuses a
// model claim for an image task, so SessionPick's preview check stays honest.
type workVerifier struct {
	byAsset map[string]chat.Attachment
}

func (verifier workVerifier) VerifyAttachment(
	_ context.Context, _, _ string, ref chat.AttachmentRef,
) (chat.Attachment, error) {
	if ref.AssetID != "" {
		attachment, found := verifier.byAsset[ref.AssetID]
		if found && (ref.Kind == "" || attachment.Kind == ref.Kind) {
			return attachment, nil
		}
	}
	if ref.TaskID != "" {
		for _, attachment := range verifier.byAsset {
			if attachment.TaskID == ref.TaskID && (ref.Kind == "" || attachment.Kind == ref.Kind) {
				return attachment, nil
			}
		}
	}
	return chat.Attachment{}, chat.ErrAttachmentRejected
}

type ledgerFixture struct {
	service     *chat.Service
	repository  *memoryChatRepository
	session     chat.Session
	policy      catalog.ChatWorkLedgerSchema
	tasks       []generation.Task
	detailTasks []generation.Task
	messages    []chat.Message
}

func newLedgerFixture(
	t *testing.T,
	adjust func(*chat.ServiceConfig),
	tasks []generation.Task,
	messages []chat.Message,
) *ledgerFixture {
	t.Helper()
	fixture := &ledgerFixture{
		repository: newMemoryChatRepository(),
		policy:     workLedgerPolicy(t),
		tasks:      tasks,
		messages:   messages,
	}
	fixture.service = newChatServiceWith(t, fixture.repository, func(config *chat.ServiceConfig) {
		config.PlaceholderHealing.PromptArgument = "prompt"
		config.WorkLedger = fixture.policy
		config.Attachments = workVerifier{byAsset: map[string]chat.Attachment{
			"asset-1":   {Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, AssetID: "asset-1", TaskID: "task-1"},
			"asset-2":   {Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, AssetID: "asset-2", TaskID: "task-2"},
			"asset-3":   {Handle: "@img3", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, AssetID: "asset-3", TaskID: "task-3"},
			"asset-ref": {Handle: "@img9", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "asset-ref", TaskID: "task-ref"},
		}}
		if adjust != nil {
			adjust(config)
		}
	})
	ctx := context.Background()
	session, err := fixture.service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	fixture.session = session
	for index := range fixture.messages {
		fixture.messages[index].SessionID = session.ID
	}
	fixture.repository.mu.Lock()
	fixture.repository.messages[session.ID] = append([]chat.Message(nil), fixture.messages...)
	fixture.repository.mu.Unlock()
	fixture.service.SetSessionTaskSource(func(context.Context, string, string) (generation.TaskPage, error) {
		return generation.TaskPage{Tasks: append([]generation.Task(nil), fixture.tasks...), Total: len(fixture.tasks)}, nil
	})
	fixture.service.SetSessionTaskDetail(func(_ context.Context, _, _, taskID string) (generation.Task, error) {
		for _, task := range append(append([]generation.Task(nil), fixture.detailTasks...), fixture.tasks...) {
			if string(task.ID) == taskID {
				return task, nil
			}
		}
		return generation.Task{}, fmt.Errorf("task %q not found", taskID)
	})
	return fixture
}

func workTask(
	id string,
	createdAt time.Time,
	rawPrompt, finalPrompt string,
	extraParameters ...generation.Parameter,
) generation.Task {
	parameters := []generation.Parameter{
		{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt},
		{Name: "negative_prompt", Type: "string", Semantic: generation.SemanticNegativePrompt, Default: json.RawMessage(`""`)},
		{Name: "size", Type: "string", Default: json.RawMessage(`"1024x1024"`)},
	}
	parameters = append(parameters, extraParameters...)
	input := map[string]any{"prompt": finalPrompt, "negative_prompt": "blurry", "size": "1024x1024"}
	for _, parameter := range extraParameters {
		_ = parameter
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	return generation.Task{
		ID:         generation.TaskID(id),
		OwnerID:    generation.OwnerID("owner-a"),
		SessionID:  "session",
		Capability: generation.Capability("text-to-image"),
		Offering: generation.Offering{
			ID:               generation.OfferingID("offering-" + id),
			ModelID:          "model-" + id,
			ModelName:        "Model " + id,
			ProviderName:     "Provider " + id,
			Capability:       generation.Capability("text-to-image"),
			Parameters:       parameters,
			EstimatedCredits: 5,
		},
		Input:     encoded,
		State:     generation.TaskSucceeded,
		Progress:  100,
		CreatedAt: createdAt,
		Assets: []generation.Asset{{
			ID: generation.AssetID("asset-" + strings.TrimPrefix(id, "task-")), TaskID: generation.TaskID(id),
			Role: "image", MediaType: "image/png",
		}},
	}
}

func workCall(callID, taskID, rawPrompt string, summary string) chat.ToolCall {
	call := chat.ToolCall{
		ID: callID, Name: "refine_image", TaskID: taskID, EstimatedCredits: 12,
		Arguments: []byte(`{"prompt":` + mustJSON(rawPrompt) + `}`),
	}
	if summary != "" {
		call.Summary = json.RawMessage(summary)
	}
	return call
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func workStepArgument(t *testing.T, step string) chat.ToolCommand {
	t.Helper()
	return chat.ToolCommand{
		OwnerID: "owner-a",
		Call: agents.ToolCall{
			ID: "ledger-call", Name: "get_production_step",
			Arguments: []byte(`{"step":` + mustJSON(step) + `}`),
		},
	}
}

func TestWorkLedgerKeepsPromptsBeyondTheHistoryPreview(t *testing.T) {
	raw := "a hero in a black cloak with silver embroidery, measured pose, soft studio light, " +
		strings.Repeat("intricate detail, ", 12) + "and a calm expression"
	final := raw + "\n\nStyle lock: painted in warm gouache"
	if len([]rune(raw)) <= 120 {
		t.Fatalf("fixture prompt must exceed the history preview: %d", len([]rune(raw)))
	}
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), raw, final)
	messages := []chat.Message{{
		Role: "configured-assistant", ToolCalls: []chat.ToolCall{workCall("call-1", "task-1", raw, "")},
		Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, AssetID: "asset-1", TaskID: "task-1"}},
	}}
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, messages)
	ctx := context.Background()
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Steps) != 1 {
		t.Fatalf("steps = %d", len(ledger.Steps))
	}
	step := ledger.Steps[0]
	if step.Step != "@step1" || step.TaskID != "task-1" {
		t.Fatalf("step = %+v", step)
	}
	if step.RawPrompt != raw {
		t.Fatalf("raw prompt lost: %q", step.RawPrompt)
	}
	if step.FinalPrompt != final {
		t.Fatalf("final prompt lost: %q", step.FinalPrompt)
	}
	result, err := fixture.service.ReadProductionStep(ctx, chat.ToolCommand{
		OwnerID: "owner-a", SessionID: fixture.session.ID,
		Call: agents.ToolCall{ID: "read", Name: "get_production_step", Arguments: []byte(`{"step":"@step1"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NoSubmission != true || result.Error != "" {
		t.Fatalf("result = %+v", result)
	}
	var payload struct {
		OK   bool          `json:"ok"`
		Step chat.WorkStep `json:"step"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil || !payload.OK {
		t.Fatalf("full step = %v %s", err, result.AgentContent)
	}
	if payload.Step.RawPrompt != raw || payload.Step.FinalPrompt != final {
		t.Fatalf("full step lost the prompts: %+v", payload.Step)
	}
	if payload.Step.TaskID != "task-1" || payload.Step.Step != "@step1" {
		t.Fatalf("full step identity = %+v", payload.Step)
	}
	var summary map[string]any
	if json.Unmarshal(result.Summary, &summary) != nil || summary["step"] != "@step1" {
		t.Fatalf("summary = %s", result.Summary)
	}
}

func TestWorkLedgerSummarizesSelfCheckLeftovers(t *testing.T) {
	inspect := `{"tool":"inspect","task_id":"task-1","usage":{"checks":[` +
		`{"check":"watertight","status":"fail","actual":false,"budget":{"required":true},"fix_hint":"Repair the mesh"},` +
		`{"check":"triangles","status":"pass","actual":100,"budget":{"max":200}}]}}`
	image := `{"tool":"inspect_image","handle":"@img1","checks":{"passed":2,"failed":1}}`
	render := `{"tool":"render","model":"task-1","views":["front"],"failed_views":["back"]}`
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "prompt", "prompt")
	messages := []chat.Message{{
		Role: "configured-assistant",
		ToolCalls: []chat.ToolCall{
			workCall("call-1", "task-1", "prompt", inspect),
			{ID: "call-2", Name: "inspect_image", Summary: json.RawMessage(image)},
			{ID: "call-3", Name: "render_model_views", Summary: json.RawMessage(render)},
		},
		Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, AssetID: "asset-1", TaskID: "task-1"}},
	}}
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, messages)
	ledger, err := fixture.service.WorkLedger(context.Background(), "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	checks := ledger.Steps[0].SelfChecks
	byTool := map[string]chat.WorkSelfCheck{}
	for _, check := range checks {
		byTool[check.Tool] = check
	}
	inspectCheck, found := byTool["inspect_model"]
	if !found || inspectCheck.Failed != 1 || len(inspectCheck.Leftovers) != 1 {
		t.Fatalf("inspect_model self-check = %+v", inspectCheck)
	}
	if inspectCheck.Leftovers[0].Check != "watertight" || inspectCheck.Leftovers[0].FixHint != "Repair the mesh" {
		t.Fatalf("leftover = %+v", inspectCheck.Leftovers[0])
	}
	if imageCheck := byTool["inspect_image"]; imageCheck.Passed != 2 || imageCheck.Failed != 1 {
		t.Fatalf("inspect_image self-check = %+v", imageCheck)
	}
	if renderCheck := byTool["render_model_views"]; renderCheck.Failed != 1 || len(renderCheck.FailedViews) != 1 || renderCheck.FailedViews[0] != "back" {
		t.Fatalf("render self-check = %+v", renderCheck)
	}
	summary := fixture.service.WorkLedgerSummary(context.Background(), "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "watertight") || !strings.Contains(summary, "Repair the mesh") {
		t.Fatalf("leftovers missing from the summary: %s", summary)
	}
	if !strings.Contains(summary, "inspect_image") {
		t.Fatalf("inspect_image counts missing from the summary: %s", summary)
	}
}

func TestWorkLedgerSummaryPriorityAndTruncation(t *testing.T) {
	tasks := []generation.Task{
		workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "first", "first"),
		workTask("task-2", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC), "second", "second"),
		workTask("task-3", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), "third", "third"),
	}
	fixture := newLedgerFixture(t, func(config *chat.ServiceConfig) {
		config.WorkLedger.MaximumSteps = 2
	}, tasks, nil)
	ctx := context.Background()
	if _, err := fixture.repository.SetSessionPick(ctx, "owner-a", fixture.session.ID, chat.Attachment{
		Kind: chat.AttachmentKindImage, AssetID: "asset-1", TaskID: "task-1",
	}); err != nil {
		t.Fatal(err)
	}
	summary := fixture.service.WorkLedgerSummary(ctx, "owner-a", fixture.session.ID, "please keep @step2")
	if !strings.Contains(summary, "@step1") || !strings.Contains(summary, "@step2") {
		t.Fatalf("current and mentioned steps must survive: %s", summary)
	}
	if strings.Contains(summary, "@step3") {
		t.Fatalf("the newest step must be truncated behind the picked/mentioned ones: %s", summary)
	}
	if !strings.Contains(summary, "另有 1 步") {
		t.Fatalf("truncated note missing: %s", summary)
	}
	if strings.Index(summary, "@step1") > strings.Index(summary, "@step2") {
		t.Fatalf("the picked step must come before the mentioned one: %s", summary)
	}
}

type staticDecisions []chat.WorkDecision

func (decisions staticDecisions) WorkDecisions(context.Context, string, string) ([]chat.WorkDecision, error) {
	return decisions, nil
}

func TestWorkLedgerRendersRejectedDirections(t *testing.T) {
	tasks := []generation.Task{
		workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "first", "first"),
		workTask("task-2", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC), "second", "second"),
		workTask("task-3", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), "third", "third"),
	}
	fixture := newLedgerFixture(t, func(config *chat.ServiceConfig) {
		config.WorkLedger.MaximumSteps = 2
	}, tasks, nil)
	fixture.service.SetDecisionReader(staticDecisions{{
		TaskID: "task-1", State: chat.WorkDecisionRejected, Reason: "the person disliked the cloak",
	}})
	ctx := context.Background()
	summary := fixture.service.WorkLedgerSummary(ctx, "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "不要再做") || !strings.Contains(summary, "the person disliked the cloak") {
		t.Fatalf("rejected block missing: %s", summary)
	}
	// The rejected step outranks the merely recent one under the bound.
	if !strings.Contains(summary, "@step1") {
		t.Fatalf("the rejected step must survive the bound: %s", summary)
	}
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Steps[0].Decision != chat.WorkDecisionRejected || ledger.Steps[0].DecisionReason != "the person disliked the cloak" {
		t.Fatalf("step decision = %+v", ledger.Steps[0])
	}
}

func TestWorkLedgerWithholdsReferenceAndHiddenParameters(t *testing.T) {
	hidden := generation.Parameter{
		Name: "hidden_note", Type: "string",
		Presentation: &generation.ParameterPresentation{Surface: "hidden"},
	}
	sourceURL := generation.Parameter{Name: "source_url", Type: "string", Semantic: generation.SemanticSourceURL}
	mask := generation.Parameter{Name: "mask", Type: "asset", Semantic: generation.SemanticMask}
	reference := generation.Parameter{Name: "reference_image", Type: "asset-array", Semantic: generation.SemanticReferenceImage}
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "raw", "final",
		hidden, sourceURL, mask, reference)
	input, _ := json.Marshal(map[string]any{
		"prompt": "final", "hidden_note": "secret-hidden", "source_url": "https://secret.example/x",
		"mask": "asset-mask", "reference_image": []string{"asset-ref"},
	})
	task.Input = input
	messages := []chat.Message{{
		Role: "configured-assistant", ToolCalls: []chat.ToolCall{workCall("call-1", "task-1", "raw", "")},
		Attachments: []chat.Attachment{
			{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, AssetID: "asset-1", TaskID: "task-1"},
			{Handle: "@img9", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "asset-ref", TaskID: "task-ref"},
		},
	}}
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, messages)
	result, err := fixture.service.ReadProductionStep(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: fixture.session.ID,
		Call: agents.ToolCall{ID: "read", Name: "get_production_step", Arguments: []byte(`{"step":"task-1"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-hidden", "https://secret.example/x", "asset-mask", "asset-ref"} {
		if strings.Contains(result.AgentContent, secret) {
			t.Fatalf("private reference %q leaked: %s", secret, result.AgentContent)
		}
	}
	if !strings.Contains(result.AgentContent, "@img9") {
		t.Fatalf("the reference picture's handle is missing: %s", result.AgentContent)
	}
}

func TestWorkLedgerReadsTheFullStepOnlyForTheTool(t *testing.T) {
	full := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "raw", "final")
	reduced := full
	reduced.Offering.Parameters = []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt}}
	reduced.Input = json.RawMessage(`{"prompt":"final"}`)
	fixture := newLedgerFixture(t, nil, []generation.Task{reduced}, nil)
	fixture.detailTasks = []generation.Task{full}
	ctx := context.Background()
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Steps) != 1 || len(ledger.Steps[0].Parameters) != 0 {
		t.Fatalf("the list projection must stay reduced: %+v", ledger.Steps)
	}
	result, err := fixture.service.ReadProductionStep(ctx, chat.ToolCommand{
		OwnerID: "owner-a", SessionID: fixture.session.ID,
		Call: agents.ToolCall{ID: "read", Name: "get_production_step", Arguments: []byte(`{"step":"@step1"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Step chat.WorkStep `json:"step"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Step.Parameters) == 0 || payload.Step.FinalPrompt != "final" {
		t.Fatalf("the tool must read the full frozen step: %+v", payload.Step)
	}
	found := false
	for _, parameter := range payload.Step.Parameters {
		if parameter.Name == "size" && parameter.Source == "default" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the full step must name the default size: %+v", payload.Step.Parameters)
	}
}

func TestWorkStepRefusesUnknownForeignAndDeletedTask(t *testing.T) {
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "raw", "final")
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, nil)
	ctx := context.Background()
	unknown, err := fixture.service.ReadProductionStep(ctx, chat.ToolCommand{
		OwnerID: "owner-a", SessionID: fixture.session.ID,
		Call: agents.ToolCall{ID: "read", Name: "get_production_step", Arguments: []byte(`{"step":"@step9"}`)},
	})
	if err != nil || !strings.Contains(unknown.Error, "@step9") {
		t.Fatalf("unknown step = %+v %v", unknown, err)
	}
	foreign, err := fixture.service.ReadProductionStep(ctx, chat.ToolCommand{
		OwnerID: "owner-b", SessionID: fixture.session.ID,
		Call: agents.ToolCall{ID: "read", Name: "get_production_step", Arguments: []byte(`{"step":"task-1"}`)},
	})
	if err != nil || foreign.Error == "" || strings.Contains(foreign.AgentContent, "final") {
		t.Fatalf("foreign session = %+v %v", foreign, err)
	}
	deleted, err := fixture.service.ReadProductionStep(ctx, chat.ToolCommand{
		OwnerID: "owner-a", SessionID: fixture.session.ID,
		Call: agents.ToolCall{ID: "read", Name: "get_production_step", Arguments: []byte(`{"step":"task-deleted"}`)},
	})
	if err != nil || deleted.Error == "" {
		t.Fatalf("deleted task = %+v %v", deleted, err)
	}
}

func TestWorkLedgerNeverLogsPromptText(t *testing.T) {
	const secret = "ledger-only-prompt-marker-7f3a"
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), secret, secret)
	messages := []chat.Message{{
		Role: "configured-assistant", ToolCalls: []chat.ToolCall{workCall("call-1", "task-1", secret, "")},
	}}
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, messages)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))) })
	ctx := context.Background()
	_ = fixture.service.WorkLedgerSummary(ctx, "owner-a", fixture.session.ID, "")
	_, _ = fixture.service.ReadProductionStep(ctx, chat.ToolCommand{
		OwnerID: "owner-a", SessionID: fixture.session.ID,
		Call: agents.ToolCall{ID: "read", Name: "get_production_step", Arguments: []byte(`{"step":"task-1"}`)},
	})
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("prompt text reached the logs: %s", logs.String())
	}
}

// ledgerPromptTurn runs one Agent turn and returns the system prompt the model
// received. It keeps the fixture's history and the Schema's configured roles.
func ledgerPromptTurn(t *testing.T, fixture *ledgerFixture, agent *scriptedAgent, store *memoryContextStore) string {
	t.Helper()
	schema := configuredChatSchema(t)
	schema.Agent.Vision = false
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: fixture.service, Schema: schema, Model: "m", Agent: agent, Tools: noToolExecutor{},
		Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture.repository.mu.Lock()
	fixture.repository.messages[fixture.session.ID] = append([]chat.Message(nil), fixture.messages...)
	fixture.repository.mu.Unlock()
	assistant, err := fixture.service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: fixture.session.ID, Content: "now what",
		Options: chat.TurnOptions{CreativeDomain: "image"},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.requests = nil
	agent.index = 0
	agent.completions = nil
	agent.completeIndex = 0
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: fixture.session.ID, AssistantMessageID: assistant.ID,
		Options: chat.TurnOptions{CreativeDomain: "image"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) == 0 {
		t.Fatal("no agent request")
	}
	return agent.requests[0].Messages[0].Content
}

func ledgerBlock(system, open, close string) string {
	start := strings.Index(system, open)
	if start < 0 {
		return ""
	}
	end := strings.Index(system[start:], close)
	if end < 0 {
		return ""
	}
	return system[start : start+end+len(close)]
}

func TestWorkLedgerAndAnchorsSurviveCompactionVerbatim(t *testing.T) {
	anchorText := strings.Repeat("印象派笔触、暖金色光线、厚涂质感；", 20)
	if len([]rune(anchorText)) <= 120 {
		t.Fatal("the anchor must exceed the history preview")
	}
	longPrompt := strings.Repeat("a detailed heroine with a silver cloak, ", 10)
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "raw", longPrompt)
	messages := []chat.Message{
		{ID: "m1", Role: "configured-user", Content: "make her"},
		{ID: "m2", Role: "configured-assistant", Content: "made her", ToolCalls: []chat.ToolCall{workCall("call-1", "task-1", "raw", "")},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, AssetID: "asset-1", TaskID: "task-1"}},
		},
	}
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, messages)
	ctx := context.Background()
	if _, err := fixture.repository.SetStyleLock(ctx, "owner-a", fixture.session.ID, chat.StyleLock{
		Enabled: true, Text: anchorText, References: []string{"asset-ref"},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.messages = append(fixture.messages, chat.Message{
		Role: "configured-user", Content: "a reference",
		Attachments: []chat.Attachment{{Handle: "@img9", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "asset-ref"}},
	})
	store := newMemoryContextStore(fixture.repository)
	fixture.service.SetContextStore(store, 50)
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
	before := ledgerPromptTurn(t, fixture, agent, store)
	ledgerBefore := ledgerBlock(before, fixture.policy.OpenMarker, fixture.policy.CloseMarker)
	anchorBefore := ledgerBlock(before, fixture.policy.Anchors.OpenMarker, fixture.policy.Anchors.CloseMarker)
	if !strings.Contains(ledgerBefore, "@step1") || !strings.Contains(ledgerBefore, "a detailed heroine") {
		t.Fatalf("ledger missing from the system prompt: %q", ledgerBefore)
	}
	if !strings.Contains(anchorBefore, anchorText) {
		t.Fatalf("the style anchor must be injected verbatim: %q", anchorBefore)
	}
	if !strings.Contains(anchorBefore, "@img9") {
		t.Fatalf("the style reference handle is missing: %q", anchorBefore)
	}
	if _, err := store.SaveCompaction(ctx, chat.Compaction{
		OwnerID: "owner-a", SessionID: fixture.session.ID, Summary: "earlier conversation",
		ThroughMessageID: "m2", ThroughCreatedAt: time.Date(2026, 11, 1, 10, 1, 0, 0, time.UTC), Rounds: 1,
	}, ""); err != nil {
		t.Fatal(err)
	}
	after := ledgerPromptTurn(t, fixture, agent, store)
	ledgerAfter := ledgerBlock(after, fixture.policy.OpenMarker, fixture.policy.CloseMarker)
	anchorAfter := ledgerBlock(after, fixture.policy.Anchors.OpenMarker, fixture.policy.Anchors.CloseMarker)
	if ledgerBefore != ledgerAfter {
		t.Fatalf("the ledger changed across compaction:\nbefore: %q\nafter:  %q", ledgerBefore, ledgerAfter)
	}
	if anchorBefore != anchorAfter {
		t.Fatalf("the anchor changed across compaction:\nbefore: %q\nafter:  %q", anchorBefore, anchorAfter)
	}
}

func TestCompactionSourceCarriesTheLedgerWithoutFullPrompts(t *testing.T) {
	fullPrompt := strings.Repeat("a private detail of the ledged prompt, ", 8) + "LEDGER-ONLY-TAIL-MARKER"
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), fullPrompt, fullPrompt)
	inspect := `{"tool":"inspect","model":"task-1","usage":{"checks":[{"check":"watertight","status":"fail","actual":false,"budget":{},"fix_hint":"Repair"}]}}`
	messages := []chat.Message{{
		ID: "a0", Role: "configured-assistant", Content: "first",
		ToolCalls: []chat.ToolCall{workCall("call-1", "task-1", fullPrompt, inspect)},
	}}
	for round := 1; round <= 6; round++ {
		messages = append(messages,
			chat.Message{ID: "u" + mustJSON(round), Role: "configured-user", Content: "round " + mustJSON(round)},
			chat.Message{ID: "a" + mustJSON(round), Role: "configured-assistant", Content: "reply " + mustJSON(round)},
		)
	}
	fixture := newLedgerFixture(t, func(config *chat.ServiceConfig) {
		config.Models = chat.ModelPolicy{Model: "m"}
	}, []generation.Task{task}, messages)
	store := newMemoryContextStore(fixture.repository)
	fixture.service.SetContextStore(store, 50)
	agent := &contextAgent{summary: "summary"}
	schema := configuredChatSchema(t)
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: fixture.service, Schema: schema, Model: "m", Agent: agent, Tools: noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture.repository.mu.Lock()
	fixture.repository.messages[fixture.session.ID] = append([]chat.Message(nil), fixture.messages...)
	fixture.repository.mu.Unlock()
	if _, err := orchestrator.CompactContext(ctx, "owner-a", fixture.session.ID); err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.summaries) != 1 {
		t.Fatalf("summary calls = %d", len(agent.summaries))
	}
	source := agent.summaries[0].Messages[1].Content
	if !strings.Contains(source, "@step1") || !strings.Contains(source, "watertight") || !strings.Contains(source, "Repair") {
		t.Fatalf("the ledger facts are missing from the summary source: %s", source)
	}
	if strings.Contains(source, "LEDGER-ONLY-TAIL-MARKER") {
		t.Fatalf("the full prompt must not be copied into the summary source: %s", source)
	}
	if !strings.Contains(source, "用 get_production_step 取回") {
		t.Fatalf("the summary source should point at the tool instead of the prompt: %s", source)
	}
}

func TestStyleLockIsInjectedAsTheStyleAnchorOnlyWhenEnabled(t *testing.T) {
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "raw", "final")
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, nil)
	ctx := context.Background()
	if _, err := fixture.repository.SetStyleLock(ctx, "owner-a", fixture.session.ID, chat.StyleLock{
		Enabled: false, Text: "disabled style",
	}); err != nil {
		t.Fatal(err)
	}
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
	system := ledgerPromptTurn(t, fixture, agent, nil)
	if strings.Contains(system, "disabled style") {
		t.Fatalf("a disabled style lock must not be injected: %q", system)
	}
	if _, err := fixture.repository.SetStyleLock(ctx, "owner-a", fixture.session.ID, chat.StyleLock{
		Enabled: true, Text: "enabled style",
	}); err != nil {
		t.Fatal(err)
	}
	system = ledgerPromptTurn(t, fixture, agent, nil)
	if !strings.Contains(system, "enabled style") {
		t.Fatalf("an enabled style lock must be injected: %q", system)
	}
}
