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
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/printreport"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

type printReportCatalog struct{ offering generation.Offering }

func (c printReportCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	return c.offering, id == c.offering.ID
}
func (c printReportCatalog) List(cap generation.Capability) []generation.Offering {
	if cap == "" || cap == c.offering.Capability {
		return []generation.Offering{c.offering}
	}
	return nil
}
func (c printReportCatalog) Normalize(cap generation.Capability, id generation.OfferingID, raw json.RawMessage) (generation.Offering, json.RawMessage, error) {
	if id != c.offering.ID || cap != c.offering.Capability || !bytes.Equal(bytes.TrimSpace(raw), []byte(`{}`)) {
		return generation.Offering{}, nil, &generation.Error{Code: generation.ErrInvalidInput}
	}
	return c.offering, json.RawMessage(`{}`), nil
}

type reportFailOnceStore struct {
	generation.AssetStore
	failed bool
}

func (s *reportFailOnceStore) Close() error {
	if closer, ok := s.AssetStore.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (s *reportFailOnceStore) Put(ctx context.Context, w generation.AssetWrite) (generation.Asset, error) {
	if w.MediaType == printreport.MediaType && !s.failed {
		s.failed = true
		return generation.Asset{}, errors.New("private storage failure")
	}
	return s.AssetStore.Put(ctx, w)
}

func TestPrintReportRealSQLiteRestartCASAndOwnedDelivery(t *testing.T) {
	for _, kind := range []string{"analysis-error", "analysis-unknown", "repair-not-reported", "repair-reported"} {
		t.Run(kind, func(t *testing.T) { runPrintReport(t, kind, false) })
	}
}
func TestPrintReportMissingRequiredReportNeverClaimsSuccess(t *testing.T) {
	runPrintReport(t, "analysis-missing-report", false)
}

func TestPrintReportMissingRequiredModelNeverClaimsSuccess(t *testing.T) {
	runPrintReport(t, "repair-missing-model", false)
}

func TestPrintReportCASFailureNeverRepeatsPaidSubmission(t *testing.T) {
	runPrintReport(t, "repair-reported", true)
}

func runPrintReport(t *testing.T, kind string, failOnce bool) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "reports.db")
	casPath := filepath.Join(root, "cas")
	repair := strings.HasPrefix(kind, "repair")
	endpoint := "print/analyze"
	reportKind := "analysis"
	variant := "printability.v1.json"
	if repair {
		endpoint = "print/repair"
		reportKind = "repair"
		variant = "print-repair.v1.json"
	}
	var posts, gets, downloads atomic.Int64
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/openapi/v1/"+endpoint:
			posts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":"private-remote"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/openapi/v1/"+endpoint+"/private-remote":
			gets.Add(1)
			doc := map[string]any{"id": "private-remote", "status": "SUCCEEDED", "progress": 100, "ignored": "https://secret.example/PRIVATE_TOKEN"}
			if repair {
				doc["model_urls"] = map[string]string{"glb": upstream.URL + "/model.glb"}
				if kind == "repair-missing-model" {
					doc["model_urls"] = map[string]string{}
				}
				if kind == "repair-reported" {
					doc["print_repair_report"] = map[string]any{"route": "thin_wall", "applied": false, "faces_before": 1, "faces_after": 1, "thin_area_ratio_before": 0.08, "thin_area_ratio_after": 0.08}
				}
			} else if kind == "analysis-missing-report" {
				// Completed without the declared report must fail, not invent healthy values.
			} else if kind == "analysis-unknown" {
				doc["printability"] = map[string]any{"_version": "v1", "status": "unknown"}
			} else {
				doc["printability"] = map[string]any{"_version": "v1", "status": "error", "issue_count": 1, "error_count": 1, "warning_count": 0, "metrics": map[string]any{"is_watertight": false, "volume": -2.5, "non_manifold_edges": 0, "degenerate_faces": 0, "holes": 0}}
			}
			_ = json.NewEncoder(w).Encode(doc)
		case r.Method == http.MethodGet && r.URL.Path == "/model.glb":
			downloads.Add(1)
			w.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = w.Write(validGLB("glb"))
		default:
			t.Errorf("unexpected HTTP %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	host, _ := url.Parse(upstream.URL)
	one := 1
	options := map[string]any{"endpoint": endpoint, "api_version": "v1", "result_reports": []map[string]string{{"kind": reportKind, "role": "report", "variant": variant}}}
	outputs := []generation.AssetRequirement{{Role: "report", Variant: variant, MediaTypes: []string{printreport.MediaType}, Minimum: &one, MaxBytes: 32 << 10}}
	if repair {
		options["result_assets"] = []map[string]string{{"path": "model_urls", "role": "model", "mode": "map"}}
		outputs = append(outputs, generation.AssetRequirement{Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}, Minimum: &one, MaxBytes: 1 << 20})
	}
	privateOptions, _ := json.Marshal(options)
	offering := generation.Offering{ID: "test/print-report", Revision: "report-v1", ProviderID: "meshy", ModelID: "report", Capability: generation.Remesh, Domain: generation.DomainModel3D, AdapterKey: "meshy.report.test", Enabled: true, Timeout: time.Minute, PollInterval: time.Second, MaterializeAttempts: 3, Retry: generation.RetryPolicy{MaxAttempts: 1}, Outputs: outputs, AssetHosts: []string{host.Hostname()}, ProviderOptions: privateOptions, CreditsMetered: repair, EstimatedCredits: 0}
	if repair {
		offering.EstimatedCredits = 10
	}
	if err := meshy.ValidateOffering(offering); err != nil {
		t.Fatal(err)
	}
	adapter, err := meshy.New(meshy.Config{BaseURL: upstream.URL, APIKey: "test-key", HTTPClient: upstream.Client(), AllowHTTPAssets: true, StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]generation.StatusRule{"succeeded": {Phase: "completed"}}}})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register(offering.AdapterKey, adapter); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)}
	var module *generation.Module
	var repository *sqlite.Repository
	var store generation.AssetStore
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
		store = blobs
		if failOnce {
			store = &reportFailOnceStore{AssetStore: store}
		}
		module, err = generation.New(generation.Dependencies{Repository: repository, Catalog: printReportCatalog{offering}, Providers: registry, Assets: store, Clock: clock, SchemaVersion: "report-test"})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { _ = module.Close() }()
	state := submitFixtureState{}
	accepted, err := state.submitApproved(t, ctx, module, repository, clock, generation.SubmitCommand{OwnerID: "report-owner", Capability: offering.Capability, OfferingID: offering.ID, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	reconcile := func() {
		if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "report-worker", LeaseFor: time.Minute, BatchSize: 1}); err != nil {
			t.Fatal(err)
		}
		clock.Advance(31 * time.Second)
	}
	reconcile()
	reconcile()
	aggregate, err := repository.Load(ctx, accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "analysis-missing-report" {
		if aggregate.Task.State != generation.TaskFailed || posts.Load() != 1 || len(aggregate.Claims) != 0 || len(aggregate.Task.Assets) != 0 {
			t.Fatalf("missing report incorrectly completed: %+v posts=%d", aggregate, posts.Load())
		}
		return
	}
	if aggregate.Task.State != generation.TaskMaterializing || aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.State != generation.AttemptUpstreamSucceeded || len(aggregate.Claims) == 0 {
		t.Fatalf("no durable materialization boundary: %+v", aggregate)
	}
	var claim generation.ArtifactClaim
	for _, candidate := range aggregate.Claims {
		if candidate.Role == "report" {
			claim = candidate
		}
	}
	normalized := append([]byte{}, claim.Inline...)
	if _, err := printreport.Decode(normalized); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-remote", "PRIVATE_TOKEN", "secret.example"} {
		if bytes.Contains(normalized, []byte(secret)) || bytes.Contains(claim.Metadata, []byte(secret)) || bytes.Contains(aggregate.CurrentAttempt.Opaque, []byte(secret)) {
			t.Fatalf("durable report leaked %q", secret)
		}
	}
	originalAttempt := aggregate.CurrentAttempt.ID
	if err := module.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	var task generation.Task
	for i := 0; i < 12; i++ {
		reconcile()
		task, err = module.Get(ctx, "report-owner", accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			break
		}
	}
	if kind == "repair-missing-model" {
		if task.State != generation.TaskFailed || posts.Load() != 1 || task.Failure == nil || task.Failure.Class != generation.FailureIntegrity {
			t.Fatalf("missing model incorrectly completed: %+v posts=%d", task, posts.Load())
		}
		return
	}
	if task.State != generation.TaskSucceeded || len(task.Assets) != len(outputs) {
		t.Fatalf("report not delivered state=%s failure=%+v assets=%v", task.State, task.Failure, task.Assets)
	}
	if posts.Load() != 1 {
		t.Fatalf("paid submission repeated %d times", posts.Load())
	}
	aggregate, err = repository.Load(ctx, task.ID)
	if err != nil || aggregate.CurrentAttempt.ID != originalAttempt || aggregate.CurrentAttempt.Ordinal != 1 || len(aggregate.Claims) != 0 {
		t.Fatalf("restart changed attempt or retained claims: %v %+v", err, aggregate)
	}
	for _, asset := range task.Assets {
		if !asset.Required {
			t.Fatalf("required output not READY: %+v", asset)
		}
		for read := 0; read < 2; read++ {
			content, err := module.OpenAsset(ctx, "report-owner", task.ID, asset.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(content.Body)
			_ = content.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			if int64(len(raw)) != asset.Size || hex.EncodeToString(sum[:]) != asset.SHA256 || content.MediaType != asset.MediaType {
				t.Fatal("delivery identity changed")
			}
			if asset.Role == "report" {
				if !bytes.Equal(raw, normalized) || asset.MediaType != printreport.MediaType || !strings.HasSuffix(asset.StorageKey, ".json") {
					t.Fatal("report delivery bytes/MIME changed")
				}
			}
		}
		if _, err := module.OpenAsset(ctx, "foreign-owner", task.ID, asset.ID); !generation.IsCode(err, generation.ErrNotFound) {
			t.Fatalf("foreign report readable: %v", err)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var attempts int
	if err := db.QueryRow("SELECT COUNT(*) FROM generation_attempts WHERE task_id=?", task.ID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("attempt count %d err=%v", attempts, err)
	}
	for _, table := range []string{"generation_tasks", "generation_prepared_inputs", "execution_grants"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE owner_id=?", "report-owner").Scan(&count); err != nil || count != 1 {
			t.Fatalf("unexpected %s count %d err=%v", table, count, err)
		}
	}
	var plans int
	if err := db.QueryRow("SELECT COUNT(*) FROM processing_plans").Scan(&plans); err != nil || plans != 0 {
		t.Fatalf("unexpected plans %d err=%v", plans, err)
	}
	if failOnce && gets.Load() < 2 {
		t.Fatal("CAS retry did not refresh the original upstream job")
	}
	if repair && downloads.Load() == 0 {
		t.Fatal("repair model was not materialized")
	}
}
