package mock_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/mock"
)

func usageAdapter(t *testing.T) *mock.Adapter {
	t.Helper()
	adapter, err := mock.New(mock.Config{
		ToolName: "configured-tool", PromptArgument: "prompt", Summary: "done",
		UserRole: "user", ToolRole: "tool",
		SummaryTemplate: "（Mock 摘要）句柄：{handles}",
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

// #322: the mock Agent reports usage like an upstream, so the context meter
// and automatic compaction run end to end on the mock stack. The count grows
// with the request; a user message can name the prompt tokens to report.
func TestMockReportsUsageThatGrowsWithTheRequest(t *testing.T) {
	adapter := usageAdapter(t)
	short, err := adapter.Stream(context.Background(), agents.Request{Messages: []agents.Message{{Role: "user", Content: "hi"}}}, nil)
	if err != nil || short.Usage == nil || short.Usage.PromptTokens <= 0 || short.Usage.CompletionTokens <= 0 {
		t.Fatalf("short usage = %#v %v", short.Usage, err)
	}
	long, err := adapter.Stream(context.Background(), agents.Request{Messages: []agents.Message{
		{Role: "system", Content: strings.Repeat("规则", 500)}, {Role: "user", Content: "hi"},
	}}, nil)
	if err != nil || long.Usage == nil || long.Usage.PromptTokens <= short.Usage.PromptTokens {
		t.Fatalf("long usage = %#v, short = %#v", long.Usage, short.Usage)
	}
	named, err := adapter.Stream(context.Background(), agents.Request{Messages: []agents.Message{
		{Role: "user", Content: "fill it up [mock:usage=900000]"},
	}}, nil)
	if err != nil || named.Usage == nil || named.Usage.PromptTokens != 900000 {
		t.Fatalf("named usage = %#v %v", named.Usage, err)
	}
	none, err := adapter.Stream(context.Background(), agents.Request{Messages: []agents.Message{
		{Role: "user", Content: "say nothing [mock:no_usage]"},
	}}, nil)
	if err != nil || none.Usage != nil {
		t.Fatalf("no-usage reply = %#v %v", none.Usage, err)
	}
}

func TestMockSummarizesWithTheHandlesAndCanFail(t *testing.T) {
	adapter := usageAdapter(t)
	summary, err := adapter.Complete(context.Background(), agents.Request{
		Purpose: agents.PurposeSummary,
		Messages: []agents.Message{
			{Role: "system", Content: "summarize"},
			{Role: "user", Content: "[用户] 画一只猫 @img1\n[助手] 好的 @img2，还有 @model1 与 @img1"},
		},
	})
	if err != nil || summary.Content != "（Mock 摘要）句柄：@img1 @img2 @model1" || summary.Usage == nil {
		t.Fatalf("summary = %#v %v", summary, err)
	}
	if _, err := adapter.Complete(context.Background(), agents.Request{
		Purpose:  agents.PurposeSummary,
		Messages: []agents.Message{{Role: "user", Content: "[用户] 这一轮会让压缩失败 [mock:compact_fail]"}},
	}); err == nil {
		t.Fatal("a summary of a transcript with the failure marker succeeded")
	}
	// A plain completion (the title summary) is unchanged.
	title, err := adapter.Complete(context.Background(), agents.Request{Messages: []agents.Message{{Role: "user", Content: "标题"}}})
	if err != nil || title.Content != "标题" {
		t.Fatalf("title = %#v %v", title, err)
	}
}
