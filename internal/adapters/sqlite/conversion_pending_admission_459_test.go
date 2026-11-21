package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

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
