package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// This synthetic fixture tests observation persistence, not the qualification of
// a production Offering or the rigging/animation fidelity of its model bytes.
func TestNativeProcessingSameProgressCreditEvidenceSurvivesSQLiteRestart(t *testing.T) {
	runNativeProcessingCreditPersistence(t, false)
}

func TestNativeProcessingExpiredLeaseCannotWritePrivateCheckpoint(t *testing.T) {
	runNativeProcessingCreditPersistence(t, true)
}

func runNativeProcessingCreditPersistence(t *testing.T, expireLease bool) {
	t.Helper()
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 10, 16, 4, 0, 0, 0, time.UTC)}
	var posts, polls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/models/texture":
			posts.Add(1)
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_native_processed"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v3/tasks/task_native_processed":
			round := polls.Add(1)
			if expireLease && round == 2 {
				// The response returns after the real repository lease expired.
				clock.Advance(2 * time.Minute)
			}
			status, progress, credits := "running", 50, ""
			if round == 2 || round == 3 {
				credits = `,"credits_consumed":3.75`
			}
			if round >= 4 {
				status, progress = "success", 100
			}
			fmt.Fprintf(w, `{"code":0,"data":{"task_id":"task_native_processed","status":%q,"progress":%d%s,"output":{"model_url":%q}}}`, status, progress, credits, "http://"+r.Host+"/model.glb")
		case r.Method == http.MethodGet && r.URL.Path == "/model.glb":
			w.Header().Set("Content-Type", "model/gltf-binary")
			w.Write(validGLB("native credits fixture"))
		default:
			t.Errorf("unexpected native fixture request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	upstreamURL, _ := url.Parse(server.URL)
	one := 1
	source := generation.Offering{ID: "tripo/native/text-to-3d", Revision: "v1", ProviderID: "tripo", ModelID: "native", Capability: generation.TextTo3D, Domain: generation.DomainModel3D, AdapterKey: "source.fixture", Enabled: true, Timeout: time.Hour, Retry: generation.RetryPolicy{MaxAttempts: 1}, MaterializeAttempts: 3, Outputs: []generation.AssetRequirement{{Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}, Minimum: &one, MaxBytes: 1 << 20}}}
	processing := generation.Offering{ID: "tripo/processing/texture", Revision: "credits-fixture", ProviderID: "tripo", ModelID: "native-texture", Capability: generation.Retexture, Domain: generation.DomainProcessing, AdapterKey: "native.fixture", Enabled: true, CreditsMetered: true, Timeout: time.Hour, PollInterval: time.Second, Retry: generation.RetryPolicy{MaxAttempts: 1}, MaterializeAttempts: 3, AssetHosts: []string{upstreamURL.Hostname()}, Parameters: []generation.Parameter{{Name: "source_task", Type: "task", Required: true, Semantic: generation.SemanticSourceTask, WireName: "input", TaskSourceKind: schema.TaskSourceUpstreamJob, SameProvider: true, TaskCapabilities: []generation.Capability{source.Capability}, TaskOfferings: []generation.OfferingID{source.ID}}}, Outputs: source.Outputs, ProviderOptions: json.RawMessage(`{"endpoint":"models/texture","observe_endpoint":"tasks/{task_id}","response_aliases":[{"key":"model_url","role":"model","variant":"glb","fields":["model_url"]}]}`)}
	if err := tripo.ValidateOffering(processing); err != nil {
		t.Fatal(err)
	}
	catalog := rigReportCatalog{[]generation.Offering{source, processing}}
	root := t.TempDir()
	dbPath := filepath.Join(root, "native-credits.db")
	var repository *sqlite.Repository
	var module *generation.Module
	open := func() {
		var err error
		repository, err = sqlite.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		repository.SetClock(clock.Now)
		store, err := local.New(filepath.Join(root, "assets"))
		if err != nil {
			t.Fatal(err)
		}
		adapter, err := tripo.New(tripo.Config{BaseURL: server.URL + "/v3", APIKey: "fixture", HTTPClient: server.Client(), AllowHTTPAssets: true, StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]generation.StatusRule{"running": {Phase: "running"}, "success": {Phase: "completed"}}}})
		if err != nil {
			t.Fatal(err)
		}
		registry := providers.NewRegistry()
		if err = registry.Register(source.AdapterKey, rigReportSourcePort{}); err != nil {
			t.Fatal(err)
		}
		if err = registry.Register(processing.AdapterKey, adapter); err != nil {
			t.Fatal(err)
		}
		module, err = generation.New(generation.Dependencies{Repository: repository, Catalog: catalog, Providers: registry, Assets: store, Clock: clock, SchemaVersion: "native-credits-fixture"})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { module.Close() }()
	reconcile := func() {
		if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "native-credits", LeaseFor: time.Minute, BatchSize: 4}); err != nil {
			t.Fatal(err)
		}
		clock.Advance(2 * time.Second)
	}
	state := submitFixtureState{}
	acceptedSource, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "native-owner", Capability: source.Capability, OfferingID: source.ID, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		reconcile()
	}
	sourceTask, err := module.Get(ctx, "native-owner", acceptedSource.TaskID)
	if err != nil || sourceTask.State != generation.TaskSucceeded {
		t.Fatalf("source not delivered %+v %v", sourceTask, err)
	}
	input, _ := json.Marshal(map[string]string{"source_task": string(sourceTask.ID)})
	accepted, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "native-owner", Capability: processing.Capability, OfferingID: processing.ID, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	load := func() generation.Aggregate {
		aggregate, err := repository.Load(ctx, accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		return aggregate
	}
	reconcile() // One POST, then a running/50 observation without billing evidence.
	reconcile()
	before := load()
	if before.Task.Progress != 50 || bytes.Contains(before.CurrentAttempt.Opaque, []byte(`"consumed_credit"`)) {
		t.Fatalf("unexpected first observation %+v", before.CurrentAttempt)
	}
	reconcile() // Still running/50; new credits must bypass observation deduplication.
	billed := load()
	if expireLease {
		if billed.Task.Version != before.Task.Version || billed.Task.State != before.Task.State ||
			billed.Task.Progress != before.Task.Progress ||
			billed.CurrentAttempt.LastObservationKey != before.CurrentAttempt.LastObservationKey ||
			!bytes.Equal(billed.CurrentAttempt.Opaque, before.CurrentAttempt.Opaque) || posts.Load() != 1 || polls.Load() != 2 {
			t.Fatalf("expired lease wrote private evidence or public state: before=%+v after=%+v", before, billed)
		}
		return
	}
	if !bytes.Contains(billed.CurrentAttempt.Opaque, []byte(`"consumed_credit":3.75`)) || billed.CurrentAttempt.LastObservationKey == before.CurrentAttempt.LastObservationKey {
		t.Fatalf("same-progress billing evidence discarded: %s", billed.CurrentAttempt.Opaque)
	}
	if err := module.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	restarted := load()
	if !bytes.Equal(restarted.CurrentAttempt.Opaque, billed.CurrentAttempt.Opaque) {
		t.Fatal("SQLite restart lost native checkpoint")
	}
	reconcile() // Unchanged merged billing evidence keeps a stable dedupe key.
	repeated := load()
	if repeated.CurrentAttempt.LastObservationKey != billed.CurrentAttempt.LastObservationKey {
		t.Fatal("unchanged credit evidence was not deduplicated")
	}
	reconcile() // Success omits credits; previously persisted evidence must survive.
	for range 3 {
		aggregate := load()
		if aggregate.Task.State.Terminal() {
			break
		}
		reconcile()
	}
	completed := load()
	if completed.Task.State != generation.TaskSucceeded || !bytes.Contains(completed.CurrentAttempt.Opaque, []byte(`"consumed_credit":3.75`)) {
		t.Fatalf("completed state/evidence %s %s", completed.Task.State, completed.CurrentAttempt.Opaque)
	}
	if posts.Load() != 1 || polls.Load() != 4 {
		t.Fatalf("restart replayed or polled unexpectedly: posts=%d polls=%d", posts.Load(), polls.Load())
	}
}
