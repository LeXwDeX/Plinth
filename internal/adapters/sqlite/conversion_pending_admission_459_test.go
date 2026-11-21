package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #459: a finished source Task can still owe a format as a post-success
// resource (#457's mixed post_success shape). It is not an active Task, but a
// second paid run for that format would duplicate work already in flight, so
// the guarded admission refuses it inside the same transaction. The rule that
// names the owed formats is the Product Schema's (tested in modeldelivery).

// conversionPendingChecker answers the declared conversions of the active Tasks
// and the post-success formats each stored Task still owes (#459), the way the
// kernel's Schema rule does.
func conversionPendingChecker(
	intents map[generation.TaskID]generation.ConversionIntent,
	pending map[generation.TaskID][]string,
) generation.ConversionConflictChecker {
	return func(task generation.Task) (generation.ConversionGuardReport, error) {
		intent, declared := intents[task.ID]
		return generation.ConversionGuardReport{
			Intent: intent, Declared: declared, PendingFormats: pending[task.ID],
		}, nil
	}
}

// setTaskDelivery writes the state and the post-success resource slots of a
// stored Task, as the kernel's coordinator does when it finalizes a slot.
func setTaskDelivery(t *testing.T, db *sql.DB, id generation.TaskID, state generation.TaskState, resources string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE generation_tasks SET state = ?, resource_state_json = ? WHERE id = ?`,
		state, resources, id); err != nil {
		t.Fatal(err)
	}
}

func TestConversionAdmissionRefusesFormatTheSourceStillOwes459(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversion-pending.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	db := openConversionDB(t, path)
	ctx := context.Background()

	source, sourceEvent := conversionAdmissionTask("source-pending", "owner-a", "session-a", "upstream-a", "glb")
	source = approveCreateFixture(t, repository, source)
	if _, _, err := repository.Create(ctx, source, sourceEvent, 0); err != nil {
		t.Fatal(err)
	}
	pendingSlot := `{"converted_model|converted.fbx|":{"state":"pending"}}`
	setTaskDelivery(t, db, source.ID, generation.TaskSucceeded, pendingSlot)

	conversion, conversionEvent := conversionAdmissionTask("conversion-pending", "owner-a", "session-a", "source-pending", "fbx")
	conversion = approveCreateFixture(t, repository, conversion)
	check := conversionPendingChecker(nil, map[generation.TaskID][]string{
		source.ID: {"fbx"},
	})
	if _, _, err := repository.CreateConversionGuarded(ctx, conversion, conversionEvent, 0, check); !errors.Is(err, generation.RepositoryConversionConflict) {
		t.Fatalf("conversion during a pending post-success format = %v", err)
	}
	// Nothing was stored: the refusal happens before the insert.
	var stored int
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE id = ?`, "conversion-pending").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("the refused conversion stored %d tasks", stored)
	}

	// Once the slot is terminal the same run is admitted.
	readySlot := `{"converted_model|converted.fbx|":{"state":"ready"}}`
	setTaskDelivery(t, db, source.ID, generation.TaskSucceeded, readySlot)
	accepted, _, err := repository.CreateConversionGuarded(ctx, conversion, conversionEvent, 0,
		conversionPendingChecker(nil, nil))
	if err != nil {
		t.Fatalf("conversion after the slot settled: %v", err)
	}
	if accepted.ID != conversion.ID {
		t.Fatalf("accepted %s, want %s", accepted.ID, conversion.ID)
	}
}

func TestConversionAdmissionIgnoresAPendingFormatItDoesNotOverlap459(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversion-pending-other.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	db := openConversionDB(t, path)
	ctx := context.Background()

	// The source still owes FBX, and this run targets OBJ: the formats do not
	// overlap, so the run is admitted.
	source, sourceEvent := conversionAdmissionTask("source-pending", "owner-a", "session-a", "upstream-a", "glb")
	source = approveCreateFixture(t, repository, source)
	if _, _, err := repository.Create(ctx, source, sourceEvent, 0); err != nil {
		t.Fatal(err)
	}
	setTaskDelivery(t, db, source.ID, generation.TaskSucceeded,
		`{"converted_model|converted.fbx|":{"state":"pending"}}`)
	conversion, conversionEvent := conversionAdmissionTask("conversion-other-format", "owner-a", "session-a", "source-pending", "obj")
	conversion = approveCreateFixture(t, repository, conversion)
	check := conversionPendingChecker(nil, map[generation.TaskID][]string{
		source.ID: {"fbx"},
	})
	if _, _, err := repository.CreateConversionGuarded(ctx, conversion, conversionEvent, 0, check); err != nil {
		t.Fatalf("conversion of a format the source does not owe = %v", err)
	}
}

// #459: two owner sessions can submit the same paid conversion of one succeeded
// source at the same time. The active-Task scan of #330 is scoped to the
// caller's session (independent sessions stay independent), so both would be
// admitted and both charged for one delivery. Once the source is a stored
// succeeded Task, the guarded admission also reads the owner's active Tasks
// across sessions: exactly one of the two submitters may be charged.
func TestConversionAdmissionRefusesACrossSessionRaceOnASucceededSource459(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversion-pending-race.db")
	first, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	db := openConversionDB(t, path)
	ctx := context.Background()

	// The source succeeded and its own format slots settled: the pending-format
	// guard has nothing to refuse, so only the cross-session scan can.
	source, sourceEvent := conversionAdmissionTask("source-race", "owner-a", "session-a", "upstream-a", "glb")
	source = approveCreateFixture(t, first, source)
	if _, _, err := first.Create(ctx, source, sourceEvent, 0); err != nil {
		t.Fatal(err)
	}
	setTaskDelivery(t, db, source.ID, generation.TaskSucceeded,
		`{"converted_model|converted.fbx|":{"state":"ready"}}`)

	one, oneEvent := conversionAdmissionTask("race-one", "owner-a", "session-a", "source-race", "fbx")
	two, twoEvent := conversionAdmissionTask("race-two", "owner-a", "session-b", "source-race", "fbx")
	one = approveCreateFixture(t, first, one)
	two = approveCreateFixture(t, first, two)
	check := conversionPendingChecker(
		map[generation.TaskID]generation.ConversionIntent{
			one.ID: *one.Conversion, two.ID: *two.Conversion,
		}, nil,
	)

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for index, repository := range []*sqlite.Repository{first, second} {
		task, event := one, oneEvent
		if index == 1 {
			task, event = two, twoEvent
		}
		wg.Add(1)
		go func(repository *sqlite.Repository, task generation.Task, event generation.Event) {
			defer wg.Done()
			<-start
			_, _, err := repository.CreateConversionGuarded(context.Background(), task, event, 0, check)
			results <- err
		}(repository, task, event)
	}
	close(start)
	wg.Wait()
	close(results)

	accepted, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, generation.RepositoryConversionConflict):
			conflicted++
		default:
			t.Fatalf("unexpected admission result: %v", err)
		}
	}
	if accepted != 1 || conflicted != 1 {
		t.Fatalf("accepted=%d conflicted=%d, want one of each across two sessions", accepted, conflicted)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE id IN ('race-one','race-two')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stored %d conversion tasks, want 1 (no second charged task)", count)
	}
}
