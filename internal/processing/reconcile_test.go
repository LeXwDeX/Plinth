package processing

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func reconcileFixture(t *testing.T) (*Service, *memoryStore, *gatewayFake, memoryCatalog, Plan, generation.Task) {
	t.Helper()
	s, store, gateway, offerings, plan := singleRemeshFixture(t)
	gateway.submitError = &generation.Error{Code: generation.ErrIdempotencyConflict}
	if err := advanceApproved(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	plan, _ = s.Get(context.Background(), plan.OwnerID, plan.ID)
	if plan.Failure != ReasonSubmitUnreconciled {
		t.Fatalf("fixture not locked: %+v", plan)
	}
	gateway.submitError = nil
	task := generation.Task{ID: "landed", OwnerID: plan.OwnerID, SessionID: plan.SessionID,
		Capability: plan.Steps[0].Capability, Offering: offerings[plan.Steps[0].OfferingID], State: generation.TaskRunning,
		Input: gateway.prepared[plan.Steps[0].PreparedInputID].NormalizedInput}
	return s, store, gateway, offerings, plan, task
}

func reconcileCommand(plan Plan) ReconcileCommand {
	return ReconcileCommand{OwnerID: plan.OwnerID, PlanID: plan.ID, ExpectedPlanVersion: plan.Version}
}

func TestReconcileFoundTaskObservesToTerminalAndRepeatsWithoutSubmission(t *testing.T) {
	ctx := context.Background()
	s, _, gateway, _, plan, task := reconcileFixture(t)
	key := plan.Steps[0].IdempotencyKey
	gateway.tasks[task.ID], gateway.byKey[key] = task, task.ID
	linked, err := s.Reconcile(ctx, reconcileCommand(plan))
	if err != nil || linked.State != Running || linked.Failure != "" || linked.Steps[0].TaskID != task.ID || linked.Version != plan.Version+1 {
		t.Fatalf("linked=%+v err=%v", linked, err)
	}
	if linked.Steps[0].SubmissionUncertain || linked.Steps[0].Resumable {
		t.Fatal("linked step still uncertain/resumable")
	}
	persisted, _ := s.Get(ctx, plan.OwnerID, plan.ID)
	if !reflect.DeepEqual(linked, persisted) {
		t.Fatal("link did not survive a read")
	}
	repeated, err := s.Reconcile(ctx, reconcileCommand(linked))
	if err != nil || !reflect.DeepEqual(repeated, linked) {
		t.Fatalf("repeat=%+v err=%v", repeated, err)
	}
	if _, err := s.Reconcile(ctx, reconcileCommand(plan)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version=%v", err)
	}
	task.State = generation.TaskSucceeded
	gateway.tasks[task.ID] = task
	if err := s.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	done, _ := s.Get(ctx, plan.OwnerID, plan.ID)
	if done.State != Succeeded || done.Steps[0].TaskID != task.ID || len(gateway.commands) != 0 {
		t.Fatalf("done=%+v submits=%d", done, len(gateway.commands))
	}
}

func TestReconcileAbsentRequiresCurrentCostResumeAndNewAuthorizationOnSameKey(t *testing.T) {
	ctx := context.Background()
	s, store, gateway, offerings, plan, _ := reconcileFixture(t)
	// Legacy keys without a frozen input can prepare a fresh input and grant.
	plan.Steps[0].PreparedInputID, plan.Steps[0].PreparedInputHash, plan.Steps[0].AuthorizationRef = "", "", ""
	store.plans[plan.ID] = plan
	key := plan.Steps[0].IdempotencyKey
	cleared, err := s.Reconcile(ctx, reconcileCommand(plan))
	if err != nil || cleared.State != Paused || cleared.Failure != ReasonReconciledAbsent || !cleared.Steps[0].Resumable {
		t.Fatalf("cleared=%+v err=%v", cleared, err)
	}
	step := cleared.Steps[0]
	if step.IdempotencyKey != key || step.Retry != 0 || step.PreparedInputID != "" || step.AuthorizationRef != "" || step.SubmissionUncertain {
		t.Fatalf("old identity not cleared safely: %+v", step)
	}
	repeat, err := s.Reconcile(ctx, reconcileCommand(cleared))
	if err != nil || repeat.Version != cleared.Version {
		t.Fatalf("repeat=%+v err=%v", repeat, err)
	}
	if err := s.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 0 {
		t.Fatal("clearing auto-submitted")
	}
	changed := offerings[step.OfferingID]
	changed.Revision, changed.EstimatedCredits = "v2", 11
	offerings[step.OfferingID] = changed
	if _, err := s.Resume(ctx, ResumeCommand{OwnerID: plan.OwnerID, PlanID: plan.ID, ExpectedPlanVersion: cleared.Version, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("old cost allowed: %v", err)
	}
	resumed, err := s.Resume(ctx, ResumeCommand{OwnerID: plan.OwnerID, PlanID: plan.ID, ExpectedPlanVersion: cleared.Version, ExpectedOfferingRevision: "v2"})
	if err != nil || resumed.Steps[0].EstimatedCredits != 11 {
		t.Fatalf("resume=%+v err=%v", resumed, err)
	}
	if err := s.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	awaiting, _ := s.Get(ctx, plan.OwnerID, plan.ID)
	if awaiting.Failure != ReasonConsentRequired || len(gateway.commands) != 0 || awaiting.Steps[0].IdempotencyKey != key {
		t.Fatalf("fresh grant not required: %+v", awaiting)
	}
	if err := advanceApproved(ctx, s); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 1 || gateway.commands[0].IdempotencyKey != key {
		t.Fatalf("submits=%+v", gateway.commands)
	}
}

// A changed cost cannot retire the approved grant of the old key: a lookup
// is only a point-in-time observation, so a new key could admit a second paid
// Task. Resume refuses and leaves the plan, key and grant unchanged.
func TestReconcileAbsentChangedCostIsRefusedWithoutNewKey(t *testing.T) {
	ctx := context.Background()
	s, _, gateway, offerings, plan, _ := reconcileFixture(t)
	cleared, err := s.Reconcile(ctx, reconcileCommand(plan))
	if err != nil {
		t.Fatal(err)
	}
	original := plan.Steps[0]
	oldOffering := offerings[original.OfferingID]
	changed := oldOffering
	changed.Revision, changed.EstimatedCredits = "v2", 11
	offerings[original.OfferingID] = changed
	if _, err := s.Resume(ctx, ResumeCommand{OwnerID: plan.OwnerID, PlanID: plan.ID, ExpectedPlanVersion: cleared.Version, ExpectedOfferingRevision: "v2"}); !errors.Is(err, ErrReconcileFrozenChanged) {
		t.Fatalf("changed grant allowed: %v", err)
	}
	kept, _ := s.Get(ctx, plan.OwnerID, plan.ID)
	if kept.Version != cleared.Version || kept.Steps[0].IdempotencyKey != original.IdempotencyKey || kept.Steps[0].Retry != original.Retry ||
		kept.Steps[0].PreparedInputID != original.PreparedInputID || kept.Steps[0].AuthorizationRef != original.AuthorizationRef {
		t.Fatalf("refused resume changed the plan: %+v", kept)
	}
	if err := s.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 0 {
		t.Fatal("changed cost submitted")
	}
}

// Review P1: after a reconciled resume the reason is cleared. A later
// unavailable pause must still keep the grant-bound frozen input.
func TestReconcileAbsentKeepsFrozenIdentityAcrossLaterUnavailablePause(t *testing.T) {
	ctx := context.Background()
	s, _, gateway, offerings, plan, _ := reconcileFixture(t)
	cleared, err := s.Reconcile(ctx, reconcileCommand(plan))
	if err != nil {
		t.Fatal(err)
	}
	original := plan.Steps[0]
	first, err := s.Resume(ctx, ResumeCommand{OwnerID: plan.OwnerID, PlanID: plan.ID, ExpectedPlanVersion: cleared.Version, ExpectedOfferingRevision: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	available := offerings[original.OfferingID]
	disabled := available
	disabled.Enabled = false
	offerings[original.OfferingID] = disabled
	if err := s.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	paused, _ := s.Get(ctx, plan.OwnerID, plan.ID)
	if paused.Failure != ReasonOfferingUnavailable || paused.Version <= first.Version || len(gateway.commands) != 0 {
		t.Fatalf("paused=%+v submits=%d", paused, len(gateway.commands))
	}
	offerings[original.OfferingID] = available
	resumed, err := s.Resume(ctx, ResumeCommand{OwnerID: plan.OwnerID, PlanID: plan.ID, ExpectedPlanVersion: paused.Version, ExpectedOfferingRevision: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	step := resumed.Steps[0]
	if step.PreparedInputID != original.PreparedInputID || step.AuthorizationRef != original.AuthorizationRef || step.IdempotencyKey != original.IdempotencyKey {
		t.Fatalf("frozen identity lost after unavailable pause: %+v", step)
	}
	if err := s.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 1 || gateway.commands[0].IdempotencyKey != original.IdempotencyKey {
		t.Fatalf("submits=%+v", gateway.commands)
	}
}

func TestReconcileAbsentRetainsExactUnusedGrantOnSameCost(t *testing.T) {
	ctx := context.Background()
	s, _, gateway, _, plan, _ := reconcileFixture(t)
	cleared, err := s.Reconcile(ctx, reconcileCommand(plan))
	if err != nil {
		t.Fatal(err)
	}
	original := plan.Steps[0]
	if cleared.Steps[0].PreparedInputID != original.PreparedInputID || cleared.Steps[0].AuthorizationRef != original.AuthorizationRef {
		t.Fatal("reconciliation replaced a reserved grant identity")
	}
	resumed, err := s.Resume(ctx, ResumeCommand{OwnerID: plan.OwnerID, PlanID: plan.ID, ExpectedPlanVersion: cleared.Version, ExpectedOfferingRevision: "v1"})
	if err != nil || resumed.Steps[0].PreparedInputID != original.PreparedInputID || resumed.Steps[0].IdempotencyKey != original.IdempotencyKey {
		t.Fatalf("resumed=%+v err=%v", resumed, err)
	}
	if err := s.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 1 || gateway.commands[0].IdempotencyKey != original.IdempotencyKey {
		t.Fatalf("same-key submit=%+v", gateway.commands)
	}
}

func TestReconcileUndeterminedAndUnsafeIdentitiesStayLocked(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*gatewayFake, *generation.Task)
	}{
		{"read error", func(g *gatewayFake, _ *generation.Task) { g.lookupError = errors.New("read failed") }},
		{"reserved tombstone", func(g *gatewayFake, _ *generation.Task) {
			g.lookupError = &generation.Error{Code: generation.ErrConflict}
		}},
		{"idempotency conflict", func(g *gatewayFake, _ *generation.Task) {
			g.lookupError = &generation.Error{Code: generation.ErrIdempotencyConflict}
		}},
		{"verification read error", func(g *gatewayFake, _ *generation.Task) { g.replayError = errors.New("grant unavailable") }},
		{"other session", func(_ *gatewayFake, task *generation.Task) { task.SessionID = "other" }},
		{"other offering", func(_ *gatewayFake, task *generation.Task) { task.Offering.ID = "other" }},
		{"other capability", func(_ *gatewayFake, task *generation.Task) { task.Capability = "other" }},
		{"not processing", func(_ *gatewayFake, task *generation.Task) { task.Offering.Domain = "image" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			s, _, gateway, _, plan, task := reconcileFixture(t)
			test.edit(gateway, &task)
			gateway.tasks[task.ID], gateway.byKey[plan.Steps[0].IdempotencyKey] = task, task.ID
			before, _ := json.Marshal(plan)
			if _, err := s.Reconcile(ctx, reconcileCommand(plan)); !errors.Is(err, ErrReconcileUndetermined) {
				t.Fatalf("err=%v", err)
			}
			locked, _ := s.Get(ctx, plan.OwnerID, plan.ID)
			after, _ := json.Marshal(locked)
			if string(before) != string(after) || len(gateway.commands) != 0 {
				t.Fatalf("undetermined changed plan: %+v", locked)
			}
		})
	}
}

func TestReconcileMismatchedFrozenTaskIsObservationOnly(t *testing.T) {
	for _, change := range []string{"source", "revision", "legacy"} {
		for _, terminal := range []generation.TaskState{generation.TaskSucceeded, generation.TaskFailed, generation.TaskCanceled} {
			t.Run(change+"/"+string(terminal), func(t *testing.T) {
				ctx := context.Background()
				s, store, gateway, _, plan, task := reconcileFixture(t)
				switch change {
				case "source":
					task.Input = json.RawMessage(`{"source_task":"different"}`)
				case "revision":
					task.Offering.Revision = "v2"
				case "legacy":
					plan.Steps[0].PreparedInputID = ""
					store.plans[plan.ID] = plan
				}
				// A successor must not consume the mismatched Task, even on success.
				plan.Steps = append(plan.Steps, Step{State: Pending, OfferingID: "remesh", Capability: "remesh"})
				store.plans[plan.ID] = plan
				gateway.tasks[task.ID], gateway.byKey[plan.Steps[0].IdempotencyKey] = task, task.ID
				linked, err := s.Reconcile(ctx, reconcileCommand(plan))
				if err != nil || linked.State != Running || linked.Steps[0].TaskID != task.ID || linked.Failure != ReasonReconciledMismatch {
					t.Fatalf("linked=%+v err=%v", linked, err)
				}
				task.State = terminal
				gateway.tasks[task.ID] = task
				if err := s.advanceAll(ctx); err != nil {
					t.Fatal(err)
				}
				stopped, _ := s.Get(ctx, plan.OwnerID, plan.ID)
				if stopped.State != Paused || stopped.Steps[0].Resumable || stopped.Steps[0].Retryable || stopped.Steps[1].State != Pending {
					t.Fatalf("mismatched task advanced: %+v", stopped)
				}
				if _, err := s.Resume(ctx, ResumeCommand{OwnerID: plan.OwnerID, PlanID: plan.ID, ExpectedPlanVersion: stopped.Version, ExpectedOfferingRevision: "v1"}); !errors.Is(err, ErrConflict) {
					t.Fatalf("mismatch resumed: %v", err)
				}
				if err := s.advanceAll(ctx); err != nil {
					t.Fatal(err)
				}
				if len(gateway.commands) != 0 {
					t.Fatal("mismatch resubmitted")
				}
			})
		}
	}
}

func TestReconcileCrossOwnerVersionAndInvalidCommand(t *testing.T) {
	s, _, gateway, _, plan, _ := reconcileFixture(t)
	command := reconcileCommand(plan)
	command.OwnerID = "other"
	if _, err := s.Reconcile(context.Background(), command); !errors.Is(err, ErrMissing) {
		t.Fatalf("cross-owner=%v", err)
	}
	command = reconcileCommand(plan)
	command.ExpectedPlanVersion++
	if _, err := s.Reconcile(context.Background(), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("version=%v", err)
	}
	command.ExpectedPlanVersion = 0
	if _, err := s.Reconcile(context.Background(), command); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid=%v", err)
	}
	if len(gateway.commands) != 0 {
		t.Fatal("invalid operation submitted")
	}
}
