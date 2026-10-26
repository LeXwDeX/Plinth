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

// #330 final-review P1: a paid format conversion must not be admitted twice
// for the same owner, session, exact model source and an overlapping target
// format. The check runs in the Task insert transaction, so direct chat
// conversions and Processing Plan steps (which submit through the same
// kernel) share it.

func conversionAdmissionTask(id, owner, session, source string, formats ...string) (generation.Task, generation.Event) {
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	task := generation.Task{
		ID: generation.TaskID(id), OwnerID: generation.OwnerID(owner), SessionID: session,
		Capability: generation.FormatConversion,
		Offering:   generation.Offering{ID: "meshy/processing/convert", Capability: generation.FormatConversion, Revision: "test"},
		Input:      json.RawMessage(`{"source_task":"` + source + `"}`),
		Conversion: &generation.ConversionIntent{SourceTaskID: generation.TaskID(source), Formats: formats},
		State:      generation.TaskQueued, Version: 1, CreatedAt: now, UpdatedAt: now, NextActionAt: now,
		IdempotencyKey: "conversion:" + id,
	}
	event := generation.Event{TaskID: task.ID, Type: "task.submitted", Data: json.RawMessage(`{}`), CreatedAt: now}
	return task, event
}

func conversionChecker(intents map[generation.TaskID]generation.ConversionIntent) generation.ConversionConflictChecker {
	return func(active generation.Task) (generation.ConversionIntent, bool, error) {
		intent, declared := intents[active.ID]
		return intent, declared, nil
	}
}

func openConversionDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testConversionsOverlap(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if a == b {
				return true
			}
		}
	}
	return false
}

func TestConversionAdmissionSerializesSimultaneousSubmissions330(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversion-admission.db")
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

	one, oneEvent := conversionAdmissionTask("conversion-one", "owner-a", "session-a", "source-a", "glb", "fbx")
	two, twoEvent := conversionAdmissionTask("conversion-two", "owner-a", "session-a", "source-a", "fbx", "obj")
	one = approveCreateFixture(t, first, one)
	two = approveCreateFixture(t, first, two)
	check := conversionChecker(map[generation.TaskID]generation.ConversionIntent{
		one.ID: *one.Conversion, two.ID: *two.Conversion,
	})

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
		t.Fatalf("accepted=%d conflicted=%d, want one of each", accepted, conflicted)
	}
	db := openConversionDB(t, path)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stored %d conversion tasks, want 1 (no second charged task)", count)
	}
}

func TestConversionAdmissionAllowsTerminalRetryAndIndependentScopes330(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversion-scopes.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	intents := map[generation.TaskID]generation.ConversionIntent{}
	check := conversionChecker(intents)

	firstTask, firstEvent := conversionAdmissionTask("retry-first", "owner-a", "session-a", "source-a", "glb")
	firstTask = approveCreateFixture(t, repository, firstTask)
	intents[firstTask.ID] = *firstTask.Conversion
	if _, _, err := repository.CreateConversionGuarded(context.Background(), firstTask, firstEvent, 0, check); err != nil {
		t.Fatal(err)
	}
	// A terminal state releases the source and formats for a new paid attempt.
	db := openConversionDB(t, path)
	if _, err := db.Exec(`UPDATE generation_tasks SET state = ? WHERE id = ?`, generation.TaskFailed, firstTask.ID); err != nil {
		t.Fatal(err)
	}
	retryTask, retryEvent := conversionAdmissionTask("retry-second", "owner-a", "session-a", "source-a", "glb")
	retryTask = approveCreateFixture(t, repository, retryTask)
	intents[retryTask.ID] = *retryTask.Conversion
	if _, _, err := repository.CreateConversionGuarded(context.Background(), retryTask, retryEvent, 0, check); err != nil {
		t.Fatalf("terminal retry was refused: %v", err)
	}

	// Another source, owner or session never blocks this conversion.
	for _, candidate := range []struct {
		id, owner, session, source string
	}{
		{"other-source", "owner-a", "session-a", "source-b"},
		{"other-owner", "owner-b", "session-owner-b", "source-a"},
		{"other-session", "owner-a", "session-b", "source-a"},
	} {
		task, event := conversionAdmissionTask(candidate.id, candidate.owner, candidate.session, candidate.source, "glb")
		task = approveCreateFixture(t, repository, task)
		intents[task.ID] = *task.Conversion
		if _, _, err := repository.CreateConversionGuarded(context.Background(), task, event, 0, check); err != nil {
			t.Fatalf("%s was refused: %v", candidate.id, err)
		}
	}
}

func TestConversionAdmissionRequiresAnOverlappingTargetFormat330(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "conversion-formats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	intents := map[generation.TaskID]generation.ConversionIntent{}
	check := conversionChecker(intents)

	whole, wholeEvent := conversionAdmissionTask("whole-run", "owner-a", "session-a", "source-a", "glb", "fbx", "obj")
	whole = approveCreateFixture(t, repository, whole)
	intents[whole.ID] = *whole.Conversion
	if _, _, err := repository.CreateConversionGuarded(context.Background(), whole, wholeEvent, 0, check); err != nil {
		t.Fatal(err)
	}
	// A per-format run of a format the active run does not provide can start.
	otherFormat, otherEvent := conversionAdmissionTask("other-format", "owner-a", "session-a", "source-a", "stl")
	otherFormat = approveCreateFixture(t, repository, otherFormat)
	intents[otherFormat.ID] = *otherFormat.Conversion
	if _, _, err := repository.CreateConversionGuarded(context.Background(), otherFormat, otherEvent, 0, check); err != nil {
		t.Fatalf("non-overlapping format was refused: %v", err)
	}
	// The same format is the same paid delivery.
	overlap, overlapEvent := conversionAdmissionTask("overlap-format", "owner-a", "session-a", "source-a", "obj")
	overlap = approveCreateFixture(t, repository, overlap)
	intents[overlap.ID] = *overlap.Conversion
	if _, _, err := repository.CreateConversionGuarded(context.Background(), overlap, overlapEvent, 0, check); !errors.Is(err, generation.RepositoryConversionConflict) {
		t.Fatalf("overlapping format was admitted: %v", err)
	}
	// The check is format-aware, not a blanket ban on the source.
	if !testConversionsOverlap([]string{"glb"}, []string{"glb"}) || testConversionsOverlap([]string{"glb"}, []string{"stl"}) {
		t.Fatal("test fixture overlap rule is wrong")
	}
}

func TestConversionAdmissionTreatsTaskAndAssetSourcesAsOneModel330(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversion-source.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	intents := map[generation.TaskID]generation.ConversionIntent{}
	check := conversionChecker(intents)

	source, sourceEvent := conversionAdmissionTask("source-task", "owner-a", "session-a", "unused")
	source.Conversion = nil
	source.Capability = generation.TextTo3D
	source.Offering = generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", Capability: generation.TextTo3D, Revision: "test"}
	source.Input = json.RawMessage(`{"prompt":"source"}`)
	source = approveCreateFixture(t, repository, source)
	if _, _, err := repository.Create(context.Background(), source, sourceEvent, 0); err != nil {
		t.Fatal(err)
	}
	db := openConversionDB(t, path)
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key, started_at, updated_at)
		VALUES (?, ?, 1, '{}', 'UPSTREAM_SUCCEEDED', ?, ?, ?)`, "source-attempt", source.ID, "submission", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO generation_assets (id, task_id, attempt_id, role, required, ordinal, storage_key, media_type, size_bytes, sha256, created_at)
		VALUES (?, ?, ?, 'model', 1, 0, 'source/glb', 'model/gltf-binary', 1, ?, ?)`,
		"source-asset", source.ID, "source-attempt", fmt.Sprintf("%064d", 0), now); err != nil {
		t.Fatal(err)
	}

	taskSource, taskEvent := conversionAdmissionTask("task-source", "owner-a", "session-a", string(source.ID), "glb")
	taskSource = approveCreateFixture(t, repository, taskSource)
	intents[taskSource.ID] = *taskSource.Conversion
	if _, _, err := repository.CreateConversionGuarded(context.Background(), taskSource, taskEvent, 0, check); err != nil {
		t.Fatal(err)
	}

	asset, assetEvent := conversionAdmissionTask("asset-source", "owner-a", "session-a", "unused", "glb")
	asset.Input = json.RawMessage(`{"source_asset":"source-asset"}`)
	asset.Conversion = &generation.ConversionIntent{SourceAssetID: "source-asset", Formats: []string{"glb"}}
	asset = approveCreateFixture(t, repository, asset)
	intents[asset.ID] = *asset.Conversion
	if _, _, err := repository.CreateConversionGuarded(context.Background(), asset, assetEvent, 0, check); !errors.Is(err, generation.RepositoryConversionConflict) {
		t.Fatalf("an asset of the active source was admitted: %v", err)
	}
}

func TestConversionAdmissionFailsClosedOnUnreadableSource330(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversion-fail-closed.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	db := openConversionDB(t, path)

	task, event := conversionAdmissionTask("unreadable", "owner-a", "session-a", "source-a", "glb")
	task.Conversion = &generation.ConversionIntent{SourceAssetID: "missing-asset", Formats: []string{"glb"}}
	task.Input = json.RawMessage(`{"source_asset":"missing-asset"}`)
	task = approveCreateFixture(t, repository, task)
	_, _, err = repository.CreateConversionGuarded(context.Background(), task, event, 0, conversionChecker(nil))
	if err == nil || errors.Is(err, generation.RepositoryConversionConflict) {
		t.Fatalf("unresolvable source was not a fatal read failure: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a failed guard stored %d tasks", count)
	}

	// A checker error is equally fatal: no Task is inserted. An active Task
	// must exist for the guard to consult the checker.
	active, activeEvent := conversionAdmissionTask("active", "owner-a", "session-a", "source-a", "glb")
	active = approveCreateFixture(t, repository, active)
	if _, _, err := repository.CreateConversionGuarded(context.Background(), active, activeEvent, 0, conversionChecker(nil)); err != nil {
		t.Fatal(err)
	}
	task, event = conversionAdmissionTask("checker-error", "owner-a", "session-a", "source-a", "glb")
	task = approveCreateFixture(t, repository, task)
	checkerErr := errors.New("active conversions unreadable")
	if _, _, err := repository.CreateConversionGuarded(context.Background(), task, event, 0,
		func(generation.Task) (generation.ConversionIntent, bool, error) {
			return generation.ConversionIntent{}, false, checkerErr
		}); !errors.Is(err, checkerErr) {
		t.Fatalf("checker failure = %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("checker failure stored %d tasks, want only the existing active Task", count)
	}
}

func TestTaskListFiltersByDeclaredConversionOfferings330(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversion-list.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	conversion, conversionEvent := conversionAdmissionTask("listed-conversion", "owner-a", "session-a", "source-a", "glb")
	conversion = approveCreateFixture(t, repository, conversion)
	if _, _, err := repository.CreateConversionGuarded(context.Background(), conversion, conversionEvent, 0, conversionChecker(nil)); err != nil {
		t.Fatal(err)
	}
	other, otherEvent := conversionAdmissionTask("listed-retexture", "owner-a", "session-a", "source-a")
	other.Conversion = nil
	other.Capability = generation.Retexture
	other.Offering = generation.Offering{ID: "meshy/processing/retexture", Capability: generation.Retexture, Revision: "test"}
	other.Input = json.RawMessage(`{"source_task":"source-a"}`)
	other = approveCreateFixture(t, repository, other)
	if _, _, err := repository.Create(context.Background(), other, otherEvent, 0); err != nil {
		t.Fatal(err)
	}

	page, err := repository.List(context.Background(), generation.OwnTasks("owner-a"), generation.ListQuery{
		SessionID: "session-a", OfferingIDs: []generation.OfferingID{"meshy/processing/convert"}, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Tasks) != 1 || page.Tasks[0].ID != conversion.ID {
		t.Fatalf("conversion filter = total %d tasks %#v", page.Total, page.Tasks)
	}
}

func TestConversionAdmissionUsesUploadedInputAssetIdentity330(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "conversion-uploaded.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	intents := map[generation.TaskID]generation.ConversionIntent{}
	check := conversionChecker(intents)
	ctx := context.Background()
	for _, id := range []generation.AssetID{"uploaded-a", "uploaded-b"} {
		if err := repository.CreateInputAsset(ctx, generation.InputAsset{
			ID: id, OwnerID: "owner-a", StorageKey: "input/" + string(id),
			MediaType: "model/gltf-binary", Size: 10, SHA256: fmt.Sprintf("%064d", len(id)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, firstEvent := conversionAdmissionTask("uploaded-first", "owner-a", "session-a", "unused", "gltf")
	first.Conversion = &generation.ConversionIntent{SourceAssetID: "uploaded-a", Formats: []string{"gltf"}}
	first.Input = json.RawMessage(`{"source_asset":"uploaded-a"}`)
	first = approveCreateFixture(t, repository, first)
	intents[first.ID] = *first.Conversion
	if _, _, err := repository.CreateConversionGuarded(ctx, first, firstEvent, 0, check); err != nil {
		t.Fatal(err)
	}

	duplicate, duplicateEvent := conversionAdmissionTask("uploaded-duplicate", "owner-a", "session-a", "unused", "gltf")
	duplicate.Conversion = &generation.ConversionIntent{SourceAssetID: "uploaded-a", Formats: []string{"gltf"}}
	duplicate.Input = json.RawMessage(`{"source_asset":"uploaded-a"}`)
	duplicate = approveCreateFixture(t, repository, duplicate)
	intents[duplicate.ID] = *duplicate.Conversion
	if _, _, err := repository.CreateConversionGuarded(ctx, duplicate, duplicateEvent, 0, check); !errors.Is(err, generation.RepositoryConversionConflict) {
		t.Fatalf("same uploaded input = %v", err)
	}

	other, otherEvent := conversionAdmissionTask("uploaded-other", "owner-a", "session-a", "unused", "gltf")
	other.Conversion = &generation.ConversionIntent{SourceAssetID: "uploaded-b", Formats: []string{"gltf"}}
	other.Input = json.RawMessage(`{"source_asset":"uploaded-b"}`)
	other = approveCreateFixture(t, repository, other)
	intents[other.ID] = *other.Conversion
	if _, _, err := repository.CreateConversionGuarded(ctx, other, otherEvent, 0, check); err != nil {
		t.Fatalf("another uploaded input = %v", err)
	}

	foreign, foreignEvent := conversionAdmissionTask("uploaded-foreign", "owner-b", "session-owner-b", "unused", "gltf")
	foreign.Conversion = &generation.ConversionIntent{SourceAssetID: "uploaded-a", Formats: []string{"gltf"}}
	foreign.Input = json.RawMessage(`{"source_asset":"uploaded-a"}`)
	foreign = approveCreateFixture(t, repository, foreign)
	if _, _, err := repository.CreateConversionGuarded(ctx, foreign, foreignEvent, 0, check); err == nil || errors.Is(err, generation.RepositoryConversionConflict) {
		t.Fatalf("foreign uploaded input = %v", err)
	}
}
