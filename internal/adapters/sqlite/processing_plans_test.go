package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

func TestProcessingPlanPersistenceAndOwnership(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	_, err = repository.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id, owner_id, owner_name, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"session", "owner", "owner", "title", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	plan := processing.Plan{ID: "plan", OwnerID: "owner", SessionID: "session", SourceTaskID: "task", SourceAssetID: "asset",
		State: processing.Pending, RequestHash: "request", Steps: []processing.Step{{OfferingID: "rigging", State: processing.Pending, Revision: "v1"}},
		Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := repository.CreateProcessingPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetProcessingPlan(ctx, "another-owner", "plan"); !errors.Is(err, processing.ErrMissing) {
		t.Fatalf("foreign read = %v", err)
	}
	stored, err := repository.GetProcessingPlan(ctx, "owner", "plan")
	if err != nil || stored.Steps[0].Revision != "v1" || stored.RequestHash != "request" {
		t.Fatalf("read = %#v, %v", stored, err)
	}
	var legacyApprovedCredits int
	if err := repository.db.QueryRowContext(ctx, `SELECT approved_credits FROM processing_plans WHERE id = ?`, plan.ID).Scan(&legacyApprovedCredits); err != nil || legacyApprovedCredits != 0 {
		t.Fatalf("legacy approved_credits = %d, error = %v", legacyApprovedCredits, err)
	}
	active, err := repository.ListActiveProcessingPlans(ctx, "", 10)
	if err != nil || len(active) != 1 {
		t.Fatalf("active = %#v, %v", active, err)
	}
	// The old column remains for migration compatibility and never authorizes a
	// paid action or changes when the plan state is saved.
	stored.State = processing.Paused
	stored.Steps[0].EstimatedCredits = 8
	stored.UpdatedAt = now.Add(time.Second)
	if err := repository.SaveProcessingPlan(ctx, stored); err != nil {
		t.Fatal(err)
	}
	paused, err := repository.GetProcessingPlan(ctx, "owner", "plan")
	if err != nil || paused.State != processing.Paused || paused.Steps[0].EstimatedCredits != 8 || paused.Version != 2 {
		t.Fatalf("paused = %#v, %v", paused, err)
	}
	if err := repository.db.QueryRowContext(ctx, `SELECT approved_credits FROM processing_plans WHERE id = ?`, plan.ID).Scan(&legacyApprovedCredits); err != nil || legacyApprovedCredits != 0 {
		t.Fatalf("legacy approved_credits after save = %d, error = %v", legacyApprovedCredits, err)
	}
	if active, err := repository.ListActiveProcessingPlans(ctx, "", 10); err != nil || len(active) != 0 {
		t.Fatalf("paused plan must not be advanced = %#v, %v", active, err)
	}
	stored = paused
	stored.State = processing.Succeeded
	stored.UpdatedAt = now.Add(2 * time.Second)
	if err := repository.SaveProcessingPlan(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveProcessingPlan(ctx, stored); !errors.Is(err, processing.ErrConflict) {
		t.Fatalf("stale save = %v", err)
	}
	active, err = repository.ListActiveProcessingPlans(ctx, "", 10)
	if err != nil || len(active) != 0 {
		t.Fatalf("completed still active = %#v, %v", active, err)
	}
}

func TestActiveProcessingPlansPageByStableID(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	_, err = repository.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id, owner_id, owner_name, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"session", "owner", "owner", "title", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		plan := processing.Plan{ID: fmt.Sprintf("plan-%d", index), OwnerID: "owner", SessionID: "session", SourceTaskID: "task", SourceAssetID: "asset",
			State: processing.Running, RequestHash: "request", Steps: []processing.Step{}, Version: 1, CreatedAt: now, UpdatedAt: now}
		if index == 2 {
			plan.State = processing.Paused
		}
		if err := repository.CreateProcessingPlan(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	seen := []string{}
	after := ""
	for {
		page, err := repository.ListActiveProcessingPlans(ctx, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, plan := range page {
			seen = append(seen, plan.ID)
		}
		if len(page) < 2 {
			break
		}
		after = page[len(page)-1].ID
	}
	if fmt.Sprint(seen) != "[plan-0 plan-1 plan-3 plan-4]" {
		t.Fatalf("paged active plans = %v", seen)
	}
	if _, err := repository.ListActiveProcessingPlans(ctx, "", 0); err == nil {
		t.Fatal("non-positive page size must be rejected")
	}
}

func TestProcessingPlanCASAcrossRepositoryInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans-cas.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := first.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id, owner_id, owner_name, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"session", "owner", "owner", "title", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	plan := processing.Plan{ID: "cas-plan", OwnerID: "owner", SessionID: "session", State: processing.Failed,
		RequestHash: "request", Steps: []processing.Step{{State: processing.Failed}}, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := first.CreateProcessingPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, repository := range []*Repository{first, second} {
		workers.Add(1)
		go func(repository *Repository) {
			defer workers.Done()
			<-start
			candidate := plan
			candidate.State = processing.Pending
			candidate.UpdatedAt = now.Add(time.Second)
			results <- repository.SaveProcessingPlan(ctx, candidate)
		}(repository)
	}
	close(start)
	workers.Wait()
	close(results)
	succeeded, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, processing.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent save error = %v", err)
		}
	}
	stored, err := first.GetProcessingPlan(ctx, "owner", plan.ID)
	if err != nil || succeeded != 1 || conflicts != 1 || stored.Version != 2 || stored.State != processing.Pending {
		t.Fatalf("CAS results: success=%d conflict=%d stored=%#v error=%v", succeeded, conflicts, stored, err)
	}
}

func TestProcessingPlansBySourceAreUncappedAndScoped(t *testing.T) {
	repository, err := Open(filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, session := range []string{"session", "other-session"} {
		_, err = repository.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id, owner_id, owner_name, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			session, "owner", "owner", "title", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			t.Fatal(err)
		}
	}
	create := func(id, session, source, state string, created time.Time) {
		t.Helper()
		plan := processing.Plan{ID: id, OwnerID: "owner", SessionID: session, SourceTaskID: generation.TaskID(source), SourceAssetID: "asset",
			State: state, RequestHash: "request", Steps: []processing.Step{}, Version: 1, CreatedAt: created, UpdatedAt: created}
		if err := repository.CreateProcessingPlan(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	// The oldest plan of the task is paused; 120 newer plans for other tasks
	// push it out of the capped session listing.
	create("old-paused", "session", "task", processing.Paused, now)
	for index := 0; index < 120; index++ {
		create(fmt.Sprintf("newer-%03d", index), "session", fmt.Sprintf("other-%03d", index), processing.Succeeded, now.Add(time.Duration(index+1)*time.Second))
	}
	create("same-task-new", "session", "task", processing.Succeeded, now.Add(time.Hour))
	create("same-task-other-session", "other-session", "task", processing.Running, now.Add(time.Hour))

	capped, err := repository.ListProcessingPlans(ctx, "owner", "session")
	if err != nil || len(capped) != 100 {
		t.Fatalf("session listing = %d plans, %v", len(capped), err)
	}
	for _, plan := range capped {
		if plan.ID == "old-paused" {
			t.Fatal("the capped listing was expected to miss the old plan; the test setup is wrong")
		}
	}
	bySource, err := repository.ListProcessingPlansBySource(ctx, "owner", "session", "task")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(planIDs(bySource)) != "[same-task-new old-paused]" {
		t.Fatalf("by source = %v", planIDs(bySource))
	}
	if foreign, err := repository.ListProcessingPlansBySource(ctx, "another-owner", "session", "task"); err != nil || len(foreign) != 0 {
		t.Fatalf("foreign owner = %v, %v", planIDs(foreign), err)
	}
}

func planIDs(plans []processing.Plan) []string {
	ids := make([]string, 0, len(plans))
	for _, plan := range plans {
		ids = append(ids, plan.ID)
	}
	return ids
}
