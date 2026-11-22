package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// #300: Tripo's GLTF export is both the converted.gltf slot and the model slot, one
// file at one signed URL. The 2026-11-12 check of #293 counted two GETs on it; the
// adapter now declares the pair identical, so the kernel downloads the file once
// and stores it for both slots. The whole stack runs against the fake upstream,
// and the fake's request log is the count.

// downloadLog counts the GETs of artifact files the fake upstream answered, by
// path (the signed query is not part of the identity of a file): the ones it
// served and the ones it refused, an expired signed URL answering 403.
type downloadLog struct {
	mu      sync.Mutex
	served  map[string]int
	refused map[string]int
}

func (l *downloadLog) record(path string, status int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if status >= 200 && status < 300 {
		l.served[path]++
	} else {
		l.refused[path]++
	}
}

// files returns the served counts and the served paths in order.
func (l *downloadLog) files() (map[string]int, []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	counts := make(map[string]int, len(l.served))
	paths := make([]string, 0, len(l.served))
	for path, count := range l.served {
		counts[path] = count
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return counts, paths
}

func (l *downloadLog) refusals() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	total := 0
	for _, count := range l.refused {
		total += count
	}
	return total
}

// statusRecorder keeps the status the fake answered with.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// newDownloadCountingHarness is the Tripo fake stack of lowPolyHarness with the
// artifact GETs counted. apiKey is the fake key suffix and picks the fake's
// scenario ("artifact-expired-2" expires the first two reads of every task's URLs).
func newDownloadCountingHarness(t *testing.T, apiKey string) (*acmeHarness, *downloadLog) {
	t.Helper()
	fake, err := registry.New(fakeupstream.Config{QueuedPolls: 1, RunningPolls: 1, ArtifactOrigin: "https://" + fakeAssetHost})
	if err != nil {
		t.Fatal(err)
	}
	log := &downloadLog{served: map[string]int{}, refused: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/artifacts/") {
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			fake.ServeHTTP(recorder, r)
			log.record(r.URL.Path, recorder.status)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	fakeURL, _ := url.Parse(server.URL)
	settings := map[string]string{
		"tripo_base_url": server.URL + "/tripo/v3",
		"tripo_api_key":  fakeUpstreamKeyPrefix + apiKey,
	}
	taskCatalog, err := loadCatalogForShape("", upstreamShape{origin: fakeURL})
	if err != nil {
		t.Fatal(err)
	}
	return newAcmeHarnessWithTransport(t, taskCatalog, settings, fakeShapeHTTPTransport(fakeURL)), log
}

// gameSetTask runs the full-format Tripo Offering for the GLB and FBX game set.
func gameSetTask(t *testing.T, harness *acmeHarness, key string) generation.Task {
	t.Helper()
	accepted := submitStep(t, harness, generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d",
		`{"prompt":"a brass robot","authorize_extra_credits":true,"output_formats":["GLTF","FBX"]}`,
		key, map[string]string{"output_formats": "the game set: GLB and FBX"})
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state = %s, failure = %#v", task.State, task.Failure)
	}
	// The GLB is delivered with the primary model; the FBX conversion lands in
	// the background (#462).
	return harness.driveMixedPostSuccess(t, "owner-acme", accepted.TaskID)
}

// slotsOf finds the model, converted.gltf and converted.fbx records of a game set
// task and checks the first two share one stored file and the FBX is another.
func slotsOf(t *testing.T, task generation.Task) (model, gltf, fbx generation.Asset) {
	t.Helper()
	for _, asset := range task.Assets {
		switch {
		case asset.Role == "model":
			model = asset
		case asset.Variant == "converted.gltf":
			gltf = asset
		case asset.Variant == "converted.fbx":
			fbx = asset
		}
	}
	if model.ID == "" || gltf.ID == "" || fbx.ID == "" || model.ID == gltf.ID {
		t.Fatalf("assets = %+v", task.Assets)
	}
	if model.StorageKey != gltf.StorageKey || model.SHA256 != gltf.SHA256 || fbx.StorageKey == model.StorageKey {
		t.Fatalf("model and converted.gltf must share a blob and the FBX must not: model=%+v gltf=%+v fbx=%+v", model, gltf, fbx)
	}
	return model, gltf, fbx
}

func TestTripoGltfExportIsDownloadedOnceForTheModelAndConvertedSlots(t *testing.T) {
	harness, log := newDownloadCountingHarness(t, "test-key")
	task := gameSetTask(t, harness, "shared-download-300")
	slotsOf(t, task)

	// Every artifact file the task materialized was fetched once, and none was refused.
	counts, paths := log.files()
	var glb, fbx int
	for _, path := range paths {
		if counts[path] != 1 {
			t.Errorf("%s was fetched %d times, want once", path, counts[path])
		}
		switch {
		case strings.HasSuffix(path, ".glb"):
			glb++
		case strings.HasSuffix(path, ".fbx"):
			fbx++
		}
	}
	if glb != 1 || fbx != 1 || log.refusals() != 0 {
		t.Fatalf("fetched %d GLB and %d FBX files (%v), %d refused; want one of each and none refused", glb, fbx, paths, log.refusals())
	}
}

// Signed URLs expire. The first pass is refused, the claims are refreshed through the
// Provider, and the second pass downloads the file for the first of the two slots and
// stores it for the other: still one file served, two records, a retry that costs no
// extra download.
func TestTripoSharedDownloadSurvivesExpiredSignedURLs(t *testing.T) {
	harness, log := newDownloadCountingHarness(t, "artifact-expired-2")
	task := gameSetTask(t, harness, "shared-download-300-expired")
	model, gltf, _ := slotsOf(t, task)

	if log.refusals() == 0 {
		t.Fatal("the fake refused no download: the expiry scenario did not run")
	}
	counts, paths := log.files()
	glbServed := 0
	for _, path := range paths {
		if strings.HasSuffix(path, ".glb") {
			glbServed += counts[path]
		}
	}
	if glbServed != 1 {
		t.Fatalf("the shared GLB was served %d times (%v), want once", glbServed, counts)
	}
	events, err := harness.module.Events(context.Background(), "owner-acme", task.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	for _, want := range []string{"materialization.retry_scheduled", "materialization.claims_refreshed"} {
		if !slices.Contains(types, want) {
			t.Fatalf("no %s event: %v", want, types)
		}
	}
	if model.Size == 0 || model.Size != gltf.Size {
		t.Fatalf("the slots hold different files: model=%+v gltf=%+v", model, gltf)
	}
}
