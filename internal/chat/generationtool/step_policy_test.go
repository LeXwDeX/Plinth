package generationtool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// stepSession is a session the steps mode governs: nothing assigns it to a real
// session yet, so the test supplies both halves of the derivation.
type stepSession struct{ entry string }

func (stepSession) SessionApprovalMode(context.Context, string, string) (string, error) {
	return approvalpolicy.ModeSteps, nil
}

func (s stepSession) SessionStepEntry(context.Context, string, string) (string, error) {
	return s.entry, nil
}

// stepHarness is the policy harness with the Product Schema's 3D step policy
// wired as the application assembly wires it.
func stepHarness(t *testing.T, toolName, capability string, tasks generationtool.SessionTasks) (
	*generationtool.Executor, chat.ToolCommand, *grantGateway, *sqlite.Repository, *executiongrant.Service) {
	t.Helper()
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "steps.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner-a",
		chat.Message{ID: "user-a", SessionID: "session-a", Role: "user", Content: "go", CreatedAt: now},
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
	if application.StepPolicy == nil {
		t.Fatal("the default Catalog declares no step policy")
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == toolName {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatalf("tool %q missing", toolName)
	}
	tool.IdempotencyKeyTemplate = "agent:{session_id}:{call_id}"
	tool.FallbackCapability = generation.Capability(capability)
	offering := generation.Offering{ID: "fake/offering", Revision: "rev-a", ModelID: "fake-model",
		Capability: generation.Capability(capability), Enabled: true, Timeout: time.Second,
		EstimatedCredits: 7,
		Parameters:       []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt}}}
	gateway := &grantGateway{fakeGateway: &fakeGateway{offerings: []generation.Offering{offering}}, repo: repo}
	var manual, known []string
	for _, capability := range application.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	for _, capability := range application.Capabilities {
		known = append(known, string(capability.ID))
	}
	counted := make([]string, 0, len(application.StepPolicy.CountedCapabilities))
	for _, capability := range application.StepPolicy.CountedCapabilities {
		counted = append(counted, string(capability))
	}
	policy := approvalpolicy.New(application.Chat.ApprovalPolicy.DefaultMode, application.Chat.ApprovalPolicy.Modes, manual, known).
		WithSteps(approvalpolicy.StepsPolicy{
			Counted:     counted,
			DefaultList: approvalpolicy.StepSetting{Mode: application.StepPolicy.DefaultMode.List, Cap: application.StepPolicy.Cap.Default},
			DefaultMCP:  approvalpolicy.StepSetting{Mode: application.StepPolicy.DefaultMode.MCP, Cap: application.StepPolicy.Cap.Default},
		})
	service := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: policy, Modes: stepSession{entry: approvalpolicy.StepEntryList}}
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, Tasks: tasks, References: &fakeReferences{},
		Operations: repo, Grants: service, Chat: application.Chat, StepPolicy: application.StepPolicy, Texts: texts,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: []byte(`{"prompt":"go"}`)},
		Tool: tool, Options: chat.TurnOptions{AIModel: "fake-model"}}
	return executor, command, gateway, repo, service
}

func saveStepSettings(t *testing.T, repo *sqlite.Repository, list, mcp approvalpolicy.StepSetting) {
	t.Helper()
	expected := int64(0)
	if stored, found, err := repo.AccountSettings(context.Background(), "owner-a"); err != nil {
		t.Fatal(err)
	} else if found {
		expected = stored.Version
	}
	if _, err := repo.PutAccountSettings(context.Background(), "owner-a", expected,
		accountsettings.Settings{List: list, MCP: mcp}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func nextCall(command chat.ToolCommand, id string) chat.ToolCommand {
	command.Call.ID = id
	return command
}

func agentError(t *testing.T, result chat.ToolResult) string {
	t.Helper()
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	if content["success"] != false {
		t.Fatalf("agent content = %v", content)
	}
	text, _ := content["error"].(string)
	return text
}

// Under the steps mode nothing waits for a person: by default the Agent manages
// its own steps, so a 3D step is approved by policy at once and submitted.
func TestAgentToolStepsModeApprovesA3DStepWithoutACard(t *testing.T) {
	executor, command, gateway, repo, _ := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)
	var observed []chat.ToolObservation
	result, err := executor.Execute(context.Background(), command, func(o chat.ToolObservation) error {
		observed = append(observed, o)
		return nil
	})
	if err != nil || result.Suspend || result.NoSubmission || gateway.submitCount != 1 {
		t.Fatalf("steps mode 3D step: %+v submits=%d err=%v", result, gateway.submitCount, err)
	}
	if len(observed) == 0 || observed[0].ApprovalSource != "policy" {
		t.Fatalf("observation = %+v", observed)
	}
	op, err := repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "call-a")
	if err != nil || op.Detached {
		t.Fatalf("a steps-mode step got an approval card: %+v err=%v", op, err)
	}
	grant, err := repo.GetExecutionGrant(context.Background(), "owner-a", op.GrantRequestID)
	if err != nil || grant.Status != executiongrant.Approved || grant.DecidedBy != executiongrant.StepsActor {
		t.Fatalf("grant = %+v err=%v", grant, err)
	}
}

// 2D steps are never counted or blocked, even in a session whose cap is reached.
func TestAgentToolStepsModeNeverCountsOrBlocksATwoDimensionalStep(t *testing.T) {
	executor, command, gateway, repo, _ := stepHarness(t, "refine_image", "text-to-image", nil)
	saveStepSettings(t, repo, capSettingOf(1), capSettingOf(1))
	for index, id := range []string{"call-a", "call-b", "call-c"} {
		result, err := executor.Execute(context.Background(), nextCall(command, id), nil)
		if err != nil || result.Suspend || result.NoSubmission || gateway.submitCount != index+1 {
			t.Fatalf("2D step %d: %+v submits=%d err=%v", index+1, result, gateway.submitCount, err)
		}
	}
}

func capSettingOf(limit int) approvalpolicy.StepSetting {
	return approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeCap, Cap: limit}
}

func TestAgentToolStepsCapRefusesTheStepOverTheCapWithTheSchemaText(t *testing.T) {
	ctx := context.Background()
	executor, command, gateway, repo, service := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)
	saveStepSettings(t, repo, capSettingOf(1), capSettingOf(1))

	first, err := executor.Execute(ctx, command, nil)
	if err != nil || first.NoSubmission || gateway.submitCount != 1 {
		t.Fatalf("the step inside the cap: %+v submits=%d err=%v", first, gateway.submitCount, err)
	}

	over := nextCall(command, "call-b")
	refused, err := executor.Execute(ctx, over, nil)
	if err != nil || refused.Suspend || !refused.NoSubmission || gateway.submitCount != 1 {
		t.Fatalf("the step over the cap: %+v submits=%d err=%v", refused, gateway.submitCount, err)
	}
	const want = "这个资产的 3D 步数已用完（已用 1 步，上限 1 步），这一步没有执行。请不要再提交新的 3D 步骤，直接告诉用户已达到步数上限；用户可以在顶栏的「3D 步数」里调高步数上限后再继续。"
	if text := agentError(t, refused); text != want || refused.Error != want {
		t.Fatalf("step-cap text = %q (error %q)", text, refused.Error)
	}
	if strings.Contains(refused.AgentContent, "{used}") || strings.Contains(refused.AgentContent, "{cap}") {
		t.Fatalf("placeholders left in %q", refused.AgentContent)
	}

	// The grant was denied in place: no card, no Task, nothing for a person.
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-b")
	if err != nil || op.Detached || op.GrantRequestID == "" {
		t.Fatalf("refused operation = %+v err=%v", op, err)
	}
	grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
	if err != nil || grant.Status != executiongrant.Denied || !grant.StepCapReached() || grant.TaskID != "" {
		t.Fatalf("refused grant = %+v err=%v", grant, err)
	}
	pending, err := repo.ListPendingDetached(ctx, "owner-a", "session-a", 10, time.Time{})
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending approvals after a refusal = %+v err=%v", pending, err)
	}

	// A replay of the same call answers with the step-cap text again, not the
	// generic denial of a person's card, and prepares and submits nothing.
	prepared := gateway.preparedCount
	replay, err := executor.Execute(ctx, over, nil)
	if err != nil || !replay.NoSubmission || replay.Suspend || gateway.submitCount != 1 || gateway.preparedCount != prepared {
		t.Fatalf("replay = %+v submits=%d prepares=%d err=%v", replay, gateway.submitCount, gateway.preparedCount, err)
	}
	if text := agentError(t, replay); text != want {
		t.Fatalf("replay text = %q", text)
	}
	denied := application(t).Chat.Agent.ConsentDeniedContent
	if denied == "" || strings.Contains(replay.AgentContent, denied) || strings.Contains(refused.AgentContent, denied) {
		t.Fatalf("the generic denial %q was used: %q", denied, replay.AgentContent)
	}

	// After the person raises the cap, a new call goes through.
	saveStepSettings(t, repo, capSettingOf(5), capSettingOf(5))
	again, err := executor.Execute(ctx, nextCall(command, "call-c"), nil)
	if err != nil || again.NoSubmission || gateway.submitCount != 2 {
		t.Fatalf("the step after the cap was raised: %+v submits=%d err=%v", again, gateway.submitCount, err)
	}
	status, err := service.StepStatus(ctx, "owner-a", "session-a")
	if err != nil || status.Used != 2 || status.Cap != 5 {
		t.Fatalf("status = %+v err=%v", status, err)
	}
}

func application(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application()
}

func TestAgentToolStepsCapTextFollowsTheTurnLocale(t *testing.T) {
	ctx := context.Background()
	executor, command, _, repo, _ := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)
	saveStepSettings(t, repo, capSettingOf(1), capSettingOf(1))
	command.Options.Locale = "en"
	if first, err := executor.Execute(ctx, command, nil); err != nil || first.NoSubmission {
		t.Fatalf("first: %+v err=%v", first, err)
	}
	refused, err := executor.Execute(ctx, nextCall(command, "call-b"), nil)
	if err != nil || !refused.NoSubmission {
		t.Fatalf("refused: %+v err=%v", refused, err)
	}
	// #383 moved the step-cap entry from the account settings to the top-bar
	// "3D steps" control, so the English text now names that place; the rest of
	// the refusal is unchanged.
	const want = "This asset has used all of its 3D steps (1 used, cap 1); this step was not run. Do not submit more 3D steps. Tell the user the step cap was reached; they can raise it in \"3D steps\" in the top bar to continue."
	if text := agentError(t, refused); text != want {
		t.Fatalf("english text = %q", text)
	}
}

// The reliability rule stays: repeating an attempt that was charged upstream
// but not stored still waits for a person on a card, whatever the mode.
func TestAgentToolStepsModeStillSendsARechargeRiskToACard(t *testing.T) {
	ctx := context.Background()
	tasks := &sessionTasks{tasks: []generation.Task{unstoredTask("task_old", "image-to-3d", time.Now().Add(-time.Hour))}}
	executor, command, gateway, repo, _ := stepHarness(t, "create_model_3d_from_image", "image-to-3d", tasks)
	result, err := executor.Execute(ctx, command, nil)
	if err != nil || !result.NoSubmission || result.Suspend || gateway.submitCount != 0 {
		t.Fatalf("a recharge risk under steps: %+v submits=%d err=%v", result, gateway.submitCount, err)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if err != nil || op.RechargeTaskID != "task_old" || !op.Detached {
		t.Fatalf("operation = %+v err=%v", op, err)
	}
	grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
	if err != nil || grant.Status != executiongrant.Pending {
		t.Fatalf("the policy decided a recharge risk: %+v err=%v", grant, err)
	}
}

// A steps session that names no entry cannot be governed: the step waits for a
// person, the stricter result, instead of running unlimited.
func TestAgentToolStepsModeWithoutAnEntryWaitsForAPerson(t *testing.T) {
	ctx := context.Background()
	executor, command, gateway, repo, service := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)
	service.Modes = stepSession{entry: "web"}
	result, err := executor.Execute(ctx, command, nil)
	if err != nil || !result.NoSubmission || result.Suspend || gateway.submitCount != 0 {
		t.Fatalf("a step without an entry: %+v submits=%d err=%v", result, gateway.submitCount, err)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if err != nil || !op.Detached {
		t.Fatalf("operation = %+v err=%v", op, err)
	}
	if grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID); err != nil || grant.Status != executiongrant.Pending {
		t.Fatalf("grant = %+v err=%v", grant, err)
	}
}

// A step the policy approved whose submit is then refused for good must give its
// step back: otherwise the cap would stay occupied by a Task that never existed.
func TestAgentToolStepsCapReleasesTheStepOfARefusedSubmit(t *testing.T) {
	ctx := context.Background()
	executor, command, gateway, repo, service := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)
	saveStepSettings(t, repo, capSettingOf(1), capSettingOf(1))
	gateway.submitErr = &generation.Error{Code: generation.ErrInvalidInput, Operation: "submit"}

	result, err := executor.Execute(ctx, nextCall(command, "call-refused"), nil)
	if err == nil || !result.NoSubmission || gateway.submitCount != 1 {
		t.Fatalf("refused submit: result=%+v err=%v submits=%d", result, err, gateway.submitCount)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-refused")
	if err != nil {
		t.Fatal(err)
	}
	abandoned, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
	if err != nil || abandoned.Status != executiongrant.Denied || abandoned.DecidedBy != executiongrant.AbandonedActor || abandoned.StepCapReached() {
		t.Fatalf("grant after a refused submit = %+v err=%v", abandoned, err)
	}
	if status, err := service.StepStatus(ctx, "owner-a", "session-a"); err != nil || status.Used != 0 {
		t.Fatalf("an abandoned step still counts: %+v err=%v", status, err)
	}
	gateway.submitErr = nil
	retried, err := executor.Execute(ctx, nextCall(command, "call-retry"), nil)
	if err != nil || retried.NoSubmission || gateway.submitCount != 2 {
		t.Fatalf("the retry was refused by a step that never ran: %+v submits=%d err=%v", retried, gateway.submitCount, err)
	}
}
