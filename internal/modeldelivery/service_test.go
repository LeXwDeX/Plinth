package modeldelivery

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

type memoryStore struct {
	mu    sync.Mutex
	plans map[string]processing.Plan
}

func (s *memoryStore) CreateProcessingPlan(_ context.Context, plan processing.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.plans[plan.ID]; exists {
		return processing.ErrConflict
	}
	s.plans[plan.ID] = plan
	return nil
}
func (s *memoryStore) GetProcessingPlan(_ context.Context, owner generation.OwnerID, id string) (processing.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, exists := s.plans[id]
	if !exists || plan.OwnerID != owner {
		return processing.Plan{}, processing.ErrMissing
	}
	return plan, nil
}

// ListProcessingPlans mimics the SQLite repository: the newest 100 plans of
// the session only.
func (s *memoryStore) ListProcessingPlans(_ context.Context, owner generation.OwnerID, session string) ([]processing.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var plans []processing.Plan
	for _, plan := range s.plans {
		if plan.OwnerID == owner && plan.SessionID == session {
			plans = append(plans, plan)
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].CreatedAt.After(plans[j].CreatedAt) })
	if len(plans) > 100 {
		plans = plans[:100]
	}
	return plans, nil
}
func (s *memoryStore) ListProcessingPlansBySource(_ context.Context, owner generation.OwnerID, session string, source generation.TaskID) ([]processing.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var plans []processing.Plan
	for _, plan := range s.plans {
		if plan.OwnerID == owner && plan.SessionID == session && plan.SourceTaskID == source {
			plans = append(plans, plan)
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].ID < plans[j].ID })
	return plans, nil
}
func (s *memoryStore) ListActiveProcessingPlans(context.Context, string, int) ([]processing.Plan, error) {
	return nil, nil
}
func (s *memoryStore) SaveProcessingPlan(_ context.Context, plan processing.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[plan.ID] = plan
	return nil
}

// fixture serves as the task kernel for both the delivery service and the
// real processing service, so plan building runs against the real Catalog.
type fixture struct {
	catalog     *catalog.Catalog
	tasks       map[generation.TaskID]generation.Task
	content     map[generation.AssetID][]byte
	unreadable  map[generation.AssetID]bool
	unavailable map[generation.OfferingID]bool
	// absent hides an Offering from the catalog entirely, as a Schema that
	// never declared it would.
	absent map[generation.OfferingID]bool
	// revisions overrides an Offering's catalog revision, as a catalog
	// publish would while plans created under the old one are still running.
	revisions       map[generation.OfferingID]string
	store           *memoryStore
	plans           *processing.Service
	service         *Service
	submitted       int
	inputValidation func(context.Context, generation.OwnerID, generation.OfferingID, string, json.RawMessage) error
	validationCalls int
	// listErr and getErrs inject reads that fail while the format projection
	// collects directly started conversions.
	listErr error
	getErrs map[generation.TaskID]error
	// lastListQuery records the filter the projection sent.
	lastListQuery generation.ListQuery
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return newFixtureWithCatalog(t, product)
}

// newPaidCompletionFixture isolates the existing paid-plan recovery contract.
// Default-route tests below separately exercise the additional free local plan.
func newPaidCompletionFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.unavailable[generatedGltf] = true
	f.unavailable["local/static-glb/to-gltf"] = true
	f.unavailable["local/static-gltf/to-glb"] = true
	return f
}

// newConversionFixture enables Tripo format conversion, which the default
// Schema ships disabled (2026-10-09: the 3D stage reads GLB only). The
// per-format coverage and plan logic is still exercised against it.
func newConversionFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithCatalog(t, catalogtest.LoadDefaultWithTripoConversion(t))
}

func newFixtureWithCatalog(t *testing.T, product *catalog.Catalog) *fixture {
	t.Helper()
	var err error
	f := &fixture{
		catalog: product, tasks: map[generation.TaskID]generation.Task{},
		content: map[generation.AssetID][]byte{}, unreadable: map[generation.AssetID]bool{},
		unavailable: map[generation.OfferingID]bool{}, absent: map[generation.OfferingID]bool{}, revisions: map[generation.OfferingID]string{},
		getErrs: map[generation.TaskID]error{},
		store:   &memoryStore{plans: map[string]processing.Plan{}},
	}
	f.plans, err = processing.New(f.store, planCatalog{f}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = New(product.Application().ModelDelivery, f, f.plans, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type planCatalog struct{ f *fixture }

func (c planCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	return c.f.Offering(id)
}
func (c planCatalog) OfferingAvailable(id generation.OfferingID) bool {
	return c.f.OfferingAvailable(id)
}
func (c planCatalog) Normalize(capability generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	return c.f.catalog.Normalize(capability, id, input)
}

func (f *fixture) Offering(id generation.OfferingID) (generation.Offering, bool) {
	if f.absent[id] {
		return generation.Offering{}, false
	}
	offering, exists := f.catalog.Get(id)
	if revision := f.revisions[id]; exists && revision != "" {
		offering.Revision = revision
	}
	return offering, exists
}
func (f *fixture) OfferingAvailable(id generation.OfferingID) bool {
	offering, exists := f.catalog.Get(id)
	return exists && offering.Enabled && !f.unavailable[id]
}

func (f *fixture) GetScoped(_ context.Context, scope generation.TaskListScope, id generation.TaskID) (generation.Task, error) {
	if err := f.getErrs[id]; err != nil {
		return generation.Task{}, err
	}
	task, exists := f.tasks[id]
	if !exists || (!scope.AllOwners() && task.OwnerID != scope.OwnerID()) {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get"}
	}
	return task, nil
}

type readerAtCloser struct{ *bytes.Reader }

func (readerAtCloser) Close() error { return nil }

func (f *fixture) OpenAssetScoped(ctx context.Context, scope generation.TaskListScope, taskID generation.TaskID, assetID generation.AssetID) (generation.AssetContent, error) {
	task, err := f.GetScoped(ctx, scope, taskID)
	if err != nil {
		return generation.AssetContent{}, err
	}
	for _, asset := range task.Assets {
		if asset.ID == assetID {
			if f.unreadable[assetID] {
				return generation.AssetContent{}, &generation.Error{Code: generation.ErrUnavailable, Operation: "open_asset"}
			}
			// Like the local store's file, the body can also be read at offsets.
			return generation.AssetContent{Body: readerAtCloser{bytes.NewReader(f.content[assetID])}, MediaType: asset.MediaType, Size: asset.Size}, nil
		}
	}
	return generation.AssetContent{}, &generation.Error{Code: generation.ErrNotFound, Operation: "open_asset"}
}

// processing.Gateway: only Get is exercised; submission is the kernel's job.
func (f *fixture) Get(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	return f.GetScoped(ctx, generation.OwnTasks(owner), id)
}
func (f *fixture) LookupByIdempotency(context.Context, generation.OwnerID, string) (generation.Task, error) {
	return generation.Task{}, &generation.Error{Code: generation.ErrNotFound}
}
func (f *fixture) VerifyPreparedTaskReplay(context.Context, generation.SubmitPreparedCommand) (generation.Task, error) {
	return generation.Task{}, errors.New("not used")
}
func (f *fixture) GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error) {
	return generation.PreparedInput{}, errors.New("not used")
}
func (f *fixture) PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error) {
	f.submitted++
	return generation.PreparedInput{}, errors.New("not used")
}
func (f *fixture) SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error) {
	f.submitted++
	return generation.Accepted{}, errors.New("not used")
}

// Legacy projection fixtures do not run Provider validators. Real byte/profile
// checks are covered by the localmodel Module/CAS integration test.
func (f *fixture) ValidateOfferingInput(ctx context.Context, owner generation.OwnerID, id generation.OfferingID, revision string, input json.RawMessage) error {
	f.validationCalls++
	if f.inputValidation != nil {
		return f.inputValidation(ctx, owner, id, revision, input)
	}
	return nil
}

type assetSpec struct {
	role, variant, mediaType string
	body                     string
}

// addTask stores a succeeded task of provider/capability with the given
// assets (ordinals follow the order) and their bytes.
func (f *fixture) addTask(id, owner, providerID string, capability generation.Capability, specs ...assetSpec) generation.Task {
	offering := generation.Offering{ID: generation.OfferingID(providerID + "/test"), ProviderID: providerID, ProviderName: providerID}
	task := generation.Task{ID: generation.TaskID(id), OwnerID: generation.OwnerID(owner), SessionID: "session-" + owner,
		Capability: capability, Offering: offering, State: generation.TaskSucceeded}
	for index, spec := range specs {
		assetID := generation.AssetID(fmt.Sprintf("%s-a%d", id, index))
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
			CreatedAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
		})
	}
	f.tasks[task.ID] = task
	return task
}

func (f *fixture) meshyText(id, owner string) generation.Task {
	var specs []assetSpec
	for _, variant := range []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"} {
		media := "application/octet-stream"
		if variant == "glb" {
			media = "model/gltf-binary"
		}
		specs = append(specs, assetSpec{role: "model", variant: variant, mediaType: media})
	}
	specs = append(specs, assetSpec{role: "thumbnail", variant: "primary", mediaType: "image/png"})
	return f.addTask(id, owner, "meshy", "text-to-3d", specs...)
}

// meshyGlbOnly is a finished Meshy 3D task that saved only the GLB. Every
// other delivery format is then a missing format that a conversion must fill.
func (f *fixture) meshyGlbOnly(id, owner string) generation.Task {
	return f.addTask(id, owner, "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
		assetSpec{role: "thumbnail", variant: "primary", mediaType: "image/png"})
}

// tripoGlbOnly is a finished Tripo task that saved only its original GLB
// container; the declared converted formats were never delivered.
func (f *fixture) tripoGlbOnly(id, owner string) generation.Task {
	return f.addTask(id, owner, "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"})
}

func (f *fixture) tripoFull(id, owner string) generation.Task {
	specs := []assetSpec{{role: "model", variant: "", mediaType: "model/gltf-binary"}}
	for _, format := range []string{"gltf", "usdz", "fbx", "obj", "stl", "3mf"} {
		media := "application/octet-stream"
		if format == "gltf" {
			media = "model/gltf-binary"
		}
		specs = append(specs, assetSpec{role: "converted_model", variant: "converted." + format, mediaType: media})
	}
	return f.addTask(id, owner, "tripo", "text-to-3d", specs...)
}

func formatByID(t *testing.T, coverage Coverage, id string) Format {
	t.Helper()
	for _, row := range coverage.Formats {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("format %q missing from %#v", id, coverage.Formats)
	return Format{}
}

func TestCoverageOfMeshyGenerationOffersOnlyTheFreeGltfPlan(t *testing.T) {
	f := newConversionFixture(t)
	f.meshyText("meshy-task", "owner")
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	if !coverage.Applicable || len(coverage.Formats) != 7 {
		t.Fatalf("coverage = %#v", coverage)
	}
	for _, id := range []string{"glb", "fbx", "obj", "stl", "usdz", "3mf"} {
		row := formatByID(t, coverage, id)
		if row.Status != StatusAvailable || row.AssetID == "" || row.TaskID != "meshy-task" {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	gltf := formatByID(t, coverage, "gltf")
	// The only missing format is the JSON glTF container, which the local
	// Task-output package converter produces without a paid upstream step.
	if gltf.Status != StatusObtainable || gltf.Conversion == nil || gltf.Conversion.OfferingID != generatedGltf || gltf.Conversion.EstimatedCredits != 0 {
		t.Fatalf("gltf = %#v", gltf)
	}
	if !coverage.CanComplete || coverage.CompletionCredits != 0 || len(coverage.Steps) != 1 ||
		coverage.Steps[0].State != "" || !coverage.ArchiveAvailable {
		t.Fatalf("steps = %#v credits=%d", coverage.Steps, coverage.CompletionCredits)
	}
	if f.submitted != 0 {
		t.Fatalf("reading coverage submitted %d tasks", f.submitted)
	}
}

func TestCoverageOfMeshyUVAndResizeTasksUsesOnlyDeclaredModelAssets(t *testing.T) {
	f := newConversionFixture(t)
	for _, capability := range []generation.Capability{"uv-unwrap", "resize"} {
		t.Run(string(capability)+"-ready-glb", func(t *testing.T) {
			id := string(capability) + "-task"
			f.addTask(id, "owner", "meshy", capability,
				assetSpec{role: modelRole, variant: "glb", mediaType: "model/gltf-binary"},
				assetSpec{role: "print-archive", variant: "3mf", mediaType: "application/zip"},
				assetSpec{role: "other", variant: "report", mediaType: "application/json"},
				assetSpec{role: "clip", variant: "animation", mediaType: "model/fbx"},
			)
			coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), generation.TaskID(id))
			if err != nil {
				t.Fatal(err)
			}
			if !coverage.Applicable || !coverage.ArchiveAvailable {
				t.Fatalf("coverage = %#v", coverage)
			}
			glb := formatByID(t, coverage, "glb")
			if glb.Status != StatusAvailable || glb.AssetID != generation.AssetID(id+"-a0") || glb.TaskID != generation.TaskID(id) {
				t.Fatalf("GLB = %#v", glb)
			}
			threeMF := formatByID(t, coverage, "3mf")
			if threeMF.Status == StatusAvailable {
				t.Fatalf("print archive was exposed as model format: %#v", threeMF)
			}
			for _, row := range coverage.Formats {
				if row.ID == "report" || row.ID == "clip" {
					t.Fatalf("non-model output leaked into model formats: %#v", row)
				}
			}
		})

		t.Run(string(capability)+"-missing-model", func(t *testing.T) {
			id := string(capability) + "-missing-model"
			f.addTask(id, "owner", "meshy", capability,
				assetSpec{role: "thumbnail", variant: "primary", mediaType: "image/png"},
				assetSpec{role: "print-archive", variant: "3mf", mediaType: "application/zip"},
				assetSpec{role: "other", variant: "report", mediaType: "application/json"},
				assetSpec{role: "clip", variant: "animation", mediaType: "model/fbx"},
			)
			coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), generation.TaskID(id))
			if err != nil {
				t.Fatal(err)
			}
			if !coverage.Applicable || coverage.ArchiveAvailable {
				t.Fatalf("coverage = %#v", coverage)
			}
			for _, row := range coverage.Formats {
				if row.Status == StatusAvailable {
					t.Fatalf("attachment without a model appeared as a delivered format: %#v", row)
				}
			}
		})
	}

	attachments := f.addTask("analysis-attachments", "owner", "meshy", "analyze-printability",
		assetSpec{role: "other", variant: "report", mediaType: "application/json"},
		assetSpec{role: "print-archive", variant: "3mf", mediaType: "application/zip"},
		assetSpec{role: "clip", variant: "animation", mediaType: "model/fbx"},
	)
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), attachments.ID)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Applicable || len(coverage.Formats) != 0 || coverage.ArchiveAvailable {
		t.Fatalf("pure attachment task must stay outside model-format delivery: %#v", coverage)
	}
}

func TestCoverageOfTripoFullDeliversEveryNativeFormat(t *testing.T) {
	f := newFixture(t)
	f.tripoFull("tripo-task", "owner")
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "tripo-task")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"glb", "fbx", "obj", "stl", "usdz", "3mf"} {
		if row := formatByID(t, coverage, id); row.Status != StatusAvailable {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	// The upstream GLTF label carries binary GLB bytes, not a JSON glTF.
	gltf := formatByID(t, coverage, "gltf")
	if gltf.Status != StatusObtainable || gltf.AssetID != "" || gltf.Conversion == nil || gltf.Conversion.OfferingID != generatedGltf || gltf.Conversion.EstimatedCredits != 0 {
		t.Fatalf("binary container advertised as JSON glTF: %#v", gltf)
	}
}

func TestCoverageOfRiggedAndAnimationSourcesNeverOffersConversion(t *testing.T) {
	f := newFixture(t)
	f.addTask("rigged", "owner", "meshy", "rigging",
		assetSpec{role: "model", variant: "rigged_character_fbx_url"},
		assetSpec{role: "model", variant: "rigged_character_glb_url", mediaType: "model/gltf-binary"},
		assetSpec{role: "model", variant: "basic_animations.walking_glb_url", mediaType: "model/gltf-binary"})
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "rigged")
	if err != nil {
		t.Fatal(err)
	}
	if formatByID(t, coverage, "glb").Status != StatusAvailable || formatByID(t, coverage, "fbx").Status != StatusAvailable {
		t.Fatalf("rigged formats = %#v", coverage.Formats)
	}
	for _, id := range []string{"gltf", "obj", "stl", "usdz", "3mf"} {
		row := formatByID(t, coverage, id)
		if row.Status != StatusUnavailable || row.Reason != "source_blocked" {
			t.Fatalf("%s = %#v", id, row)
		}
	}
	if coverage.CanComplete || len(coverage.Steps) != 0 {
		t.Fatalf("rigged source offered conversion: %#v", coverage.Steps)
	}
	f.addTask("animated", "owner", "meshy", "animation", assetSpec{role: "model", variant: "animation_glb_url", mediaType: "model/gltf-binary"})
	if coverage, _ = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "animated"); coverage.Applicable {
		t.Fatalf("animation task is not a format-delivery task: %#v", coverage)
	}
	// Completing it is refused as well.
	if _, err := f.service.Complete(context.Background(), "owner", "animated"); !errors.Is(err, ErrNotApplicable) {
		t.Fatalf("complete animation = %v", err)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "rigged"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete rigged = %v", err)
	}
}

func TestCoverageReportsUnavailableOfferingAndUnfinishedTask(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	f.unavailable["meshy/processing/convert"] = true
	coverage, _ := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	usdz := formatByID(t, coverage, "usdz")
	if usdz.Status != StatusUnavailable || usdz.Reason != "offering_unavailable" || coverage.CanComplete {
		t.Fatalf("usdz = %#v", usdz)
	}
	running := f.meshyText("running", "owner")
	running.State = generation.TaskRunning
	f.tasks["running"] = running
	if coverage, _ = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "running"); coverage.Applicable || len(coverage.Formats) != 0 {
		t.Fatalf("unfinished task = %#v", coverage)
	}
}

func TestCoverageIsOwnerScoped(t *testing.T) {
	f := newFixture(t)
	f.meshyText("meshy-task", "owner")
	if _, err := f.service.Coverage(context.Background(), generation.OwnTasks("intruder"), "meshy-task"); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("foreign coverage = %v", err)
	}
	if _, err := f.service.Complete(context.Background(), "intruder", "meshy-task"); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("foreign complete = %v", err)
	}
	if _, err := f.service.Prepare(context.Background(), generation.OwnTasks("intruder"), "meshy-task"); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("foreign archive = %v", err)
	}
	if len(f.store.plans) != 0 {
		t.Fatalf("foreign request created plans: %#v", f.store.plans)
	}
	// An administrator-style all-owner scope may read but Complete is owner only.
	if _, err := f.service.Coverage(context.Background(), generation.AllTasks(), "meshy-task"); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteBuildsOneIdempotentPlanThroughTheKernelContract(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	coverage, err := f.service.Complete(context.Background(), "owner", "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.store.plans) != 1 || len(coverage.Steps) != 1 || coverage.Steps[0].State != processing.Pending ||
		coverage.CanComplete || coverage.CompletionCredits != 0 {
		t.Fatalf("plans=%#v coverage=%#v", f.store.plans, coverage)
	}
	var plan processing.Plan
	for _, plan = range f.store.plans {
	}
	step := plan.Steps[0]
	if plan.SourceTaskID != "meshy-task" || plan.SourceAssetID != "meshy-task-a0" || plan.SessionID != "session-owner" ||
		step.OfferingID != "meshy/processing/convert" || step.EstimatedCredits != 1 {
		t.Fatalf("plan = %#v", plan)
	}
	// #330: Meshy Convert at 1 credit. The step has no fixed input (the plan
	// binds the source; the Offering fixes target_formats) and never Remesh's
	// convert_format_only.
	var input map[string]any
	if err := json.Unmarshal(step.Input, &input); err != nil || len(input) != 0 {
		t.Fatalf("step input = %s", step.Input)
	}
	if usdz := formatByID(t, coverage, "usdz"); usdz.Status != StatusConverting {
		t.Fatalf("usdz while converting = %#v", usdz)
	}
	// Asking again never starts a second paid run.
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("second complete = %v", err)
	}
	if len(f.store.plans) != 1 || f.submitted != 0 {
		t.Fatalf("plans=%d submitted=%d", len(f.store.plans), f.submitted)
	}
}

func TestCompleteFromTripoUsesTheMeshySourceAssetRoute(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.tripoGlbOnly("tripo-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "tripo-task"); err != nil {
		t.Fatal(err)
	}
	for _, plan := range f.store.plans {
		step := plan.Steps[0]
		if step.OfferingID != "meshy/processing/convert" || plan.SourceAssetID != "tripo-task-a0" {
			t.Fatalf("plan = %#v", plan)
		}
	}
}

// A Tripo GLB above Meshy's inline input limit can never be accepted
// upstream, so the Meshy route must not be offered for it: the format reads
// source_incompatible before any paid step instead of failing after a click.
// The legacy Tripo conversion is hidden so the Meshy source-asset route is the
// only candidate for the missing formats.
func TestTripoSourceAboveMeshyInlineLimitOffersNoMeshyRoute(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.absent[tripoConvert] = true
	task := f.tripoGlbOnly("big-tripo", "owner")
	task.Assets[0].Size = 71 << 20 // above the 70 MiB data URI bound (#329)
	f.tasks[task.ID] = task
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "big-tripo")
	if err != nil {
		t.Fatal(err)
	}
	if usdz := formatByID(t, coverage, "usdz"); usdz.Conversion != nil || usdz.Reason != reasonSourceIncompatible {
		t.Fatalf("usdz = %#v", usdz)
	}
	if len(coverage.Steps) != 0 {
		t.Fatalf("steps = %#v", coverage.Steps)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "big-tripo"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete = %v", err)
	}
}

func TestPerFormatConversionsGetOneStablePlanEach(t *testing.T) {
	f := newConversionFixture(t)
	// A Tripo task that only delivered glb and the glTF container.
	f.addTask("old-tripo", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"},
		assetSpec{role: "converted_model", variant: "converted.gltf", mediaType: "model/gltf-binary"})
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "old-tripo")
	if err != nil {
		t.Fatal(err)
	}
	// The binary GLTF-labeled asset is only GLB: six missing formats by
	// explicitly enabled legacy Tripo at 5 credits each.
	if len(coverage.Steps) != 6 || coverage.CompletionCredits != 30 {
		t.Fatalf("steps=%#v credits=%d", coverage.Steps, coverage.CompletionCredits)
	}
	if usdz := formatByID(t, coverage, "usdz"); usdz.Conversion == nil || usdz.Conversion.OfferingID != "tripo/tripo-v3.1/format-conversion" {
		t.Fatalf("usdz = %#v", usdz)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "old-tripo"); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0)
	formats := map[string]bool{}
	for _, plan := range f.store.plans {
		var input map[string]any
		_ = json.Unmarshal(plan.Steps[0].Input, &input)
		keys = append(keys, string(plan.Steps[0].OfferingID))
		if format, ok := input["format"].(string); ok {
			formats[format] = true
		}
	}
	sort.Strings(keys)
	if len(f.store.plans) != 6 || len(formats) != 6 || !formats["USDZ"] || !formats["3MF"] {
		t.Fatalf("plans=%d formats=%v offerings=%v", len(f.store.plans), formats, keys)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "old-tripo"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("repeat = %v", err)
	}
}

func TestFinishedCompletionPlanMakesTheFormatAvailableFromItsResultTask(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); err != nil {
		t.Fatal(err)
	}
	// The kernel finished the Convert task; the plan worker marks the plan done.
	var specs []assetSpec
	for _, variant := range []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"} {
		specs = append(specs, assetSpec{role: "model", variant: variant})
	}
	result := f.addTask("convert-task", "owner", "meshy", "format-conversion", specs...)
	for id, plan := range f.store.plans {
		plan.State, plan.Steps[0].State, plan.Steps[0].TaskID = processing.Succeeded, processing.Succeeded, result.ID
		f.store.plans[id] = plan
	}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	usdz := formatByID(t, coverage, "usdz")
	if usdz.Status != StatusAvailable || usdz.TaskID != "convert-task" || usdz.AssetID == "" {
		t.Fatalf("usdz = %#v", usdz)
	}
	// The task's own files keep pointing at the task itself.
	if own := formatByID(t, coverage, "glb"); own.TaskID != "meshy-task" {
		t.Fatalf("glb = %#v", own)
	}
	if coverage.CanComplete || coverage.CompletionCredits != 0 {
		t.Fatalf("coverage = %#v", coverage)
	}
}

func TestFailedAndSucceededButEmptyPlansAreReported(t *testing.T) {
	f := newFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); err != nil {
		t.Fatal(err)
	}
	for id, plan := range f.store.plans {
		plan.State, plan.Failure, plan.Version = processing.Failed, processing.ReasonTaskFailed, 3
		f.store.plans[id] = plan
	}
	coverage, _ := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	step := coverage.Steps[0]
	if usdz := formatByID(t, coverage, "usdz"); usdz.Status != StatusObtainable ||
		step.State != processing.Failed || step.Failure != processing.ReasonTaskFailed || step.PlanVersion != 3 || step.PlanID == "" {
		t.Fatalf("failed: usdz=%#v step=%#v", usdz, step)
	}
	if coverage.CanComplete {
		t.Fatal("a failed plan must be retried, not silently recreated")
	}
	// Succeeded, but the result task lacks the requested format.
	result := f.addTask("empty-result", "owner", "meshy", "remesh", assetSpec{role: "model", variant: "glb"})
	for id, plan := range f.store.plans {
		plan.State, plan.Steps[0].TaskID = processing.Succeeded, result.ID
		f.store.plans[id] = plan
	}
	coverage, _ = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if usdz := formatByID(t, coverage, "usdz"); usdz.Status != StatusUnavailable || usdz.Reason != "conversion_incomplete" {
		t.Fatalf("usdz = %#v", usdz)
	}
}

func readZip(t *testing.T, archive *Archive) map[string]string {
	t.Helper()
	var buffer bytes.Buffer
	if _, err := archive.WriteTo(&buffer); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatalf("archive is not a valid ZIP: %v", err)
	}
	files := map[string]string{}
	for _, file := range reader.File {
		if _, duplicate := files[file.Name]; duplicate {
			t.Fatalf("duplicate entry %q", file.Name)
		}
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(handle)
		handle.Close()
		files[file.Name] = string(body)
	}
	return files
}

func TestArchiveContainsEveryFormatAndAReadmeListingWhatIsMissing(t *testing.T) {
	f := newFixture(t)
	f.meshyText("meshy-task", "owner")
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if archive.Filename != "model-meshy-task.zip" || archive.MediaType != "application/zip" {
		t.Fatalf("archive = %#v", archive)
	}
	files := readZip(t, archive)
	for _, name := range []string{"model.glb", "model.fbx", "model.obj", "model.stl", "model.usdz", "model.3mf", "README.txt"} {
		if _, present := files[name]; !present {
			t.Fatalf("missing %q in %v", name, keysOf(files))
		}
	}
	if len(files) != 7 {
		t.Fatalf("entries = %v", keysOf(files))
	}
	if archive.Entries != 7 || len(archive.MissingFormats) != 1 || archive.MissingFormats[0] != "gltf" {
		t.Fatalf("entries=%d missing=%v", archive.Entries, archive.MissingFormats)
	}
	if files["model.glb"] != "model:glb" {
		t.Fatalf("glb bytes = %q", files["model.glb"])
	}
	readme := files["README.txt"]
	for _, want := range []string{"meshy-task", "GLB：model.glb", "未包含的格式", "glTF：", "补齐格式"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README lacks %q:\n%s", want, readme)
		}
	}
	if strings.Contains(readme, "BLEND") {
		t.Fatalf("README still describes BLEND:\n%s", readme)
	}
	if strings.Contains(readme, "所有目标格式均已包含") {
		t.Fatalf("README claims completeness:\n%s", readme)
	}
	if strings.Contains(readme, "https://") {
		t.Fatalf("README carries an address:\n%s", readme)
	}
}

func keysOf(files map[string]string) []string {
	keys := make([]string, 0, len(files))
	for name := range files {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

// Archive names are compared case-insensitively: a name that differs only in
// case would overwrite its sibling when extracted on a case-insensitive disk.
func TestUniquePathIgnoresCase(t *testing.T) {
	used := map[string]bool{"readme.txt": true}
	if got := uniquePath(used, "model.glb"); got != "model.glb" {
		t.Fatalf("first name = %q", got)
	}
	if got := uniquePath(used, "Model.GLB"); got != "Model-2.GLB" {
		t.Fatalf("case-only collision = %q, want Model-2.GLB", got)
	}
	if got := uniquePath(used, "MODEL.glb"); got != "MODEL-3.glb" {
		t.Fatalf("third case variant = %q, want MODEL-3.glb", got)
	}
}

// A task that saved one model file and nothing to go with it gets a flat ZIP:
// the model sits at the root next to README.txt, with no folder of its own.
func TestArchivePutsALoneModelFileAtTheRoot(t *testing.T) {
	f := newFixture(t)
	f.meshyGlbOnly("solo-task", "owner")
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "solo-task")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if len(files) != 2 || files["model.glb"] != "model:glb" {
		t.Fatalf("entries = %v", keysOf(files))
	}
	if !strings.Contains(files["README.txt"], "GLB：model.glb") {
		t.Fatalf("README = \n%s", files["README.txt"])
	}
	for name := range files {
		if strings.Contains(name, "/") {
			t.Fatalf("a lone model file kept a folder: %v", keysOf(files))
		}
	}
}

// The folder exists for the OBJ package, not for the OBJ alone: a material
// library or a texture is what makes relative references worth preserving.
func TestArchiveKeepsAFolderOnlyForAnObjThatShipsDependencies(t *testing.T) {
	bare := newFixture(t)
	bare.addTask("bare-obj", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "obj", body: "v 0 0 0\n"})
	archive, err := bare.service.Prepare(context.Background(), generation.OwnTasks("owner"), "bare-obj")
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, archive)
	archive.Close()
	if files["model.obj"] != "v 0 0 0\n" || len(files) != 2 {
		t.Fatalf("bare OBJ entries = %v", keysOf(files))
	}
	if !strings.Contains(files["README.txt"], "OBJ：model.obj") {
		t.Fatalf("README = \n%s", files["README.txt"])
	}

	packaged := newFixture(t)
	packaged.addTask("packaged-obj", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "obj", body: "v 0 0 0\n"},
		assetSpec{role: "texture", variant: "texture.1.base_color", mediaType: "image/png", body: "PNGBASE"})
	archive, err = packaged.service.Prepare(context.Background(), generation.OwnTasks("owner"), "packaged-obj")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files = readZip(t, archive)
	if files["obj/model.obj"] != "v 0 0 0\n" || files["obj/texture.1.base_color.png"] != "PNGBASE" {
		t.Fatalf("OBJ package entries = %v", keysOf(files))
	}
	if !strings.Contains(files["README.txt"], "OBJ：obj/model.obj") ||
		!strings.Contains(files["README.txt"], "- obj/texture.1.base_color.png") {
		t.Fatalf("README must list the folder and its dependency:\n%s", files["README.txt"])
	}
	for name := range files {
		if strings.Contains(name, "/") && !strings.HasPrefix(name, "obj/") {
			t.Fatalf("unexpected folder %q in %v", name, keysOf(files))
		}
	}
}

// Two formats can resolve to the same extension (the GLB and glTF formats both
// take their extension from a JSON glTF media type here). Flattened names stay
// unique and README matches each file.
func TestArchiveGivesCollidingFlatNamesUniquePaths(t *testing.T) {
	f := newFixture(t)
	f.addTask("collision", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf+json", body: "GLB-BYTES"},
		assetSpec{role: "converted_model", variant: "converted.gltf", mediaType: "model/gltf+json", body: "GLTF-BYTES"})
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "collision")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if files["model.gltf"] != "GLB-BYTES" || files["model-2.gltf"] != "GLTF-BYTES" {
		t.Fatalf("colliding names = %v", keysOf(files))
	}
	for name := range files {
		if strings.Contains(name, "/") {
			t.Fatalf("single-file formats kept a folder: %v", keysOf(files))
		}
	}
	readme := files["README.txt"]
	for _, want := range []string{"GLB：model.gltf\n", "glTF：model-2.gltf\n"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README lacks %q:\n%s", want, readme)
		}
	}
	// Every format the README lists is a real entry, and every entry is listed.
	included := 0
	inside := false
	for _, line := range strings.Split(readme, "\n") {
		if line == f.service.schema.Archive.Readme.IncludedHeading {
			inside = true
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			inside = false
			continue
		}
		if !inside {
			continue
		}
		path := line[strings.Index(line, "：")+len("："):]
		if _, present := files[path]; !present {
			t.Fatalf("README lists %q, which is not an entry: %v", path, keysOf(files))
		}
		included++
	}
	if included != 2 || len(files) != 3 {
		t.Fatalf("README lists %d formats, ZIP holds %v", included, keysOf(files))
	}
}

func TestArchiveKeepsObjMaterialAndTexturePathsValid(t *testing.T) {
	f := newFixture(t)
	f.addTask("obj-task", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
		assetSpec{role: "model", variant: "obj", body: "# model\nmtllib scene/model_material.mtl\no cube\n"},
		assetSpec{role: "model", variant: "mtl", body: "newmtl m\nmap_Kd texture_0.png\nmap_Bump -bm 1 normal_map.png\nmap_Pm metal.png\nmap_Ks specular_unsaved.png\n"},
		assetSpec{role: "texture", variant: "texture.1.base_color", mediaType: "image/png", body: "PNGBASE"},
		assetSpec{role: "texture", variant: "texture.1.normal", mediaType: "image/png", body: "PNGNORMAL"},
		assetSpec{role: "texture", variant: "texture.1.roughness", mediaType: "image/png", body: "PNGROUGH"},
		assetSpec{role: "thumbnail", variant: "primary", mediaType: "image/png"},
	)
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "obj-task")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	// The OBJ names its material library; the library names its textures. Both
	// resolve relative to the OBJ's folder.
	want := map[string]string{
		"obj/model.obj":                "mtllib scene/model_material.mtl",
		"obj/scene/model_material.mtl": "map_Kd texture_0.png",
		"obj/scene/texture_0.png":      "PNGBASE",
		"obj/scene/normal_map.png":     "PNGNORMAL",
		"obj/texture.1.roughness.png":  "PNGROUGH",
	}
	for name, content := range want {
		if !strings.Contains(files[name], content) {
			t.Fatalf("%s = %q (entries %v)", name, files[name], keysOf(files))
		}
	}
	if _, present := files["obj/scene/metal.png"]; present {
		t.Fatal("a texture nobody saved appeared in the archive")
	}
	readme := files["README.txt"]
	if !strings.Contains(readme, "obj/scene/model_material.mtl") || !strings.Contains(readme, "obj/scene/metal.png（OBJ 引用了该文件，但任务没有保存它）") {
		t.Fatalf("README = \n%s", readme)
	}
	// Nothing else leaks into other/: thumbnails are not model files.
	for name := range files {
		if strings.HasPrefix(name, "other/") {
			t.Fatalf("unexpected other file %q", name)
		}
	}
}

func TestArchiveDisclosesMissingObjMaterialWithoutChangingItsBytes(t *testing.T) {
	for _, test := range []struct{ name, provider, role, variant string }{
		{"native", "meshy", "model", "obj"},
		{"converted", "tripo", "converted_model", "converted.obj"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			const object = "# preserved OBJ\nmtllib scene/temp.mtl\nusemtl surface\nv 0 0 0\n"
			task := f.addTask("missing-material", "owner", test.provider, "text-to-3d",
				assetSpec{role: test.role, variant: test.variant, body: object})
			before, _ := json.Marshal(task)
			archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			files := readZip(t, archive)
			warning := strings.ReplaceAll(f.service.schema.Archive.Readme.DependencyMissingLine, "{path}", "scene/temp.mtl")
			if test.name == "converted" {
				// #330: a conversion ships no material library at all, so the
				// README explains the dangling name instead of blaming the task.
				warning = strings.NewReplacer("{path}", "model.obj", "{references}", "scene/temp.mtl").Replace(f.service.schema.Archive.Readme.ConvertedObjReferenceNote)
			}
			if files["model.obj"] != object || strings.Count(files["README.txt"], warning) != 1 {
				t.Fatalf("OBJ = %q\nREADME =\n%s", files["model.obj"], files["README.txt"])
			}
			if len(files) != 2 {
				t.Fatalf("fabricated dependency or unexpected file: %v", keysOf(files))
			}
			if strings.Contains(files["README.txt"], objNote(f, "model.obj")) {
				t.Fatalf("dangling material reference described as geometry-only:\n%s", files["README.txt"])
			}
			after, _ := json.Marshal(f.tasks[task.ID])
			if !bytes.Equal(before, after) || string(f.content[task.Assets[0].ID]) != object || f.submitted != 0 || len(f.store.plans) != 0 {
				t.Fatal("archive changed persisted evidence or prepared paid work")
			}
			coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
			if err != nil || formatByID(t, coverage, "obj").Status != StatusAvailable {
				t.Fatalf("existing local OBJ must remain downloadable: coverage=%#v error=%v", coverage, err)
			}
		})
	}
}

func TestArchiveNestedObjMaterialPackageHasNoMissingDependencyWarning(t *testing.T) {
	f := newFixture(t)
	const object = "mtllib scene/material.mtl\nusemtl surface\nv 0 0 0\n"
	const material = "newmtl surface\nmap_Kd textures/base.png\n"
	f.addTask("complete-obj", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "obj", body: object},
		assetSpec{role: "model", variant: "mtl", body: material},
		assetSpec{role: "texture", variant: "texture.1.base_color", mediaType: "image/png", body: "PNGBASE"})
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "complete-obj")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if files["obj/model.obj"] != object || files["obj/scene/material.mtl"] != material || files["obj/scene/textures/base.png"] != "PNGBASE" {
		t.Fatalf("package changed bytes or relative references: %v", keysOf(files))
	}
	for _, name := range []string{"obj/scene/material.mtl", "obj/scene/textures/base.png"} {
		warning := strings.ReplaceAll(f.service.schema.Archive.Readme.DependencyMissingLine, "{path}", name)
		if strings.Contains(files["README.txt"], warning) {
			t.Fatalf("complete package declared missing %s:\n%s", name, files["README.txt"])
		}
	}
}

func TestArchiveDisclosesAnUnreadableObjMaterial(t *testing.T) {
	f := newFixture(t)
	const object = "mtllib scene/temp.mtl\nusemtl surface\n"
	task := f.addTask("unreadable-material", "owner", "tripo", "text-to-3d",
		assetSpec{role: "converted_model", variant: "converted.obj", body: object},
		assetSpec{role: "model", variant: "mtl", body: "newmtl surface\nmap_Kd base.png\n"})
	f.unreadable[task.Assets[1].ID] = true
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	// The material could not be packed, so the OBJ is the whole package and
	// belongs at the root; the disclosed path follows the same folder.
	if files["model.obj"] != object || len(files) != 2 {
		t.Fatalf("unexpected material bytes or entries: %v", keysOf(files))
	}
	if !strings.Contains(files["README.txt"], "scene/temp.mtl："+f.service.reasonText(reasonAssetUnreadable)) || strings.Contains(files["README.txt"], objNote(f, "model.obj")) {
		t.Fatalf("README must disclose unreadable material without geometry-only note:\n%s", files["README.txt"])
	}
}

func TestArchiveKeepsAnObjAtTheRootWhenItsMaterialWasNotSaved(t *testing.T) {
	f := newFixture(t)
	const object = "mtllib scene/material.mtl\nusemtl surface\n"
	task := f.addTask("unsaved-material", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "obj", body: object},
		assetSpec{role: "model", variant: "mtl", body: "newmtl surface\n"})
	// An unsaved row is not a dependency: only ready assets are packaged.
	task.Assets[1].StorageKey = ""
	f.tasks[task.ID] = task
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if files["model.obj"] != object || len(files) != 2 {
		t.Fatalf("an unsaved material must not add a folder: %v", keysOf(files))
	}
	warning := strings.ReplaceAll(f.service.schema.Archive.Readme.DependencyMissingLine, "{path}", "scene/material.mtl")
	if !strings.Contains(files["README.txt"], warning) {
		t.Fatalf("README must disclose the unsaved material reference:\n%s", files["README.txt"])
	}
}

// A saved texture row puts the OBJ in its folder, but when the texture cannot
// be read and the material was never saved, the OBJ is alone again. The README
// must switch back to the root paths, for both the unsaved reference and the
// unreadable attachment.
func TestArchiveFlattensAnObjWhenItsDependenciesCannotBePacked(t *testing.T) {
	f := newFixture(t)
	const object = "mtllib scene/temp.mtl\nusemtl surface\n"
	task := f.addTask("orphan-obj", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "obj", body: object},
		assetSpec{role: "texture", variant: "texture.1.base_color", mediaType: "image/png", body: "PNG"})
	f.unreadable[task.Assets[1].ID] = true
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if files["model.obj"] != object || len(files) != 2 {
		t.Fatalf("an unreadable attachment must not leave the OBJ in a folder: %v", keysOf(files))
	}
	for name := range files {
		if strings.Contains(name, "/") {
			t.Fatalf("unexpected folder %q in %v", name, keysOf(files))
		}
	}
	warning := strings.ReplaceAll(f.service.schema.Archive.Readme.DependencyMissingLine, "{path}", "scene/temp.mtl")
	if !strings.Contains(files["README.txt"], warning) {
		t.Fatalf("README must disclose the unsaved reference at the root:\n%s", files["README.txt"])
	}
	unreadable := "texture.1.base_color.png：" + f.service.reasonText(reasonAssetUnreadable)
	if !strings.Contains(files["README.txt"], unreadable) {
		t.Fatalf("README must disclose the unreadable attachment at the root:\n%s", files["README.txt"])
	}
}

// A completion run is a different task from the one being downloaded; the OBJ
// package must follow the task that actually holds the OBJ and its material.
func TestArchivePacksACompletionObjWithItsOwnMaterial(t *testing.T) {
	f := newFixture(t)
	f.tripoGlbOnly("tripo-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "tripo-task"); err != nil {
		t.Fatal(err)
	}
	const object = "mtllib material.mtl\nusemtl surface\nv 0 0 0\n"
	const material = "newmtl surface\n"
	result := f.addTask("obj-result", "owner", "local", "format-conversion",
		assetSpec{role: "converted_model", variant: "converted.obj", body: object},
		assetSpec{role: "model", variant: "mtl", body: material})
	for id, plan := range f.store.plans {
		plan.State, plan.Steps[0].TaskID = processing.Succeeded, result.ID
		f.store.plans[id] = plan
	}
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "tripo-task")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if files["obj/model.obj"] != object || files["obj/material.mtl"] != material {
		t.Fatalf("completion OBJ package = %v", keysOf(files))
	}
	if !strings.Contains(files["README.txt"], "OBJ：obj/model.obj") || !strings.Contains(files["README.txt"], "- obj/material.mtl\n") {
		t.Fatalf("README must list the packaged OBJ and material:\n%s", files["README.txt"])
	}
}

func TestArchiveNeverWritesOutsideItsFolders(t *testing.T) {
	f := newFixture(t)
	f.addTask("evil-task", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "obj", body: "mtllib ../../outside.mtl\n"},
		assetSpec{role: "model", variant: "mtl", body: "map_Kd /etc/passwd\nmap_Bump ..\\..\\x.png\nmap_Pm C:\\evil.png\n"},
		assetSpec{role: "texture", variant: "texture.1.base_color", mediaType: "image/png"},
	)
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "evil-task")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for name := range readZip(t, archive) {
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, ":") {
			t.Fatalf("unsafe entry %q", name)
		}
	}
}

func TestArchiveNamesATripoGltfContainerAsGlbAndIncludesCompletionResults(t *testing.T) {
	f := newFixture(t)
	f.tripoFull("tripo-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "tripo-task"); err != nil {
		t.Fatal(err)
	}
	gltfResult := f.addTask("gltf-task", "owner", "local", "format-conversion", assetSpec{role: "model", variant: "gltf", mediaType: "model/gltf+json", body: `{"asset":{"version":"2.0"}}`})
	for id, plan := range f.store.plans {
		plan.State, plan.Steps[0].TaskID = processing.Succeeded, gltfResult.ID
		f.store.plans[id] = plan
	}
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "tripo-task")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if _, present := files["model.gltf"]; !present {
		t.Fatalf("entries = %v", keysOf(files))
	}
	for name := range files {
		if strings.Contains(name, "/") && !strings.HasPrefix(name, "other/") {
			t.Fatalf("single-file formats kept a folder: %v", keysOf(files))
		}
	}
	if !strings.Contains(files["README.txt"], "glTF：model.gltf") {
		t.Fatalf("binary GLB filed as a glTF format:\n%s", files["README.txt"])
	}
	if _, present := files["model.blend"]; present {
		t.Fatalf("BLEND was packaged as a delivery format: %v", keysOf(files))
	}
	if len(archive.MissingFormats) != 0 || !strings.Contains(files["README.txt"], "所有目标格式均已包含") {
		t.Fatalf("missing=%v\n%s", archive.MissingFormats, files["README.txt"])
	}
}

// A task that finished before BLEND was withdrawn keeps its stored file
// downloadable as an ordinary attachment: the format list and README never
// describe BLEND as a delivery format again.
func TestArchiveKeepsAHistoricalBlendAssetAsAnOrdinaryFile(t *testing.T) {
	f := newFixture(t)
	f.addTask("meshy-task", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
		assetSpec{role: "model", variant: "blend", mediaType: "application/octet-stream"},
		assetSpec{role: "thumbnail", variant: "primary", mediaType: "image/png"})
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if _, present := files["model.blend"]; present {
		t.Fatalf("BLEND was packaged as a delivery format: %v", keysOf(files))
	}
	const other = "other/model-blend.bin"
	if _, present := files[other]; !present {
		t.Fatalf("historical BLEND file is gone: %v", keysOf(files))
	}
	readme := string(files["README.txt"])
	if !strings.Contains(readme, other) || strings.Contains(readme, "BLEND") {
		t.Fatalf("README = \n%s", readme)
	}
	for _, id := range archive.MissingFormats {
		if id == "blend" {
			t.Fatalf("missing formats still declare BLEND: %v", archive.MissingFormats)
		}
	}
}

func TestArchiveReadmeStatesFilesThatCouldNotBeRead(t *testing.T) {
	f := newFixture(t)
	task := f.meshyText("meshy-task", "owner")
	for _, asset := range task.Assets {
		if asset.Variant == "stl" {
			f.unreadable[asset.ID] = true
		}
	}
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if _, present := files["model.stl"]; present {
		t.Fatal("an unreadable file was claimed")
	}
	readme := files["README.txt"]
	if !strings.Contains(readme, "STL：文件暂时无法读取") || strings.Contains(readme, "STL：model.stl") {
		t.Fatalf("README = \n%s", readme)
	}
	found := false
	for _, id := range archive.MissingFormats {
		found = found || id == "stl"
	}
	if !found {
		t.Fatalf("missing = %v", archive.MissingFormats)
	}
}

func TestArchiveSizeAndEntryBounds(t *testing.T) {
	f := newFixture(t)
	f.meshyText("meshy-task", "owner")
	schema := f.service.schema
	schema.Archive.MaxBytes = 10
	small, _ := New(schema, f, f.plans, nil)
	if _, err := small.Prepare(context.Background(), generation.OwnTasks("owner"), "meshy-task"); !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("byte bound = %v", err)
	}
	schema = f.service.schema
	schema.Archive.MaxEntries = 3
	few, _ := New(schema, f, f.plans, nil)
	if _, err := few.Prepare(context.Background(), generation.OwnTasks("owner"), "meshy-task"); !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("entry bound = %v", err)
	}
	// Exactly at the bounds is allowed.
	schema = f.service.schema
	schema.Archive.MaxEntries = 7
	exact, _ := New(schema, f, f.plans, nil)
	archive, err := exact.Prepare(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	archive.Close()
}

func TestArchiveRefusesTasksWithoutSavedModelFiles(t *testing.T) {
	f := newFixture(t)
	f.addTask("image-task", "owner", "openai", "text-to-image", assetSpec{role: "image", mediaType: "image/png"})
	if _, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "image-task"); !errors.Is(err, ErrEmptyArchive) {
		t.Fatalf("image task = %v", err)
	}
	running := f.meshyText("running", "owner")
	running.State = generation.TaskRunning
	f.tasks["running"] = running
	if _, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "running"); !errors.Is(err, ErrEmptyArchive) {
		t.Fatalf("running task = %v", err)
	}
}

func TestArchiveOfRiggedTaskListsAnimationsAndOmitsFormatNoise(t *testing.T) {
	f := newFixture(t)
	f.addTask("rigged", "owner", "meshy", "rigging",
		assetSpec{role: "model", variant: "rigged_character_fbx_url"},
		assetSpec{role: "model", variant: "rigged_character_glb_url", mediaType: "model/gltf-binary"},
		assetSpec{role: "model", variant: "basic_animations.walking_glb_url", mediaType: "model/gltf-binary"})
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "rigged")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if _, present := files["other/model-basic_animations.walking_glb_url.glb"]; !present {
		t.Fatalf("entries = %v", keysOf(files))
	}
	if !strings.Contains(files["README.txt"], "骨骼与动作") {
		t.Fatalf("README = \n%s", files["README.txt"])
	}
}

func TestArchiveCloseReleasesOpenFiles(t *testing.T) {
	f := newFixture(t)
	f.meshyText("meshy-task", "owner")
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.files {
		if file.body != nil {
			t.Fatal("file left open")
		}
	}
}

// With the default Schema Tripo conversion is disabled: a Tripo task keeps the
// formats it was generated with and no format is routed to the converter.
func TestDisabledTripoConversionOffersNoCompletionOfATripoTask(t *testing.T) {
	f := newFixture(t)
	f.addTask("old-tripo", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"})
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "old-tripo")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range coverage.Steps {
		if step.OfferingID == tripoConvert {
			t.Fatalf("disabled conversion offered: %#v", step)
		}
	}
	for _, format := range coverage.Formats {
		if format.Conversion != nil && format.Conversion.OfferingID == tripoConvert {
			t.Fatalf("%s routed to the disabled conversion: %#v", format.ID, format)
		}
	}
}

const generatedGltf generation.OfferingID = "local/generated-glb/to-gltf"

// #264: real Meshy (29 MB, 8K texture) and Tripo (63 MB, ~1M vertices) GLBs
// exceed the free local static-glb upload profile, and the default Schema
// disables the Tripo conversion. The Task-output package route takes them; the
// upload route still refuses them by size. A Schema-disabled Offering is not a
// route at all, so a reason always names a route that actually exists.
func TestDisabledConversionDoesNotMaskTheReasonOfTheRealRoute(t *testing.T) {
	f := newFixture(t)
	if offering, _ := f.catalog.Get(tripoConvert); offering.Enabled {
		t.Fatal("default Schema is expected to disable the Tripo conversion")
	}
	meshy := f.meshyText("big-meshy", "owner")
	meshy.Assets[0].Size = 29 << 20
	f.tasks[meshy.ID] = meshy
	tripo := f.tripoFull("big-tripo", "owner")
	for index := range tripo.Assets {
		if tripo.Assets[index].MediaType == "model/gltf-binary" {
			tripo.Assets[index].Size = 63 << 20
		}
	}
	f.tasks[tripo.ID] = tripo
	gltfOf := func(id generation.TaskID) Format {
		t.Helper()
		coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), id)
		if err != nil {
			t.Fatal(err)
		}
		return formatByID(t, coverage, "gltf")
	}
	for _, id := range []generation.TaskID{"big-meshy", "big-tripo"} {
		if gltf := gltfOf(id); gltf.Status != StatusObtainable || gltf.Reason != reasonNotConverted || gltf.Conversion == nil || gltf.Conversion.OfferingID != generatedGltf || gltf.Conversion.EstimatedCredits != 0 {
			t.Fatalf("%s gltf = %#v", id, gltf)
		}
	}
	// A source above the generated profile fits no route: the first enabled
	// route names the reason, never the disabled Tripo conversion.
	huge := f.meshyText("huge-meshy", "owner")
	huge.Assets[0].Size = 129 << 20
	f.tasks[huge.ID] = huge
	if gltf := gltfOf("huge-meshy"); gltf.Status != StatusUnavailable || gltf.Reason != reasonSourceIncompatible || gltf.Conversion != nil {
		t.Fatalf("oversized source gltf = %#v", gltf)
	}
	// Without the Task-output route the upload-profile route refuses both.
	f.absent[generatedGltf] = true
	for _, id := range []generation.TaskID{"big-meshy", "big-tripo"} {
		if gltf := gltfOf(id); gltf.Status != StatusUnavailable || gltf.Reason != reasonSourceIncompatible || gltf.Conversion != nil {
			t.Fatalf("upload profile route: %s gltf = %#v", id, gltf)
		}
	}
	// With the local routes also gone only the disabled Offering is left: no route.
	f.absent["local/static-glb/to-gltf"] = true
	if gltf := gltfOf("big-meshy"); gltf.Status != StatusUnavailable || gltf.Reason != reasonNoRoute {
		t.Fatalf("only a disabled route left: gltf = %#v", gltf)
	}
	// A declared, enabled route that is down at runtime still reads unavailable.
	delete(f.absent, generatedGltf)
	f.unavailable[generatedGltf] = true
	if gltf := gltfOf("big-meshy"); gltf.Status != StatusUnavailable || gltf.Reason != reasonOfferingUnavailable {
		t.Fatalf("runtime-unavailable route: gltf = %#v", gltf)
	}
}

// #264: the Task-output package route delivers glTF as a ZIP (model.gltf,
// model.bin, textures). Its finished plan makes the glTF row available with
// that ZIP, and the one-click archive names it .gltf.zip instead of passing a
// ZIP off as a .gltf file.
func TestFinishedGltfPackagePlanMakesGltfAvailableAsAZip(t *testing.T) {
	f := newFixture(t)
	f.meshyText("meshy-task", "owner")
	coverage, err := f.service.Complete(context.Background(), "owner", "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage.Steps) != 1 || coverage.Steps[0].OfferingID != generatedGltf || coverage.Steps[0].EstimatedCredits != 0 {
		t.Fatalf("steps = %#v", coverage.Steps)
	}
	result := f.addTask("package-task", "owner", "local", "format-conversion",
		assetSpec{role: "model", variant: "gltf", mediaType: "application/zip", body: "PK-gltf-package"})
	for id, plan := range f.store.plans {
		plan.State, plan.Steps[0].State, plan.Steps[0].TaskID = processing.Succeeded, processing.Succeeded, result.ID
		f.store.plans[id] = plan
	}
	coverage, err = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	gltf := formatByID(t, coverage, "gltf")
	if gltf.Status != StatusAvailable || gltf.TaskID != "package-task" || gltf.MediaType != "application/zip" || gltf.AssetID == "" {
		t.Fatalf("gltf = %#v", gltf)
	}
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	if files["model.gltf.zip"] != "PK-gltf-package" || len(archive.MissingFormats) != 0 {
		t.Fatalf("entries = %v missing = %v", keysOf(files), archive.MissingFormats)
	}
	if _, wrong := files["model.gltf"]; wrong {
		t.Fatal("a ZIP was named as a .gltf file")
	}
	if !strings.Contains(files["README.txt"], "glTF：model.gltf.zip") {
		t.Fatalf("README:\n%s", files["README.txt"])
	}
}
