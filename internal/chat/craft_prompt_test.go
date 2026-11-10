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
			rig.turn(t, scope)
			system := rig.agent.requests[0].Messages[0].Content
			if got := strings.Contains(system, craft.Text); got != want {
				t.Fatalf("%s scope: craft brief present = %v, want %v", scope, got, want)
			}
			if !want {
				return
			}
			if strings.Count(system, craft.Text) != 1 {
				t.Fatalf("%s scope: craft brief appears %d times", scope, strings.Count(system, craft.Text))
			}
		})
	}
}

// The brief sits after the domain context and before the Agent scope block, so
// it is part of the craft instructions rather than a scope fact.
func TestCraftPromptSitsBetweenDomainContextAndScope(t *testing.T) {
	rig := newScopeRig(t, agents.Completion{Content: "ok"})
	rig.turn(t, "3d")
	system := rig.agent.requests[0].Messages[0].Content
	craft := ""
	for _, prompt := range rig.schema.Agent.CraftPrompts {
		if prompt.ID == "model3d" {
			craft = prompt.Text
		}
	}
	if craft == "" {
		t.Fatal("no model3d craft prompt")
	}
	const domainContext = "Use configured 2D tools"
	start := strings.Index(system, domainContext)
	end := strings.Index(system, "【Agent 范围】")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("domain context (%d) or scope block (%d) not found", start, end)
	}
	segment := system[start+len(domainContext) : end]
	if segment != "\n\n"+craft+"\n\n" {
		t.Fatalf("between domain context and scope = %q, want the craft brief with blank lines", segment)
	}
	if strings.Index(system, "【批准方式】") < end {
		t.Fatal("the approval block does not follow the scope block")
	}
}
