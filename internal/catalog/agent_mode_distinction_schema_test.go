package catalog

import (
	"strings"
	"testing"
)

// #150: the Agent once told a person to "switch to manual mode" to get a
// Remesh -> Rigging confirmation, mixing the Agent scope (2D/3D/auto, limits
// the Agent's tools) with the approval mode (auto/manual, decides which paid
// steps need a person). The Schema text the Agent reads must keep them apart.
func TestAgentTextsSeparateAgentScopeFromApprovalMode(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat
	require := func(name, text string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s omits %q", name, want)
			}
		}
	}
	require("system prompt", chat.Agent.SystemPrompt,
		"Agent 范围模式", "批准方式", "输入区右下角", "顶栏", "你不能切换",
		"重拓扑（Remesh）、绑骨（Rigging）、动画", "不要说需要切换到手动模式", "固定的额外积分", "以工具结果里的 quote 为准")
	require("agent scope context", chat.AgentScope.ContextTemplate,
		"与付费步骤是否需要批准无关", "批准方式", "你都不能切换", "不要为了得到付费确认建议用户切换 Agent 范围")
	require("agent scope rejection", chat.AgentScope.RejectionTemplate,
		"Agent scope", "not the approval mode", "Do not mix the two up")
	seen := 0
	for _, tool := range chat.Tools {
		if tool.Name != "plan_model_processing" && tool.Name != "process_model" {
			continue
		}
		seen++
		require("tool "+tool.Name, tool.Description,
			"approval mode", "capabilities are approved by policy in both modes, except that a step with fixed extra credits",
			"unrelated to the Agent scope", "Never tell the person to switch the Agent scope or the approval mode")
	}
	if seen != 2 {
		t.Fatalf("found %d of the 2 processing tools", seen)
	}
}
