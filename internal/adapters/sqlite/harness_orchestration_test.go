package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

func TestHarnessSkillRunPersistsBindingAndStrictlyRecoversAcceptedTask(t *testing.T) {
	ctx := context.Background()
	repo, _, path := skillFixture(t)
	insertGrantTurn(t, repo, "turn-a", "owner-a", "session-a")
	resolver := testRecipeResolver{skillrun.Recipe{ID: "three-view", Revision: "r1", Steps: []skillrun.StepSpec{{Key: "front", Label: "Front", Capability: generation.ImageToImage, OfferingID: "offering-i2i", OfferingRevision: "r7", PromptTemplate: "front view", PromptParameter: "prompt", SourceParameter: "reference_assets", OutputRole: "image"}}}}
	runs, err := skillrun.NewService(repo, resolver)
	if err != nil {
		t.Fatal(err)
	}
	cmd := skillCreateCommand()
	cmd.Harness = harness.DefaultSelection("owner-a", "session-a").Binding
	run, err := runs.Create(ctx, cmd)
	if err != nil || run.Harness != cmd.Harness {
		t.Fatalf("create %+v %v", run, err)
	}
	changed := cmd
	changed.Harness.Revision = 1
	if _, err := runs.Create(ctx, changed); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("rebound run accepted %v", err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	recovered, err := second.GetSkillRun(ctx, "owner-a", run.ID)
	if err != nil || recovered.Harness != cmd.Harness {
		t.Fatalf("binding lost after reopen %+v %v", recovered, err)
	}
	gateway := &skillRecoveryGateway{repo: second}
	grants := &executiongrant.Service{Grants: second, Prepared: second}
	recoveredRuns, err := skillrun.NewService(second, resolver)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := skillrun.NewRuntime(recoveredRuns, gateway, grants)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.PrepareStep(ctx, "owner-a", run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	_, grant, err := runtime.RequestAgentGrant(ctx, "owner-a", run.ID, "front", "turn-a")
	if err != nil || grant.Harness != cmd.Harness {
		t.Fatalf("grant binding %+v %v", grant, err)
	}
	if _, err := second.DecideExecutionGrant(ctx, "owner-a", grant.RequestID, "human", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	admitted, accepted, err := runtime.SubmitStep(ctx, "owner-a", run.ID, "front")
	if err != nil || accepted.TaskID == "" || admitted.Steps[0].TaskID != accepted.TaskID {
		t.Fatalf("admission %+v %+v %v", admitted, accepted, err)
	}
	selection := harness.DefaultSelection("owner-a", "session-a")
	selection.OperationID = "changed"
	if _, err := second.CompareAndSwapHarnessSelection(ctx, selection, 0); err != nil {
		t.Fatal(err)
	}
	before := gateway.submits
	if _, result, err := runtime.SubmitStep(ctx, "owner-a", run.ID, "front"); err != nil || !result.Replayed || gateway.submits != before {
		t.Fatalf("bound recovery resubmitted %+v %v", result, err)
	}
	if _, err := second.db.Exec(`UPDATE skill_runs SET harness_revision=1 WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.SubmitStep(ctx, "owner-a", run.ID, "front"); err == nil {
		t.Fatal("bound recovery accepted wrong Harness identity")
	}
	operation, key, _ := skillrun.StepIdentity(run.ID, "front")
	if _, err := recoveredRuns.BindTask(ctx, skillrun.TaskCommand{StepCommand: skillrun.StepCommand{OwnerID: "owner-a", RunID: run.ID, StepKey: "front", OperationID: operation, IdempotencyKey: key}, TaskID: accepted.TaskID}); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("idempotent task bind accepted wrong Harness: %v", err)
	}

}

func TestHarnessSkillGrantAttachRejectsOtherBinding(t *testing.T) {
	ctx := context.Background()
	repo, runs, _ := skillFixture(t)
	cmd := skillCreateCommand()
	cmd.Harness = harness.DefaultSelection("owner-a", "session-a").Binding
	run, err := runs.Create(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	op, key, plan := skillrun.StepIdentity(run.ID, "front")
	step := skillrun.StepCommand{OwnerID: "owner-a", RunID: run.ID, StepKey: "front", OperationID: op, IdempotencyKey: key}
	prepared := generation.PreparedInput{ID: "prepared-proof", OwnerID: "owner-a", SessionID: "session-a", Capability: generation.ImageToImage, OfferingID: "offering-i2i", OfferingRevision: "r7", InputHash: "hash-proof", Assets: []generation.PreparedInputAsset{{ID: "source-a"}}, CreatedAt: time.Now()}
	if err := repo.CreatePreparedInput(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.AttachPrepared(ctx, skillrun.PreparedCommand{StepCommand: step, PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash}); err != nil {
		t.Fatal(err)
	}
	identity := executiongrant.Identity{OwnerID: "owner-a", SessionID: "session-a", OfferingID: "offering-i2i", OfferingRevision: "r7", PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, OperationID: op, IdempotencyKey: key, PlanStepID: plan}
	wrong, err := repo.PreparePending(ctx, identity, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.AttachGrant(ctx, skillrun.GrantCommand{StepCommand: step, GrantRequestID: wrong.RequestID}); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("wrong binding attached %v", err)
	}
}

func TestHarnessProcessingPlanPersistsBindingAndCannotBeReboundOnSave(t *testing.T) {
	ctx := context.Background()
	repo := openGrantTestRepo(t)
	now := time.Now()
	binding := harness.DefaultSelection("owner-a", "session-a").Binding
	plan := processing.Plan{ID: "harness-plan", OwnerID: "owner-a", SessionID: "session-a", SourceTaskID: "source", SourceAssetID: "asset", State: processing.Pending, RequestHash: "hash", Steps: []processing.Step{}, Version: 1, CreatedAt: now, UpdatedAt: now, Harness: binding}
	if err := repo.CreateProcessingPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := repo.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	recovered, err := second.GetProcessingPlan(ctx, "owner-a", plan.ID)
	if err != nil || recovered.Harness != binding {
		t.Fatalf("reopen %+v %v", recovered, err)
	}
	recovered.Harness.Revision = 1
	if err := second.SaveProcessingPlan(ctx, recovered); !errors.Is(err, processing.ErrConflict) {
		t.Fatalf("plan binding mutable %v", err)
	}
	// The binding is metadata on Plan, never inserted in its step/provider input.
	raw, _ := json.Marshal(plan.Steps)
	if string(raw) != "[]" {
		t.Fatal("unexpected step metadata")
	}
}
