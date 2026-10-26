package mock_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

func TestProcessingChainUsesPersistentPlanTool(t *testing.T) {
	for _, phrase := range []string{"给 @model1 重拓扑后绑骨再做动画", "remesh @model1 then rig then animate"} {
		tool, args := plan(t, []agents.Message{{Role: "user", Content: phrase}}, allTools)
		if tool != "plan_model_processing" || args["source_task_id"] != "@model1" {
			t.Fatalf("%q: %s %v", phrase, tool, args)
		}
		steps := args["steps"].([]any)
		for i, want := range []string{"remesh", "rigging", "animation"} {
			if steps[i].(map[string]any)["capability"] != want {
				t.Fatalf("steps = %v", steps)
			}
		}
	}
}

func TestProcessingPlanIsRefusedIn2DScope(t *testing.T) {
	completion, err := newRoutingAdapter(t).Stream(context.Background(), agents.Request{
		Messages: []agents.Message{{Role: "user", Content: "重拓扑后绑骨再做动画"}},
		Tools:    []agents.Tool{{Name: "refine_image"}}, ScopeWithheld: []string{"plan_model_processing"},
	}, nil)
	if err != nil || len(completion.ToolCalls) != 0 {
		t.Fatalf("completion = %+v, err=%v", completion, err)
	}
}
