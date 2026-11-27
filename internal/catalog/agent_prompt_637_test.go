package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #637: a reply that says it will do something calls the tool in that same
// reply. A turn ends when the model stops calling tools, so a reply that only
// announces the work ends the turn with nothing done; the person has to send
// it again. When the work cannot be done, the reply says why and what can be
// done instead. The rule extends "直接开始" in one place and is stated once.
func TestShippedSystemPromptActsInTheReplyThatAnnouncesTheWork(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	prompt := product.Application().Chat.Agent.SystemPrompt
	rule := "用户给了明确的生成指令且输入齐全时，直接开始。说要做就在同一回复里调用工具，做不到就说原因和替代做法，不只说开始。"
	if count := strings.Count(prompt, rule); count != 1 {
		t.Fatalf("the system prompt states the act-in-the-same-reply rule %d times, want once", count)
	}
	if count := strings.Count(prompt, "同一回复里调用工具"); count != 1 {
		t.Errorf("the system prompt says %q %d times, want once", "同一回复里调用工具", count)
	}
}
