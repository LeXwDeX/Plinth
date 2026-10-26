package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

func harnessAdmissionFixture(t *testing.T, r *Repository, binding harness.Binding, source string) (generation.Task, generation.Event) {
	t.Helper()
	ctx := context.Background()
	i := grantIdentity()
	i.Harness = binding
	turn := ""
	if source == executiongrant.AgentPending {
		turn = "turn-harness"
		insertGrantTurn(t, r, turn, i.OwnerID, i.SessionID)
	}
	g, err := r.PreparePending(ctx, i, source, turn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := generation.Task{ID: "task-harness", OwnerID: generation.OwnerID(i.OwnerID), SessionID: i.SessionID,
		Capability: generation.TextToImage, Offering: generation.Offering{ID: generation.OfferingID(i.OfferingID), Revision: i.OfferingRevision},
		Input: []byte(`{}`), IdempotencyKey: i.IdempotencyKey, State: generation.TaskQueued, Version: 1, CreatedAt: now, UpdatedAt: now,
		Admission: &generation.ExecutionAdmission{RequestID: g.RequestID, PreparedInputID: generation.PreparedInputID(i.PreparedInputID), PreparedInputHash: i.PreparedInputHash, OperationID: i.OperationID, PlanStepID: i.PlanStepID, Harness: binding}}
	task.RequestHash = generation.ExpectedRequestHash(task.Offering.ID, task.Input)
	return task, generation.Event{TaskID: task.ID, Type: "task.submitted", Data: []byte(`{}`), CreatedAt: now}
}

func changeHarness(t *testing.T, r *Repository) harness.Selection {
	t.Helper()
	ctx := context.Background()
	s, err := r.GetHarnessSelection(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	s.OperationID = "changed"
	updated, err := r.CompareAndSwapHarnessSelection(ctx, s, s.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func assertHarnessUnclaimed(t *testing.T, r *Repository, task generation.Task) {
	t.Helper()
	g, err := r.GetExecutionGrant(context.Background(), string(task.OwnerID), task.Admission.RequestID)
	if err != nil || g.Status != executiongrant.Approved || g.TaskID != "" {
		t.Fatalf("grant was consumed %+v %v", g, err)
	}
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM generation_tasks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("tasks=%d err=%v", count, err)
	}
}

func TestHarnessSelectionChangeRejectsApprovedUnsubmittedWithoutConsumingGrant(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		legacy       bool
	}{
		{"bound-agent", executiongrant.AgentPending, false}, {"legacy-agent", executiongrant.AgentPending, true},
		{"bound-plan", executiongrant.HumanBrowser, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := openGrantTestRepo(t)
			binding := harness.DefaultSelection("owner-a", "session-a").Binding
			if tc.legacy {
				binding = harness.Binding{}
			}
			task, event := harnessAdmissionFixture(t, r, binding, tc.source)
			changeHarness(t, r)
			if _, _, err := r.Create(context.Background(), task, event, 0); !errors.Is(err, harness.ErrStale) {
				t.Fatalf("stale admission %v", err)
			}
			assertHarnessUnclaimed(t, r, task)
		})
	}
}

func TestHarnessDirectLegacyHumanOperationKeepsExistingContract(t *testing.T) {
	r := openGrantTestRepo(t)
	task, event := harnessAdmissionFixture(t, r, harness.Binding{}, executiongrant.HumanBrowser)
	changeHarness(t, r)
	if _, replay, err := r.Create(context.Background(), task, event, 0); err != nil || replay {
		t.Fatalf("direct operation rejected %v", err)
	}
}

func TestHarnessAcceptedReplayRequiresOriginalIdentityAfterSelectionChange(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	old := harness.DefaultSelection("owner-a", "session-a").Binding
	task, event := harnessAdmissionFixture(t, r, old, executiongrant.AgentPending)
	stored, replay, err := r.Create(ctx, task, event, 0)
	if err != nil || replay {
		t.Fatalf("admit %v", err)
	}
	current := changeHarness(t, r)
	if err := r.VerifyTaskGrantReplay(ctx, stored, task.Admission); err != nil {
		t.Fatal("original identity replay", err)
	}
	if got, replayed, err := r.Create(ctx, task, event, 0); err != nil || !replayed || got.ID != stored.ID {
		t.Fatalf("race branch replay %+v %v %v", got, replayed, err)
	}
	mutations := []func(*generation.ExecutionAdmission){
		func(a *generation.ExecutionAdmission) { a.Harness = current.Binding },
		func(a *generation.ExecutionAdmission) { a.Harness = harness.Binding{} },
		func(a *generation.ExecutionAdmission) { a.OperationID = "other" },
		func(a *generation.ExecutionAdmission) { a.PreparedInputID = "other" },
		func(a *generation.ExecutionAdmission) { a.PreparedInputHash = "other" },
		func(a *generation.ExecutionAdmission) { a.RequestID = "other" },
	}
	for i, mutate := range mutations {
		candidate := task
		admission := *task.Admission
		mutate(&admission)
		candidate.Admission = &admission
		if err := r.VerifyTaskGrantReplay(ctx, stored, candidate.Admission); !errors.Is(err, generation.RepositoryAuthorizationRequired) {
			t.Fatalf("mutation %d replay: %v", i, err)
		}
		if _, _, err := r.Create(ctx, candidate, event, 0); !errors.Is(err, generation.RepositoryAuthorizationRequired) {
			t.Fatalf("mutation %d race replay: %v", i, err)
		}
	}
}

func TestHarnessAdmissionDatabaseErrorAndTaskFailureRollback(t *testing.T) {
	t.Run("database", func(t *testing.T) {
		r := openGrantTestRepo(t)
		task, event := harnessAdmissionFixture(t, r, harness.DefaultSelection("owner-a", "session-a").Binding, executiongrant.AgentPending)
		if _, err := r.db.Exec(`DROP TABLE harness_selections`); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.Create(context.Background(), task, event, 0); err == nil {
			t.Fatal("admitted with broken selection table")
		}
		assertHarnessUnclaimed(t, r, task)
	})
	t.Run("rollback", func(t *testing.T) {
		r := openGrantTestRepo(t)
		task, event := harnessAdmissionFixture(t, r, harness.DefaultSelection("owner-a", "session-a").Binding, executiongrant.AgentPending)
		if _, err := r.db.Exec(`CREATE TRIGGER test_reject_harness_event BEFORE INSERT ON generation_task_events
 BEGIN SELECT RAISE(ABORT, 'synthetic event write failure'); END`); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.Create(context.Background(), task, event, 0); err == nil {
			t.Fatal("event storage failure accepted")
		}
		assertHarnessUnclaimed(t, r, task)
	})
}

func TestHarnessCASAndAdmissionSerializeBothCommitOrders(t *testing.T) {
	// Hold the same write lock that Create uses, let a second repository start
	// CAS, then commit an admitted task. The queued CAS cannot interleave claim.
	ctx := context.Background()
	r := openGrantTestRepo(t)
	task, _ := harnessAdmissionFixture(t, r, harness.DefaultSelection("owner-a", "session-a").Binding, executiongrant.AgentPending)
	var seq int
	var name, path string
	if err := r.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request, identity, err := taskGrantIdentity(task)
	if err != nil {
		rollbackGrantConn(conn)
		t.Fatal(err)
	}
	if _, err = r.ClaimExecutionGrantOnConn(ctx, conn, request, identity); err != nil {
		rollbackGrantConn(conn)
		t.Fatal(err)
	}
	result := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		s := harness.DefaultSelection("owner-a", "session-a")
		s.OperationID = "changed"
		_, err := other.CompareAndSwapHarnessSelection(ctx, s, 0)
		result <- err
	}()
	<-started
	insertGrantedTask(t, ctx, conn, string(task.ID), identity)
	if err = r.BindExecutionGrantOnConn(ctx, conn, request, identity, string(task.ID)); err != nil {
		rollbackGrantConn(conn)
		t.Fatal(err)
	}
	if err = commitGrantConn(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if err = r.VerifyTaskGrantReplay(ctx, task, task.Admission); err != nil {
		t.Fatal("admitted-first task lost identity", err)
	}
	// The selection-first ordering is covered above through Create's complete
	// transaction; there a stale grant remains approved with no Task.
}
