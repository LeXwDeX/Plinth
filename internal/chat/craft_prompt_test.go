package chat_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The shipped model3d brief is part of the system context only when the turn
// really offers a 3D tool, so a 2D-only turn does not pay for it.
func TestCraftPromptIsInjectedOnlyWhenA3DToolIsOffered(t *testing.T) {
	for scope, want := range map[string]bool{"3d": true, "auto": true, "2d": false} {
		t.Run(scope, func(t *testing.T) {
			rig := newScopeRig(t, agents.Completion{Content: "ok"})
			var craft catalog.ChatAgentCraftPrompt
			found := false
			for _, prompt := range rig.schema.Agent.CraftPrompts {
				if prompt.ID == "model3d" {
					craft, found = prompt, true
				}
			}
			if !found {
				t.Fatalf("test setup: no model3d craft prompt in %#v", rig.schema.Agent.CraftPrompts)
			}
			rig.turnDomain(t, scope, "model-3d")
			system := rig.agent.requests[0].Messages[0].Content
			wantCraft := renderedCraftText(craft)
			if got := strings.Contains(system, wantCraft); got != want {
				t.Fatalf("%s scope: craft brief present = %v, want %v", scope, got, want)
			}
			if !want {
				return
			}
			if strings.Count(system, wantCraft) != 1 {
				t.Fatalf("%s scope: craft brief appears %d times", scope, strings.Count(system, wantCraft))
			}
		})
	}
}

// #239: auto scope offers both tool sets, so when_domain keeps the 3D craft
// out of a 2D turn and the 2D craft out of a 3D turn.
func TestCraftPromptWhenDomainSeparatesTheTwoCrafts(t *testing.T) {
	prompts := map[string]string{}
	rig := newScopeRig(t, agents.Completion{Content: "ok"})
	for _, prompt := range rig.schema.Agent.CraftPrompts {
		prompts[prompt.ID] = renderedCraftText(prompt)
	}
	for _, test := range []struct {
		domain, wanted, unwanted string
	}{
		{"image", "image2d_production", "model3d"},
		{"model-3d", "model3d", "image2d_production"},
	} {
		t.Run(test.domain, func(t *testing.T) {
			rig.turnDomain(t, "auto", test.domain)
			system := rig.agent.requests[len(rig.agent.requests)-1].Messages[0].Content
			if !strings.Contains(system, prompts[test.wanted]) {
				t.Fatalf("%s turn omits the %s craft brief", test.domain, test.wanted)
			}
			if strings.Contains(system, prompts[test.unwanted]) {
				t.Fatalf("%s turn includes the %s craft brief", test.domain, test.unwanted)
			}
		})
	}
}

// The brief sits after the domain context and before the Agent scope block, so
// it is part of the craft instructions rather than a scope fact.
// renderedCraftText is what craftPrompt sends for a test rig without an
// Offering source: the knowledge placeholder becomes empty and the empty line
// it leaves is dropped (#249).
func renderedCraftText(prompt catalog.ChatAgentCraftPrompt) string {
	text := prompt.Text
	if !strings.Contains(text, "{offering_tradeoffs}") {
		return text
	}
	text = strings.ReplaceAll(text, "{offering_tradeoffs}", "")
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func TestCraftPromptSitsBetweenDomainContextAndScope(t *testing.T) {
	rig := newScopeRig(t, agents.Completion{Content: "ok"})
	rig.turn(t, "auto")
	system := rig.agent.requests[0].Messages[0].Content
	craft := ""
	for _, prompt := range rig.schema.Agent.CraftPrompts {
		if prompt.ID == "image2d_production" {
			craft = prompt.Text
		}
	}
	if craft == "" {
		t.Fatal("no image2d_production craft prompt")
	}
	const domainContext = "Use configured 2D tools"
	start := strings.Index(system, domainContext)
	craftStart := strings.Index(system, craft)
	end := strings.Index(system, "【Agent 范围】")
	if start < 0 || craftStart < 0 || end < 0 || end < start || craftStart < start {
		t.Fatalf("domain context (%d), craft (%d) or scope block (%d) not found in order", start, craftStart, end)
	}
	if craftStart > end {
		t.Fatal("the craft brief does not sit before the scope block")
	}
	if strings.Index(system, "【批准方式】") < end {
		t.Fatal("the approval block does not follow the scope block")
	}
}
