package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #630: after the person advanced the views on the card, the Agent passed a
// view's Task ID as three_view_group and was refused without being told the
// run ID. The refusal now names the group that Task belongs to and lists the
// session's groups with their run IDs and view states; nothing is submitted.
func TestMultiviewRefusalOfAViewTaskNamesTheGroupsRunID(t *testing.T) {
	views := groupOfFour()
	views.listed = map[string][]chat.ThreeViewGroupFact{"owner-a/session-a": {{
		RunID: "run-1",
		Views: []chat.ThreeViewViewFact{
			{View: "front", Status: "succeeded", TaskID: "task-front", AssetID: "asset-front"},
			{View: "left", Status: "succeeded", TaskID: "task-left", AssetID: "asset-left"},
			{View: "back", Status: "running", TaskID: "task-back"},
		},
	}}}
	run := runMultiview(t, views, `{"model":"tripo-v3.1-multiview","three_view_group":"task-left"}`, nil)
	if run.err != nil || !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" || len(run.resolver.urls) != 0 {
		t.Fatalf("a view's Task ID must refuse before anything is prepared: %+v %v", run.result, run.err)
	}
	messages := run.chat.ThreeView.Messages
	refusal := run.result.Error
	hint := strings.NewReplacer("{value}", "task-left", "{run}", "run-1").Replace(messages.GroupTaskNamed)
	for _, want := range []string{
		messages.GroupNotFound, hint,
		"run-1：front=succeeded / left=succeeded / back=running",
	} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, refusal)
		}
	}
	if !strings.Contains(run.result.AgentContent, "run-1") {
		t.Errorf("the Agent's tool result must carry the run ID: %s", run.result.AgentContent)
	}

	// A value that is no view of any listed group still lists the run IDs,
	// without claiming the value belongs to one.
	other := runMultiview(t, views, `{"model":"tripo-v3.1-multiview","three_view_group":"made-up"}`, nil)
	if !other.result.NoSubmission || !strings.Contains(other.result.Error, "run-1：front=succeeded") ||
		strings.Contains(other.result.Error, "made-up") {
		t.Fatalf("an unknown value must list the groups only: %+v", other.result)
	}
}
