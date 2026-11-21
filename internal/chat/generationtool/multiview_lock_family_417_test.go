package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
)

// threeViewsOfOneRig is a group whose front, left and back views finished: the
// contiguous prefix an order-only multiview Offering reads (#243).
func threeViewsOfOneRig() *fakeThreeViews {
	return &fakeThreeViews{groups: map[string][]generationtool.GroupView{
		"owner-a/session-a/run-1": {
			{View: "front", TaskID: "task-front", AssetID: "asset-front"},
			{View: "left", TaskID: "task-left", AssetID: "asset-left"},
			{View: "back", TaskID: "task-back", AssetID: "asset-back"},
		},
	}}
}

// #417 / #380 step 1: a session lock on Meshy 7.1 and a multiview step. The
// person's model is the family, so the step runs Meshy 7.1 multiview instead of
// refusing the lock or falling to another vendor's multiview model.
func TestAMultiviewStepRunsTheLockFamilysShape(t *testing.T) {
	run := runMultiviewIn(t, multiviewScenario{
		locked: "meshy-7.1",
		also:   []string{"meshy/meshy-7.1/image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d"},
	}, threeViewsOfOneRig(), `{"three_view_group":"run-1"}`, nil)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("multiview under a lock on the family: %+v %v", run.result, run.err)
	}
	if run.gateway.submitted.OfferingID != "meshy/meshy-7.1-multiview/image-to-3d" {
		t.Fatalf("offering = %q, want the locked model's multiview shape", run.gateway.submitted.OfferingID)
	}
	if model, _ := run.content["model"].(string); !strings.Contains(model, "Meshy 7.1") {
		t.Fatalf("model = %q, want the multiview shape of Meshy 7.1", model)
	}
}

// Without a family member that takes the views the step keeps the person's
// Offering and the existing view check refuses it: it never runs another
// vendor's model instead.
func TestAMultiviewStepDoesNotLeaveTheLockFamily(t *testing.T) {
	run := runMultiviewIn(t, multiviewScenario{
		locked:      "meshy-7.1",
		also:        []string{"meshy/meshy-7.1/image-to-3d", "meshy/meshy-7.1-multiview/image-to-3d"},
		unavailable: []string{"meshy/meshy-7.1-multiview/image-to-3d"},
	}, threeViewsOfOneRig(), `{"three_view_group":"run-1"}`, nil)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" {
		t.Fatalf("the step ran instead of refusing: %+v %q", run.result, run.gateway.submitted.OfferingID)
	}
	if !strings.Contains(run.result.AgentContent, run.chat.ThreeView.Messages.ViewsInvalid) {
		t.Fatalf("refusal = %q, want the view check's words", run.result.AgentContent)
	}
}
