package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

func TestTripoPrivateConversionsSurviveSourceRegression(t *testing.T) {
	// The blocking GLTF conversion carries the primary model and is the only
	// private child before success (#462); the five requested formats are
	// converted after the task is already SUCCEEDED.
	requested := []string{"USDZ", "FBX", "OBJ", "STL", "3MF"}
	all := append([]string{"GLTF"}, requested...)
	for _, restart := range []bool{false, true} {
		name := "without_restart"
		if restart {
			name = "after_sqlite_restart"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &tripoCheckpointServer{}
			server := httptest.NewTLSServer(http.HandlerFunc(upstream.serveHTTP))
			upstream.server = server
			t.Cleanup(server.Close)
			adapter := newTripoCheckpointAdapter(t, server)
			dbPath := filepath.Join(t.TempDir(), "tripo-checkpoint.db")
			h := newCheckpointHarnessWithCatalog(t, adapter, dbPath, nil,
				tripoCheckpointCatalog(t, checkpointFixtureHost(server.URL)))
			accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
				OwnerID:    "owner-dispatch",
				SessionID:  "tripo-checkpoint-session",
				Capability: generation.TextTo3D,
				OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
				Input:      []byte(`{"prompt":"synthetic checkpoint fixture","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`),
			})
			if err != nil {
				t.Fatal(err)
			}

			// Start once, then observe a successful source and submit GLTF.
			tripoCheckpointStep(t, h)
			tripoCheckpointStep(t, h)
			beforeRegression := tripoCheckpointAggregate(t, h, accepted.TaskID)
			gltf := tripoCheckpointConversion(t, beforeRegression.CurrentAttempt.Opaque, "GLTF")
			if gltf.Status != "submitted" || gltf.RemoteID != "synthetic-child-gltf-1" {
				t.Fatalf("GLTF checkpoint = %+v; want submitted synthetic child 1", gltf)
			}

			// Confirm GLTF is done before the source regresses to running/99.
			tripoCheckpointStep(t, h)
			beforeRegression = tripoCheckpointAggregate(t, h, accepted.TaskID)
			gltf = tripoCheckpointConversion(t, beforeRegression.CurrentAttempt.Opaque, "GLTF")
			if gltf.Status != "done" || gltf.RemoteID != "synthetic-child-gltf-1" {
				t.Fatalf("completed GLTF checkpoint = %+v; want done with the original remote ID", gltf)
			}

			tripoCheckpointStep(t, h)
			afterRegression := tripoCheckpointAggregate(t, h, accepted.TaskID)
			if len(strings.TrimSpace(string(afterRegression.CurrentAttempt.Opaque))) == 0 {
				t.Fatal("completed GLTF private checkpoint was cleared after source status regressed to running/99")
			}
			regressedGLTF := tripoCheckpointConversion(t, afterRegression.CurrentAttempt.Opaque, "GLTF")
			if regressedGLTF != gltf {
				t.Fatalf("GLTF checkpoint after source regression = %+v; want unchanged %+v", regressedGLTF, gltf)
			}
			if got := upstream.formatPostCount("GLTF"); got != 1 {
				t.Fatalf("GLTF conversion POSTs after source regression = %d; want 1", got)
			}
			if got := upstream.totalFormatPostCount(); got != 1 {
				t.Fatalf("private conversion POSTs after source regression = %d; want only the original GLTF POST", got)
			}

			if restart {
				h = h.restart(t, adapter)
				afterRestart := tripoCheckpointAggregate(t, h, accepted.TaskID)
				restartedGLTF := tripoCheckpointConversion(t, afterRestart.CurrentAttempt.Opaque, "GLTF")
				if restartedGLTF != gltf {
					t.Fatalf("GLTF checkpoint after SQLite reopen = %+v; want same identity %+v", restartedGLTF, gltf)
				}
			}

			// The next source success claims the primary model and the task
			// succeeds, with the requested formats still pending.
			var final generation.Aggregate
			for range 40 {
				tripoCheckpointStep(t, h)
				final = tripoCheckpointAggregate(t, h, accepted.TaskID)
				if final.Task.State == generation.TaskSucceeded {
					break
				}
			}
			if final.Task.State != generation.TaskSucceeded || final.Task.Failure != nil {
				t.Fatalf("task = %s/%+v; want SUCCEEDED with the primary model", final.Task.State, final.Task.Failure)
			}
			variants := map[string]bool{}
			for _, asset := range final.Task.Assets {
				variants[asset.Role+"/"+asset.Variant] = true
			}
			if !variants["model/"] || !variants["converted_model/converted.gltf"] {
				t.Fatalf("the primary model and its GLTF conversion are missing: %v", variants)
			}
			pending := 0
			for key, state := range final.Task.Resources {
				if state.State != generation.ResourcePending {
					continue
				}
				if requestedVariant(key, requested) {
					pending++
				}
			}
			if pending != len(requested) {
				t.Fatalf("pending requested slots = %d, want %d (%v)", pending, len(requested), final.Task.Resources)
			}

			// After success the requested formats are converted one private child
			// at a time, from the same upstream job.
			for range 200 {
				tripoCheckpointStep(t, h)
				final = tripoCheckpointAggregate(t, h, accepted.TaskID)
				if tripoCheckpointAllDone(t, final.CurrentAttempt.Opaque, all) {
					break
				}
			}
			if !tripoCheckpointAllDone(t, final.CurrentAttempt.Opaque, all) {
				t.Fatalf("private conversions did not all reach done: %s", final.CurrentAttempt.Opaque)
			}
			for _, format := range all {
				if got := upstream.formatPostCount(format); got != 1 {
					t.Errorf("%s conversion POSTs = %d; want exactly 1", format, got)
				}
			}
			if got := upstream.totalFormatPostCount(); got != len(all) {
				t.Errorf("private conversion POSTs = %d; want exactly %d", got, len(all))
			}
			if got := upstream.sourcePostCount(); got != 1 {
				t.Errorf("source generation POSTs = %d; want exactly 1", got)
			}
			// The claims become assets one round later: keep reconciling until
			// every requested slot is READY.
			ready := 0
			for range 40 {
				ready = 0
				for key, state := range final.Task.Resources {
					if requestedVariant(key, requested) && state.State == generation.ResourceReady {
						ready++
					}
				}
				if ready == len(requested) {
					break
				}
				tripoCheckpointStep(t, h)
				final = tripoCheckpointAggregate(t, h, accepted.TaskID)
			}
			// The task stays SUCCEEDED and every requested format is now READY.
			ready = 0
			for key, state := range final.Task.Resources {
				if !requestedVariant(key, requested) {
					continue
				}
				if state.State == generation.ResourceReady {
					ready++
				}
				if state.State == generation.ResourcePending {
					t.Errorf("slot still pending after every format landed: %v", final.Task.Resources)
				}
			}
			if ready != len(requested) {
				t.Fatalf("ready requested slots = %d, want %d (%v)", ready, len(requested), final.Task.Resources)
			}
		})
	}
}

// checkpointFixtureHost is the host part of the fixture's own URL: an asset host
// declaration holds a host, not a port.
func checkpointFixtureHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return parsed.Hostname()
}

// tripoCheckpointCatalog is the default Schema with the full-format Offering's
// asset hosts pointed at the loopback fixture: its locators are the only bytes
// the test can serve, and the kernel checks the host against the frozen
// declaration.
func tripoCheckpointCatalog(t *testing.T, host string) *catalog.Catalog {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &document); err != nil {
		t.Fatal(err)
	}
	offerings, _ := document["offerings"].([]any)
	found := false
	for _, entry := range offerings {
		offering, _ := entry.(map[string]any)
		if offering["id"] != "tripo/tripo-v3.1-full/text-to-3d" {
			continue
		}
		offering["asset_hosts"] = []string{host}
		found = true
	}
	if !found {
		t.Fatal("default Catalog is missing the Tripo full-format Offering")
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// requestedVariant reports whether a resource-slot key is one of the requested
// formats ("converted_model|converted.fbx|").
func requestedVariant(key string, requested []string) bool {
	_, scope, _, err := generation.ParseResourceStateKey(key)
	if err != nil {
		return false
	}
	for _, format := range requested {
		if scope == "converted."+strings.ToLower(format) {
			return true
		}
	}
	return false
}

type tripoCheckpointServer struct {
	mu          sync.Mutex
	sourcePosts int
	baseGets    int
	formatPosts map[string]int
	// server is the TLS test server the fixture answers on: the kernel refuses
	// a plain-HTTP asset URL, so every locator it writes is its own HTTPS one.
	server *httptest.Server
}

// checkpointChildFormat reads the format back from a fixture child's remote id
// ("synthetic-child-stl-1" → "STL").
func checkpointChildFormat(remoteID string) string {
	rest := strings.TrimPrefix(remoteID, "synthetic-child-")
	if index := strings.LastIndex(rest, "-"); index > 0 {
		rest = rest[:index]
	}
	return strings.ToUpper(rest)
}

// checkpointFixtureFormat maps a fixture path to the fake's model fixture name.
func checkpointFixtureFormat(path string) string {
	switch {
	case strings.HasSuffix(path, ".fbx"):
		return "fbx"
	case strings.HasSuffix(path, ".zip"):
		return "obj"
	case strings.HasSuffix(path, ".stl"):
		return "stl"
	case strings.HasSuffix(path, ".usdz"):
		return "usdz"
	case strings.HasSuffix(path, ".3mf"):
		return "3mf"
	}
	return "glb"
}

func (s *tripoCheckpointServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/generation/text-to-model":
		s.sourcePosts++
		writeTripoCheckpointJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "synthetic-base"}})
	case r.Method == http.MethodPost && r.URL.Path == "/models/convert":
		var request struct {
			Format string `json:"format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Format == "" {
			http.Error(w, "invalid synthetic conversion request", http.StatusBadRequest)
			return
		}
		if s.formatPosts == nil {
			s.formatPosts = make(map[string]int)
		}
		s.formatPosts[request.Format]++
		remoteID := fmt.Sprintf("synthetic-child-%s-%d", strings.ToLower(request.Format), s.formatPosts[request.Format])
		writeTripoCheckpointJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": remoteID}})
	case r.Method == http.MethodGet && r.URL.Path == "/tasks/synthetic-base":
		s.baseGets++
		status, progress := "success", 100
		if s.baseGets == 3 {
			status, progress = "running", 99
		}
		writeTripoCheckpointJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"task_id": "synthetic-base", "status": status, "progress": progress,
			"output": map[string]any{"model_url": s.server.URL + "/synthetic-base.glb"},
		}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/synthetic-child."):
		// The converted file the fixture points at: the bytes must pass the
		// kernel's structural delivery check for the slot's variant, so every
		// format gets its own fixture.
		file, fileErr := fakeupstream.ModelFile(checkpointFixtureFormat(r.URL.Path))
		if fileErr != nil {
			http.Error(w, fileErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(file.Data)
	case r.Method == http.MethodGet && r.URL.Path == "/synthetic-base.glb":
		file, fileErr := fakeupstream.ModelFile("glb")
		if fileErr != nil {
			http.Error(w, fileErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "model/gltf-binary")
		_, _ = w.Write(file.Data)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/synthetic-child-"):
		remoteID := strings.TrimPrefix(r.URL.Path, "/tasks/")
		format := strings.ToLower(checkpointChildFormat(remoteID))
		writeTripoCheckpointJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"task_id": remoteID, "status": "success", "progress": 100,
			"output": map[string]any{"model_url": s.server.URL + "/synthetic-child." + format},
		}})
	default:
		http.NotFound(w, r)
	}
}

func (s *tripoCheckpointServer) sourcePostCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sourcePosts
}

func (s *tripoCheckpointServer) formatPostCount(format string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.formatPosts[format]
}

func (s *tripoCheckpointServer) totalFormatPostCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, count := range s.formatPosts {
		total += count
	}
	return total
}

func newTripoCheckpointAdapter(t *testing.T, server *httptest.Server) *tripo.Adapter {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !ok {
		t.Fatal("default Catalog is missing the Tripo full-format Offering")
	}
	// The fixture answers on the loopback TLS server, so that host is the only
	// asset host this Offering may download from. The frozen Offering of a Task
	// comes from the Catalog, so the harness gets a Catalog that declares it.
	offering.AssetHosts = []string{checkpointFixtureHost(server.URL)}
	var errorMapping *generation.ErrorMapping
	var statusMapping *generation.StatusMapping
	for _, connection := range c.Application().Runtime.ProviderConnections {
		if connection.AdapterKey == "tripo.v3" {
			errorMapping = connection.ErrorMapping
			statusMapping = connection.StatusMapping
			break
		}
	}
	if statusMapping == nil {
		t.Fatal("default Catalog is missing the Tripo status mapping")
	}
	adapter, err := tripo.New(tripo.Config{
		BaseURL:        server.URL,
		APIKey:         "synthetic-fixture-key",
		HTTPClient:     server.Client(),
		ErrorMapping:   errorMapping,
		StatusMapping:  statusMapping,
		OfferingRoutes: []generation.Offering{offering},
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func tripoCheckpointStep(t *testing.T, h *checkpointHarness) {
	t.Helper()
	h.reconcile(t)
	h.clock.Advance(6 * time.Second)
}

func tripoCheckpointAggregate(t *testing.T, h *checkpointHarness, id generation.TaskID) generation.Aggregate {
	t.Helper()
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return aggregate
}

func tripoCheckpointAssertRunningWithoutAssets(t *testing.T, task generation.Task) {
	t.Helper()
	if task.State != generation.TaskRunning || len(task.Assets) != 0 {
		t.Fatalf("task state/assets = %s/%d; want RUNNING/0 until materialization", task.State, len(task.Assets))
	}
}

func tripoCheckpointConversion(t *testing.T, opaque json.RawMessage, format string) tripoCheckpointConversionState {
	t.Helper()
	var checkpoint struct {
		Conversions map[string]tripoCheckpointConversionState `json:"conversions"`
	}
	if err := json.Unmarshal(opaque, &checkpoint); err != nil {
		t.Fatalf("decode Tripo conversion checkpoint: %v", err)
	}
	item, ok := checkpoint.Conversions[format]
	if !ok {
		t.Fatalf("%s conversion missing from opaque checkpoint %s", format, opaque)
	}
	return item
}

type tripoCheckpointConversionState struct {
	Status   string `json:"status"`
	RemoteID string `json:"remote_id"`
	Locator  string `json:"locator"`
}

func tripoCheckpointAllDone(t *testing.T, opaque json.RawMessage, formats []string) bool {
	t.Helper()
	var checkpoint struct {
		Conversions map[string]tripoCheckpointConversionState `json:"conversions"`
	}
	if err := json.Unmarshal(opaque, &checkpoint); err != nil {
		t.Fatalf("decode Tripo conversion checkpoint: %v", err)
	}
	if len(checkpoint.Conversions) != len(formats) {
		return false
	}
	for _, format := range formats {
		item, ok := checkpoint.Conversions[format]
		if !ok || item.Status != "done" || item.RemoteID == "" {
			return false
		}
	}
	return true
}

func writeTripoCheckpointJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, "failed to write synthetic response", http.StatusInternalServerError)
	}
}
