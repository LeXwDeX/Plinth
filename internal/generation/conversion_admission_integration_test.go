package generation_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #330 final-review P1: the format coverage projection already avoids a
// second run while a direct conversion is listed, but that read is not an
// admission decision. These tests exercise the kernel guard shared by an
// Agent's direct conversion and a Processing Plan step: no second paid Task
// may be created for the same owner, session, exact source and overlapping
// formats until the first reaches a terminal state.

func submitConversionSource(t *testing.T, h *harness, prompt string) generation.TaskID {
	t.Helper()
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"` + prompt + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}

func conversionInput(source generation.TaskID) []byte {
	return []byte(`{"source_task":"` + string(source) + `"}`)
}

// planConversionSubmission prepares a step exactly as the Processing Plan
// does: a frozen input, an approved grant carrying the plan step, and the
// same SubmitPrepared entry point.
func planConversionSubmission(t *testing.T, h *harness, source generation.TaskID, planID, stepID, key string) generation.SubmitPreparedCommand {
	t.Helper()
	ctx := context.Background()
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: "generation-test-session:owner-a",
		Capability: generation.FormatConversion, OfferingID: "meshy/processing/convert",
		Input: conversionInput(source),
	})
	if err != nil {
		t.Fatal(err)
	}
	grants := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "processing-plan", Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: prepared.SessionID,
		OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: key, OperationID: planID, PlanStepID: stepID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Status != executiongrant.Approved {
		if _, err := grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
			t.Fatal(err)
		}
	}
	return generation.SubmitPreparedCommand{
		OwnerID: "owner-a", SessionID: prepared.SessionID, PreparedInputID: prepared.ID,
		IdempotencyKey: key, OperationID: planID, PlanStepID: stepID, AuthorizationRef: grant.RequestID,
	}
}

func directConversionSubmission(t *testing.T, h *harness, source generation.TaskID, key string) generation.SubmitPreparedCommand {
	t.Helper()
	ctx := context.Background()
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: "generation-test-session:owner-a",
		Capability: generation.FormatConversion, OfferingID: "meshy/processing/convert",
		Input: conversionInput(source),
	})
	if err != nil {
		t.Fatal(err)
	}
	return authorizePrepared(t, ctx, h.repository, prepared, key)
}

func setTaskState(t *testing.T, h *harness, id generation.TaskID, state generation.TaskState) {
	t.Helper()
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE generation_tasks SET state = ? WHERE id = ?`, state, id); err != nil {
		t.Fatal(err)
	}
}

func TestConversionAdmissionBlocksPlanAndDirectAcrossEachOther330(t *testing.T) {
	h, _, _ := newCompletionHarness(t)
	ctx := context.Background()
	source := submitConversionSource(t, h, "admission cross path")
	sourceTask := runToTerminal(t, h, "owner-a", source, 30)

	direct, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "meshy/processing/convert", Input: conversionInput(sourceTask.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	// A Processing Plan step of the same source and formats must not create a
	// second charged Task while the direct one is active.
	plan := planConversionSubmission(t, h, sourceTask.ID, "plan-330", "0:0", "processing:plan-330:0:0")
	if _, err := h.module.SubmitPrepared(ctx, plan); !errors.Is(err, generation.ErrConversionInProgress) {
		t.Fatalf("plan step during a direct conversion = %v", err)
	}
	// The direct path is refused while the plan's Task is active.
	setTaskState(t, h, direct.TaskID, generation.TaskFailed)
	planAccepted, err := h.module.SubmitPrepared(ctx, plan)
	if err != nil {
		t.Fatalf("plan step after the direct Task ended: %v", err)
	}
	secondDirect := directConversionSubmission(t, h, sourceTask.ID, "direct-after-plan")
	if _, err := h.module.SubmitPrepared(ctx, secondDirect); !errors.Is(err, generation.ErrConversionInProgress) {
		t.Fatalf("direct conversion during a plan's Task = %v", err)
	}
	// A terminal plan Task releases the source for an explicit retry.
	setTaskState(t, h, planAccepted.TaskID, generation.TaskSucceeded)
	if _, err := h.module.SubmitPrepared(ctx, secondDirect); err != nil {
		t.Fatalf("direct retry after the plan ended: %v", err)
	}
}

func TestConversionAdmissionSerializesSimultaneousDirectSubmissions330(t *testing.T) {
	h, _, _ := newCompletionHarness(t)
	ctx := context.Background()
	source := submitConversionSource(t, h, "admission concurrent direct")
	sourceTask := runToTerminal(t, h, "owner-a", source, 30)
	one := directConversionSubmission(t, h, sourceTask.ID, "direct-concurrent-one")
	two := directConversionSubmission(t, h, sourceTask.ID, "direct-concurrent-two")

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, command := range []generation.SubmitPreparedCommand{one, two} {
		wg.Add(1)
		go func(command generation.SubmitPreparedCommand) {
			defer wg.Done()
			<-start
			_, err := h.module.SubmitPrepared(ctx, command)
			results <- err
		}(command)
	}
	close(start)
	wg.Wait()
	close(results)

	accepted, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, generation.ErrConversionInProgress):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if accepted != 1 || conflicted != 1 {
		t.Fatalf("accepted=%d conflicted=%d, want one paid Task", accepted, conflicted)
	}
	// Only the source Task and one conversion exist in the session.
	page, err := h.module.List(ctx, generation.OwnTasks("owner-a"), generation.ListQuery{
		SessionID: "generation-test-session:owner-a", Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversions := 0
	for _, task := range page.Tasks {
		if task.Offering.ID == "meshy/processing/convert" {
			conversions++
		}
	}
	if conversions != 1 {
		t.Fatalf("stored %d conversion tasks, want 1", conversions)
	}
}

func TestConversionAdmissionAllowsAnotherSource330(t *testing.T) {
	h, _, _ := newCompletionHarness(t)
	ctx := context.Background()
	first := runToTerminal(t, h, "owner-a", submitConversionSource(t, h, "admission source one"), 30)
	second := runToTerminal(t, h, "owner-a", submitConversionSource(t, h, "admission source two"), 30)
	for _, source := range []generation.Task{first, second} {
		if _, err := h.module.SubmitPrepared(ctx, directConversionSubmission(t, h, source.ID, "direct-source-"+string(source.ID))); err != nil {
			t.Fatalf("conversion of another source %s was refused: %v", source.ID, err)
		}
	}
}
