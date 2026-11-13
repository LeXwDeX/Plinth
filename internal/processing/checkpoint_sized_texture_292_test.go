package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// #292: a checkpoint judges texture_size the way inspect_model does. The native
// file a step returns keeps the upstream's 8192-pixel textures; the exports the
// purpose sized are what the budget is about. Without a resolver for the sized
// deliverable the checkpoint judges the file (and fails the 2048 budget); with
// one it judges the export and says so.

func texturePlan(t *testing.T) (*Service, *memoryStore, *gatewayFake, Plan) {
	t.Helper()
	service, store, gateway := checkpointService(t)
	service.WithModelReport(func(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (modelinspect.Report, error) {
		return modelinspect.Report{Materials: []modelinspect.MaterialReport{{Slots: []modelinspect.MaterialSlotReport{
			{Present: true, Width: 8192, Height: 8192},
		}}}}, nil
	})
	maximum := 2048.0
	plan, err := service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session",
		SourceTaskID: "source", SourceAssetID: "model", IdempotencyKey: "texture-plan-key-0001",
		Origin: OriginAgent,
		Steps: []StepRequest{
			{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`),
				Checkpoint: &Checkpoint{Checks: []budget.PlanCheck{{Check: "texture_size", Maximum: &maximum}}}},
			{OfferingID: "rigging", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)},
		}})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, gateway, plan
}

func TestCheckpointJudgesTheNativeFileWithoutASizedDeliverable(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, plan := texturePlan(t)
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	stored := store.plans[plan.ID]
	checkpoint := stored.Steps[0].Checkpoint
	if stored.State != Paused || checkpoint.State != CheckpointFailed ||
		len(checkpoint.Results) != 1 || checkpoint.Results[0].Status != "fail" || checkpoint.Results[0].Actual != 8192 {
		t.Fatalf("native file against 2048: plan %s checkpoint %+v", stored.State, checkpoint)
	}
}

func TestCheckpointJudgesTheSizedDeliverableTheResolverNames(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, plan := texturePlan(t)
	var seen generation.Task
	var inspected generation.AssetID
	service.WithSizedTextureEdge(func(task generation.Task, asset generation.AssetID) int {
		seen, inspected = task, asset
		return 2048
	})
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	// The resolver is asked about the step's own Task and the file the checkpoint reads.
	if seen.ID != "step-1" || inspected != "out" {
		t.Fatalf("resolver saw task %q asset %q", seen.ID, inspected)
	}
	stored := store.plans[plan.ID]
	checkpoint := stored.Steps[0].Checkpoint
	if checkpoint.State != CheckpointPassed || len(checkpoint.Results) != 1 {
		t.Fatalf("sized export within 2048: checkpoint %+v", checkpoint)
	}
	result := checkpoint.Results[0]
	if result.Status != "pass" || result.Actual != 2048 || result.Detail != "sized_export" {
		t.Fatalf("result = %+v, want a pass on the 2048-pixel export that names its basis", result)
	}
	// The plan goes on to the next step: nothing blocked it.
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 2 {
		t.Fatalf("a passed checkpoint must let the next step start: %d submissions", len(gateway.commands))
	}
}

func TestCheckpointFailsASizedDeliverableOverTheBudget(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, plan := texturePlan(t)
	service.WithSizedTextureEdge(func(generation.Task, generation.AssetID) int { return 4096 })
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	succeedTask(gateway, "step-1")
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	stored := store.plans[plan.ID]
	checkpoint := stored.Steps[0].Checkpoint
	if stored.State != Paused || checkpoint.State != CheckpointFailed ||
		checkpoint.Results[0].Actual != 4096 || checkpoint.Results[0].Detail != "sized_export" {
		t.Fatalf("export over 2048: plan %s checkpoint %+v", stored.State, checkpoint)
	}
}
