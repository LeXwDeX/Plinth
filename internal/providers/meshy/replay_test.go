package meshy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func expectedBytes(t *testing.T) []byte {
	t.Helper()
	_, data, err := wirefixture.SyntheticMedia("glb-triangle")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type replayUpstream struct {
	server  *httptest.Server
	creates []map[string]any
}

func newReplayUpstream(t *testing.T) *replayUpstream {
	t.Helper()
	upstream := &replayUpstream{}
	upstream.server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/openapi/v2/text-to-3d":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			upstream.creates = append(upstream.creates, body)
			result := "raw-1"
			if body["mode"] == "refine" {
				result = "refine-1"
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{"result": result})
		case request.Method == http.MethodGet && request.URL.Path == "/openapi/v2/text-to-3d/raw-1":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id": "raw-1", "status": "SUCCEEDED", "progress": 100,
				"model_urls": map[string]string{"glb": "https://" + request.Host + "/asset.glb"},
			})
		case request.Method == http.MethodGet && request.URL.Path == "/openapi/v2/text-to-3d/refine-1":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id": "refine-1", "status": "SUCCEEDED", "progress": 100,
				"model_urls": map[string]string{"glb": "https://" + request.Host + "/asset.glb"},
			})
		case request.Method == http.MethodGet && request.URL.Path == "/asset.glb":
			_, glb, err := wirefixture.SyntheticMedia("glb-triangle")
			if err != nil {
				t.Error(err)
				http.Error(writer, "fixture", http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = writer.Write(glb)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func replayPolicy(host, assetHost string) wirefixture.Policy {
	keys := []string{
		"result", "id", "status", "progress", "model_urls", "glb", "thumbnail_url",
		"alpha_thumbnail_url", "texture_urls", "prompt", "mode", "ai_model", "preview_task_id",
		"target_formats", "target_polycount", "geometry_resolution", "texture_resolution",
		"enable_pbr", "should_remesh", "topology", "pose_mode", "task_error", "type",
	}
	allowed := []string{
		"castle", "preview", "meshy-7.1", "glb", "obj", "fbx", "stl", "usdz", "3mf",
		"4k", "8k", "triangle", "refine", "v2", "application/json", "openapi", "text-to-3d",
		"SUCCEEDED", "PENDING", "IN_PROGRESS",
	}
	policy := wirefixture.Policy{
		MaxBodyBytes: 8 << 20,
		Keys:         map[string]bool{},
		Allowed:      map[string]bool{},
		Aliases:      map[string]string{host: "example.test"},
		PublicHosts:  map[string]bool{assetHost: true},
		Projection: map[string]string{
			"result": "id", "id": "id", "preview_task_id": "id", "model_urls": "url",
			"thumbnail_url": "url", "alpha_thumbnail_url": "url", "texture_urls": "url",
		},
	}
	for _, key := range keys {
		policy.Keys[key] = true
	}
	for _, value := range allowed {
		policy.Allowed[value] = true
	}
	for _, value := range []string{"0", "1", "100", "300000"} {
		policy.Allowed[value] = true
	}
	return policy
}

func replayStore(t *testing.T) *wirefixture.PrivateStore {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := wirefixture.NewPrivateStore(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestMeshyAdapterOfflineReplay(t *testing.T) {
	upstream := newReplayUpstream(t)
	host := strings.TrimPrefix(upstream.server.URL, "https://")
	policy := replayPolicy(host, "assets.meshy.ai")
	store := replayStore(t)
	recorder, err := wirefixture.NewDeferredRecorderWithEvidence(wirefixture.Metadata{
		Scenario: "meshy-replay", Connection: "meshy", APIVersion: "v2",
		RequestedModel: "meshy-7.1", ReportedModel: "meshy-7.1", ServerBuild: "offline",
		CapturedAt: time.Now().UTC(), AppRevision: "replay", CatalogRevision: "replay",
		Environment: "test", Provenance: wirefixture.ProvenanceCaptured,
	}, wirefixture.DeferredLimits{MaxBytes: 8 << 20, MaxExchanges: 16}, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Release() })
	client := upstream.server.Client()
	client.Transport = recorder.Wrap("meshy", client.Transport)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL: upstream.server.URL, APIKey: "test", HTTPClient: client, AllowHTTPAssets: true,
	})
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/meshy-7.1/text-to-3d")
	if !ok {
		t.Fatal("missing Meshy offering")
	}
	offering.AssetHosts = []string{strings.Split(host, ":")[0]}
	input := normalizedInput(t, offering, `{"prompt":"castle","mode":"preview","target_formats":["glb","obj","fbx","stl","usdz","3mf"]}`)
	start, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", SubmissionKey: "submission",
		Offering: offering, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if start.Job == nil || start.Job.RemoteID != "raw-1" {
		t.Fatalf("start job = %+v", start.Job)
	}
	observe, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: offering, Job: *start.Job, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observe.Phase != generation.UpstreamRunning || len(observe.Opaque) == 0 {
		t.Fatalf("preview observe = %+v", observe)
	}
	completed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: offering, Job: *start.Job,
		Input: input, Opaque: observe.Opaque,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) == 0 {
		t.Fatalf("refine observe = %+v", completed)
	}
	claim := completed.Artifacts[0]
	claim.Locator = "https://" + host + "/asset.glb"
	claim.MediaType = "model/gltf-binary"
	asset, metadata, err := adapter.OpenAsset(context.Background(), generation.OpenAssetRequest{
		Offering: offering, Claim: claim,
	})
	if err != nil {
		t.Fatal(err)
	}
	downloaded, err := io.ReadAll(asset)
	if err != nil {
		t.Fatal(err)
	}
	if err := asset.Close(); err != nil {
		t.Fatal(err)
	}
	if metadata.MediaType != "model/gltf-binary" || len(downloaded) == 0 {
		t.Fatalf("asset = %+v", metadata)
	}
	if err := recorder.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	exchanges := recorder.Exchanges()
	if len(exchanges) != 5 {
		t.Fatalf("exchanges = %d", len(exchanges))
	}
	if len(recorder.MediaEvidence()) != 1 {
		t.Fatalf("media evidence = %d", len(recorder.MediaEvidence()))
	}
	publicExchanges, err := wirefixture.SubstituteMediaWithExtensions(exchanges,
		map[string]string{"model/gltf-binary": "glb-triangle"}, map[string]string{".glb": "glb-triangle"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exchanges[0].Response.Body.JSON), `"id-1"`) ||
		!strings.Contains(string(exchanges[1].Response.Body.JSON), `"https://example.test/fixture-1.glb"`) ||
		!strings.Contains(string(exchanges[2].Response.Body.JSON), `"id-2"`) ||
		!strings.Contains(string(exchanges[3].Response.Body.JSON), `"https://example.test/fixture-1.glb"`) {
		t.Fatalf("projected exchanges = %+v", exchanges)
	}
	replayPolicy := policy
	replayPolicy.Projection = nil
	replayPolicy.Aliases = nil
	replayPolicy.Allowed = maps.Clone(policy.Allowed)
	for _, value := range []string{
		"id-1", "id-2", "fixture-1.glb", "fixture-2.glb",
		"https://example.test/fixture-1.glb", "https://example.test/fixture-2.glb",
	} {
		replayPolicy.Allowed[value] = true
	}
	root, candidateDir := t.TempDir(), t.TempDir()
	if err := os.Chmod(candidateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := wirefixture.WriteCandidate(root, candidateDir+"/candidate.json", publicExchanges, replayPolicy); err != nil {
		t.Fatal(err)
	}
	replay, err := wirefixture.NewReplayWithMedia(publicExchanges, replayPolicy, wirefixture.SyntheticMediaResolver{})
	if err != nil {
		t.Fatal(err)
	}
	replayOffering := offering
	replayOffering.AssetHosts = []string{"example.test"}
	replayAdapter := newMeshyAdapter(t, meshy.Config{
		BaseURL: "https://example.test", APIKey: "test",
		HTTPClient: &http.Client{Transport: replay}, AllowHTTPAssets: true,
	})
	replayedStart, err := replayAdapter.Start(context.Background(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", SubmissionKey: "submission",
		Offering: replayOffering, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayedStart.Job == nil || replayedStart.Job.RemoteID != "id-1" {
		t.Fatalf("replayed start job = %+v", replayedStart.Job)
	}
	replayedObserve, err := replayAdapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: replayOffering, Job: *replayedStart.Job, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayedObserve.Phase != generation.UpstreamRunning || len(replayedObserve.Opaque) == 0 {
		t.Fatalf("replayed preview observe = %+v", replayedObserve)
	}
	replayedCompleted, err := replayAdapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: replayOffering, Job: *replayedStart.Job,
		Input: input, Opaque: replayedObserve.Opaque,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayedCompleted.Phase != generation.UpstreamCompleted || len(replayedCompleted.Artifacts) == 0 {
		t.Fatalf("replayed refine observe = %+v", replayedCompleted)
	}
	replayedClaim := replayedCompleted.Artifacts[0]
	replayedClaim.Locator = "https://example.test/fixture-1.glb"
	replayedClaim.MediaType = "model/gltf-binary"
	replayedAsset, replayedMetadata, err := replayAdapter.OpenAsset(context.Background(), generation.OpenAssetRequest{
		Offering: replayOffering, Claim: replayedClaim,
	})
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := io.ReadAll(replayedAsset)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayedAsset.Close(); err != nil {
		t.Fatal(err)
	}
	expected, _, err := wirefixture.SyntheticMedia("glb-triangle")
	if err != nil {
		t.Fatal(err)
	}
	if replayedMetadata.MediaType != expected.MediaType || !bytes.Equal(replayedBytes, expectedBytes(t)) {
		t.Fatalf("replayed asset = %+v", replayedMetadata)
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
	if len(upstream.creates) != 2 {
		t.Fatal("offline replay contacted the fake upstream")
	}
}
