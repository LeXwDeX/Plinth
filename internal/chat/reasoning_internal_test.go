package chat

// 思考与回传的内部单测（#377）：来源过滤与检查点清理依赖私有构造函数
// （followUpRequest 的消息拼装），外部测试包无法构造，故放内部包。

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// 只有一个模型自己的 reasoning 会回到它的请求里：消息上的每一项都带着来源
// （连接、模型、协议、类型），换模型、换协议、换类型或换连接都丢弃；被丢弃
// 项的助手消息仍然保留，带工具调用的消息仍然合法（厂商要求的是那个字段，
// 不是其中的内容）。
func TestFilterReasoningKeepsOnlyTheRequestsOwnSource(t *testing.T) {
	own := agents.ReasoningItem{
		Connection: "llm-1", Model: "deepseek", Wire: "chat_completions",
		Kind: agents.ReasoningPlaintext, Content: "deepseek plan",
	}
	messages := []agents.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: "c0", Name: "lookup"}}, Reasoning: []agents.ReasoningItem{
			own,
			{Connection: "llm-1", Model: "qwen-max", Wire: "chat_completions", Kind: agents.ReasoningPlaintext, Content: "qwen plan"},
			{Connection: "llm-1", Model: "deepseek", Wire: "responses", Kind: agents.ReasoningEncrypted, Content: `{"type":"reasoning"}`},
			{Connection: "llm-1", Model: "deepseek", Wire: "chat_completions", Kind: agents.ReasoningEncrypted, Content: `{"type":"reasoning"}`},
			{Connection: "llm-2", Model: "deepseek", Wire: "chat_completions", Kind: agents.ReasoningPlaintext, Content: "another connection"},
			{Content: "a checkpoint written before the kinds existed"},
		}},
		{Role: "tool", ToolCallID: "c0", Content: "[]"},
	}
	filterReasoning(messages, agents.Source{
		Connection: "llm-1", Model: "deepseek", Wire: "chat_completions", Kind: agents.ReasoningPlaintext,
	})
	kept := messages[1].Reasoning
	if len(kept) != 1 || kept[0].Content != "deepseek plan" {
		t.Fatalf("kept = %#v", kept)
	}
	if len(messages[0].Reasoning) != 0 || len(messages[2].Reasoning) != 0 {
		t.Fatalf("a message that never carried reasoning gained some: %#v", messages)
	}
	// The tool-call message is still there, with no reasoning at all: the
	// follow-up request stays legal for a vendor that requires the field.
	if messages[1].Role != "assistant" || len(messages[1].ToolCalls) != 1 || len(messages[1].Reasoning) != 1 {
		t.Fatalf("the assistant message changed: %#v", messages[1])
	}
	// 换模型（deepseek → qwen → deepseek）：qwen 那一轮记录的 reasoning 带着
	// qwen 的来源，等会话切回 deepseek 时不再回传；请求本身仍然发出。
	qwen := agents.Request{Model: "qwen-max", Thinking: &agents.Thinking{
		Wire: "chat_completions", Kind: agents.ReasoningPlaintext,
	}}
	filterReasoning(messages, agents.ForRequest(qwen))
	if len(messages[1].Reasoning) != 0 {
		t.Fatalf("qwen was given deepseek's reasoning: %#v", messages[1].Reasoning)
	}
	messages[1].Reasoning = reasoningItems(qwen, agents.Completion{ReasoningContent: "qwen plan"})
	if len(messages[1].Reasoning) != 1 {
		t.Fatalf("qwen round items = %#v", messages[1].Reasoning)
	}
	back := agents.Request{Model: "deepseek", Thinking: &agents.Thinking{
		Wire: "chat_completions", Kind: agents.ReasoningPlaintext,
	}}
	filterReasoning(messages, agents.ForRequest(back))
	if len(messages[1].Reasoning) != 0 {
		t.Fatalf("reasoning came back to a model that never produced it: %#v", messages[1].Reasoning)
	}
	// 请求仍然合法：助手消息与工具调用都在，只是没有 reasoning。
	if messages[1].Role != "assistant" || len(messages[1].ToolCalls) != 1 {
		t.Fatalf("the assistant message changed: %#v", messages[1])
	}
}

// 一轮的 reasoning 由编排器记录：来源取自请求，内容为空时不产生条目。
func TestReasoningItemsCarryTheRequestsSource(t *testing.T) {
	request := agents.Request{Model: "deepseek", Connection: "llm-1", Thinking: &agents.Thinking{
		Wire: "chat_completions", Kind: agents.ReasoningPlaintext, Passback: true,
	}}
	items := reasoningItems(request, agents.Completion{Content: "done", ReasoningContent: "plan"})
	if len(items) != 1 || items[0].Model != "deepseek" || items[0].Connection != "llm-1" ||
		items[0].Wire != "chat_completions" || items[0].Kind != agents.ReasoningPlaintext || items[0].Content != "plan" {
		t.Fatalf("items = %#v", items)
	}
	if got := reasoningItems(request, agents.Completion{Content: "done"}); got != nil {
		t.Fatalf("a reply without reasoning produced %#v", got)
	}
	// 没有冻结契约的请求（标题、压缩摘要）不产生来源为空的条目。
	bare := agents.Request{Model: "deepseek"}
	if got := reasoningItems(bare, agents.Completion{ReasoningContent: "plan"}); len(got) != 1 || got[0].Kind != "" {
		t.Fatalf("bare request items = %#v", got)
	}
}

// 回合结束后，检查点里的 reasoning 被清掉（#382 已清空请求，#377 补上
// reasoning），正文与工具调用保留。
func TestTurnCheckpointDropsReasoningWhenTheTurnEnds(t *testing.T) {
	checkpoint := TurnCheckpoint{
		Round: 2,
		Request: agents.Request{Messages: []agents.Message{
			{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: "c0"}}, Reasoning: []agents.ReasoningItem{
				{Model: "deepseek", Wire: "chat_completions", Kind: agents.ReasoningPlaintext, Content: "plan"},
			}},
		}},
		Completion: agents.Completion{Content: "partial", ReasoningContent: "private plan"},
		Completed: []agents.Message{
			{Role: "tool", ToolCallID: "c0", Content: "[]"},
		},
	}
	cleared := checkpoint.WithoutRequests()
	if cleared.Completion.ReasoningContent != "" || cleared.Completion.Content != "partial" {
		t.Fatalf("cleared completion = %#v", cleared.Completion)
	}
	if len(cleared.Request.Messages) != 0 || len(cleared.Completed) != 1 || cleared.Round != 2 {
		t.Fatalf("cleared checkpoint = %#v", cleared)
	}
}
