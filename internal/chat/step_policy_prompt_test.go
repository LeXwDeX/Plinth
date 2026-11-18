package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// stepMCPAssets is the narrow reader the application injects into chat: it
// marks the requested sessions as external MCP assets.
type stepMCPAssets struct {
	mark map[string]bool
	err  error
}

func (reader stepMCPAssets) MCPAssetsOfSessions(
	_ context.Context,
	_ string,
	sessionIDs []string,
) (map[string]chat.MCPAsset, error) {
	if reader.err != nil {
		return nil, reader.err
	}
	assets := map[string]chat.MCPAsset{}
	for _, id := range sessionIDs {
		if reader.mark[id] {
			assets[id] = chat.MCPAsset{SessionID: id}
		}
	}
	return assets, nil
}

type stepPromptCase struct {
	// steps installs the MCP asset reader for the session, so its derived
	// approval mode is the internal steps mode.
	steps bool
	// storedMode is written to the session before the turn; it matters only
	// for a non-steps session.
	storedMode string
	// usage is the standing the turn reads; usageErr and noUsage make it
	// unknown.
	usage    executiongrant.StepSummary
	usageErr error
	noUsage  bool
	balances []chat.StepBalance
	costs    []chat.StepCost
	// noBalances and noCosts leave the readers nil.
	noBalances bool
	noCosts    bool
}

// stepPromptSystem runs one turn of a session set up by c with the shipped
// Schema and returns the system context the model read on its first request.
func stepPromptSystem(t *testing.T, c stepPromptCase) string {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	shipped := taskCatalog.Application()
	schema := configuredChatSchema(t)
	schema.Agent.SystemPrompt = shipped.Chat.Agent.SystemPrompt
	schema.ApprovalPolicy = shipped.Chat.ApprovalPolicy
	schema.Tools = shipped.Chat.Tools
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.ApprovalPolicy = approvalpolicy.New("manual",
			[]string{"auto", "budget", "manual"},
			[]string{"text-to-3d"}, []string{"text-to-3d", "text-to-image"})
	})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if c.steps {
		service.SetMCPStepSessions(stepMCPAssets{mark: map[string]bool{session.ID: true}})
	}
	if c.storedMode != "" {
		if _, _, err := repository.SetSessionApprovalMode(ctx, "owner-a", session.ID, c.storedMode); err != nil {
			t.Fatal(err)
		}
	}
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
	config := chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: &scopeToolExecutor{},
		StepPolicy: shipped.StepPolicy,
	}
	if !c.noUsage {
		config.StepUsage = func(context.Context, string, string) (executiongrant.StepSummary, error) {
			return c.usage, c.usageErr
		}
	}
	if !c.noBalances {
		config.StepBalances = func(context.Context) []chat.StepBalance { return c.balances }
	}
	if !c.noCosts {
		config.StepCosts = func(context.Context, string, string) []chat.StepCost { return c.costs }
	}
	orchestrator, err := chat.NewOrchestrator(config)
	if err != nil {
		t.Fatal(err)
	}
	options := chat.TurnOptions{CreativeDomain: "model-3d"}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID,
		Content: "hello", Options: options})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID,
		AssistantMessageID: assistant.ID, Options: options}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) == 0 {
		t.Fatal("the Agent was never called")
	}
	return agent.requests[0].Messages[0].Content
}

const stepBlockHead = "【3D 步数】"

// #330: a steps session carries the mode, the used steps, the three provider
// balances and the approximate per-step costs; the block follows the approval
// section.
func TestStepPolicyPromptCarriesTheAgentManagedFacts(t *testing.T) {
	system := stepPromptSystem(t, stepPromptCase{
		steps: true,
		usage: executiongrant.StepSummary{Entry: approvalpolicy.StepEntryMCP, Mode: approvalpolicy.StepModeAgent, Cap: 20, Used: 3},
		balances: []chat.StepBalance{
			{Label: "MeshyAI", Known: true, Amount: "1000", UnitLabel: "Meshy credits"},
			{Label: "Tripo"},
			{Label: "Hyper3D", Known: true, Amount: "42.5", UnitLabel: "Hyper3D credits"},
		},
		costs: []chat.StepCost{{OfferingID: "tripo-p1", Capability: "text-to-3d", Amount: "20", UnitLabel: "Tripo credits"}},
	})
	for _, want := range []string{
		stepBlockHead,
		"当前模式「Agent 自己管理」",
		"由你自己管理步数",
		"本资产已用 3 步；当前模式不设硬上限",
		"MeshyAI 1000 Meshy credits",
		"Tripo 余额未知",
		"Hyper3D 42.5 Hyper3D credits",
		"text-to-3d（tripo-p1）每步约 20 Tripo credits",
		"结果已经达标就停",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context omits %q", want)
		}
	}
	// steps is not an approval mode the Schema offers, so the approval section
	// states nothing for this session; the step section is inserted right
	// after that (empty) position in the section order.
	if strings.Contains(system, approvalBlockHead) {
		t.Error("a steps session states an approval mode")
	}
	if strings.Count(system, stepBlockHead) != 1 {
		t.Errorf("the step block appears %d times", strings.Count(system, stepBlockHead))
	}
}

// #330: the hard cap mode states the cap and the remaining steps.
func TestStepPolicyPromptStatesTheCapAndWhatIsLeft(t *testing.T) {
	system := stepPromptSystem(t, stepPromptCase{
		steps: true,
		usage: executiongrant.StepSummary{Entry: approvalpolicy.StepEntryMCP, Mode: approvalpolicy.StepModeCap, Cap: 20, Used: 3},
	})
	for _, want := range []string{
		"当前模式「限制步数」",
		"每个资产最多 20 步",
		"本资产已用 3 / 20 步，剩余 17 步",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context omits %q", want)
		}
	}
	if strings.Contains(system, "当前模式不设硬上限") {
		t.Error("the cap session states no cap")
	}
}

// #330: the unlimited mode says the platform sets no cap and does not present
// the person's stored cap number as a limit.
func TestStepPolicyPromptStatesTheUnlimitedMode(t *testing.T) {
	system := stepPromptSystem(t, stepPromptCase{
		steps: true,
		usage: executiongrant.StepSummary{Entry: approvalpolicy.StepEntryMCP, Mode: approvalpolicy.StepModeUnlimited, Cap: 20, Used: 4},
	})
	for _, want := range []string{
		"当前模式「不限步数」",
		"不限制 3D 步数",
		"本资产已用 4 步；当前模式不设硬上限",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context omits %q", want)
		}
	}
	if strings.Contains(system, "每个资产最多 20 步") {
		t.Error("the unlimited session states a cap")
	}
}

// #330: only a steps session renders the block; every other approval mode
// leaves it out.
func TestStepPolicyPromptIsOmittedOutsideTheStepsMode(t *testing.T) {
	for name, stored := range map[string]string{
		"manual session":  "manual",
		"auto session":    "auto",
		"budget session":  "budget",
		"unknown session": "yolo",
	} {
		system := stepPromptSystem(t, stepPromptCase{storedMode: stored})
		if strings.Contains(system, stepBlockHead) {
			t.Errorf("%s: the step block is present", name)
		}
		if strings.Contains(system, "当前模式「Agent 自己管理」") {
			t.Errorf("%s: the step mode is stated", name)
		}
	}
}

// #330: a failed step read or an absent reader writes unknown instead of
// zero, and the turn still runs.
func TestStepPolicyPromptDegradesToUnknownWithoutBlockingTheTurn(t *testing.T) {
	for name, testCase := range map[string]stepPromptCase{
		"the standing read fails": {steps: true, usageErr: errors.New("grant store unavailable")},
		"no standing reader":      {steps: true, noUsage: true},
	} {
		system := stepPromptSystem(t, testCase)
		for _, want := range []string{
			stepBlockHead,
			"读取用户的 3D 步数设置失败，模式按未知处理",
			"本资产的已用步数读取失败，按未知处理；不要当成 0 步",
			"三家 3D 供应商余额未知",
			"本资产还没有可估算每步消耗的 3D 服务",
		} {
			if !strings.Contains(system, want) {
				t.Errorf("%s: the system context omits %q", name, want)
			}
		}
	}
}

// #330: a balance reader that answers unknown per account is shown as unknown
// per account, not as zero.
func TestStepPolicyPromptKeepsAnUnknownBalanceUnknown(t *testing.T) {
	system := stepPromptSystem(t, stepPromptCase{
		steps: true,
		usage: executiongrant.StepSummary{Entry: approvalpolicy.StepEntryMCP, Mode: approvalpolicy.StepModeAgent, Cap: 20, Used: 0},
		balances: []chat.StepBalance{
			{Label: "MeshyAI"},
			{Label: "Tripo", Known: true, Amount: "0", UnitLabel: "Tripo credits"},
		},
	})
	for _, want := range []string{
		"MeshyAI 余额未知",
		"Tripo 0 Tripo credits",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context omits %q", want)
		}
	}
}
