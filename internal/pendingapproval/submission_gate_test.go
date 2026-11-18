package pendingapproval_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// TestBlockedSubmitKeepsTheApprovalForResume pins §7.4: a paused production
// list refuses the new Task, but the card, the grant and the frozen input stay
// pending, and the same approval submits after the run resumes.
func TestBlockedSubmitKeepsTheApprovalForResume(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "blocked.db"), time.Hour)
	_, grant := w.pending("blocked-submit")
	w.gateway.submitError = &generation.SubmissionBlockedError{Reason: "user"}
	if _, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err != nil || !handled {
		t.Fatalf("decide = handled %v, err %v", handled, err)
	}
	if _, ok := w.message(pendingapproval.MessageID(grant.RequestID)); ok {
		t.Fatal("blocked submit settled the approval as failed")
	}
	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// The decision moved the card out of the pending list, but the operation
	// and its approved grant stay durable for the sweep to retry.
	if len(items) != 0 {
		t.Fatalf("blocked approval still listed as undecided: %d", len(items))
	}
	operation, err := w.repo.LoadAgentOperationByGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || operation.Settled {
		t.Fatalf("blocked operation settled = %v, err %v", operation.Settled, err)
	}
	stored, err := w.grants.Grants.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || stored.Status != executiongrant.Approved {
		t.Fatalf("blocked grant = %+v, err %v; want the approval kept", stored.Status, err)
	}
	if w.gateway.submits != 1 || w.gateway.distinctTasks() != 0 {
		t.Fatalf("blocked submit = %d submits, %d tasks", w.gateway.submits, w.gateway.distinctTasks())
	}

	w.gateway.submitError = nil
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.gateway.distinctTasks() != 1 {
		t.Fatalf("resumed sweep accepted %d Tasks", w.gateway.distinctTasks())
	}
	if _, ok := w.message(pendingapproval.MessageID(grant.RequestID)); !ok {
		t.Fatal("resumed approval did not post its message")
	}
}
