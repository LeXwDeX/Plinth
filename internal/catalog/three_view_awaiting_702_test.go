package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #702: the approval mode has no production plan any more, and the approval
// window no longer stops a three-view continuation, so the reason a view waits
// for approval names only the card, never a plan's budget.
func TestThreeViewAwaitingApprovalReasonNamesOnlyTheCard(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	three := product.Application().Chat.ThreeView
	reason := three.Continuation.Reasons["view_awaiting_approval"]
	if !strings.Contains(reason, "卡片") {
		t.Fatalf("view_awaiting_approval = %q, want it to name the card", reason)
	}
	for _, text := range []string{reason, three.Messages.AgentPausedStarted} {
		for _, stale := range []string{"计划", "预算"} {
			if strings.Contains(text, stale) {
				t.Fatalf("three-view awaiting-approval wording mentions %q: %q", stale, text)
			}
		}
	}
}
