package modeldelivery

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #330 third follow-ups: conversions the Agent starts directly are public
// Tasks without a Processing Plan. The formats tab must see their delivered
// files, must not offer the same conversion a second time while one runs, and
// the public media type and archive availability must match the download and
// ZIP routes.

func (f *fixture) List(_ context.Context, scope generation.TaskListScope, query generation.ListQuery) (generation.TaskPage, error) {
	f.lastListQuery = query
	if f.listErr != nil {
		return generation.TaskPage{}, f.listErr
	}
	var tasks []generation.Task
	for _, task := range f.tasks {
		if !scope.AllOwners() && task.OwnerID != scope.OwnerID() {
			continue
		}
		if query.SessionID != "" && task.SessionID != query.SessionID {
			continue
		}
		if len(query.OfferingIDs) > 0 {
			matched := false
			for _, id := range query.OfferingIDs {
				matched = matched || task.Offering.ID == id
			}
			if !matched {
				continue
			}
		}
		if len(query.States) > 0 {
			allowed := false
			for _, state := range query.States {
				allowed = allowed || task.State == state
			}
			if !allowed {
				continue
			}
		}
		excluded := false
		for _, state := range query.ExcludedStates {
			excluded = excluded || task.State == state
		}
		if excluded {
			continue
		}
		tasks = append(tasks, task)
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
		}
		return tasks[i].ID > tasks[j].ID
	})
	offset := 0
	if query.Cursor != "" {
		parsed, err := strconv.Atoi(query.Cursor)
		if err != nil {
			return generation.TaskPage{}, err
		}
		offset = parsed
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if offset > len(tasks) {
		offset = len(tasks)
	}
	end := offset + limit
	if end > len(tasks) {
		end = len(tasks)
	}
	page := generation.TaskPage{Tasks: tasks[offset:end], Total: len(tasks)}
	if end < len(tasks) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

// addConversionTask stores a Task like the Agent's direct conversion: a real
// conversion Offering, its frozen source input and the given outputs.
func (f *fixture) addConversionTask(id, owner, session string, offeringID generation.OfferingID, input string, state generation.TaskState, createdAt time.Time, specs ...assetSpec) generation.Task {
	offering, exists := f.catalog.Get(offeringID)
	if !exists {
		panic("conversion offering not in catalog: " + string(offeringID))
	}
	task := generation.Task{
		ID: generation.TaskID(id), OwnerID: generation.OwnerID(owner), SessionID: session,
		Capability: offering.Capability, Offering: offering, Input: json.RawMessage(input),
		State: state, CreatedAt: createdAt,
	}
	for index, spec := range specs {
		assetID := generation.AssetID(id + "-a" + strconv.Itoa(index))
		body := spec.body
		if body == "" {
			body = spec.role + ":" + spec.variant
		}
		f.content[assetID] = []byte(body)
		mediaType := spec.mediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		task.Assets = append(task.Assets, generation.Asset{
			ID: assetID, TaskID: task.ID, Role: spec.role, Variant: spec.variant, Ordinal: index,
			StorageKey: "key-" + string(assetID), MediaType: mediaType, Size: int64(len(body)),
			CreatedAt: createdAt,
		})
	}
	f.tasks[task.ID] = task
	return task
}

func conversionAt(second int) time.Time {
	return time.Date(2026, 10, 8, 0, 0, second, 0, time.UTC)
}

func TestDeliveredAgentConversionFillsTheFormatRows330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	f.addConversionTask("agent-convert", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskSucceeded, conversionAt(1),
		assetSpec{role: "model", variant: "fbx", mediaType: "model/fbx"},
		assetSpec{role: "model", variant: "obj", mediaType: "application/zip"})

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fbx", "obj"} {
		row := formatByID(t, coverage, id)
		if row.Status != StatusAvailable || row.TaskID != "agent-convert" || row.AssetID == "" {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	if gltf := formatByID(t, coverage, "gltf"); gltf.Status != StatusObtainable {
		t.Fatalf("gltf = %#v", gltf)
	}
	if !coverage.ArchiveAvailable {
		t.Fatalf("the delivered conversion must be packable: %#v", coverage)
	}

	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	readme := archive.readme
	if !strings.Contains(string(readme), "FBX") || !strings.Contains(string(readme), "OBJ") {
		t.Fatalf("README omits the delivered conversion:\n%s", readme)
	}
}

func TestSourceFileWinsOverTheSameFormatFromAConversion330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.addTask("meshy-task", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
		assetSpec{role: "model", variant: "fbx", mediaType: "model/fbx"})
	f.addConversionTask("agent-convert", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskSucceeded, conversionAt(1),
		assetSpec{role: "model", variant: "fbx", mediaType: "model/fbx"})

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row := formatByID(t, coverage, "fbx"); row.Status != StatusAvailable || row.TaskID != source.ID {
		t.Fatalf("the source file must stay the version: %#v", row)
	}
}

func TestDeliveredAgentConversionRequiresTheExactSource330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	other := f.meshyGlbOnly("other-task", "owner")

	f.addConversionTask("other-source", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"other-task"}`, generation.TaskSucceeded, conversionAt(1),
		assetSpec{role: "model", variant: "fbx", mediaType: "model/fbx"})
	f.addConversionTask("other-session", "owner", "another-session", "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskSucceeded, conversionAt(2),
		assetSpec{role: "model", variant: "obj", mediaType: "model/obj"})
	f.addConversionTask("other-owner", "other-owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskSucceeded, conversionAt(3),
		assetSpec{role: "model", variant: "stl", mediaType: "model/stl"})
	f.addConversionTask("asset-of-source", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_asset":"meshy-task-a0"}`, generation.TaskSucceeded, conversionAt(4),
		assetSpec{role: "model", variant: "usdz", mediaType: "model/vnd.usdz+zip"})
	f.addConversionTask("asset-of-other", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_asset":"`+string(other.Assets[0].ID)+`"}`, generation.TaskSucceeded, conversionAt(5),
		assetSpec{role: "model", variant: "3mf", mediaType: "model/3mf"})

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Only the asset of the source Task is the same source.
	if row := formatByID(t, coverage, "usdz"); row.Status != StatusAvailable || row.TaskID != "asset-of-source" {
		t.Fatalf("usdz = %#v", row)
	}
	for id, want := range map[string]generation.TaskID{
		"fbx": "other-source", "obj": "other-session", "stl": "other-owner", "3mf": "asset-of-other",
	} {
		if row := formatByID(t, coverage, id); row.Status == StatusAvailable && row.TaskID == want {
			t.Fatalf("%s was taken from a different source: %#v", id, row)
		}
	}
}

func TestDeliveredAgentConversionIgnoresOtherWorkAndUnreadyResults330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	f.addConversionTask("retexture", "owner", source.SessionID, "meshy/processing/retexture",
		`{"source_task":"meshy-task"}`, generation.TaskSucceeded, conversionAt(1),
		assetSpec{role: "converted_model", variant: "fbx", mediaType: "model/fbx"})
	f.addConversionTask("remesh", "owner", source.SessionID, "meshy/processing/remesh",
		`{"source_task":"meshy-task"}`, generation.TaskSucceeded, conversionAt(2),
		assetSpec{role: "converted_model", variant: "obj", mediaType: "model/obj"})
	f.addConversionTask("retired-conversion", "owner", source.SessionID, "meshy/processing/remesh",
		`{"source_task":"meshy-task","convert_format_only":true}`, generation.TaskSucceeded, conversionAt(3),
		assetSpec{role: "converted_model", variant: "usdz", mediaType: "model/vnd.usdz+zip"})
	f.addConversionTask("failed-conversion", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskFailed, conversionAt(4),
		assetSpec{role: "model", variant: "stl", mediaType: "model/stl"})

	unready := f.addConversionTask("unready-conversion", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskSucceeded, conversionAt(5),
		assetSpec{role: "model", variant: "3mf", mediaType: "model/3mf"})
	unready.Assets[0].StorageKey = ""
	f.tasks[unready.ID] = unready

	// A chain A -> B -> C only links C to B, never to A.
	chain := f.addConversionTask("chain-middle", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskFailed, conversionAt(6))
	f.addConversionTask("chain-end", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"`+string(chain.ID)+`"}`, generation.TaskSucceeded, conversionAt(7),
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"})

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fbx", "obj", "stl", "3mf"} {
		if row := formatByID(t, coverage, id); row.Status == StatusAvailable {
			t.Fatalf("non-conversion or unready %s was delivered: %#v", id, row)
		}
	}
	// The retired Remesh conversion is a conversion by its fixed input and
	// its delivered file still counts.
	if row := formatByID(t, coverage, "usdz"); row.Status != StatusAvailable || row.TaskID != "retired-conversion" {
		t.Fatalf("retired conversion = %#v", row)
	}
	if row := formatByID(t, coverage, "glb"); row.TaskID != source.ID {
		t.Fatalf("chain result must not replace the source GLB: %#v", row)
	}
}

func TestRunningAgentConversionPreventsASecondRun330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	f.addConversionTask("running-conversion", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskRunning, conversionAt(1))

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fbx", "obj", "stl", "usdz", "3mf"} {
		row := formatByID(t, coverage, id)
		if row.Status != StatusConverting || row.Reason != reasonConverting {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	if _, err := f.service.Complete(context.Background(), "owner", source.ID, "fbx"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete during a direct conversion = %v", err)
	}
	for id, plan := range f.store.plans {
		if plan.SourceTaskID == source.ID {
			t.Fatalf("a second conversion was planned: %s %#v", id, plan)
		}
	}
}

func TestFormatRowsPublishTheDownloadMediaType330(t *testing.T) {
	f := newConversionFixture(t)
	task := f.addTask("mixed-media", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "glb", mediaType: "application/octet-stream"},
		assetSpec{role: "model", variant: "obj", mediaType: "application/x-tgif"},
		assetSpec{role: "model", variant: "usdz", mediaType: "application/zip"},
		assetSpec{role: "model", variant: "gltf", mediaType: "application/zip"},
		assetSpec{role: "model", variant: "fbx", mediaType: "model/fbx"})

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"glb":  "model/gltf-binary",
		"obj":  "model/obj",
		"usdz": "model/vnd.usdz+zip",
		"gltf": "application/zip",
		"fbx":  "application/octet-stream",
	}
	for id, mediaType := range want {
		if row := formatByID(t, coverage, id); row.MediaType != mediaType {
			t.Fatalf("%s media_type = %q, want %q", id, row.MediaType, mediaType)
		}
	}
	// The stored bytes and their old labels are untouched.
	if got := task.Assets[1].MediaType; got != "application/x-tgif" {
		t.Fatalf("stored OBJ media type changed to %q", got)
	}
}

func TestArchiveAvailabilityUsesTheArchiveBounds330(t *testing.T) {
	f := newConversionFixture(t)
	task := f.meshyText("meshy-task", "owner")
	var total int64
	for _, asset := range task.Assets {
		if asset.Role == "model" {
			total += asset.Size
		}
	}
	if total == 0 {
		t.Fatal("fixture has no model bytes")
	}

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !coverage.ArchiveAvailable {
		t.Fatalf("archive should fit: %#v", coverage)
	}
	f.service.schema.Archive.MaxBytes = total - 1
	coverage, err = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.ArchiveAvailable {
		t.Fatalf("archive over max_bytes reported available: %#v", coverage)
	}
	f.service.schema.Archive.MaxBytes = total
	f.service.schema.Archive.MaxEntries = 6 // six files, but the README makes seven entries
	coverage, err = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.ArchiveAvailable {
		t.Fatalf("archive over max_entries reported available: %#v", coverage)
	}
}

func TestArchiveAvailabilityListsTheSameFilesAsPrepare330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	f.addConversionTask("agent-convert", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskSucceeded, conversionAt(1),
		assetSpec{role: "model", variant: "fbx", mediaType: "model/fbx"},
		assetSpec{role: "model", variant: "obj", mediaType: "model/obj"})

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := f.service.resolve(context.Background(), source)
	want := map[generation.AssetID]bool{}
	for _, asset := range f.service.archiveAssets(view) {
		want[asset.ID] = true
	}
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.files) != len(want) {
		t.Fatalf("Prepare packs %d files, coverage counted %d", len(archive.files), len(want))
	}
	for _, file := range archive.files {
		if !want[file.asset.ID] {
			t.Fatalf("Prepare packed an asset coverage did not count: %s", file.asset.ID)
		}
	}
	if !coverage.ArchiveAvailable {
		t.Fatalf("coverage says the archive is unavailable: %#v", coverage)
	}
}

func TestArchiveAvailabilityCountsObjDependenciesAndOtherFiles330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.addTask("obj-task", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "obj", mediaType: "model/obj"},
		assetSpec{role: "model", variant: "mtl", mediaType: "text/plain"},
		assetSpec{role: "texture", variant: "texture.0", mediaType: "image/png"},
		assetSpec{role: "converted_model", variant: "mystery", mediaType: "application/octet-stream"})

	view := f.service.resolve(context.Background(), source)
	assets := f.service.archiveAssets(view)
	ids := map[generation.AssetID]bool{}
	var total int64
	for _, asset := range assets {
		ids[asset.ID] = true
		total += asset.Size
	}
	for _, id := range []generation.AssetID{"obj-task-a0", "obj-task-a1", "obj-task-a2", "obj-task-a3"} {
		if !ids[id] {
			t.Fatalf("archive assets omit %s: %v", id, assets)
		}
	}
	f.service.schema.Archive.MaxBytes = total - 1
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.ArchiveAvailable {
		t.Fatalf("dependencies were not counted towards max_bytes: %#v", coverage)
	}
}

func TestArchiveAvailabilityCountsDeliveredAgentConversions330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	var own int64
	for _, asset := range source.Assets {
		if asset.Role == "model" {
			own += asset.Size
		}
	}
	f.addConversionTask("agent-convert", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskSucceeded, conversionAt(1),
		assetSpec{role: "model", variant: "fbx", mediaType: "model/fbx", body: strings.Repeat("f", 40)})
	f.service.schema.Archive.MaxBytes = own + 1
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.ArchiveAvailable {
		t.Fatalf("converted file not counted towards max_bytes: %#v", coverage)
	}
}

func TestRetiredPlanRowsStillUseTheDownloadMediaType330(t *testing.T) {
	f := newConversionFixture(t)
	task := f.addTask("old-media", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"},
		assetSpec{role: "converted_model", variant: "converted.obj", mediaType: "application/x-tgif"})
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row := formatByID(t, coverage, "obj"); row.MediaType != "model/obj" {
		t.Fatalf("obj media_type = %q", row.MediaType)
	}
}

func TestArchiveAvailabilityOfUnfinishedAndEmptyTasks330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	running := source
	running.State = generation.TaskRunning
	f.tasks[running.ID] = running
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.ArchiveAvailable {
		t.Fatalf("unfinished task offered an archive: %#v", coverage)
	}
}

func TestCompletedStepHidesAStaleFailedPlan330(t *testing.T) {
	f := newConversionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task", "gltf"); err != nil {
		t.Fatal(err)
	}
	for id, plan := range f.store.plans {
		plan.State = processing.Failed
		plan.Failure = processing.ReasonSourceIncompatible
		plan.Steps[0].State = processing.Failed
		plan.Steps[0].Failure = processing.ReasonSourceIncompatible
		// The step Task is now a successful delivery; the older Plan row was
		// never written back. The Formats tab must read the delivered fact.
		plan.Steps[0].TaskID = "meshy-task"
		f.store.plans[id] = plan
	}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range coverage.Steps {
		if step.State == processing.Failed {
			t.Fatalf("stale failed plan is still shown: %#v", step)
		}
	}
	if gltf := formatByID(t, coverage, "gltf"); gltf.Status == StatusAvailable {
		// The source task has no glTF package in this fixture; the point is
		// only that a stale failure is not shown again.
		t.Fatalf("unexpected fixture state: %#v", gltf)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task", "gltf"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("a stale failed plan offered another paid run: %v", err)
	}
}

func TestInFlightDirectConversionUsesItsFrozenQuote330(t *testing.T) {
	f := newConversionFixture(t)
	source := f.meshyGlbOnly("meshy-task", "owner")
	offering, _ := f.catalog.Get("meshy/processing/convert")
	quote := &schema.NativeQuote{}
	task := f.addConversionTask("agent-convert", "owner", source.SessionID, "meshy/processing/convert",
		`{"source_task":"meshy-task"}`, generation.TaskRunning, conversionAt(1))
	task.Offering.EstimatedCredits = 99
	task.Offering.CreditsMetered = true
	task.NativeQuote = quote
	f.tasks[task.ID] = task
	// A later Catalog no longer serves this Offering at all. The running Task
	// still projects the price frozen with its admission.
	f.absent[generation.OfferingID(offering.ID)] = true

	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fbx", "obj", "usdz", "stl", "3mf"} {
		row := formatByID(t, coverage, id)
		if row.Status != StatusConverting || row.Conversion == nil {
			t.Fatalf("%s = %#v", id, row)
		}
		if row.Conversion.EstimatedCredits != 99 || row.Conversion.NativeQuote != quote || !row.Conversion.CreditsMetered {
			t.Fatalf("%s frozen quote = %#v", id, row.Conversion)
		}
	}
}
