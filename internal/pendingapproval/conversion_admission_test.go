package pendingapproval_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// #330 final-review P1: an overlapping active conversion refuses the submit
// before any Task or fee exists. The approval must stay pending and be retried
// by the sweep after the other conversion ends, not be reported as a permanent
// submit failure.
func TestOverlappingConversionKeepsTheApprovalPendingAndRetries330(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	_, grant := w.pending("call-conversion")
	w.gateway.submitError = &generation.Error{
		Code: generation.ErrConflict, Operation: "submit_prepared",
		Detail: "同一来源的格式转换正在进行",
		Cause:  errors.Join(generation.ErrConversionInProgress, generation.RepositoryConversionConflict),
	}
	outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
	if err != nil || !handled {
		t.Fatalf("approve = %+v handled=%v err=%v", outcome, handled, err)
	}
	if outcome.TaskID != "" {
		t.Fatalf("a refused conversion reported a Task: %+v", outcome)
	}
	if _, ok := w.message(pendingapproval.MessageID(grant.RequestID)); ok {
		t.Fatal("an overlapping conversion was reported as a permanent submit failure")
	}
	if w.gateway.distinctTasks() != 0 {
		t.Fatalf("an overlapping conversion accepted %d tasks", w.gateway.distinctTasks())
	}

	w.gateway.submitError = nil
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.gateway.distinctTasks() != 1 {
		t.Fatalf("retried approval accepted %d tasks", w.gateway.distinctTasks())
	}
	if _, ok := w.message(pendingapproval.MessageID(grant.RequestID)); !ok {
		t.Fatal("retried approval did not post its message")
	}
}
