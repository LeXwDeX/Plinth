package pendingapproval_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

func TestHarnessStaleApprovalSettlesOnceWithChangedMessage(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "harness.db"), time.Hour)
	w.binding = harness.DefaultSelection("owner-a", "session-a").Binding
	_, grant := w.pending("stale-harness")
	w.gateway.submitError = &generation.Error{Code: generation.ErrConflict, Cause: harness.ErrStale}
	if _, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err != nil || !handled {
		t.Fatalf("decision %v %v", handled, err)
	}
	message, exists := w.message(pendingapproval.MessageID(grant.RequestID))
	if !exists || message.Content != w.schema.Harness.ChangedMessage {
		t.Fatalf("stale result %+v", message)
	}
	if w.gateway.lastSubmit.Harness != w.binding {
		t.Fatal("approval rebound to current selection")
	}
	count := w.gateway.submits
	if _, err := w.service.Sweep(context.Background()); err != nil || w.gateway.submits != count {
		t.Fatal("stale approval retried", err)
	}
}

func TestAgentSourceSchemaRequiresVerifierAtAssembly(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "assembly.db"), time.Hour)
	if _, err := pendingapproval.New(pendingapproval.Config{Operations: w.repo, Grants: w.grants, Gateway: w.gateway, Chat: w.chat, Schema: w.schema}); err == nil {
		t.Fatal("new Agent-source schema assembled without verifier")
	}
}

func TestDetachedApprovedSourceIsRecheckedWithoutDecisionRequest(t *testing.T) {
	for _, transient := range []bool{false, true} {
		t.Run(fmt.Sprint(transient), func(t *testing.T) {
			w := openWorld(t, filepath.Join(t.TempDir(), "detached.db"), time.Hour)
			op, grant := w.pending("detached-source")
			if _, err := w.grants.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err != nil {
				t.Fatal(err)
			}
			calls := 0
			storageErr := errors.New("storage unavailable")
			verifier := func(_ context.Context, got generationtool.Operation, p generation.PreparedInput) error {
				calls++
				if got.PreparedInputID != op.PreparedInputID || p.InputHash != op.PreparedInputHash {
					t.Fatal("wrong frozen identity")
				}
				if transient {
					return storageErr
				}
				return generationtool.ErrOperationConflict
			}
			service, err := pendingapproval.New(pendingapproval.Config{Operations: w.repo, Grants: w.grants, Gateway: w.gateway, Chat: w.chat, Schema: w.schema, VerifyPreparedSource: verifier})
			if err != nil {
				t.Fatal(err)
			}
			err = service.Continue(context.Background(), op)
			if transient {
				if !errors.Is(err, storageErr) {
					t.Fatal("storage failure hidden", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = service.Sweep(context.Background()); err != nil || calls != 1 {
					t.Fatal("permanent source failure retried", err)
				}
			}
			if w.gateway.submits != 0 {
				t.Fatal("invalid detached source submitted")
			}
		})
	}
}

func TestApprovedPreparedReadFailuresSettleOnlyPermanentErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		permanent bool
	}{
		{"missing", &generation.Error{Code: generation.ErrNotFound}, true},
		{"corrupt", &generation.Error{Code: generation.ErrConflict}, true},
		{"busy", errors.New("database busy"), false},
		{"repository_conflict", &generation.Error{Code: generation.ErrConflict, Cause: generation.RepositoryConflict}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := openWorld(t, filepath.Join(t.TempDir(), "prepared.db"), time.Hour)
			op, grant := w.pending("prepared-read")
			if _, err := w.grants.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve); err != nil {
				t.Fatal(err)
			}
			w.gateway.preparedError = tc.err
			before := w.gateway.preparedReads
			err := w.service.Continue(context.Background(), op)
			if tc.permanent {
				if err != nil {
					t.Fatal(err)
				}
				message, ok := w.message(pendingapproval.MessageID(grant.RequestID))
				if !ok || message.Content != w.schema.PendingApprovals.Messages.SubmitFailed {
					t.Fatal("permanent read error not reported")
				}
				if _, err = w.service.Sweep(context.Background()); err != nil || w.gateway.preparedReads != before+1 {
					t.Fatal("settled read failure retried", err)
				}
			} else {
				if !errors.Is(err, tc.err) {
					t.Fatal("transient read error hidden", err)
				}
				if _, ok := w.message(pendingapproval.MessageID(grant.RequestID)); ok {
					t.Fatal("transient read error settled")
				}
				w.gateway.preparedError = nil
				if _, err = w.service.Sweep(context.Background()); err != nil {
					t.Fatal(err)
				}
				if w.gateway.distinctTasks() != 1 {
					t.Fatal("recoverable read error not retried")
				}
			}
			if tc.permanent && w.gateway.submits != 0 {
				t.Fatal("invalid prepared input submitted")
			}
		})
	}
}

type boundGrantRepository struct {
	executiongrant.Repository
	grant executiongrant.Grant
}

func (r boundGrantRepository) GetExecutionGrant(context.Context, string, string) (executiongrant.Grant, error) {
	return r.grant, nil
}

func TestBoundApprovalDoesNotReloadPreparedInput(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "bound.db"), time.Hour)
	op, grant := w.pending("bound-no-read")
	grant.Status = executiongrant.Bound
	grant.TaskID = "task-already-bound"
	w.grants.Grants = boundGrantRepository{Repository: w.repo, grant: grant}
	before := w.gateway.preparedReads
	w.gateway.preparedError = &generation.Error{Code: generation.ErrNotFound}
	if err := w.service.Continue(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if w.gateway.preparedReads != before || w.gateway.submits != 0 {
		t.Fatal("bound task resumed through prepared input")
	}
}
