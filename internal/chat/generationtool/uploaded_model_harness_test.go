package generationtool_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// This wrapper counts calls while delegating preparation and admission to the
// real kernel, SQLite repository and actual CAS-backed input resolver.
type uploadedKernel struct {
	*generation.Module
	prepares, submits int
}

func (k *uploadedKernel) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	k.prepares++
	return k.Module.PrepareInput(ctx, cmd)
}
func (k *uploadedKernel) SubmitPrepared(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	k.submits++
	return k.Module.SubmitPrepared(ctx, cmd)
}

type uploadedProvider struct {
	*mock.Adapter
	requests []generation.StartRequest
}

func (p *uploadedProvider) Start(ctx context.Context, req generation.StartRequest) (generation.Observation, error) {
	p.requests = append(p.requests, req)
	return p.Adapter.Start(ctx, req)
}

type uploadedHarness struct {
	provider *uploadedProvider
	path     string
	registry *providers.Registry
	config   generationtool.Config
	repo     *sqlite.Repository
	db       *sql.DB
	blobs    *local.Store
	native   *inputassets.Service
	sources  *modelsource.Service
	resolver *resolve.Service
	kernel   *uploadedKernel
	executor *generationtool.Executor
	chat     *chat.Service
	product  *catalog.Catalog
	asset    generation.InputAsset
	command  chat.ToolCommand
}

func newUploadedHarness(t *testing.T) *uploadedHarness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	h := &uploadedHarness{}
	var err error
	path := filepath.Join(dir, "test.db")
	h.path = path
	h.repo, err = sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.repo.Close() })
	h.db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.db.Close() })
	h.blobs, err = local.New(filepath.Join(dir, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.blobs.Close() })
	h.product, err = catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := h.product.Application()
	profile := app.Runtime.InputAssets.ModelProfile
	if profile == nil {
		t.Fatal("Schema must declare uploaded model profile")
	}
	l := profile.Limits
	p := modelinput.Policy{ProfileID: profile.ID, Revision: profile.Revision, Limits: modelinput.Limits{MaxBytes: l.MaxBytes, MaxJSONBytes: l.MaxJSONBytes, MaxDepth: l.MaxDepth, MaxElements: l.MaxElements, MaxVertices: l.MaxVertices, MaxTriangles: l.MaxTriangles, MaxNodes: l.MaxNodes, MaxWork: l.MaxWork, MaxImagePixels: l.MaxImagePixels, MaxTotalImagePixels: l.MaxTotalImagePixels}}
	h.native, err = inputassets.NewWithConfig(h.repo, h.blobs, inputassets.Config{UploadValidator: modelsource.UploadValidator(p, profile.AssetType, profile.Extensions, profile.MediaTypes)})
	if err != nil {
		t.Fatal(err)
	}
	h.sources, err = modelsource.New(h.native, p)
	if err != nil {
		t.Fatal(err)
	}
	raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
	h.asset, err = h.native.UploadAsset(ctx, "owner-a", generation.InputAssetUpload{Body: bytes.NewReader(raw), OriginalFilename: "triangle.glb", AssetType: profile.AssetType, MediaType: profile.UploadMediaType, MaxBytes: l.MaxBytes})
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := h.product.Get("meshy/processing/remesh")
	if !ok {
		t.Fatal("missing remesh offering")
	}
	h.provider = &uploadedProvider{Adapter: mock.New()}
	registry := providers.NewRegistry()
	h.registry = registry
	if err = registry.Register(offering.AdapterKey, h.provider); err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{Repository: h.repo, Catalog: h.product, Providers: registry, Assets: h.blobs, Inputs: h.sources})
	if err != nil {
		t.Fatal(err)
	}
	h.kernel = &uploadedKernel{Module: module}
	h.resolver = &resolve.Service{Store: h.repo, Schema: app.Chat.Harness, Tools: app.Chat.Tools, Catalog: h.product, Gateway: module, UploadedModels: h.sources}
	var known, manual []string
	for _, c := range app.Capabilities {
		known = append(known, string(c.ID))
	}
	for _, c := range app.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(c))
	}
	policy := approvalpolicy.New(app.Chat.ApprovalPolicy.DefaultMode, app.Chat.ApprovalPolicy.Modes, manual, known)
	h.chat, err = chat.NewService(chat.ServiceConfig{Repository: h.repo, ApprovalPolicy: policy, DefaultTitle: "test", UserRole: "user", AssistantRole: "assistant", PendingContent: "pending", StreamActive: "active", StreamDone: "done", StreamError: "error", TurnQueued: "queued", TurnRunning: "running", TurnWaitingTask: "waiting_task", TurnDone: "done", TurnError: "error", PlaceholderHealing: chat.PlaceholderHealingPolicy{StaleAfter: time.Minute, PromptArgument: "prompt", PromptPreviewCharacters: 20, InterruptedMessage: "interrupted", InterruptedPromptTemplate: "interrupted {prompt}"}, StreamCleanupTTL: time.Minute, SubscriberBufferSize: 8, EventFields: chat.EventFieldPolicy{Type: "type", MessageID: "message_id"}, EventTypes: chat.EventTypePolicy{MessageCreated: "message.created"}})
	if err != nil {
		t.Fatal(err)
	}
	h.chat.SetHarnessSnapshotResolver(h.resolver.Snapshot)
	session, err := h.chat.CreateSession(ctx, "owner-a", "Owner", "uploaded source")
	if err != nil {
		t.Fatal(err)
	}
	selected := harness.DefaultSelection("owner-a", session.ID)
	selected.OperationID = "remesh"
	selected.EnabledOperations = []string{"remesh"}
	selected.OfferingID = string(offering.ID)
	selected.OfferingRevision = offering.Revision
	selected.Sources = []harness.Source{{Kind: harness.SourceInputAsset, Parameter: "source_asset", AssetID: string(h.asset.ID)}}
	if _, err = h.resolver.Update(ctx, "owner-a", session.ID, 0, selected); err != nil {
		t.Fatal(err)
	}
	assistant, err := h.chat.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "remesh uploaded model"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := h.chat.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Options.Harness == nil || turn.Options.Harness.Status != resolve.StatusReady {
		t.Fatalf("not ready: %+v", turn.Options.Harness)
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range app.Chat.Tools {
		if candidate.Name == turn.Options.Harness.ToolName {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatal("snapshot tool missing")
	}
	grants := &executiongrant.Service{Grants: h.repo, Prepared: h.repo, Policy: policy, Modes: sessionMode{mode: "auto"}}
	h.config = generationtool.Config{Gateway: h.kernel, Harness: h.repo, HarnessResolver: h.resolver, References: &fakeReferences{}, Operations: h.repo, Grants: grants, Chat: app.Chat, TaskStates: app.TaskStates, StopRequested: h.repo.TurnStopRequested}
	h.executor, err = generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	h.command = chat.ToolCommand{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID, Call: agents.ToolCall{ID: "uploaded-call", Name: tool.Name, Arguments: json.RawMessage(`{"parameters":{}}`)}, Tool: tool, Options: turn.Options}
	return h
}
func (h *uploadedHarness) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	// Table is a test-owned constant, never user or product input.
	if err := h.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestUploadedModelHarnessRealKernelAdmissionAndBoundReplay(t *testing.T) {
	h := newUploadedHarness(t)
	ctx := context.Background()
	if h.count(t, "generation_tasks") != 0 {
		t.Fatal("selection/turn invented source Task")
	}
	result, err := h.executor.Execute(ctx, h.command, nil)
	if err != nil || result.NoSubmission || result.TaskID == "" || !result.AwaitTask {
		t.Fatalf("actual submission %+v %v", result, err)
	}
	op, err := h.repo.LoadAgentOperation(ctx, h.command.OwnerID, h.command.SessionID, h.command.AssistantMessageID, h.command.Call.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := h.kernel.GetPreparedInput(ctx, "owner-a", op.PreparedInputID)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Assets) != 1 || prepared.Assets[0].ID != h.asset.ID || prepared.Assets[0].SHA256 != h.asset.SHA256 || prepared.Assets[0].Size != h.asset.Size || len(prepared.Dependencies) != 0 {
		t.Fatalf("original bytes lost or fake Task used: %+v", prepared)
	}
	grant, err := h.repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.kernel.Get(ctx, "owner-a", generation.TaskID(result.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := h.repo.AdmittedPreparedInput(ctx, "owner-a", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if op.Harness != h.command.Options.Harness.Selection.Binding || grant.Harness != op.Harness || grant.Status != executiongrant.Bound || grant.TaskID != string(task.ID) || admitted != prepared.ID {
		t.Fatal("durable operation/grant/task admission identity diverged")
	}
	var input map[string]json.RawMessage
	if err = json.Unmarshal(task.Input, &input); err != nil {
		t.Fatal(err)
	}
	if string(input["source_asset"]) != `"`+string(h.asset.ID)+`"` || input["source_task"] != nil || input["task_id"] != nil {
		t.Fatalf("wrong task source %s", task.Input)
	}
	if h.kernel.prepares != 1 || h.kernel.submits != 1 || h.count(t, "generation_tasks") != 1 {
		t.Fatal("duplicate/new task during admission")
	}
	processed, err := h.kernel.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "upload-test", BatchSize: 1, Concurrency: 1})
	if err != nil || processed != 1 {
		t.Fatalf("actual mock dispatch %d %v", processed, err)
	}
	if len(h.provider.requests) != 1 {
		t.Fatal("task did not reach the mock Provider exactly once")
	}
	request := h.provider.requests[0]
	sources := request.InputAssets["source_asset"]
	if request.TaskID != task.ID || len(sources) != 1 || sources[0].ID != h.asset.ID || sources[0].SHA256 != h.asset.SHA256 || sources[0].Size != h.asset.Size || len(request.TaskDependencies) != 0 {
		t.Fatalf("Provider lost actual upload byte identity: %+v", request)
	}
	// Current selection and bytes may change after acceptance. Bound observation
	// must replay the original durable grant; it must not prepare or pay again.
	current := h.command.Options.Harness.Selection
	if _, err = h.repo.CompareAndSwapHarnessSelection(ctx, harness.DefaultSelection("owner-a", h.command.SessionID), current.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`UPDATE generation_input_assets SET owner_id='other', sha256='changed-after-admission' WHERE id=?`, h.asset.ID); err != nil {
		t.Fatal(err)
	}
	// Restore the original checkpoint and admission through fresh SQLite/module/
	// executor instances, rather than relying on an in-memory operation cache.
	restartedRepo, err := sqlite.Open(h.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restartedRepo.Close() })
	restoredTurn, err := restartedRepo.GetTurn(ctx, "owner-a", h.command.SessionID, h.command.AssistantMessageID)
	if err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{Repository: restartedRepo, Catalog: h.product, Providers: h.registry, Assets: h.blobs, Inputs: h.sources})
	if err != nil {
		t.Fatal(err)
	}
	recoveredKernel := &uploadedKernel{Module: module}
	config := h.config
	config.Gateway = recoveredKernel
	config.Harness = restartedRepo
	config.Operations = restartedRepo
	config.StopRequested = restartedRepo.TurnStopRequested
	config.Grants = &executiongrant.Service{Grants: restartedRepo, Prepared: restartedRepo, Policy: h.config.Grants.Policy, Modes: sessionMode{mode: "auto"}}
	restarted, err := generationtool.New(config)
	if err != nil {
		t.Fatal(err)
	}
	h.command.Options = restoredTurn.Options
	h.command.BoundTaskID = result.TaskID
	again, err := restarted.Execute(ctx, h.command, nil)
	if err != nil || again.TaskID != result.TaskID || recoveredKernel.prepares != 0 || recoveredKernel.submits != 0 || h.kernel.prepares != 1 || h.kernel.submits != 1 || h.count(t, "generation_tasks") != 1 {
		t.Fatalf("bound replay changed task: %+v %v", again, err)
	}
	h.command.Options.Harness.Selection.Binding = harness.DefaultSelection("owner-a", h.command.SessionID).Binding
	if _, err = restarted.Execute(ctx, h.command, nil); err == nil {
		t.Fatal("wrong bound identity accepted")
	}
}
func TestUploadedModelHarnessRefusesChangedFactsBeforeRealPrepare(t *testing.T) {
	for _, name := range []string{"selection", "foreign_owner", "stale_sha", "stale_size", "stale_bytes", "proof", "source_override"} {
		t.Run(name, func(t *testing.T) {
			h := newUploadedHarness(t)
			ctx := context.Background()
			switch name {
			case "selection":
				_, err := h.repo.CompareAndSwapHarnessSelection(ctx, harness.DefaultSelection("owner-a", h.command.SessionID), h.command.Options.Harness.Selection.Revision)
				if err != nil {
					t.Fatal(err)
				}
			case "foreign_owner":
				if _, err := h.db.Exec(`UPDATE generation_input_assets SET owner_id='other' WHERE id=?`, h.asset.ID); err != nil {
					t.Fatal(err)
				}
			case "stale_sha":
				if _, err := h.db.Exec(`UPDATE generation_input_assets SET sha256='wrong' WHERE id=?`, h.asset.ID); err != nil {
					t.Fatal(err)
				}
			case "stale_size":
				if _, err := h.db.Exec(`UPDATE generation_input_assets SET size_bytes=size_bytes+1 WHERE id=?`, h.asset.ID); err != nil {
					t.Fatal(err)
				}
			case "stale_bytes":
				body, err := h.blobs.OpenBlob(ctx, h.asset.StorageKey)
				if err != nil {
					t.Fatal(err)
				}
				path := body.(*os.File).Name()
				body.Close()
				raw := modelinputtest.Triangle(modelinputtest.Options{})
				if err = os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "proof":
				h.command.Options.Harness.SourceFingerprints[0].SHA256 = "tampered"
			case "source_override":
				h.command.Call.Arguments = json.RawMessage(`{"parameters":{"source_asset":"another"}}`)
			}
			result, err := h.executor.Execute(ctx, h.command, nil)
			if !result.NoSubmission {
				t.Fatalf("unsafe source admitted: %+v %v", result, err)
			}
			if h.kernel.prepares != 0 || h.kernel.submits != 0 || len(h.provider.requests) != 0 {
				t.Fatal("rejected source reached real prepare/submit")
			}
			for _, table := range []string{"generation_tasks", "generation_prepared_inputs", "execution_grants", "agent_generation_operations"} {
				if h.count(t, table) != 0 {
					t.Fatalf("refusal wrote %s", table)
				}
			}
		})
	}
}
func TestUploadedModelHarnessCannotRelabelTaskOutputAsUpload(t *testing.T) {
	h := newUploadedHarness(t)
	ctx := context.Background()
	// A successful Task output is visible through the legacy source projection,
	// but is not a row in the native uploads table and cannot acquire that tag.
	_, err := h.db.Exec(`INSERT INTO generation_tasks (id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES ('source-task','owner-a',?,'text-to-3d','{}','{}','old-hash','SUCCEEDED',1,'2026-09-23T00:00:00Z','2026-09-23T00:00:00Z')`, h.command.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Exec(`INSERT INTO generation_attempts (id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('source-attempt','source-task',1,'{}','SUCCEEDED','old-key','2026-09-23T00:00:00Z','2026-09-23T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Exec(`INSERT INTO generation_assets (id,task_id,attempt_id,role,variant,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at) VALUES ('task-output','source-task','source-attempt','model','glb',1,0,?,'model/gltf-binary',?,?,'2026-09-23T00:00:00Z')`, h.asset.StorageKey, h.asset.Size, h.asset.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	selected := h.command.Options.Harness.Selection
	selected.Sources[0].AssetID = "task-output"
	if _, err = h.resolver.Update(ctx, "owner-a", h.command.SessionID, selected.Revision, selected); !schema.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("Task relabeled: %v", err)
	}
	if h.kernel.prepares != 0 || h.kernel.submits != 0 || h.count(t, "generation_tasks") != 1 || h.count(t, "execution_grants") != 0 {
		t.Fatal("relabel attempt created paid work")
	}
}

// Fail only the native read port; preparation remains the real Module and must
// never run on either a source rejection or an infrastructure read failure.
type failingUploadedInspector struct{ err error }

func (f failingUploadedInspector) Inspect(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, modelinput.Profile, error) {
	return generation.InputAsset{}, modelinput.Profile{}, f.err
}
func (f failingUploadedInspector) InspectForParameter(ctx context.Context, owner generation.OwnerID, id generation.AssetID, _ generation.Parameter) (generation.InputAsset, modelinput.Profile, error) {
	return f.Inspect(ctx, owner, id)
}

func TestUploadedModelHarnessPreservesInfrastructureFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		refusal bool
	}{
		{"stale", harness.ErrStale, true}, {"invalid_snapshot", harness.ErrInvalid, true},
		{"not_found", &generation.Error{Code: generation.ErrNotFound}, true},
		{"invalid_profile", &generation.Error{Code: generation.ErrInvalidModelInput}, true},
		{"unsupported_profile", &generation.Error{Code: generation.ErrUnsupportedModelInput}, true},
		{"profile_limit", &generation.Error{Code: generation.ErrModelInputLimit}, true},
		{"conflict", &generation.Error{Code: generation.ErrConflict}, true},
		{"database_io", errors.New("synthetic read failure"), false},
		{"unavailable", &generation.Error{Code: generation.ErrUnavailable}, false},
		{"canceled", context.Canceled, false}, {"deadline", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newUploadedHarness(t)
			h.resolver.UploadedModels = failingUploadedInspector{err: tc.err}
			result, err := h.executor.Execute(context.Background(), h.command, nil)
			if !result.NoSubmission || (tc.refusal && err != nil) || (!tc.refusal && !errors.Is(err, tc.err)) {
				t.Fatalf("failure incorrectly classified: %+v %v", result, err)
			}
			if h.kernel.prepares != 0 || h.kernel.submits != 0 || h.count(t, "generation_tasks") != 0 || h.count(t, "execution_grants") != 0 {
				t.Fatal("failure created paid work")
			}
		})
	}
}
