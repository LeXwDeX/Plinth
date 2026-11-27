package catalog_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #406: a new session starts in the automatic approval mode. The default is
// the Product Schema's application.chat.approval_policy.default_mode and lives
// nowhere else: the session service, the public projection and the browser all
// read it from there. Which steps each mode approves is not part of this change.

func TestDefaultCatalogNewSessionApprovalModeIsAuto_406(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	policy := application.Chat.ApprovalPolicy
	if policy.DefaultMode != "auto" {
		t.Fatalf("default approval mode = %q, want auto", policy.DefaultMode)
	}
	// The person can still switch to either of the other modes, in the order the
	// top bar lists them.
	if !slices.Equal(policy.Modes, []string{"auto", "budget", "manual"}) {
		t.Fatalf("offered approval modes = %v, want [auto budget manual]", policy.Modes)
	}
	// The browser reads the same value from the public projection.
	public := application.Public().Chat.ApprovalPolicy
	if public.DefaultMode != policy.DefaultMode || !slices.Equal(public.Modes, policy.Modes) {
		t.Fatalf("public approval policy = %q %v, private = %q %v",
			public.DefaultMode, public.Modes, policy.DefaultMode, policy.Modes)
	}
}

// Changing the default must not move the policy of any mode: the capabilities
// strict (and budget without a plan) asks a person about are the same four.
func TestDefaultCatalogKeepsTheStrictApprovalCapabilities_406(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var manual []string
	for _, capability := range product.Application().Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	want := []string{"text-to-3d", "image-to-3d", "image-to-splat", "retexture"}
	if !slices.Equal(manual, want) {
		t.Fatalf("manual approval capabilities = %v, want %v", manual, want)
	}
}

// The words the Agent reads about plans follow the session's mode, not the
// default. Under the automatic default a plan card is the exception, so no text
// may send the Agent to propose one outside the budget mode (#406).
func TestPlanCardWordingDoesNotAssumeTheBudgetDefault_406(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat

	prompt := chat.Agent.SystemPrompt
	// The persona line used to promise a budget confirmation to every person.
	if strings.Contains(prompt, "确认制作简报和预算、验收结果") {
		t.Errorf("the system prompt still promises every person a budget confirmation")
	}
	// #407: the plan rule lives in the tool description and the approval block,
	// not in the standing prompt. The prompt only says the server authorises a
	// paid step by the session's mode, so no text invites a plan outside budget.
	if strings.Contains(prompt, "propose_production_plan") {
		t.Errorf("the system prompt duplicates the budget-mode plan rule")
	}
	for _, want := range []string{"按会话的「批准方式」授权", "不要让用户去勾选设置或切换模式"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}

	var planTool catalog.ChatAgentToolSchema
	for _, tool := range chat.AgentTools {
		if tool.Name == "propose_production_plan" {
			planTool = tool
		}
	}
	if planTool.Name == "" {
		t.Fatal("propose_production_plan is not declared")
	}
	// #702: no approval mode needs a plan; the Agent never proposes one by
	// itself, and the tool belongs to the budget ("approve") mode only.
	const first = "Do not propose a production plan on your own"
	if !strings.HasPrefix(planTool.Description, first) {
		t.Errorf("the plan tool does not start by forbidding unasked plans: %s", planTool.Description)
	}
	if !strings.Contains(planTool.Description, "Use this tool only when the person explicitly asks for a production plan") {
		t.Errorf("the plan tool does not say when it may be used: %s", planTool.Description)
	}

	// The automatic mode text is what the Agent reads in a default session: it
	// keeps the approval facts and adds that no plan is needed.
	var auto string
	for _, mode := range chat.ApprovalPolicy.Prompt.Modes {
		if mode.ID == "auto" {
			auto = mode.Description
		}
	}
	for _, want := range []string{
		"自动方式下，付费步骤由系统按会话批准方式自动批准，不出批准卡",
		"本会话每批准满 100 步，下一步会出一张批准卡",
		"不需要制作计划，也不要提交制作计划",
	} {
		if !strings.Contains(auto, want) {
			t.Errorf("the automatic mode text lacks %q: %s", want, auto)
		}
	}
	// #702: the budget ("approve") text no longer asks for a plan.
	for _, mode := range chat.ApprovalPolicy.Prompt.Modes {
		if mode.ID == "budget" && !strings.Contains(mode.Description, "不要主动提议或提交制作计划") {
			t.Errorf("the budget mode text still invites a plan: %s", mode.Description)
		}
	}
}
