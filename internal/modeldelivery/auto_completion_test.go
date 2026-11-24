package modeldelivery

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// #461: the platform keeps its own delivery promise. After a generation
// succeeded, the formats it still owes are completed without a person or an
// Agent asking: the free local route first, then the paid Meshy Convert, all
// driven by application.model_delivery.auto_completion and recorded durably so
// a restart neither loses the work nor pays twice.

// memoryAutoCompletions is the durable record of the tests: the same rules as
// the SQLite repository (one row per Task, one-row watermark, first scan writes
// "now"), with none of its SQL.
type memoryAutoCompletions struct {
	mu      sync.Mutex
	records map[generation.TaskID]AutoCompletionRecord
	cached  map[generation.TaskID]AutoCompletionCandidate
	cursor  AutoCompletionCursor
	known   bool
}

func newMemoryAutoCompletions(candidates ...AutoCompletionCandidate) *memoryAutoCompletions {
	store := &memoryAutoCompletions{
		records: map[generation.TaskID]AutoCompletionRecord{},
		cached:  map[generation.TaskID]AutoCompletionCandidate{},
	}
	for _, candidate := range candidates {
		store.cached[candidate.TaskID] = candidate
	}
	return store
}

// prime records that a scan has already run, so a test can register candidates
// and drive one round without the first-scan behaviour in the way.
func (s *memoryAutoCompletions) prime(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursor, s.known = AutoCompletionCursor{UpdatedAt: now}, true
}

func (s *memoryAutoCompletions) AutoCompletionWatermark(context.Context) (AutoCompletionCursor, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor, s.known, nil
}

func (s *memoryAutoCompletions) SetAutoCompletionWatermark(_ context.Context, cursor AutoCompletionCursor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursor, s.known = cursor, true
	return nil
}

func (s *memoryAutoCompletions) AutoCompletionCandidates(
	_ context.Context,
	_ []generation.Capability,
	limit int,
) ([]AutoCompletionCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known {
		// First enable: the position is written now, so everything stored before
		// it is history and is never returned.
		s.cursor, s.known = AutoCompletionCursor{UpdatedAt: time.Now().UTC()}, true
		s.cached = map[generation.TaskID]AutoCompletionCandidate{}
		return nil, nil
	}
	if limit <= 0 || len(s.cached) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(s.cached))
	for id := range s.cached {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	var result []AutoCompletionCandidate
	for _, id := range ids {
		if len(result) >= limit {
			break
		}
		candidate := s.cached[generation.TaskID(id)]
		result = append(result, candidate)
		delete(s.cached, candidate.TaskID)
	}
	return result, nil
}

func (s *memoryAutoCompletions) AutoCompletionRecord(_ context.Context, taskID generation.TaskID) (AutoCompletionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.records[taskID]
	return record, exists, nil
}

func (s *memoryAutoCompletions) SaveAutoCompletionRecord(_ context.Context, record AutoCompletionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.TaskID] = record
	return nil
}

func (s *memoryAutoCompletions) AutoCompletionDue(_ context.Context, now time.Time, limit int) ([]AutoCompletionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []AutoCompletionRecord
	for _, record := range s.records {
		if record.State == AutoStateDone || record.NextRunAt.After(now) {
			continue
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TaskID < result[j].TaskID })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// autoFixture is the conversion fixture with the automatic completion's durable
// record wired in, so the reconciler can be driven like the App drives it.
type autoFixture struct {
	*fixture
	auto  *memoryAutoCompletions
	clock time.Time
}

func newAutoFixture(t *testing.T) *autoFixture {
	t.Helper()
	f := newConversionFixture(t)
	store := newMemoryAutoCompletions()
	clock := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	store.prime(clock.Add(-time.Minute))
	found := &autoFixture{fixture: f, auto: store, clock: clock}
	f.service.WithAutoCompletions(store).WithClock(func() time.Time { return found.clock })
	return found
}

// candidate registers a stored Task with the scan.
func (f *autoFixture) candidate(task generation.Task) {
	f.auto.cached[task.ID] = AutoCompletionCandidate{
		TaskID: task.ID, OwnerID: task.OwnerID, SessionID: task.SessionID,
	}
}

// record registers an existing durable record.
func (f *autoFixture) record(record AutoCompletionRecord) {
	f.auto.records[record.TaskID] = record
}

// pass lets the record's next round come due.
func (f *autoFixture) pass() {
	f.clock = f.clock.Add(time.Duration(f.service.schema.AutoCompletion.ScanIntervalMilliseconds)*time.Millisecond + time.Second)
}

func (f *autoFixture) reconcile(t *testing.T) int {
	t.Helper()
	acted, err := f.service.ReconcileAutoCompletions(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return acted
}

func (f *autoFixture) plansOf(t *testing.T, taskID generation.TaskID) []processing.Plan {
	t.Helper()
	f.auto.mu.Lock()
	defer f.auto.mu.Unlock()
	var result []processing.Plan
	for _, plan := range f.store.plans {
		if plan.SourceTaskID == taskID {
			result = append(result, plan)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// recordOf reads the durable record, failing the test when none was written.
func (f *autoFixture) recordOf(t *testing.T, taskID generation.TaskID) AutoCompletionRecord {
	t.Helper()
	record, exists, err := f.auto.AutoCompletionRecord(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("no automatic completion record for %s", taskID)
	}
	return record
}

// TestAutoCompletionFillsTheFreeRouteForEveryProvider is the acceptance of the
// free route: Meshy, P2 and Rodin results get their glTF without anyone asking,
// in every approval mode, with no card, no step and no budget. The routes are
// declared in the Schema, so the provider of the generation is not a branch
// here: only the Task's capability and its saved files decide.
func TestAutoCompletionFillsTheFreeRouteForEveryProvider(t *testing.T) {
	for _, provider := range []string{"meshy", "tripo-p2", "rodin"} {
		t.Run(provider, func(t *testing.T) {
			f := newAutoFixture(t)
			// The paid route is unavailable: only the free local one may run.
			f.unavailable["meshy/processing/convert"] = true
			task := f.addTask(provider+"-task", "owner", provider, generation.TextTo3D,
				assetSpec{role: modelRole, variant: "glb", mediaType: "model/gltf-binary"})
			f.candidate(task)

			if acted := f.reconcile(t); acted != 1 {
				t.Fatalf("acted on %d tasks, want the one candidate", acted)
			}
			plans := f.plansOf(t, task.ID)
			if len(plans) != 1 {
				t.Fatalf("plans = %#v, want the one free run", plans)
			}
			plan := plans[0]
			// A delivery plan: not a browser's, so it produces no card, no 3D
			// step and no session budget use (#457).
			if plan.Origin != processing.OriginDelivery {
				t.Fatalf("plan origin = %q, want %q", plan.Origin, processing.OriginDelivery)
			}
			if len(plan.Steps) != 1 || plan.Steps[0].OfferingID != generatedGltf {
				t.Fatalf("steps = %#v, want the local glTF converter", plan.Steps)
			}
			if plan.Steps[0].EstimatedCredits != 0 {
				t.Fatalf("the free route is estimated at %d credits", plan.Steps[0].EstimatedCredits)
			}
			record := f.recordOf(t, task.ID)
			if record.State != AutoStateConverting || record.PlanID != plan.ID {
				t.Fatalf("record = %+v", record)
			}
			if record.PaidRuns != 0 || record.PaidCredits != 0 {
				t.Fatalf("a free run was recorded as paid: %+v", record)
			}

			// A restart, a later round: the run already exists, so nothing is
			// started a second time.
			f.pass()
			f.reconcile(t)
			if plans := f.plansOf(t, task.ID); len(plans) != 1 {
				t.Fatalf("a second round started %d plans", len(plans))
			}
		})
	}
}

// TestAutoCompletionPaysTheSecondRouteOnceForOneTask is the paid acceptance: the
// formats the free route cannot produce are filled by Meshy Convert, and a task
// is paid for exactly once even when the first run left formats missing.
func TestAutoCompletionPaysTheSecondRouteOnceForOneTask(t *testing.T) {
	f := newAutoFixture(t)
	f.service.schema.AutoCompletion.MaxInflightPlans = 1
	task := f.meshyGlbOnly("meshy-task", "owner")
	f.candidate(task)

	// With one plan of its own at a time (auto_completion.max_inflight_plans =
	// 1), the free route goes first and the paid one waits for it.
	f.reconcile(t)
	plans := f.plansOf(t, task.ID)
	if len(plans) != 1 || plans[0].Steps[0].OfferingID != generatedGltf {
		t.Fatalf("plans = %#v, want the one free run", plans)
	}
	free := plans[0]
	if free.Origin != processing.OriginDelivery {
		t.Fatalf("plan %s origin = %q", free.ID, free.Origin)
	}
	if free.Steps[0].EstimatedCredits != 0 {
		t.Fatalf("the free route is estimated at %d credits", free.Steps[0].EstimatedCredits)
	}
	if record := f.recordOf(t, task.ID); record.PaidRuns != 0 {
		t.Fatalf("record = %+v, want no paid run yet", record)
	}

	// The free run finished and still left formats missing: the next round
	// starts the paid route.
	free.State = processing.Succeeded
	f.store.plans[free.ID] = free
	f.pass()
	f.reconcile(t)
	plans = f.plansOf(t, task.ID)
	if len(plans) != 2 {
		t.Fatalf("plans = %#v, want the free glTF run and the paid one", plans)
	}
	var paid processing.Plan
	for _, plan := range plans {
		if plan.Origin != processing.OriginDelivery {
			t.Fatalf("plan %s origin = %q", plan.ID, plan.Origin)
		}
		if plan.Steps[0].OfferingID == "meshy/processing/convert" {
			paid = plan
		}
	}
	if paid.ID == "" {
		t.Fatalf("no paid plan in %#v", plans)
	}
	record := f.recordOf(t, task.ID)
	if record.PaidRuns != 1 || record.PaidCredits != 1 {
		t.Fatalf("record = %+v, want one paid run of one credit", record)
	}
	if record.PolicyRevision == "" {
		t.Fatalf("record = %+v, want the Offering revision it planned under", record)
	}

	// The paid run finished and still left formats missing: a restart must not
	// pay a second time for the same task.
	paid.State = processing.Failed
	paid.Failure = processing.ReasonSubmitRejected
	f.store.plans[paid.ID] = paid
	f.record(record)
	f.pass()
	f.reconcile(t)
	for _, plan := range f.plansOf(t, task.ID) {
		if plan.ID != paid.ID && plan.Steps[0].OfferingID == "meshy/processing/convert" {
			t.Fatalf("a second paid run was started: %#v", plan)
		}
	}
	if again := f.recordOf(t, task.ID); again.PaidRuns != 1 {
		t.Fatalf("paid runs = %d, want one", again.PaidRuns)
	}
}

// TestAutoCompletionStopsSpendingWhenTheTasksCeilingIsReached covers the record
// that already spent its paid run: the free route still runs, the paid one does
// not, and the record says so.
func TestAutoCompletionStopsSpendingWhenTheTasksCeilingIsReached(t *testing.T) {
	f := newAutoFixture(t)
	task := f.meshyGlbOnly("meshy-task", "owner")
	f.candidate(task)
	f.record(AutoCompletionRecord{
		TaskID: task.ID, OwnerID: task.OwnerID, State: AutoStatePending,
		PaidRuns: f.service.schema.AutoCompletion.MaxPaidRunsPerTask, PaidCredits: 1,
	})

	f.reconcile(t)
	var paid int
	for _, plan := range f.plansOf(t, task.ID) {
		if plan.Steps[0].OfferingID == "meshy/processing/convert" {
			paid++
		}
	}
	if paid != 0 {
		t.Fatalf("%d paid plans were started after the ceiling was reached", paid)
	}
	record := f.recordOf(t, task.ID)
	if record.Reason == "" {
		t.Fatalf("record = %+v, want why the paid route did not run", record)
	}
}

// TestAutoCompletionReportsASourceOverTheRoutesLimit is the 70 MiB acceptance: a
// saved file the paid route cannot take reads as unavailable with a reason,
// instead of being started and failing on every retry. The free route still
// converts what it accepts.
func TestAutoCompletionReportsASourceOverTheRoutesLimit(t *testing.T) {
	f := newAutoFixture(t)
	task := f.meshyGlbOnly("meshy-task", "owner")
	// 80 MiB: over Meshy Convert's declared 70 MiB inline limit, under the
	// local converter's own 128 MiB bound.
	stored := f.tasks[task.ID]
	stored.Assets[0].Size = 80 << 20
	f.tasks[task.ID] = stored
	f.candidate(task)

	f.reconcile(t)
	for _, plan := range f.plansOf(t, task.ID) {
		if plan.Steps[0].OfferingID == "meshy/processing/convert" {
			t.Fatalf("the paid route was started for an 80 MiB source: %#v", plan)
		}
	}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fbx", "obj", "stl", "usdz", "3mf"} {
		row := formatByID(t, coverage, id)
		if row.Status != StatusUnavailable || row.Reason != reasonSourceTooLarge || row.ReasonText == "" {
			t.Fatalf("%s = %#v, want unavailable with the source-size reason", id, row)
		}
	}
	// The format the free route can still produce is on its way.
	if row := formatByID(t, coverage, "gltf"); row.Status != StatusConverting {
		t.Fatalf("gltf = %#v, want the free run converting", row)
	}
}

// TestAutoCompletionWaitsForTheInflightLimit is the concurrency acceptance: the
// platform runs at most auto_completion.max_inflight_plans of its own plans.
func TestAutoCompletionWaitsForTheInflightLimit(t *testing.T) {
	f := newAutoFixture(t)
	f.service.schema.AutoCompletion.MaxInflightPlans = 1
	f.store.plans["other-plan"] = processing.Plan{
		ID: "other-plan", OwnerID: "owner", State: processing.Running,
		Origin: processing.OriginDelivery, SourceTaskID: "other-task",
	}
	task := f.meshyGlbOnly("meshy-task", "owner")
	f.candidate(task)

	f.reconcile(t)
	if plans := f.plansOf(t, task.ID); len(plans) != 0 {
		t.Fatalf("plans = %#v, want none while the platform is at its limit", plans)
	}
	record := f.recordOf(t, task.ID)
	if record.State != AutoStatePending {
		t.Fatalf("record = %+v, want it left for a later round", record)
	}
	// Once the other plan lands, the round goes through — one plan at a time,
	// so the paid route follows the free one in a round of its own.
	f.store.plans["other-plan"] = processing.Plan{
		ID: "other-plan", OwnerID: "owner", State: processing.Succeeded,
		Origin: processing.OriginDelivery, SourceTaskID: "other-task",
	}
	f.pass()
	f.reconcile(t)
	plans := f.plansOf(t, task.ID)
	if len(plans) != 1 || plans[0].Steps[0].OfferingID != generatedGltf {
		t.Fatalf("plans = %#v, want the free run once the limit is free", plans)
	}
	plans[0].State = processing.Succeeded
	f.store.plans[plans[0].ID] = plans[0]
	f.pass()
	f.reconcile(t)
	if plans := f.plansOf(t, task.ID); len(plans) != 2 {
		t.Fatalf("plans = %#v, want the free and the paid run in the end", plans)
	}
}

// TestAutoCompletionNeverLooksAtHistoryAndSkipsWhatIsNotACandidate covers the
// durable watermark (no historical Task is ever processed) and the tasks a
// delivery may not touch: no session (a deleted one leaves session_id NULL) and
// another capability.
func TestAutoCompletionNeverLooksAtHistoryAndSkipsWhatIsNotACandidate(t *testing.T) {
	f := newAutoFixture(t)
	// The platform has never scanned: its watermark is not written yet.
	f.auto.mu.Lock()
	f.auto.known = false
	f.auto.mu.Unlock()
	f.candidate(f.meshyGlbOnly("meshy-task", "owner"))
	// A Task whose session was deleted: its session_id is gone with it.
	orphan := f.tripoGlbOnly("orphan-task", "owner")
	orphan.SessionID = ""
	f.tasks[orphan.ID] = orphan
	f.candidate(orphan)
	// A Task of a capability the automatic completion does not cover.
	picture := f.addTask("picture-task", "owner", "meshy", generation.TextToImage,
		assetSpec{role: modelRole, variant: "glb", mediaType: "model/gltf-binary"})
	f.candidate(picture)

	if acted := f.reconcile(t); acted != 0 {
		t.Fatalf("the first scan acted on %d tasks, want none", acted)
	}
	if _, known, err := f.auto.AutoCompletionWatermark(context.Background()); err != nil || !known {
		t.Fatalf("first scan did not write the watermark: known=%v err=%v", known, err)
	}
	if plans := f.plansOf(t, "meshy-task"); len(plans) != 0 {
		t.Fatalf("a historical Task was processed: %#v", plans)
	}

	// After the watermark, the two Tasks that changed since it are looked at;
	// both are skipped without starting anything.
	f.candidate(orphan)
	f.candidate(picture)
	f.pass()
	acted := f.reconcile(t)
	if acted != 2 {
		t.Fatalf("acted on %d tasks, want the two records", acted)
	}
	for _, id := range []generation.TaskID{"orphan-task", "picture-task"} {
		record := f.recordOf(t, id)
		if record.State != AutoStateDone || record.Reason != autoReasonNotCandidate {
			t.Fatalf("%s = %+v, want it left alone", id, record)
		}
	}
	if plans := f.plansOf(t, "orphan-task"); len(plans) != 0 {
		t.Fatalf("a Task without a session was completed: %#v", plans)
	}
}

// TestAutoCompletionLeavesAMissingTaskToTheFormatsPage: a Task that is gone (a
// tombstone, a retention pass) is closed without an error.
func TestAutoCompletionLeavesAMissingTaskToTheFormatsPage(t *testing.T) {
	f := newAutoFixture(t)
	f.record(AutoCompletionRecord{
		TaskID: "gone-task", OwnerID: "owner", State: AutoStateConverting,
		NextRunAt: f.clock.Add(-time.Minute),
	})
	f.reconcile(t)
	record := f.recordOf(t, "gone-task")
	if record.State != AutoStateDone || record.Reason != autoReasonTaskGone {
		t.Fatalf("record = %+v", record)
	}
}

// perFormatAutoFixture is the conversion fixture whose automatic routes are the
// free local one and a per-format paid one: five missing formats would be five
// paid runs in one round, which is what the platform's own limit and the
// per-Task ceiling have to bound.
func perFormatAutoFixture(t *testing.T) *autoFixture {
	t.Helper()
	f := newAutoFixture(t)
	f.service.schema.AutoCompletion.RouteOrder = []string{
		"local/generated-glb/to-gltf", string(tripoConvert),
	}
	for index := range f.service.schema.Conversions {
		conversion := &f.service.schema.Conversions[index]
		if conversion.OfferingID != string(tripoConvert) {
			continue
		}
		conversion.Auto = catalog.ConversionAutoPaid
		conversion.MaxCredits = 30
	}
	return f
}

// tripoMissing is a finished Tripo task that saved every delivery format except
// the named ones: only those are left for the automatic completion.
func (f *fixture) tripoMissing(id, owner string, missing ...string) generation.Task {
	skip := map[string]bool{}
	for _, format := range missing {
		skip[format] = true
	}
	specs := []assetSpec{{role: "model", variant: "", mediaType: "model/gltf-binary"}}
	for _, item := range []struct {
		format string
		asset  assetSpec
	}{
		{"gltf", assetSpec{role: "model", variant: "gltf", mediaType: "model/gltf+json"}},
		{"fbx", assetSpec{role: "converted_model", variant: "converted.fbx", mediaType: "model/fbx"}},
		{"obj", assetSpec{role: "converted_model", variant: "converted.obj", mediaType: "model/obj"}},
		{"stl", assetSpec{role: "converted_model", variant: "converted.stl", mediaType: "model/stl"}},
		{"usdz", assetSpec{role: "converted_model", variant: "converted.usdz", mediaType: "model/vnd.usdz+zip"}},
		{"3mf", assetSpec{role: "converted_model", variant: "converted.3mf", mediaType: "model/3mf"}},
	} {
		if skip[item.format] {
			continue
		}
		specs = append(specs, item.asset)
	}
	return f.addTask(id, owner, "tripo", "text-to-3d", specs...)
}

// plannedFormats names the formats the round's plans convert, read from the
// frozen input of each one's only step. A per-format run freezes the
// Provider's own value (FBX, 3MF, ...), so the name is lower-cased back to the
// Schema's format ID.
func plannedFormats(t *testing.T, plans []processing.Plan) map[string]bool {
	t.Helper()
	formats := map[string]bool{}
	for _, plan := range plans {
		var input map[string]any
		if err := json.Unmarshal(plan.Steps[0].Input, &input); err != nil {
			t.Fatal(err)
		}
		format, _ := input["format"].(string)
		formats[strings.ToLower(format)] = true
	}
	return formats
}

// #478: the scan position moves past every candidate it lists, so each one's
// record has to be written before the round looks at any of them. A candidate
// whose Task cannot be read must not take the candidates after it with it.
func TestAutoCompletionRecordsEveryCandidateBeforeTheRound(t *testing.T) {
	f := newAutoFixture(t)
	f.service.schema.AutoCompletion.MaxInflightPlans = 2
	first := f.tripoMissing("first-task", "owner", "usdz")
	second := f.tripoMissing("second-task", "owner", "usdz")
	f.candidate(first)
	f.candidate(second)
	readErr := errors.New("task read unavailable")
	f.getErrs[first.ID] = readErr

	if _, err := f.service.ReconcileAutoCompletions(context.Background(), 100); !errors.Is(err, readErr) {
		t.Fatalf("reconcile hid the failed read: %v", err)
	}
	for _, id := range []generation.TaskID{first.ID, second.ID} {
		if _, exists, err := f.auto.AutoCompletionRecord(context.Background(), id); err != nil || !exists {
			t.Fatalf("%s: record exists=%v err=%v, want one written before the round", id, exists, err)
		}
	}

	// The Task can be read again: both are looked at, so the second one was not
	// skipped by the first one's failed round. The failed round is put off
	// (#510), so it comes due again at the next scan.
	delete(f.getErrs, first.ID)
	f.pass()
	f.reconcile(t)
	for _, id := range []generation.TaskID{first.ID, second.ID} {
		if plans := f.plansOf(t, id); len(plans) != 1 {
			t.Fatalf("%s: plans = %#v, want the one run it still misses", id, plans)
		}
	}
}

// #478: the platform runs at most auto_completion.max_inflight_plans of its own
// plans, and a round may not select more than that even when one route offers
// one plan per missing format.
func TestAutoCompletionStartsOnlyWhatThePlatformMayRunPerRound(t *testing.T) {
	f := perFormatAutoFixture(t)
	f.service.schema.AutoCompletion.MaxInflightPlans = 2
	f.service.schema.AutoCompletion.MaxPaidRunsPerTask = 5
	task := f.tripoMissing("tripo-task", "owner", "fbx", "obj", "stl", "usdz", "3mf")
	f.candidate(task)

	f.reconcile(t)
	plans := f.plansOf(t, task.ID)
	if len(plans) != 2 {
		t.Fatalf("plans = %#v, want the two the platform may run at once", plans)
	}
	if record := f.recordOf(t, task.ID); record.PaidRuns != 2 {
		t.Fatalf("record = %+v, want the two runs it started", record)
	}
	// The formats after the two are still missing: a later round takes them.
	if record := f.recordOf(t, task.ID); record.State != AutoStateConverting {
		t.Fatalf("record = %+v, want it converting what it started", record)
	}
}

// #478: the per-Task paid ceiling is compared for every plan of a round, not
// only once at its start: a per-format route would place one paid run per
// missing format and pass the ceiling with each single one.
func TestAutoCompletionComparesThePaidCeilingForEveryPlanOfTheRound(t *testing.T) {
	f := perFormatAutoFixture(t)
	f.service.schema.AutoCompletion.MaxInflightPlans = 10
	task := f.tripoMissing("tripo-task", "owner", "fbx", "obj", "stl", "usdz", "3mf")
	f.candidate(task)

	f.reconcile(t)
	plans := f.plansOf(t, task.ID)
	if len(plans) != 1 {
		t.Fatalf("plans = %#v, want the one paid run the Task may afford", plans)
	}
	record := f.recordOf(t, task.ID)
	if record.PaidRuns != 1 || record.PaidCredits != 5 {
		t.Fatalf("record = %+v, want one paid run of five credits", record)
	}
}

// #478: a format a directly started conversion already runs, or one that failed
// with an unknown result, is nobody's to start a second paid run for. A failure
// that proves nothing was charged leaves the format convertible.
func TestAutoCompletionLeavesFormatsAnUnfinishedDirectConversionOwns(t *testing.T) {
	cases := []struct {
		name    string
		state   generation.TaskState
		failure *generation.Failure
		held    bool
	}{
		{"running", generation.TaskRunning, nil, true},
		{"queued", generation.TaskQueued, nil, true},
		{"failed ambiguously", generation.TaskFailed,
			&generation.Failure{Class: generation.FailureSubmissionUnknown, Ambiguous: true}, true},
		{"failed without a reason", generation.TaskFailed, nil, true},
		{"failed for a definite reason", generation.TaskFailed,
			&generation.Failure{Class: generation.FailureInvalidInput}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := perFormatAutoFixture(t)
			f.service.schema.AutoCompletion.MaxInflightPlans = 10
			f.service.schema.AutoCompletion.MaxPaidRunsPerTask = 5
			task := f.tripoMissing("tripo-task", "owner", "fbx", "obj", "stl", "usdz", "3mf")
			direct := f.addConversionTask("agent-convert", "owner", task.SessionID, tripoConvert,
				`{"source_task":"tripo-task","format":"FBX"}`, tc.state, conversionAt(1))
			direct.Failure = tc.failure
			f.tasks[direct.ID] = direct
			f.candidate(task)

			f.reconcile(t)
			formats := plannedFormats(t, f.plansOf(t, task.ID))
			if formats["fbx"] == tc.held {
				t.Fatalf("fbx planned = %v, want %v: %#v", formats["fbx"], !tc.held, formats)
			}
		})
	}
}

// #478: a run that was given up after a failure no retry can fix is never
// re-sent automatically, and a record that never counted it must not charge it
// twice. The guard reads the key the run lives under now, not the base one.
func TestAutoCompletionDoesNotPayAgainForARunItAlreadyGaveUp(t *testing.T) {
	f := perFormatAutoFixture(t)
	task := f.tripoMissing("tripo-task", "owner", "usdz")
	f.candidate(task)

	f.reconcile(t)
	plans := f.plansOf(t, task.ID)
	if len(plans) != 1 {
		t.Fatalf("plans = %#v, want the one paid run", plans)
	}
	given := plans[0]
	given.State = processing.Failed
	given.Failure = processing.ReasonSubmitRejected
	given.Steps[0].State = processing.Failed
	given.Steps[0].Failure = processing.ReasonSubmitRejected
	f.store.plans[given.ID] = given
	// The record never counted the run, as after a crash between the plan and
	// the record.
	f.record(AutoCompletionRecord{
		TaskID: task.ID, OwnerID: task.OwnerID, State: AutoStatePending, NextRunAt: f.clock,
	})

	f.reconcile(t)
	if plans := f.plansOf(t, task.ID); len(plans) != 1 || plans[0].ID != given.ID {
		t.Fatalf("plans = %#v, want the given-up run left alone", plans)
	}
	if record := f.recordOf(t, task.ID); record.PaidRuns != 0 || record.PaidCredits != 0 {
		t.Fatalf("record = %+v, want nothing charged for a run that already exists", record)
	}
}

// #478: deadline_hours runs from the record's first round. A round that only
// waits must not move that base, or the deadline never arrives.
func TestAutoCompletionStopsAtTheDeadlineFromTheFirstRound(t *testing.T) {
	f := newAutoFixture(t)
	f.service.schema.AutoCompletion.MaxInflightPlans = 1
	task := f.tripoMissing("tripo-task", "owner", "usdz")
	first := f.clock
	f.record(AutoCompletionRecord{
		TaskID: task.ID, OwnerID: task.OwnerID, State: AutoStatePending,
		FirstRunAt: first, NextRunAt: first,
	})
	f.store.plans["other-plan"] = processing.Plan{
		ID: "other-plan", OwnerID: "owner", State: processing.Running,
		Origin: processing.OriginDelivery, SourceTaskID: "other-task",
	}

	// The platform is at its own limit, so the round decides nothing — and
	// leaves the first-round time alone.
	f.reconcile(t)
	record := f.recordOf(t, task.ID)
	if record.State != AutoStatePending || !record.FirstRunAt.Equal(first) {
		t.Fatalf("record = %+v, want it undecided with its first round intact", record)
	}
	if plans := f.plansOf(t, task.ID); len(plans) != 0 {
		t.Fatalf("plans = %#v, want none while the platform is at its limit", plans)
	}

	// The Schema's two hours are up, even though the round in between
	// refreshed next_run_at: one hour of grace keeps the two bases apart.
	f.store.plans["other-plan"] = processing.Plan{
		ID: "other-plan", OwnerID: "owner", State: processing.Succeeded,
		Origin: processing.OriginDelivery, SourceTaskID: "other-task",
	}
	f.clock = f.clock.Add(2*time.Hour + time.Minute)
	f.reconcile(t)
	record = f.recordOf(t, task.ID)
	if record.State != AutoStateDone || record.Reason != autoReasonDeadline {
		t.Fatalf("record = %+v, want it closed at the deadline", record)
	}
	if plans := f.plansOf(t, task.ID); len(plans) != 0 {
		t.Fatalf("plans = %#v, want none started after the deadline", plans)
	}
}

// #478: a read that fails while the round collects the directly started
// conversions decides nothing: it starts no plan and leaves the record for
// another round, because the formats may already be paid for.
func TestAutoCompletionDecidesNothingWhenTheConversionsCannotBeRead(t *testing.T) {
	f := newAutoFixture(t)
	task := f.tripoMissing("tripo-task", "owner", "usdz")
	f.candidate(task)
	f.listErr = errors.New("conversion listing unavailable")

	f.reconcile(t)
	if plans := f.plansOf(t, task.ID); len(plans) != 0 {
		t.Fatalf("plans = %#v, want none while the direct conversions are unreadable", plans)
	}
	record := f.recordOf(t, task.ID)
	if record.State != AutoStatePending || record.Reason != autoReasonWaiting || record.Attempts != 1 {
		t.Fatalf("record = %+v, want it undecided, tried once and due again", record)
	}

	f.listErr = nil
	f.pass()
	f.reconcile(t)
	if plans := f.plansOf(t, task.ID); len(plans) != 1 {
		t.Fatalf("plans = %#v, want the run once the conversions can be read", plans)
	}
}
