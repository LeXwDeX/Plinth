package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #630: the person advanced the views on the three-view card, so the run ID
// reached the Agent only in a create_three_view result that may be far back or
// compacted away. Every turn that offers the multiview tool now carries the
// session's groups in the system context: the run ID to pass as
// three_view_group and where each view stands.
func TestSystemContextListsTheSessionsThreeViewGroups(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	service := newChatServiceForInternal(t)
	service.repository = choicePromptRepository{}
	o := &Orchestrator{
		schema: schema, registry: registry, agentScope: agentscope.FromSchema(schema.AgentScope), service: service,
		threeViewGroups: func(_ context.Context, ownerID, sessionID string) ([]ThreeViewGroupFact, error) {
			asked = append(asked, ownerID+"/"+sessionID)
			return []ThreeViewGroupFact{
				{RunID: "run-new", Views: []ThreeViewViewFact{
					{View: "front", Status: "succeeded", TaskID: "task-f"},
					{View: "left", Status: "running", TaskID: "task-l"},
					{View: "back", Status: "not_started"},
				}},
				{RunID: "run-old", Views: []ThreeViewViewFact{{View: "front", Status: "failed", TaskID: "task-o"}}},
			}, nil
		},
	}
	request := o.agentRequest(context.Background(), nil, TurnOptions{AgentScope: "auto"}, "owner-a", "session-a")
	system := ""
	for _, message := range request.Messages {
		if message.Role == "system" {
			system += message.Content
		}
	}
	for _, want := range []string{
		"【本会话三视图组】", schema.ThreeView.Multiview.Tool,
		"- run-new：front=succeeded / left=running / back=not_started",
		"- run-old：front=failed",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context lacks %q", want)
		}
	}
	if strings.Contains(system, "task-f") {
		t.Errorf("the groups block must name run IDs, not the views' Task IDs")
	}
	// The mock Agent reads "three_view_group 填 <group>" from a button's
	// directive; the block must not look like one.
	if strings.Contains(ThreeViewGroupsText(schema.ThreeView, []ThreeViewGroupFact{{RunID: "r"}}), "three_view_group 填") {
		t.Errorf("the groups block reads like a button directive")
	}
	if len(asked) != 1 || asked[0] != "owner-a/session-a" {
		t.Fatalf("the groups were read for %v, want the turn's own session", asked)
	}

	// A 2D-only turn offers no multiview tool, so it reads no groups; a
	// failed read leaves the block out instead of failing the turn.
	asked = nil
	if text := o.threeViewGroupsPrompt(context.Background(), registry, TurnOptions{AgentScope: "2d"}, "owner-a", "session-a"); text != "" || len(asked) != 0 {
		t.Fatalf("a 2D turn carries the groups block: %q", text)
	}
	o.threeViewGroups = func(context.Context, string, string) ([]ThreeViewGroupFact, error) {
		return nil, errors.New("down")
	}
	if text := o.threeViewGroupsPrompt(context.Background(), registry, TurnOptions{AgentScope: "auto"}, "owner-a", "session-a"); text != "" {
		t.Fatalf("a failed read rendered %q", text)
	}
}
