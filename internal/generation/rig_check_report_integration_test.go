package generation_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/rigcheckreport"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type rigReportCatalog struct{ offerings []generation.Offering }

func (c rigReportCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	for _, o := range c.offerings {
		if o.ID == id {
			return o, true
		}
	}
	return generation.Offering{}, false
}
func (c rigReportCatalog) List(cap generation.Capability) []generation.Offering {
	var result []generation.Offering
	for _, o := range c.offerings {
		if cap == "" || o.Capability == cap {
			result = append(result, o)
		}
	}
	return result
}
func (c rigReportCatalog) Normalize(cap generation.Capability, id generation.OfferingID, raw json.RawMessage) (generation.Offering, json.RawMessage, error) {
	o, ok := c.Get(id)
	var input map[string]json.RawMessage
	if !ok || o.Capability != cap || json.Unmarshal(raw, &input) != nil || input == nil {
		return generation.Offering{}, nil, &generation.Error{Code: generation.ErrInvalidInput}
	}
	normalized, _ := json.Marshal(input)
	return o, normalized, nil
}

type rigReportSourcePort struct{}

func (rigReportSourcePort) Start(_ context.Context, r generation.StartRequest) (generation.Observation, error) {
	remoteID := "task_native_source"
	if r.Offering.ID != "tripo/native/text-to-3d" {
		remoteID = "task_native_" + string(r.TaskID)
	}
	return generation.Observation{Phase: generation.UpstreamCompleted, Job: &generation.UpstreamJob{ProviderID: r.Offering.ProviderID, RemoteID: remoteID}, Progress: 100, Artifacts: []generation.ArtifactClaim{{ID: string(r.AttemptID) + ":native-model", Role: "model", Variant: "glb", MediaType: "model/gltf-binary", Inline: validGLB("glb")}}}, nil
}
func (rigReportSourcePort) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, errors.New("source is synchronous fixture")
}
func (rigReportSourcePort) OpenAsset(_ context.Context, r generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(r.Claim.Inline)), generation.AssetMetadata{MediaType: r.Claim.MediaType}, nil
}

type rigReportStore struct {
	generation.AssetStore
	fail bool
}

func (s *rigReportStore) Close() error { return s.AssetStore.(interface{ Close() error }).Close() }
func (s *rigReportStore) Put(ctx context.Context, w generation.AssetWrite) (generation.Asset, error) {
	if s.fail && w.MediaType == rigcheckreport.MediaType {
		s.fail = false
		return generation.Asset{}, errors.New("fixture CAS temporarily unavailable")
	}
	return s.AssetStore.Put(ctx, w)
}

type rigDropReport struct{ generation.ProviderPort }

func (p rigDropReport) ValidateInput(ctx context.Context, r generation.InputValidationRequest) error {
	return p.ProviderPort.(generation.ProviderInputValidator).ValidateInput(ctx, r)
}
func (p rigDropReport) Observe(ctx context.Context, r generation.ObserveRequest) (generation.Observation, error) {
	observation, err := p.ProviderPort.Observe(ctx, r)
	if observation.Phase == generation.UpstreamCompleted {
		observation.Artifacts = nil
	}
	return observation, err
}
func TestRigCheckReportSQLiteRestartOwnedCASAndNoReplay(t *testing.T) {
	for _, kind := range []string{"false", "avian", "cas-failure", "missing-report", "missing-output", "missing-rig-type-true", "missing-rig-type-false", "unknown-post"} {
		t.Run(kind, func(t *testing.T) { runRigReport(t, kind) })
	}
}
func runRigReport(t *testing.T, kind string) {
	t.Helper()
	ctx := context.Background()
	invalidOutput := kind == "missing-output" || strings.HasPrefix(kind, "missing-rig-type-")
	root := t.TempDir()
	dbPath := filepath.Join(root, "rig.db")
	casPath := filepath.Join(root, "cas")
	var posts, gets atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v3/animations/rig-check":
			posts.Add(1)
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != `{"input":"task_native_source"}` {
				t.Errorf("unexpected native request %s", raw)
			}
			if kind == "unknown-post" {
				w.WriteHeader(502)
				return
			}
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_rig_check"}}`)
		case r.Method == "GET" && r.URL.Path == "/v3/tasks/task_rig_check":
			gets.Add(1)
			output := `{"riggable":false,"rig_type":"quadruped","private_url":"https://secret.example/TOKEN"}`
			if kind == "avian" {
				output = `{"riggable":true,"rig_type":"avian"}`
			}
			if strings.HasPrefix(kind, "missing-rig-type-") {
				output = `{"riggable":` + strings.TrimPrefix(kind, "missing-rig-type-") + `}`
			}
			if kind == "missing-output" {
				output = `{}`
			}
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_rig_check","status":"success","progress":100,"output":`+output+`}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	one := 1
	source := generation.Offering{ID: "tripo/native/text-to-3d", Revision: "v1", ProviderID: "tripo", ModelID: "native", Capability: generation.TextTo3D, Domain: generation.DomainModel3D, AdapterKey: "source.fixture", Enabled: true, Timeout: time.Minute, Retry: generation.RetryPolicy{MaxAttempts: 1}, MaterializeAttempts: 3, Outputs: []generation.AssetRequirement{{Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}, Minimum: &one, MaxBytes: 1 << 20}}}
	check := generation.Offering{ID: "tripo/processing/rig-check", Revision: "v1", ProviderID: "tripo", ModelID: "tripo-rig-check", Capability: schema.RigCheck, Domain: generation.DomainProcessing, AdapterKey: "tripo.fixture", Enabled: true, CreditsMetered: true, Timeout: time.Minute, PollInterval: time.Second, Retry: generation.RetryPolicy{MaxAttempts: 1}, MaterializeAttempts: 3, Parameters: []generation.Parameter{{Name: "source_task", Type: "task", Required: true, Semantic: generation.SemanticSourceTask, WireName: "input", TaskSourceKind: schema.TaskSourceUpstreamJob, SameProvider: true, TaskCapabilities: []generation.Capability{generation.TextTo3D}, TaskOfferings: []generation.OfferingID{source.ID}}}, Outputs: []generation.AssetRequirement{{Role: "other", Variant: "report", MediaTypes: []string{rigcheckreport.MediaType}, Minimum: &one, MaxBytes: rigcheckreport.MaxBytes}}, ProviderOptions: json.RawMessage(`{"endpoint":"animations/rig-check","observe_endpoint":"tasks/{task_id}"}`)}
	if err := tripo.ValidateOffering(check); err != nil {
		t.Fatal(err)
	}
	catalog := rigReportCatalog{[]generation.Offering{source, check}}
	for _, identity := range []struct {
		id         generation.OfferingID
		provider   string
		capability generation.Capability
	}{
		{"other/native/text-to-3d", "other", generation.TextTo3D},
		{"tripo/unlisted/text-to-3d", "tripo", generation.TextTo3D},
		{"tripo/processing/conversion", "tripo", generation.FormatConversion},
	} {
		alternate := source
		alternate.ID = identity.id
		alternate.ProviderID = identity.provider
		alternate.Capability = identity.capability
		catalog.offerings = append(catalog.offerings, alternate)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC)}
	var repository *sqlite.Repository
	var module *generation.Module
	open := func() {
		var err error
		repository, err = sqlite.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		repository.SetClock(clock.Now)
		blobs, err := local.New(casPath)
		if err != nil {
			t.Fatal(err)
		}
		adapter, err := tripo.New(tripo.Config{BaseURL: server.URL + "/v3", APIKey: "test", HTTPClient: server.Client(), StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]generation.StatusRule{"success": {Phase: "completed"}}}})
		if err != nil {
			t.Fatal(err)
		}
		registry := providers.NewRegistry()
		if err := registry.Register(source.AdapterKey, rigReportSourcePort{}); err != nil {
			t.Fatal(err)
		}
		var port generation.ProviderPort = adapter
		if kind == "missing-report" {
			port = rigDropReport{port}
		}
		if err := registry.Register(check.AdapterKey, port); err != nil {
			t.Fatal(err)
		}
		module, err = generation.New(generation.Dependencies{Repository: repository, Catalog: catalog, Providers: registry, Assets: &rigReportStore{AssetStore: blobs, fail: kind == "cas-failure"}, Clock: clock, SchemaVersion: "rig-report-test"})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { module.Close() }()
	reconcile := func() {
		if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "rig-report", LeaseFor: time.Minute, BatchSize: 4}); err != nil {
			t.Fatal(err)
		}
		clock.Advance(31 * time.Second)
	}
	state := submitFixtureState{}
	acceptedSource, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "rig-owner", Capability: source.Capability, OfferingID: source.ID, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		reconcile()
	}
	sourceTask, err := module.Get(ctx, "rig-owner", acceptedSource.TaskID)
	if err != nil || sourceTask.State != generation.TaskSucceeded {
		t.Fatalf("source not delivered %v %+v", err, sourceTask)
	}
	input, _ := json.Marshal(map[string]string{"source_task": string(sourceTask.ID)})
	// A successful native source belonging to someone else is rejected before any POST.
	if _, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "foreign-owner", Capability: check.Capability, OfferingID: check.ID, Input: input}); !generation.IsCode(err, generation.ErrInvalidInput) || posts.Load() != 0 {
		t.Fatalf("foreign source accepted %v", err)
	}
	if kind == "false" {
		for _, candidate := range catalog.offerings[2:] {
			native, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "rig-owner", Capability: candidate.Capability, OfferingID: candidate.ID, Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 4; i++ {
				reconcile()
			}
			incompatible, _ := json.Marshal(map[string]string{"source_task": string(native.TaskID)})
			if _, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "rig-owner", Capability: check.Capability, OfferingID: check.ID, Input: incompatible}); !generation.IsCode(err, generation.ErrInvalidInput) || posts.Load() != 0 {
				t.Fatalf("incompatible actual source admitted %s %v", candidate.ID, err)
			}
		}
	}
	accepted, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "rig-owner", Capability: check.Capability, OfferingID: check.ID, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	reconcile()
	if kind != "unknown-post" {
		reconcile()
	}
	aggregate, err := repository.Load(ctx, accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	originalAttempt := aggregate.CurrentAttempt.ID
	var normalized []byte
	if !invalidOutput && kind != "unknown-post" {
		if aggregate.Task.State != generation.TaskMaterializing {
			t.Fatalf("no durable boundary state=%s failure=%+v", aggregate.Task.State, aggregate.Task.Failure)
		}
		if kind != "missing-report" {
			if len(aggregate.Claims) != 1 {
				t.Fatal("missing persisted inline claim")
			}
			normalized = append([]byte(nil), aggregate.Claims[0].Inline...)
			report, err := rigcheckreport.Decode(normalized)
			expectedRigType := "quadruped"
			if kind == "avian" {
				expectedRigType = "avian"
			}
			if err != nil || report.Riggable != (kind == "avian") || report.RigType != expectedRigType {
				t.Fatalf("bad durable report %s %v", normalized, err)
			}
			if strings.Contains(string(normalized), "TOKEN") || strings.Contains(string(normalized), "task_") {
				t.Fatal("private identity escaped report")
			}
		}
	}
	if err := module.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	var task generation.Task
	for i := 0; i < 14; i++ {
		reconcile()
		task, err = module.Get(ctx, "rig-owner", accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			break
		}
	}
	if posts.Load() != 1 {
		t.Fatalf("POST repeated %d times", posts.Load())
	}
	if invalidOutput || kind == "missing-report" || kind == "unknown-post" {
		if task.State != generation.TaskFailed || len(task.Assets) != 0 {
			t.Fatalf("invalid result claimed success %+v", task)
		}
		if kind == "unknown-post" && (task.Failure == nil || task.Failure.Class != generation.FailureSubmissionUnknown) {
			t.Fatalf("unknown submission misclassified %+v", task.Failure)
		}
	} else {
		if task.State != generation.TaskSucceeded || len(task.Assets) != 1 {
			t.Fatalf("report delivery incomplete %+v", task)
		}
		if task.Offering.EstimatedCredits != 0 || !task.Offering.CreditsMetered {
			t.Fatal("explicit zero-credit estimate was lost")
		}
		reportSource, _ := json.Marshal(map[string]string{"source_task": string(task.ID)})
		if _, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "rig-owner", Capability: check.Capability, OfferingID: check.ID, Input: reportSource}); !generation.IsCode(err, generation.ErrInvalidInput) {
			t.Fatalf("report accepted as native source: %v", err)
		}
		asset := task.Assets[0]
		if !asset.Required || asset.Role != "other" || asset.Variant != "report" || asset.MediaType != rigcheckreport.MediaType || !strings.HasSuffix(asset.StorageKey, ".json") {
			t.Fatalf("wrong output identity %+v", asset)
		}
		for i := 0; i < 2; i++ {
			content, err := module.OpenAsset(ctx, "rig-owner", task.ID, asset.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(content.Body)
			content.Body.Close()
			sum := sha256.Sum256(raw)
			if err != nil || !bytes.Equal(raw, normalized) || int64(len(raw)) != asset.Size || hex.EncodeToString(sum[:]) != asset.SHA256 {
				t.Fatal("authenticated report bytes changed")
			}
		}
		if _, err := module.OpenAsset(ctx, "foreign-owner", task.ID, asset.ID); !generation.IsCode(err, generation.ErrNotFound) {
			t.Fatalf("foreign report readable %v", err)
		}
		if kind == "cas-failure" && gets.Load() < 2 {
			t.Fatal("CAS retry did not refresh original remote ID")
		}
	}
	aggregate, err = repository.Load(ctx, task.ID)
	if err != nil || aggregate.CurrentAttempt.ID != originalAttempt || aggregate.CurrentAttempt.Ordinal != 1 {
		t.Fatalf("attempt changed after restart %v %+v", err, aggregate.CurrentAttempt)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var attempts int
	if err := db.QueryRow("SELECT COUNT(*) FROM generation_attempts WHERE task_id=?", task.ID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("unexpected attempts %d %v", attempts, err)
	}
}
