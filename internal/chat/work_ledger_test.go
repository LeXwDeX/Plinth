package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
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
	fixture.service.SetSessionTaskSource(func(_ context.Context, _, _ string, offset, limit int) (generation.TaskPage, error) {
		// Mirror generation.Module.List: newest first (CreatedAt DESC, ID DESC)
		// with the production page bound applied. Tests that lower
		// MaximumSteps therefore exercise the real list bound, not a fixture
		// that always returns every Task.
		ordered := append([]generation.Task(nil), fixture.tasks...)
		sort.SliceStable(ordered, func(left, right int) bool {
			if ordered[left].CreatedAt.Equal(ordered[right].CreatedAt) {
				return ordered[left].ID > ordered[right].ID
			}
			return ordered[left].CreatedAt.After(ordered[right].CreatedAt)
		})
		total := len(ordered)
		if limit <= 0 {
			limit = 20
		}
		if offset < 0 {
			offset = 0
		}
		if offset >= total {
			return generation.TaskPage{Tasks: []generation.Task{}, Total: total}, nil
		}
		end := min(offset+limit, total)
		return generation.TaskPage{Tasks: append([]generation.Task(nil), ordered[offset:end]...), Total: total}, nil
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

func workStepArgument(t *testing.T, sessionID, step string) chat.ToolCommand {
	t.Helper()
	return chat.ToolCommand{
		OwnerID:   "owner-a",
		SessionID: sessionID,
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
		`{"check":"triangles","status":"pass","actual":100,"budget":{"max":200}},` +
		`{"check":"uv","status":"unknown","actual":false,"budget":{"required":false}}]}}`
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
	if !found || inspectCheck.Passed != 1 || inspectCheck.Unknown != 1 || inspectCheck.Failed != 1 || len(inspectCheck.Leftovers) != 1 {
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
	if !strings.Contains(summary, "inspect_model 通过 1 未判定 1 失败 1") {
		t.Fatalf("inspect_model counts must separate unknown from pass: %s", summary)
	}
	if !strings.Contains(summary, "inspect_image") {
		t.Fatalf("inspect_image counts missing from the summary: %s", summary)
	}
}

// TestWorkLedgerDoesNotCountUnknownInspectChecksAsPassed is the #248
// regression: an inspect_model check the purpose did not ask for or the file
// could not judge is unknown, not passed. Both the summary and
// get_production_step must report it as such.
func TestWorkLedgerDoesNotCountUnknownInspectChecksAsPassed(t *testing.T) {
	inspect := `{"tool":"inspect","task_id":"task-1","usage":{"checks":[` +
		`{"check":"triangles","status":"pass","actual":100,"budget":{"max":200}},` +
		`{"check":"watertight","status":"unknown","actual":false,"budget":{"required":false}},` +
		`{"check":"skin","status":"unknown","actual":0,"budget":{"required":false}},` +
		`{"check":"uv","status":"fail","actual":false,"budget":{"required":true},"fix_hint":"Unwrap the mesh"}]}}`
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "prompt", "prompt")
	messages := []chat.Message{{
		Role:      "configured-assistant",
		ToolCalls: []chat.ToolCall{workCall("call-1", "task-1", "prompt", inspect)},
	}}
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, messages)
	ctx := context.Background()
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	check := ledger.Steps[0].SelfChecks[0]
	if check.Passed != 1 || check.Unknown != 2 || check.Failed != 1 || len(check.Leftovers) != 1 {
		t.Fatalf("self-check = %+v", check)
	}
	summary := fixture.service.WorkLedgerSummary(ctx, "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "inspect_model 通过 1 未判定 2 失败 1") {
		t.Fatalf("summary folds unknown into pass: %s", summary)
	}
	if strings.Contains(summary, "通过 3") {
		t.Fatalf("summary counted unknown checks as passed: %s", summary)
	}
	result, err := fixture.service.ReadProductionStep(ctx, workStepArgument(t, fixture.session.ID, "@step1"))
	if err != nil || result.Error != "" {
		t.Fatalf("read = %+v, %v", result, err)
	}
	var payload struct {
		Step chat.WorkStep `json:"step"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatal(err)
	}
	readCheck := payload.Step.SelfChecks[0]
	if readCheck.Passed != 1 || readCheck.Unknown != 2 || readCheck.Failed != 1 {
		t.Fatalf("get_production_step self-check = %+v", readCheck)
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

func workLedgerFourTasks() []generation.Task {
	return []generation.Task{
		workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "first", "first"),
		workTask("task-2", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC), "second", "second"),
		workTask("task-3", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), "third", "third"),
		workTask("task-4", time.Date(2026, 11, 1, 13, 0, 0, 0, time.UTC), "fourth", "fourth"),
	}
}

// TestWorkLedgerReadsStepsBeyondTheSummaryBound is the #248 regression for the
// bounded Task read: the summary still lists at most maximum_steps Tasks, but
// get_production_step must resolve any @stepN or Task ID in the complete
// session set, including a step the summary page never loaded.
func TestWorkLedgerReadsStepsBeyondTheSummaryBound(t *testing.T) {
	tasks := workLedgerFourTasks()
	messages := []chat.Message{{
		Role:      "configured-assistant",
		ToolCalls: []chat.ToolCall{workCall("call-1", "task-1", "first", "")},
	}}
	fixture := newLedgerFixture(t, func(config *chat.ServiceConfig) {
		config.WorkLedger.MaximumSteps = 2
	}, tasks, messages)
	ctx := context.Background()
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.TotalSteps != 4 {
		t.Fatalf("total steps = %d", ledger.TotalSteps)
	}
	if len(ledger.Steps) != 2 || ledger.Steps[0].Step != "@step3" || ledger.Steps[1].Step != "@step4" {
		t.Fatalf("the bounded projection must hold only the newest page: %+v", ledger.Steps)
	}
	summary := fixture.service.WorkLedgerSummary(ctx, "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "另有 2 步") || !strings.Contains(summary, "get_production_step 取回") {
		t.Fatalf("the summary must name the steps it left out: %s", summary)
	}
	for token, wantTask := range map[string]string{"@step1": "task-1", "@step2": "task-2", "task-1": "task-1"} {
		result, err := fixture.service.ReadProductionStep(ctx, workStepArgument(t, fixture.session.ID, token))
		if err != nil || result.Error != "" {
			t.Fatalf("read %s = %+v, %v", token, result, err)
		}
		var payload struct {
			Step  chat.WorkStep `json:"step"`
			Total int           `json:"total_steps"`
		}
		if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Step.TaskID != wantTask || payload.Total != 4 {
			t.Fatalf("read %s = %+v", token, payload)
		}
		wantStep := map[string]string{"@step1": "@step1", "@step2": "@step2", "task-1": "@step1"}[token]
		if payload.Step.Step != wantStep {
			t.Fatalf("read %s step handle = %q; want %q", token, payload.Step.Step, wantStep)
		}
		if len(payload.Step.Parameters) == 0 {
			t.Fatalf("read %s must return the full frozen step: %+v", token, payload.Step)
		}
	}
	if result, _ := fixture.service.ReadProductionStep(ctx, workStepArgument(t, fixture.session.ID, "first")); result.Error == "" {
		t.Fatalf("a display name must not resolve as a Task ID: %+v", result)
	}
}

// TestWorkLedgerSummaryKeepsAnOldPickedStep is the #248 regression for the
// picked object: even when its Task is older than the bounded summary page, it
// is fetched and ranked first instead of silently disappearing.
func TestWorkLedgerSummaryKeepsAnOldPickedStep(t *testing.T) {
	fixture := newLedgerFixture(t, func(config *chat.ServiceConfig) {
		config.WorkLedger.MaximumSteps = 2
	}, workLedgerFourTasks(), nil)
	ctx := context.Background()
	if _, err := fixture.repository.SetSessionPick(ctx, "owner-a", fixture.session.ID, chat.Attachment{
		Kind: chat.AttachmentKindImage, AssetID: "asset-1", TaskID: "task-1",
	}); err != nil {
		t.Fatal(err)
	}
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.CurrentTaskID != "task-1" {
		t.Fatalf("current = %q", ledger.CurrentTaskID)
	}
	found := false
	for _, step := range ledger.Steps {
		if step.TaskID == "task-1" && step.Step == "@step1" && step.Decision == chat.WorkDecisionAdopted {
			found = true
		}
	}
	if !found {
		t.Fatalf("the old picked step must be fetched into the projection: %+v", ledger.Steps)
	}
	summary := fixture.service.WorkLedgerSummary(ctx, "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "@step1") || !strings.Contains(summary, "task-1") {
		t.Fatalf("the old picked step is missing from the summary: %s", summary)
	}
	if !strings.Contains(summary, "另有 2 步") {
		t.Fatalf("the summary must count every unlisted step: %s", summary)
	}
}

type failingDecisions struct{}

func (failingDecisions) WorkDecisions(context.Context, string, string) ([]chat.WorkDecision, error) {
	return nil, errors.New("decision store unavailable")
}

// TestDecisionReadFailuresBecomeOmissions is the #248 regression for the
// previously swallowed rejected-direction read error: it must surface as a
// diagnosable omission and a Schema note, never as an empty "no rejections"
// list.
func TestDecisionReadFailuresBecomeOmissions(t *testing.T) {
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "raw", "final")
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, nil)
	fixture.service.SetDecisionReader(failingDecisions{})
	ctx := context.Background()
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Rejected != nil {
		t.Fatalf("an unreadable decision store must not report a decision: %+v", ledger.Rejected)
	}
	if len(ledger.Omissions) != 1 {
		t.Fatalf("omissions = %+v", ledger.Omissions)
	}
	omission := ledger.Omissions[0]
	if omission.Kind != "decisions_unreadable" || omission.Detail == "" {
		t.Fatalf("omission = %+v", omission)
	}
	summary := fixture.service.WorkLedgerSummary(ctx, "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "另有 1 项账本事实未能读取或归属") {
		t.Fatalf("the summary must surface the failed read: %s", summary)
	}
}

// addSelectionTurn records one persisted turn with the objects the person
// selected for it. picked marks a selection that is only the session pick
// carried into the turn, not a new choice.
func addSelectionTurn(t *testing.T, fixture *ledgerFixture, at time.Time, picked bool, taskIDs ...string) {
	t.Helper()
	attachments := make([]chat.Attachment, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		suffix := taskID[len(taskID)-1:]
		attachments = append(attachments, chat.Attachment{
			Handle: "@img" + suffix, Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated,
			AssetID: "asset-" + suffix, TaskID: taskID,
		})
	}
	fixture.repository.mu.Lock()
	fixture.repository.turns["selection-turn-"+at.Format("150405.000")] = chat.Turn{
		OwnerID:   "owner-a",
		SessionID: fixture.session.ID,
		Options:   chat.TurnOptions{Selection: attachments, SelectionPicked: picked},
		CreatedAt: at,
	}
	fixture.repository.mu.Unlock()
}

// TestSessionDecisionReaderProjectsSupersededSelections is the #248 regression
// for the production decision source: the persisted turn selections and the
// session pick are the verifiable facts, and no fact means no block.
func TestSessionDecisionReaderProjectsSupersededSelections(t *testing.T) {
	tasks := []generation.Task{
		workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "first", "first"),
		workTask("task-2", time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC), "second", "second"),
	}
	fixture := newLedgerFixture(t, nil, tasks, nil)
	ctx := context.Background()
	const reason = "已被后续选择取代，不要再按这个结果继续"
	addSelectionTurn(t, fixture, time.Date(2026, 11, 1, 10, 1, 0, 0, time.UTC), false, "task-1")
	addSelectionTurn(t, fixture, time.Date(2026, 11, 1, 11, 1, 0, 0, time.UTC), false, "task-2")
	reader := chat.NewSessionDecisionReader(fixture.repository, reason)
	decisions, err := reader.WorkDecisions(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].TaskID != "task-1" || decisions[0].State != chat.WorkDecisionRejected || decisions[0].Reason != reason {
		t.Fatalf("decisions = %+v", decisions)
	}
	fixture.service.SetDecisionReader(reader)
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Rejected) != 1 || ledger.Rejected[0].Step != "@step1" {
		t.Fatalf("rejected steps must carry their handle: %+v", ledger.Rejected)
	}
	stepByID := map[string]chat.WorkStep{}
	for _, step := range ledger.Steps {
		stepByID[step.TaskID] = step
	}
	if stepByID["task-1"].Decision != chat.WorkDecisionRejected || stepByID["task-2"].Decision == chat.WorkDecisionRejected {
		t.Fatalf("step decisions = %+v", stepByID)
	}
	summary := fixture.service.WorkLedgerSummary(ctx, "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "不要再做") || !strings.Contains(summary, "@step1") || !strings.Contains(summary, reason) {
		t.Fatalf("rejected block missing from the summary: %s", summary)
	}

	// A session pick proven newer than the last explicit selection (a later
	// turn carried it) supersedes that selection.
	pickFixture := newLedgerFixture(t, nil, tasks, nil)
	addSelectionTurn(t, pickFixture, time.Date(2026, 11, 1, 10, 1, 0, 0, time.UTC), false, "task-1")
	if _, err := pickFixture.repository.SetSessionPick(ctx, "owner-a", pickFixture.session.ID, chat.Attachment{
		Kind: chat.AttachmentKindImage, AssetID: "asset-2", TaskID: "task-2",
	}); err != nil {
		t.Fatal(err)
	}
	addSelectionTurn(t, pickFixture, time.Date(2026, 11, 1, 10, 2, 0, 0, time.UTC), true, "task-2")
	pickDecisions, err := chat.NewSessionDecisionReader(pickFixture.repository, reason).
		WorkDecisions(ctx, "owner-a", pickFixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pickDecisions) != 1 || pickDecisions[0].TaskID != "task-1" {
		t.Fatalf("pick decisions = %+v", pickDecisions)
	}

	// A stale pick with no later turn carrying it must not override the newest
	// explicit selection: an old pick is not proof of a later choice.
	staleFixture := newLedgerFixture(t, nil, tasks, nil)
	if _, err := staleFixture.repository.SetSessionPick(ctx, "owner-a", staleFixture.session.ID, chat.Attachment{
		Kind: chat.AttachmentKindImage, AssetID: "asset-2", TaskID: "task-2",
	}); err != nil {
		t.Fatal(err)
	}
	addSelectionTurn(t, staleFixture, time.Date(2026, 11, 1, 10, 1, 0, 0, time.UTC), false, "task-1")
	staleDecisions, err := chat.NewSessionDecisionReader(staleFixture.repository, reason).
		WorkDecisions(ctx, "owner-a", staleFixture.session.ID)
	if err != nil || len(staleDecisions) != 0 {
		t.Fatalf("a stale pick must not invent a rejection: %+v, %v", staleDecisions, err)
	}

	// A turn that only carried the session pick is not a new choice; a session
	// whose existing records prove nothing must omit the block.
	emptyFixture := newLedgerFixture(t, nil, tasks, nil)
	addSelectionTurn(t, emptyFixture, time.Date(2026, 11, 1, 10, 1, 0, 0, time.UTC), true, "task-1")
	emptyDecisions, err := chat.NewSessionDecisionReader(emptyFixture.repository, reason).
		WorkDecisions(ctx, "owner-a", emptyFixture.session.ID)
	if err != nil || len(emptyDecisions) != 0 {
		t.Fatalf("empty decisions = %+v, %v", emptyDecisions, err)
	}
	emptyFixture.service.SetDecisionReader(chat.NewSessionDecisionReader(emptyFixture.repository, reason))
	if summary := emptyFixture.service.WorkLedgerSummary(ctx, "owner-a", emptyFixture.session.ID, ""); strings.Contains(summary, "不要再做") {
		t.Fatalf("no verifiable rejection must render no block: %s", summary)
	}
}

// TestWorkParameterSourceReportsTheUserOnlyWhenThePinMatches is the #248
// regression for the parameter source: the existing parameter store's
// last-writer state is used only when it still equals the frozen value.
func TestWorkParameterSourceReportsTheUserOnlyWhenThePinMatches(t *testing.T) {
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "raw", "final")
	task.Input = json.RawMessage(`{"prompt":"final","negative_prompt":"blurry","size":"2048x2048"}`)
	messages := []chat.Message{{
		Role: "configured-assistant", ToolCalls: []chat.ToolCall{workCall("call-1", "task-1", "raw", "")},
	}}
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, messages)
	ctx := context.Background()
	readSizeSource := func() string {
		t.Helper()
		result, err := fixture.service.ReadProductionStep(ctx, workStepArgument(t, fixture.session.ID, "@step1"))
		if err != nil || result.Error != "" {
			t.Fatalf("read = %+v, %v", result, err)
		}
		var payload struct {
			Step chat.WorkStep `json:"step"`
		}
		if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
			t.Fatal(err)
		}
		for _, parameter := range payload.Step.Parameters {
			if parameter.Name == "size" {
				if parameter.Value != "2048x2048" {
					t.Fatalf("size value = %q", parameter.Value)
				}
				return parameter.Source
			}
		}
		t.Fatalf("size parameter missing: %+v", payload.Step.Parameters)
		return ""
	}
	if source := readSizeSource(); source != "unknown" {
		t.Fatalf("without a parameter source the source must stay unknown: %q", source)
	}
	fixture.service.SetParameterSource(func(context.Context, string, string, string) (map[string]chat.WorkParameterState, error) {
		return map[string]chat.WorkParameterState{
			"size": {Value: json.RawMessage(`"2048x2048"`), Source: "user"},
		}, nil
	})
	if source := readSizeSource(); source != "user" {
		t.Fatalf("a matching persisted user pin must report user: %q", source)
	}
	fixture.service.SetParameterSource(func(context.Context, string, string, string) (map[string]chat.WorkParameterState, error) {
		return map[string]chat.WorkParameterState{
			"size": {Value: json.RawMessage(`"1024x1024"`), Source: "user"},
		}, nil
	})
	if source := readSizeSource(); source != "unknown" {
		t.Fatalf("a stale user pin must not claim the frozen value: %q", source)
	}
	fixture.service.SetParameterSource(func(context.Context, string, string, string) (map[string]chat.WorkParameterState, error) {
		return nil, errors.New("parameter store unavailable")
	})
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, omission := range ledger.Omissions {
		if omission.Kind == "parameter_source_unreadable" && omission.Subject != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("an unreadable parameter store must be an omission: %+v", ledger.Omissions)
	}
}

// TestWorkLedgerAggregatesMultiPromptTools is the #248 regression for tools
// that carry one prompt per view: raw and final prompts must list the named
// values instead of being empty, and the summary must preview that content.
func TestWorkLedgerAggregatesMultiPromptTools(t *testing.T) {
	full := generation.Task{
		ID:         generation.TaskID("task-1"),
		OwnerID:    generation.OwnerID("owner-a"),
		SessionID:  "session",
		Capability: generation.Capability("edit-multiview"),
		Offering: generation.Offering{
			ID:      generation.OfferingID("offering-multiview"),
			ModelID: "model-multiview",
			Parameters: []generation.Parameter{
				{Name: "front_prompt", Type: "text"},
				{Name: "back_prompt", Type: "text"},
			},
		},
		Input:     json.RawMessage(`{"front_prompt":"green cloak on the front","back_prompt":"red cape on the back"}`),
		State:     generation.TaskSucceeded,
		Progress:  100,
		CreatedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
		Assets: []generation.Asset{{
			ID: generation.AssetID("asset-1"), TaskID: "task-1", Role: "image", Variant: "front", MediaType: "image/png",
		}},
	}
	reduced := full
	reduced.Offering.Parameters = nil
	reduced.Input = json.RawMessage(`{}`)
	call := chat.ToolCall{
		ID: "call-1", Name: "edit_multiview_images", TaskID: "task-1",
		Arguments: json.RawMessage(`{"front_prompt":"green","back_prompt":"red"}`),
	}
	messages := []chat.Message{{
		Role: "configured-assistant", ToolCalls: []chat.ToolCall{call},
		Attachments: []chat.Attachment{{
			Handle: "@img4", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated,
			AssetID: "asset-1", TaskID: "task-1",
		}},
	}}
	fixture := newLedgerFixture(t, nil, []generation.Task{reduced}, messages)
	fixture.detailTasks = []generation.Task{full}
	ctx := context.Background()
	ledger, err := fixture.service.WorkLedger(ctx, "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Steps[0].RawPrompt != "back_prompt: red; front_prompt: green" {
		t.Fatalf("raw prompt = %q", ledger.Steps[0].RawPrompt)
	}
	summary := fixture.service.WorkLedgerSummary(ctx, "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "front_prompt: green") || !strings.Contains(summary, "back_prompt: red") {
		t.Fatalf("the summary must preview the named prompts: %s", summary)
	}
	result, err := fixture.service.ReadProductionStep(ctx, workStepArgument(t, fixture.session.ID, "@step1"))
	if err != nil || result.Error != "" {
		t.Fatalf("read = %+v, %v", result, err)
	}
	var payload struct {
		Step chat.WorkStep `json:"step"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatal(err)
	}
	want := "front_prompt: green cloak on the front; back_prompt: red cape on the back"
	if payload.Step.FinalPrompt != want {
		t.Fatalf("final prompt = %q; want %q", payload.Step.FinalPrompt, want)
	}
	for _, parameter := range payload.Step.Parameters {
		if strings.HasSuffix(parameter.Name, "_prompt") {
			t.Fatalf("a prompt slot must not be projected as a parameter: %+v", parameter)
		}
	}
}

// TestWorkLedgerDoesNotAttributeSelfChecksToUnknownTokens is the #248
// regression for old sessions whose self-check summaries name @selected or a
// display name: an unknown token must not become a phantom Task ID.
func TestWorkLedgerDoesNotAttributeSelfChecksToUnknownTokens(t *testing.T) {
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "raw", "final")
	messages := []chat.Message{{
		Role: "configured-assistant",
		ToolCalls: []chat.ToolCall{
			{ID: "call-1", Name: "render_model_views",
				Summary: json.RawMessage(`{"tool":"render","model":"@selected","views":["front"]}`)},
			{ID: "call-2", Name: "render_model_views",
				Summary: json.RawMessage(`{"tool":"render","model":"Tripo v3.1","views":["back"]}`)},
			{ID: "call-3", Name: "render_model_views",
				Summary: json.RawMessage(`{"tool":"render","model":"@step1","views":["left"]}`)},
		},
	}}
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, messages)
	ledger, err := fixture.service.WorkLedger(context.Background(), "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	checks := ledger.Steps[0].SelfChecks
	if len(checks) != 1 || checks[0].Tool != "render_model_views" || checks[0].Passed != 1 {
		t.Fatalf("only the resolvable self-check must attach: %+v", checks)
	}
	if len(ledger.Omissions) != 2 {
		t.Fatalf("unresolved tokens must be recorded: %+v", ledger.Omissions)
	}
	for _, omission := range ledger.Omissions {
		if omission.Kind != "self_check_unattributed" || omission.Subject == "" {
			t.Fatalf("omission = %+v", omission)
		}
	}
}
