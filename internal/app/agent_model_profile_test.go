package app

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #253: a standard OpenAI-compatible model gets no vendor fields and no DSML
// parsing; DeepSeek, Qwen and GLM keep their declared behavior through profiles.
func TestAgentVendorBehaviorFollowsModelProfiles(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := taskCatalog.Application().Chat
	for _, model := range []string{"gpt-4o", "some-vision-model", ""} {
		if options := chatThinkingOptions(chat.Agent, model); options != nil {
			t.Fatalf("%q got vendor options %#v", model, options)
		}
		if orchestratorChatSchema(chat, model).DSML.Enabled {
			t.Fatalf("%q got DSML parsing", model)
		}
	}
	deepseek := chatThinkingOptions(chat.Agent, "DeepSeek-Flash")
	// #322: the level is the session's choice written to effort_field, with
	// DeepSeek's documented levels and the user's default "low".
	if deepseek == nil || !deepseek.ReasoningPassback || deepseek.EffortField != "reasoning_effort" ||
		deepseek.DefaultEffort != "low" || strings.Join(deepseek.EffortLevels, ",") != "low,high,max" ||
		!deepseek.StreamIncludeUsage || deepseek.RequestFields["reasoning_effort"] != nil ||
		!orchestratorChatSchema(chat, "deepseek").DSML.Enabled {
		t.Fatalf("deepseek options = %#v", deepseek)
	}
	qwen := chatThinkingOptions(chat.Agent, "qwen3.8-flash")
	if qwen == nil || string(qwen.RequestFields["enable_thinking"]) != "true" || qwen.ReasoningPassback ||
		strings.Join(qwen.EffortLevels, ",") != "low,medium,xhigh" || qwen.DefaultEffort != "xhigh" ||
		orchestratorChatSchema(chat, "qwen3.8-flash").DSML.Enabled {
		t.Fatalf("qwen options = %#v", qwen)
	}
	// GLM (Zhipu) thinking mode at its highest level, per the current official
	// docs (checked 2026-10-20):
	//   - https://docs.bigmodel.cn/api-reference/模型-api/对话补全 :
	//     thinking.type enum enabled|disabled; reasoning_effort enum
	//     max|xhigh|high|medium|low|minimal|none (GLM-5.2+, default max;
	//     GLM-5.3 accepts low|high|max; vision models max|high|low).
	//   - https://docs.bigmodel.cn/cn/guide/capabilities/thinking-mode :
	//     with tools, the assistant message must carry reasoning_content back
	//     (interleaved thinking). GLM does not write DSML tool calls.
	glm := chatThinkingOptions(chat.Agent, "GLM-5.3")
	if glm == nil || !glm.ReasoningPassback || glm.DefaultEffort != "max" || glm.StreamIncludeUsage ||
		strings.Join(glm.EffortLevels, ",") != "low,high,max" ||
		strings.Join(strings.Fields(string(glm.RequestFields["thinking"])), "") != `{"type":"enabled"}` ||
		orchestratorChatSchema(chat, "glm-5.3").DSML.Enabled {
		t.Fatalf("glm options = %#v", glm)
	}
	disabled := *chat.Agent.Thinking
	disabled.Enabled = false
	agent := chat.Agent
	agent.Thinking = &disabled
	if chatThinkingOptions(agent, "deepseek") != nil {
		t.Fatal("profiles must be off when thinking is disabled")
	}
}
