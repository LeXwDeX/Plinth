package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// TestWorkLedgerFlagsAVisualItemTheAgentFailed is #286: the Agent's verdicts on
// the 3d_input checklist's visual items reach the step's self-check record. A
// failed item stays a leftover with the Agent's note and the Schema's fix hint,
// a passed one only counts, and not_applicable is neither.
func TestWorkLedgerFlagsAVisualItemTheAgentFailed(t *testing.T) {
	image := `{"tool":"inspect_image","handle":"@img1","checks":{"passed":6,"failed":1},"visual":[` +
		`{"item":"pose","verdict":"pass","note":"A pose, arms away from the torso"},` +
		`{"item":"perspective","verdict":"fail","note":"shot from above, head looks large","fix_hint":"Redraw from a level orthographic view"},` +
		`{"item":"cast_shadow","verdict":"not_applicable"}]}`
	task := workTask("task-1", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "prompt", "prompt")
	messages := []chat.Message{{
		Role: "configured-assistant",
		ToolCalls: []chat.ToolCall{
			{ID: "call-1", Name: "inspect_image", Summary: json.RawMessage(image)},
		},
		Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, AssetID: "asset-1", TaskID: "task-1"}},
	}}
	fixture := newLedgerFixture(t, nil, []generation.Task{task}, messages)
	ledger, err := fixture.service.WorkLedger(context.Background(), "owner-a", fixture.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var check *chat.WorkSelfCheck
	for index := range ledger.Steps[0].SelfChecks {
		if ledger.Steps[0].SelfChecks[index].Tool == "inspect_image" {
			check = &ledger.Steps[0].SelfChecks[index]
		}
	}
	if check == nil || check.Passed != 6 || check.Failed != 1 || len(check.Leftovers) != 1 {
		t.Fatalf("inspect_image self-check = %+v", check)
	}
	leftover := check.Leftovers[0]
	if leftover.Check != "perspective" || leftover.Actual != "shot from above, head looks large" ||
		leftover.FixHint != "Redraw from a level orthographic view" {
		t.Fatalf("leftover = %+v", leftover)
	}
	summary := fixture.service.WorkLedgerSummary(context.Background(), "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "perspective") || !strings.Contains(summary, "Redraw from a level orthographic view") {
		t.Fatalf("the failed visual item is missing from the ledger summary: %s", summary)
	}
	if strings.Contains(summary, "cast_shadow") || strings.Contains(summary, "(A pose") {
		t.Fatalf("a passed or inapplicable item was projected as a leftover: %s", summary)
	}
}
