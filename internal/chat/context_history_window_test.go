package chat_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #322 dev-test finding (note 98983): with compaction on, the old hard window
// of chat.context.maximum_messages (20) still cut every request, so after
// about ten rounds the oldest ones left the request without ever reaching a
// summary. With compaction on, history is bounded by compaction only.
func TestCompactionOnSendsEveryUnsummarizedMessage(t *testing.T) {
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek"})
	fixture.agent.usage = &agents.Usage{PromptTokens: 100, CompletionTokens: 10}
	rounds := fixture.schema.Context.MaximumMessages/2 + 3
	for round := 1; round <= rounds; round++ {
		fixture.turn(t, fmt.Sprintf("round-%02d", round))
	}
	text := requestText(fixture.agent.lastStream())
	for round := 1; round <= rounds; round++ {
		if marker := fmt.Sprintf("round-%02d", round); !strings.Contains(text, marker) {
			fixture.agent.mu.Lock()
			summaries := len(fixture.agent.summaries)
			fixture.agent.mu.Unlock()
			t.Fatalf("%s is neither in the request nor summarized (%d summary calls)", marker, summaries)
		}
	}
}

// Without a token measure (a model with no declared window, or an upstream
// that reports no usage) the unsummarized history is bounded by
// auto_message_threshold: crossing it compacts first, so the oldest messages
// reach the summary before they leave the request.
func TestMessageThresholdCompactsWhenTokensAreUnknown(t *testing.T) {
	for name, setup := range map[string]struct {
		model string
		usage *agents.Usage
	}{
		"model without window":   {model: "plain-model", usage: &agents.Usage{PromptTokens: 100, CompletionTokens: 10}},
		"upstream without usage": {model: "deepseek"},
	} {
		fixture := newContextFixtureWith(t, chat.ModelPolicy{Model: setup.model}, func(schema *catalog.ChatSchema) {
			schema.ContextCompaction.AutoMessageThreshold = 16
		})
		fixture.agent.usage = setup.usage
		for round := 1; round <= 9; round++ {
			fixture.turn(t, fmt.Sprintf("round-%02d", round))
		}
		fixture.agent.mu.Lock()
		summaries := slices.Clone(fixture.agent.summaries)
		fixture.agent.mu.Unlock()
		if len(summaries) == 0 {
			t.Fatalf("%s: no automatic compaction past the message threshold", name)
		}
		source := requestText(summaries[0])
		text := requestText(fixture.agent.lastStream())
		for round := 1; round <= 9; round++ {
			marker := fmt.Sprintf("round-%02d", round)
			if !strings.Contains(text, marker) && !strings.Contains(source, marker) {
				t.Fatalf("%s: %s is neither in the request nor in the summary", name, marker)
			}
		}
		if !strings.Contains(source, "round-01") || strings.Contains(text, "round-01") {
			t.Fatalf("%s: round-01 should have moved into the summary", name)
		}
	}
}

// With compaction off there is no summary, so the documented fallback keeps
// the newest chat.context.maximum_messages messages.
func TestCompactionOffKeepsTheMessageWindow(t *testing.T) {
	fixture := newContextFixtureWith(t, chat.ModelPolicy{Model: "deepseek"}, func(schema *catalog.ChatSchema) {
		schema.ContextCompaction.Enabled = false
	})
	rounds := fixture.schema.Context.MaximumMessages/2 + 3
	for round := 1; round <= rounds; round++ {
		fixture.turn(t, fmt.Sprintf("round-%02d", round))
	}
	request := fixture.agent.lastStream()
	if len(request.Messages) != fixture.schema.Context.MaximumMessages+1 || strings.Contains(requestText(request), "round-01") {
		t.Fatalf("compaction off: %d messages", len(request.Messages))
	}
}
