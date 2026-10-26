package mock_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/mock"
)

type localeKey struct{}

func TestAdapterClosesTheTurnInItsLocale(t *testing.T) {
	adapter, err := mock.New(mock.Config{
		ToolName: "configured-tool", PromptArgument: "configured-prompt",
		Summary: "默认总结", FailureSummary: "失败：{error}",
		UserRole: "configured-user", ToolRole: "configured-tool-role",
		Localize: func(ctx context.Context, text string) string {
			if ctx.Value(localeKey{}) != "en" {
				return text
			}
			return map[string]string{"默认总结": "Done", "失败：{error}": "Failed: {error}"}[text]
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	english := context.WithValue(context.Background(), localeKey{}, "en")
	closing := func(ctx context.Context, result string) string {
		completion, err := adapter.Stream(ctx, agents.Request{Messages: []agents.Message{{Role: "configured-tool-role", Content: result}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return completion.Content
	}
	if got := closing(english, `{"result":"ok"}`); got != "Done" {
		t.Fatalf("en summary = %q", got)
	}
	if got := closing(english, `{"success":false,"error":"quota"}`); got != "Failed: quota" {
		t.Fatalf("en failure = %q", got)
	}
	if got := closing(context.Background(), `{"result":"ok"}`); got != "默认总结" {
		t.Fatalf("default summary = %q", got)
	}
}
