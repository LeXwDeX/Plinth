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

// #282: an inspect_image summary keeps what failed, not only how many. The
// failed checks reach the step as leftovers with their fix codes, the checks the
// picture could not show are counted as unknown and never as passed, and the
// next turn's ledger line says so.
func TestWorkLedgerKeepsFailedImageChecksAndSeparatesUnknown(t *testing.T) {
	image := `{"tool":"inspect_image","handle":"@img1","width":64,"height":64,` +
		`"checks":{"passed":2,"failed":1,"unknown":1},` +
		`"failed_checks":[{"check":"short_edge","actual":"64 px (64 x 64)","fix":"raise_resolution"}]}`
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
	var found *chat.WorkSelfCheck
	for index := range ledger.Steps[0].SelfChecks {
		if ledger.Steps[0].SelfChecks[index].Tool == "inspect_image" {
			found = &ledger.Steps[0].SelfChecks[index]
		}
	}
	if found == nil || found.Passed != 2 || found.Unknown != 1 || found.Failed != 1 ||
		len(found.Leftovers) != 1 || found.Leftovers[0].Check != "short_edge" ||
		found.Leftovers[0].FixHint != "raise_resolution" || found.Leftovers[0].Actual != "64 px (64 x 64)" {
		t.Fatalf("inspect_image self-check = %+v", found)
	}
	summary := fixture.service.WorkLedgerSummary(context.Background(), "owner-a", fixture.session.ID, "")
	if !strings.Contains(summary, "inspect_image 通过 2 未判定 1 失败 1") ||
		!strings.Contains(summary, "short_edge(raise_resolution)") {
		t.Fatalf("the ledger line omits the failed image check: %s", summary)
	}
}
