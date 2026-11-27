package chat

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// promptToolFallbackMarkers are the words a sentence uses to say what to do
// when the tool it names is not offered this turn (#665).
var promptToolFallbackMarkers = []string{"不提供它时", "可用时", "when it is offered", "if it is offered"}

// #665: a craft brief is chosen by the tools offered this turn, yet it can
// still name another tool the scope, the harness or an administrator withheld
// (the 3D craft named create_three_view in the 3D scope, which never offers
// it). Every sentence of a craft brief that names a tool this turn does not
// offer must say what to do without it; otherwise the Agent is told to call a
// tool it cannot use.
func TestCraftBriefsNameUnofferedToolsOnlyWithAFallback(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	orchestrator := &Orchestrator{schema: schema}
	declared := promptConsistencyDeclaredTools(schema)
	findings := []promptConsistencyFinding{}
	for _, turn := range promptConsistencyTurns(t, orchestrator, schema) {
		if turn.op != "" {
			// A harness operation binds its own tools and states them in the
			// Harness block; the scope's free turns are what this guards.
			continue
		}
		offered := map[string]bool{}
		for _, tool := range turn.request.Tools {
			offered[tool.Name] = true
		}
		for _, domain := range []string{"image", "model-3d"} {
			craft := orchestrator.craftPrompt(turn.registry, turn.scope, domain, ProductionBrief{})
			for _, sentence := range strings.FieldsFunc(craft, func(r rune) bool { return r == '。' || r == '\n' }) {
				for _, name := range declared {
					if offered[name] || !mentionsToolName(sentence, name) || containsAnyFallback(sentence) {
						continue
					}
					findings = append(findings, promptConsistencyFinding{"工具名", turn.label + " " + domain + " craft", name,
						"点名了本轮不提供的工具，且没有说明不提供时怎么做"})
				}
			}
		}
	}
	reportPromptConsistency(t, "craft 工具名", findings)
}

func containsAnyFallback(sentence string) bool {
	for _, marker := range promptToolFallbackMarkers {
		if strings.Contains(sentence, marker) {
			return true
		}
	}
	return false
}
