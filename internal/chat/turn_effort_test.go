package chat_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #382: a title, a compaction summary and a prompt enhancement run beside the
// turn, not for the person, so they take the level the model family declares
// for them (chat.agent.thinking.turn_efforts) instead of the session's own.
// Here the model is glm-5.3, whose family offers low/high/max and defaults to
// max: the turn keeps max, the two side calls drop to low.
func TestTitleAndSummaryUseTheLevelTheModelFamilyDeclares(t *testing.T) {
	ctx := context.Background()
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "glm-5.3"})
	for range fixture.schema.ContextCompaction.KeepRecentRounds + 2 {
		fixture.turn(t, "round")
	}
	if request := fixture.agent.lastStream(); request.ReasoningEffort != "max" {
		t.Fatalf("turn request effort = %q, want the family default max", request.ReasoningEffort)
	}
	agent := fixture.agent
	agent.mu.Lock()
	titles := append([]agents.Request(nil), agent.titles...)
	agent.mu.Unlock()
	if len(titles) != 1 {
		t.Fatalf("title calls = %d", len(titles))
	}
	if titles[0].Model != "glm-5.3" || titles[0].ReasoningEffort != "low" {
		t.Fatalf("title request = model %q effort %q, want low",
			titles[0].Model, titles[0].ReasoningEffort)
	}
	if _, err := fixture.orchestrator.CompactContext(ctx, "owner-a", fixture.session); err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	summaries := append([]agents.Request(nil), agent.summaries...)
	agent.mu.Unlock()
	if len(summaries) == 0 {
		t.Fatal("no summary call")
	}
	last := summaries[len(summaries)-1]
	if last.Purpose != agents.PurposeSummary || last.ReasoningEffort != "low" {
		t.Fatalf("summary request = purpose %q effort %q, want low",
			last.Purpose, last.ReasoningEffort)
	}
}

// A model whose family offers one level only keeps it for every kind: the
// declared level falls back to the family default, as a session's choice does.
func TestTitleKeepsTheFamilyDefaultWhenItDeclaresNoCheaperLevel(t *testing.T) {
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "qwen-omni-flash"})
	fixture.turn(t, "hello")
	agent := fixture.agent
	agent.mu.Lock()
	titles := append([]agents.Request(nil), agent.titles...)
	agent.mu.Unlock()
	if len(titles) != 1 {
		t.Fatalf("title calls = %d", len(titles))
	}
	// The qwen family offers low, medium and xhigh and defaults to xhigh: the
	// declared title level is the cheapest one it has.
	if titles[0].ReasoningEffort != "low" {
		t.Fatalf("qwen title effort = %q, want low", titles[0].ReasoningEffort)
	}
}
