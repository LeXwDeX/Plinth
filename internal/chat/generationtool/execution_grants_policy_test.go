package generationtool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
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
)

type sessionMode struct{ mode string }

func (m sessionMode) SessionApprovalMode(context.Context, string, string) (string, error) {
	return m.mode, nil
}

// runPolicyCase executes one agent tool call for the capability under the given
// session approval mode and returns the result plus the gateway it used.
func runPolicyCase(t *testing.T, mode, toolName, capability string) (chat.ToolResult, *grantGateway, []chat.ToolObservation) {
	t.Helper()
	executor, command, gateway, _ := policyHarness(t, sessionMode{mode: mode}, toolName, capability)
	var observed []chat.ToolObservation
	result, err := executor.Execute(context.Background(), command, func(o chat.ToolObservation) error {
		observed = append(observed, o)
		return nil
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return result, gateway, observed
}

// switchableMode is a session whose approval mode a test changes mid-wait.
type switchableMode struct{ mode *string }

func (m switchableMode) SessionApprovalMode(context.Context, string, string) (string, error) {
	return *m.mode, nil
}

func policyHarness(t *testing.T, modes executiongrant.SessionModeReader, toolName, capability string) (
	*generationtool.Executor, chat.ToolCommand, *grantGateway, *sqlite.Repository) {
	t.Helper()
	return policyHarnessConfigured(t, modes, toolName, capability, nil)
}

// policyHarnessConfigured is policyHarness with a hook to adjust the grant
// service (expiry, clock) before the executor is built.
func policyHarnessConfigured(t *testing.T, modes executiongrant.SessionModeReader, toolName, capability string,
	configure func(*executiongrant.Service)) (*generationtool.Executor, chat.ToolCommand, *grantGateway, *sqlite.Repository) {
	t.Helper()
	return policyHarnessWithTasks(t, modes, toolName, capability, configure, nil)
}

// policyHarnessWithTasks is policyHarnessConfigured with the session task reader
// the recharge guard uses (#294).
func policyHarnessWithTasks(t *testing.T, modes executiongrant.SessionModeReader, toolName, capability string,
	configure func(*executiongrant.Service), tasks generationtool.SessionTasks) (*generationtool.Executor, chat.ToolCommand, *grantGateway, *sqlite.Repository) {
	t.Helper()
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "policy.db"))
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
	policy := approvalpolicy.New(
		application.Chat.ApprovalPolicy.DefaultMode, application.Chat.ApprovalPolicy.Modes,
		manual, known)
	service := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: policy, Modes: modes}
	if configure != nil {
		configure(service)
	}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, Tasks: tasks, References: &fakeReferences{},
		Operations: repo, Grants: service, Chat: application.Chat,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: []byte(`{"prompt":"go"}`)},
		Tool: tool, Options: chat.TurnOptions{AIModel: "fake-model"}}
	return executor, command, gateway, repo
}

func TestAgentToolManualModeImageStepSubmitsWithoutSuspending(t *testing.T) {
	result, gateway, observed := runPolicyCase(t, "manual", "refine_image", "text-to-image")
	if result.Suspend || gateway.submitCount != 1 {
		t.Fatalf("manual image step waited: %+v submits=%d", result, gateway.submitCount)
	}
	if len(observed) == 0 || observed[0].ApprovalSource != "policy" || observed[0].EstimatedCredits != 7 {
		t.Fatalf("observation = %+v", observed)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	if content["estimated_credits"] != float64(7) || content["approval"] != "policy" {
		t.Fatalf("result fields = %v", content)
	}
}

// The turn does not wait for the human: it ends as soon as the approval exists,
// and the pending-approval service owns the request from then on.
func TestAgentToolManualModeImageTo3DEndsTheTurnAtTheApprovalCard(t *testing.T) {
	result, gateway, observed := runPolicyCase(t, "manual", "create_model_3d_from_image", "image-to-3d")
	if result.Suspend || !result.NoSubmission || result.AgentContent == "" || gateway.submitCount != 0 || len(observed) != 0 {
		t.Fatalf("manual image-to-3d must end the turn without submitting: %+v submits=%d observed=%d", result, gateway.submitCount, len(observed))
	}
}

func TestAgentToolAutoModeImageTo3DDoesNotSuspend(t *testing.T) {
	result, gateway, observed := runPolicyCase(t, "auto", "create_model_3d_from_image", "image-to-3d")
	if result.Suspend || gateway.submitCount != 1 {
		t.Fatalf("auto image-to-3d waited: %+v submits=%d", result, gateway.submitCount)
	}
	if len(observed) == 0 || observed[0].ApprovalSource != "policy" {
		t.Fatalf("observation = %+v", observed)
	}
}

func TestAgentToolUnknownModeBehavesAsManual(t *testing.T) {
	result, gateway, _ := runPolicyCase(t, "bogus", "create_model_3d_from_image", "image-to-3d")
	if result.Suspend || !result.NoSubmission || gateway.submitCount != 0 {
		t.Fatalf("unknown mode skipped the human: %+v submits=%d", result, gateway.submitCount)
	}
}

// A request the user was shown must not be approved behind their back because
// they later switched the session to automatic approval.
func TestSwitchingToAutoWhileAnAgentGrantWaitsDoesNotApproveIt(t *testing.T) {
	mode := "manual"
	executor, command, gateway, repo := policyHarness(t, switchableMode{mode: &mode},
		"create_model_3d_from_image", "image-to-3d")
	ctx := context.Background()
	first, err := executor.Execute(ctx, command, nil)
	if err != nil || first.Suspend || !first.NoSubmission || gateway.submitCount != 0 {
		t.Fatalf("manual image-to-3d must wait for the human: %+v submits=%d err=%v", first, gateway.submitCount, err)
	}
	mode = "auto"
	// A replay of the turn (restart, late wake-up) leaves the detached request alone.
	resumed, err := executor.Execute(ctx, command, nil)
	if err != nil || resumed.Suspend || !resumed.NoSubmission || gateway.submitCount != 0 {
		t.Fatalf("auto mode approved a waiting request: %+v submits=%d err=%v", resumed, gateway.submitCount, err)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if err != nil || !op.Detached || !op.PolicyEvaluated {
		t.Fatalf("operation = %+v err=%v", op, err)
	}
	grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
	if err != nil || grant.Status != executiongrant.Pending {
		t.Fatalf("grant = %+v err=%v", grant, err)
	}
	// A new call in the same session is evaluated afresh: now it is automatic.
	next := command
	next.ConsentWaitDeadline = time.Time{}
	next.Call.ID = "call-b"
	auto, err := executor.Execute(ctx, next, nil)
	if err != nil || auto.Suspend || gateway.submitCount != 1 {
		t.Fatalf("a new request under auto mode should run: %+v submits=%d err=%v", auto, gateway.submitCount, err)
	}
}

// An input the Schema refuses (the extra-credit consent was not given) is a
// tool failure the Agent can relay, not an empty receipt and not a submission.
func TestRefusedInputReachesTheAgentAsAToolFailure(t *testing.T) {
	executor, command, gateway, _ := policyHarness(t, sessionMode{mode: "manual"}, "create_model_3d_from_image", "image-to-3d")
	const reason = "已选择附加导出格式，请先勾选额外积分消耗同意"
	gateway.prepareErr = &generation.Error{Code: generation.ErrInvalidInput, Operation: "normalize_input", Detail: reason, UserFacing: true}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.Suspend || !result.NoSubmission || result.Error != reason || gateway.submitCount != 0 {
		t.Fatalf("refused input: %+v submits=%d err=%v", result, gateway.submitCount, err)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil || content["success"] != false || content["error"] != reason {
		t.Fatalf("agent content = %q (%v)", result.AgentContent, err)
	}
	// Other failures stay errors: nothing is invented for them.
	gateway.prepareErr = &generation.Error{Code: generation.ErrUnavailable, Detail: "store down"}
	if _, err := executor.Execute(context.Background(), command, nil); err == nil {
		t.Fatal("an unavailable store must stay an error")
	}
}
