package sqlite

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/taskretry"
)

func TestGrantOriginLookupsAreOwnerScoped(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	g, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	// By key: the owner finds its own grant, nobody else does, unknown keys are absent.
	found, ok, err := r.GrantByKey(ctx, "owner-a", i.IdempotencyKey)
	if err != nil || !ok || found.RequestID != g.RequestID || found.Status != executiongrant.Pending {
		t.Fatalf("by key = %+v ok=%v err=%v", found, ok, err)
	}
	if _, ok, err := r.GrantByKey(ctx, "owner-b", i.IdempotencyKey); err != nil || ok {
		t.Fatalf("another owner found the grant: ok=%v err=%v", ok, err)
	}
	if _, ok, err := r.GrantByKey(ctx, "owner-a", "unknown"); err != nil || ok {
		t.Fatalf("unknown key: ok=%v err=%v", ok, err)
	}
	// Before the grant is bound to a task the task has no origin.
	if origin, err := r.PreparedInputOf(ctx, "owner-a", "task-a"); err != nil || origin != (taskretry.Origin{}) {
		t.Fatalf("unbound origin = %+v err=%v", origin, err)
	}
	if id, err := r.AdmittedPreparedInput(ctx, "owner-a", "task-a"); err != nil || id != "" {
		t.Fatalf("unbound admission = %q err=%v", id, err)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insertGrantedTask(t, ctx, conn, "task-a", i)
	if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); err != nil {
		t.Fatal(err)
	}
	if err := r.BindExecutionGrantOnConn(ctx, conn, g.RequestID, i, "task-a"); err != nil {
		t.Fatal(err)
	}
	_, _ = conn.ExecContext(ctx, "COMMIT")
	conn.Close()
	if origin, err := r.PreparedInputOf(ctx, "owner-a", "task-a"); err != nil || origin.PreparedInputID != i.PreparedInputID || origin.PlanStepID != i.PlanStepID {
		t.Fatalf("origin = %+v err=%v", origin, err)
	}
	if origin, err := r.PreparedInputOf(ctx, "owner-b", "task-a"); err != nil || origin != (taskretry.Origin{}) {
		t.Fatalf("another owner read the origin: %+v err=%v", origin, err)
	}
	if id, err := r.AdmittedPreparedInput(ctx, "owner-a", "task-a"); err != nil || string(id) != i.PreparedInputID {
		t.Fatalf("bound admission = %q err=%v", id, err)
	}
	if id, err := r.AdmittedPreparedInput(ctx, "owner-b", "task-a"); err != nil || id != "" {
		t.Fatalf("another owner read admission = %q err=%v", id, err)
	}
}

func TestAdmittedPreparedInputReadFailureIsNotLegacy(t *testing.T) {
	r := openGrantTestRepo(t)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AdmittedPreparedInput(context.Background(), "owner-a", "task-a"); err == nil {
		t.Fatal("closed database returned an unbound legacy admission")
	}
}

// A task admitted as a plan step reports that step, so a standalone retry of
// it is refused and the plan's own retry path stays the only one.
func TestGrantOriginReportsThePlanStep(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	i.OperationID, i.PlanStepID, i.IdempotencyKey = "plan:plan-1", "plan-1:step-1", "plan-step-key"
	g, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insertGrantedTask(t, ctx, conn, "task-step", i)
	if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); err != nil {
		t.Fatal(err)
	}
	if err := r.BindExecutionGrantOnConn(ctx, conn, g.RequestID, i, "task-step"); err != nil {
		t.Fatal(err)
	}
	_, _ = conn.ExecContext(ctx, "COMMIT")
	conn.Close()
	origin, err := r.PreparedInputOf(ctx, "owner-a", "task-step")
	if err != nil || origin.PreparedInputID != i.PreparedInputID || origin.PlanStepID != "plan-1:step-1" {
		t.Fatalf("origin = %+v err=%v", origin, err)
	}
}
