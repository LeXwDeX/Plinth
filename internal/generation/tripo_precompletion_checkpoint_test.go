package generation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

func TestTripoPrivateConversionsSurviveSourceRegression(t *testing.T) {
	formats := []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"}
	for _, restart := range []bool{false, true} {
		name := "without_restart"
		if restart {
			name = "after_sqlite_restart"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &tripoCheckpointServer{}
			server := httptest.NewServer(http.HandlerFunc(upstream.serveHTTP))
			t.Cleanup(server.Close)
			adapter := newTripoCheckpointAdapter(t, server)
			dbPath := filepath.Join(t.TempDir(), "tripo-checkpoint.db")
			h := newCheckpointHarness(t, adapter, dbPath)
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
			tripoCheckpointAssertRunningWithoutAssets(t, afterRegression.Task)
			if afterRegression.Task.Progress != 98 {
				t.Fatalf("progress after source running/99 = %d; want 98", afterRegression.Task.Progress)
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
				tripoCheckpointAssertRunningWithoutAssets(t, afterRestart.Task)
			}

			// The next source success resumes from USDZ. Stop after all private
			// children are done, before the adapter returns assets to materialize.
			var final generation.Aggregate
			for range 40 {
				tripoCheckpointStep(t, h)
				final = tripoCheckpointAggregate(t, h, accepted.TaskID)
				tripoCheckpointAssertRunningWithoutAssets(t, final.Task)
				if tripoCheckpointAllDone(t, final.CurrentAttempt.Opaque, formats) {
					break
				}
			}
			if !tripoCheckpointAllDone(t, final.CurrentAttempt.Opaque, formats) {
				t.Fatalf("private conversions did not all reach done: %s", final.CurrentAttempt.Opaque)
			}
			for _, format := range formats {
				if got := upstream.formatPostCount(format); got != 1 {
					t.Errorf("%s conversion POSTs = %d; want exactly 1", format, got)
				}
			}
			if got := upstream.totalFormatPostCount(); got != len(formats) {
				t.Errorf("private conversion POSTs = %d; want exactly %d", got, len(formats))
			}
			if got := upstream.sourcePostCount(); got != 1 {
				t.Errorf("source generation POSTs = %d; want exactly 1", got)
			}
			tripoCheckpointAssertRunningWithoutAssets(t, final.Task)
		})
	}
}

type tripoCheckpointServer struct {
	mu          sync.Mutex
	sourcePosts int
	baseGets    int
	formatPosts map[string]int
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
			"output": map[string]any{"model_url": "http://127.0.0.1/synthetic-base.glb"},
		}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/synthetic-child-"):
		remoteID := strings.TrimPrefix(r.URL.Path, "/tasks/")
		writeTripoCheckpointJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"task_id": remoteID, "status": "success", "progress": 100,
			"output": map[string]any{"model_url": "http://127.0.0.1/synthetic-child.glb"},
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
