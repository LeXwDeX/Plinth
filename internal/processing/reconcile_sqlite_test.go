package processing_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

type reconcileCatalog struct{ *catalog.Catalog }

func (reconcileCatalog) OfferingAvailable(generation.OfferingID) bool { return true }

type reconcileSQLiteGateway struct{ repository *sqlite.Repository }

func (g reconcileSQLiteGateway) LookupByIdempotency(ctx context.Context, owner generation.OwnerID, key string) (generation.Task, error) {
	task, err := g.repository.GetByIdempotency(ctx, owner, key)
	if errors.Is(err, generation.RepositoryNotFound) {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound}
	}
	if errors.Is(err, generation.RepositoryConflict) {
		return generation.Task{}, &generation.Error{Code: generation.ErrConflict}
	}
	return task, err
}
func (g reconcileSQLiteGateway) Get(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	return g.repository.Get(ctx, owner, id)
}
func (reconcileSQLiteGateway) VerifyPreparedTaskReplay(context.Context, generation.SubmitPreparedCommand) (generation.Task, error) {
	return generation.Task{}, errors.New("unexpected replay")
}
func (reconcileSQLiteGateway) GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error) {
	return generation.PreparedInput{}, errors.New("unexpected read")
}
func (reconcileSQLiteGateway) PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error) {
	return generation.PreparedInput{}, errors.New("reconciliation must not prepare")
}
func (reconcileSQLiteGateway) SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error) {
	return generation.Accepted{}, errors.New("reconciliation must not submit")
}

// Real grants reserve their key before there is a Task. A fake gateway which
// permits a fresh prepared input under that key would hide this recovery bug.
func TestReconcileAbsentWithSQLiteRetainsGrantIdentityAndSameKeyAdmission(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlite.Open(t.TempDir() + "/reconcile.db")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/processing/remesh")
	if !ok {
		t.Fatal("offering missing")
	}
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", OwnerName: "owner", Title: "reconcile", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"source_task":"source"}`)
	prepared := generation.PreparedInput{ID: "frozen-input", OwnerID: "owner", SessionID: "session", OfferingID: offering.ID,
		OfferingRevision: offering.Revision, Capability: offering.Capability, NormalizedInput: input, InputHash: "original-input-hash", CreatedAt: now}
	if err := repository.CreatePreparedInput(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	identity := executiongrant.Identity{OwnerID: "owner", SessionID: "session", OfferingID: string(offering.ID), OfferingRevision: offering.Revision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, OperationID: "plan", PlanStepID: "0:0", IdempotencyKey: "original-processing-key"}
	grant, err := repository.PreparePending(ctx, identity, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DecideExecutionGrant(ctx, "owner", grant.RequestID, "owner", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	plan := processing.Plan{ID: "plan", OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "asset",
		State: processing.Paused, Failure: processing.ReasonSubmitUnreconciled, Version: 1, CreatedAt: now, UpdatedAt: now, RequestHash: "plan-request",
		Steps: []processing.Step{{State: processing.Paused, Failure: processing.ReasonSubmitUnreconciled, Capability: offering.Capability,
			OfferingID: offering.ID, Revision: offering.Revision, Input: json.RawMessage(`{}`), EstimatedCredits: offering.EstimateTotalCredits(json.RawMessage(`{}`)),
			PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash, OperationID: identity.OperationID, PlanStepID: identity.PlanStepID,
			IdempotencyKey: identity.IdempotencyKey, AuthorizationRef: grant.RequestID}}}
	if err := repository.CreateProcessingPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	service, err := processing.New(repository, reconcileCatalog{product}, reconcileSQLiteGateway{repository}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := service.Reconcile(ctx, processing.ReconcileCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: 1})
	if err != nil || cleared.Failure != processing.ReasonReconciledAbsent {
		t.Fatalf("clear=%+v err=%v", cleared, err)
	}
	// No Task was created, but this key cannot be bound to a different grant input.
	other := identity
	other.PreparedInputID = "different-input"
	other.PreparedInputHash = "different-hash"
	if _, err := repository.PreparePending(ctx, other, executiongrant.HumanBrowser, ""); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("same key was repurposed: %v", err)
	}
	resumed, err := service.Resume(ctx, processing.ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: cleared.Version, ExpectedOfferingRevision: offering.Revision})
	if err != nil {
		t.Fatal(err)
	}
	step := resumed.Steps[0]
	storedGrant, err := repository.GetExecutionGrant(ctx, "owner", grant.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := executiongrant.Identity{OwnerID: "owner", SessionID: resumed.SessionID, OfferingID: string(offering.ID), OfferingRevision: offering.Revision,
		PreparedInputID: string(step.PreparedInputID), PreparedInputHash: step.PreparedInputHash, OperationID: step.OperationID, PlanStepID: step.PlanStepID, IdempotencyKey: step.IdempotencyKey}
	if storedGrant.Identity != wantIdentity {
		t.Fatalf("retained grant identity=%+v resume identity=%+v", storedGrant.Identity, wantIdentity)
	}
	if step.PreparedInputID != prepared.ID || step.AuthorizationRef != grant.RequestID || step.IdempotencyKey != identity.IdempotencyKey {
		t.Fatalf("resume replaced frozen identity: %+v", step)
	}
	// The worker may pause again before submitting (offering_unavailable). The
	// reconciled reason is gone by then; the grant-bound identity must remain.
	repaused := resumed
	repaused.State, repaused.Failure = processing.Paused, processing.ReasonOfferingUnavailable
	repaused.Steps[0].State, repaused.Steps[0].Failure, repaused.Steps[0].Resumable = processing.Paused, processing.ReasonOfferingUnavailable, true
	if err := repository.SaveProcessingPlan(ctx, repaused); err != nil {
		t.Fatal(err)
	}
	repaused, err = repository.GetProcessingPlan(ctx, "owner", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err = service.Resume(ctx, processing.ResumeCommand{OwnerID: "owner", PlanID: plan.ID, ExpectedPlanVersion: repaused.Version, ExpectedOfferingRevision: offering.Revision})
	if err != nil {
		t.Fatal(err)
	}
	step = resumed.Steps[0]
	if step.PreparedInputID != prepared.ID || step.AuthorizationRef != grant.RequestID || step.IdempotencyKey != identity.IdempotencyKey {
		t.Fatalf("second resume replaced frozen identity: %+v", step)
	}
	if _, err := repository.GetByIdempotency(ctx, "owner", identity.IdempotencyKey); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("reconcile/resume created Task: %v", err)
	}
	// The normal kernel admission transaction can consume the retained exact
	// grant once. This is test fixture setup, not a new Processing Task writer.
	task := generation.Task{ID: "accepted-after-confirmation", OwnerID: "owner", SessionID: "session", Offering: offering, Capability: offering.Capability,
		Input: input, IdempotencyKey: step.IdempotencyKey, RequestHash: generation.ExpectedRequestHash(offering.ID, input), State: generation.TaskQueued, Version: 1,
		CreatedAt: now, UpdatedAt: now, NextActionAt: now, Admission: &generation.ExecutionAdmission{RequestID: step.AuthorizationRef,
			PreparedInputID: step.PreparedInputID, PreparedInputHash: step.PreparedInputHash, OperationID: step.OperationID, PlanStepID: step.PlanStepID}}
	for index := 0; index < 2; index++ {
		created, replayed, err := repository.Create(ctx, task, generation.Event{TaskID: task.ID, Type: "task.submitted", Data: json.RawMessage(`{}`), CreatedAt: now}, 0)
		if err != nil || created.ID != task.ID || replayed != (index == 1) {
			t.Fatalf("admission %d: task=%+v replay=%v err=%v", index, created, replayed, err)
		}
	}
}
