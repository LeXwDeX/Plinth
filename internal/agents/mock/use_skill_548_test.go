package mock_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// #548: with use_skill offered, the mock follows the routing sentence: a
// three-view or four-view request reads the matching Skill before
// create_three_view, "读 Skill <name>" reads the named one, and
// "skill=<name>" passes a style Skill on the 2D generation call.
func TestMockReadsTheSkillBeforeActing(t *testing.T) {
	tools := append(append([]agents.Tool{}, allTools...), agents.Tool{Name: "use_skill"})
	for _, test := range []struct {
		name, text string
		want       [][2]string // tool, the argument checked
	}{
		{"three view", "用 @img2 生成三视图", [][2]string{{"use_skill", "three-view"}, {"create_three_view", ""}}},
		{"four view", "用 @img2 生成三视图，做成四视图", [][2]string{{"use_skill", "four-view"}, {"create_three_view", ""}}},
		{"named skill", "读 Skill my-howto 再画一只猫", [][2]string{{"use_skill", "my-howto"}, {"refine_image", ""}}},
		{"style argument", "画一只猫 skill=watercolor-style", [][2]string{{"refine_image", "watercolor-style"}}},
	} {
		completion, err := newRoutingAdapter(t).Stream(context.Background(), agents.Request{
			Messages: []agents.Message{{Role: "user", Content: test.text}}, Tools: tools}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(completion.ToolCalls) != len(test.want) {
			t.Fatalf("%s: calls = %#v", test.name, completion.ToolCalls)
		}
		for index, want := range test.want {
			call := completion.ToolCalls[index]
			var arguments map[string]any
			if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
				t.Fatal(err)
			}
			if call.Name != want[0] {
				t.Fatalf("%s: call %d = %s, want %s", test.name, index, call.Name, want[0])
			}
			if want[1] == "" {
				continue
			}
			key := "name"
			if call.Name != "use_skill" {
				key = "skill"
			}
			if arguments[key] != want[1] {
				t.Errorf("%s: call %d %s = %v, want %s", test.name, index, key, arguments[key], want[1])
			}
		}
	}
	// Without use_skill offered the routing is unchanged.
	name, _ := plan(t, []agents.Message{{Role: "user", Content: "用 @img2 生成三视图"}}, allTools)
	if name != "create_three_view" {
		t.Fatalf("without use_skill the call = %s", name)
	}
}
