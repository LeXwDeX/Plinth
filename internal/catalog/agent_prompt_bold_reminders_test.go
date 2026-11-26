package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #655: key reminders are marked with Markdown bold, which the chat renderer already draws (src/lib/chat/markdown.ts).
// The renderer deliberately does not parse HTML or colour tags (#654), so the prompt must say so in one place, once.
func TestSystemPromptAsksForBoldKeyRemindersOnce(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	prompt := product.Application().Chat.Agent.SystemPrompt
	if got := strings.Count(prompt, "**粗体**"); got != 1 {
		t.Fatalf("the system prompt names the bold marker %d times, want exactly once: it is one rule", got)
	}
	for _, need := range []string{"关键提醒", "其余不加粗", "不用 HTML"} {
		if !strings.Contains(prompt, need) {
			t.Errorf("the bold-reminder rule lacks %q", need)
		}
	}
	// The rule must not ask for colour or HTML tags, which the renderer shows as plain text.
	for _, forbidden := range []string{"<b>", "<red>", "<yellow>"} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("the system prompt asks for %q, which the renderer does not parse", forbidden)
		}
	}
	// What the pass trimmed to make room keeps its constraint.
	for _, kept := range []string{"互不依赖的付费步骤在同一次回复里一起发出", "没检查过的结果说成已检查", "不把没做完的计划说成已完成"} {
		if !strings.Contains(prompt, kept) {
			t.Errorf("a trimmed sentence lost its rule %q", kept)
		}
	}
}
