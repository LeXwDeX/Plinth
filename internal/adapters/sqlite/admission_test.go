package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestCreateAdmissionSerializesIndependentRepositories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admission.db")
	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	const limit = 3
	const candidates = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	type outcome struct {
		id  generation.TaskID
		err error
	}
	results := make(chan outcome, candidates)
	tasks := make([]generation.Task, candidates)
	events := make([]generation.Event, candidates)
	for i := range candidates {
		tasks[i], events[i] = admissionFixture(fmt.Sprintf("concurrent-%d", i), "owner-a")
		tasks[i] = approveCreateFixture(t, first, tasks[i])
	}
	for i := 0; i < candidates; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			repository := first
			if i%2 == 1 {
				repository = second
			}
			task, event := tasks[i], events[i]
			created, replayed, err := repository.Create(context.Background(), task, event, limit)
			if replayed {
				results <- outcome{err: errors.New("different key unexpectedly replayed")}
				return
			}
			results <- outcome{id: created.ID, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	accepted := make([]generation.TaskID, 0, limit)
	for result := range results {
		switch {
		case result.err == nil:
			accepted = append(accepted, result.id)
		case errors.Is(result.err, generation.RepositoryAdmissionFull):
		default:
			t.Fatalf("unexpected admission result: %v", result.err)
		}
	}
	if len(accepted) != limit {
		t.Fatalf("accepted %d tasks, want %d", len(accepted), limit)
	}

	// All three inflight states consume a slot. A terminal state releases one.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, state := range []generation.TaskState{generation.TaskQueued, generation.TaskRunning, generation.TaskMaterializing} {
		if _, err := db.Exec(`UPDATE generation_tasks SET state = ? WHERE id = ?`, state, accepted[i]); err != nil {
			t.Fatal(err)
		}
	}
	newTask, newEvent := admissionFixture("overflow", "owner-a")
	newTask = approveCreateFixture(t, second, newTask)
	if _, _, err := second.Create(context.Background(), newTask, newEvent, limit); !errors.Is(err, generation.RepositoryAdmissionFull) {
		t.Fatalf("full with all inflight states: %v", err)
	}
	if _, err := db.Exec(`UPDATE generation_tasks SET state = ? WHERE id = ?`, generation.TaskSucceeded, accepted[0]); err != nil {
		t.Fatal(err)
	}
	if _, replayed, err := second.Create(context.Background(), newTask, newEvent, limit); err != nil || replayed {
		t.Fatalf("released slot: replayed=%v err=%v", replayed, err)
	}
	var taskCount, eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE owner_id = 'owner-a'`).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_task_events`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != limit+1 || eventCount != taskCount {
		t.Fatalf("tasks=%d events=%d, want matching admitted writes", taskCount, eventCount)
	}
}

func TestCreateAdmissionConcurrentSameKeyReplaysAtCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.db")
	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	task, event := admissionFixture("same-key", "owner-a")
	task = approveCreateFixture(t, first, task)
	var wg sync.WaitGroup
	start := make(chan struct{})
	type outcome struct {
		id       generation.TaskID
		replayed bool
		err      error
	}
	results := make(chan outcome, 2)
	for i, repository := range []*sqlite.Repository{first, second} {
		wg.Add(1)
		go func(i int, repository *sqlite.Repository) {
			defer wg.Done()
			<-start
			candidate := task
			candidate.ID = generation.TaskID(fmt.Sprintf("candidate-%d", i))
			candidateEvent := event
			candidateEvent.TaskID = candidate.ID
			created, replayed, err := repository.Create(context.Background(), candidate, candidateEvent, 1)
			results <- outcome{id: created.ID, replayed: replayed, err: err}
		}(i, repository)
	}
	close(start)
	wg.Wait()
	close(results)
	createdCount, replayCount := 0, 0
	var acceptedID generation.TaskID
	for result := range results {
		if result.err != nil {
			t.Fatalf("same-key request: %v", result.err)
		}
		if acceptedID != "" && result.id != acceptedID {
			t.Fatalf("same key returned two task IDs: %s, %s", acceptedID, result.id)
		}
		acceptedID = result.id
		if result.replayed {
			replayCount++
		} else {
			createdCount++
		}
	}
	if createdCount != 1 || replayCount != 1 {
		t.Fatalf("created=%d replayed=%d, want 1 each", createdCount, replayCount)
	}
	task.RequestHash = "different-request"
	if _, _, err := second.Create(context.Background(), task, event, 1); !errors.Is(err, generation.RepositoryIdempotencyConflict) {
		t.Fatalf("same key with different input at capacity: %v", err)
	}
	events, err := first.Events(context.Background(), "owner-a", acceptedID, 0, 10)
	if err != nil || len(events) != 1 || events[0].Type != "task.submitted" {
		t.Fatalf("replay created extra events: %+v, %v", events, err)
	}
}

func admissionFixture(key string, owner generation.OwnerID) (generation.Task, generation.Event) {
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	task := generation.Task{
		ID: generation.TaskID("task-" + key), OwnerID: owner,
		Capability: generation.TextToImage,
		Offering:   generation.Offering{ID: "test/offering", Capability: generation.TextToImage},
		Input:      json.RawMessage(`{"prompt":"fixture"}`), RequestHash: "same-request", IdempotencyKey: key,
		State: generation.TaskQueued, Version: 1, CreatedAt: now, UpdatedAt: now, NextActionAt: now,
	}
	event := generation.Event{TaskID: task.ID, Type: "task.submitted", Data: json.RawMessage(`{}`), CreatedAt: now}
	return task, event
}
