package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

type bindingWireTransport func(*http.Request) (*http.Response, error)

func (f bindingWireTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type changedBindingReadStore struct{ generation.AssetStore }

func (s changedBindingReadStore) Open(context.Context, generation.Asset) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("changed local preview bytes")), nil
}

type bindingWirePort struct{ *meshy.Adapter }

func (p bindingWirePort) Start(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	if strings.HasSuffix(string(r.Offering.ID), "/prototype") {
		return generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-1"}}, nil
	}
	return p.Adapter.Start(ctx, r)
}
func (p bindingWirePort) OpenAsset(context.Context, generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(postSuccessThumbnailPNG())), generation.AssetMetadata{MediaType: "image/png"}, nil
}

// The source input is synthetic prompt-only; result observation, claim metadata,
// actual PNG Store.Put, SQLite restart, PreparedInput/Grant and build wire are real.
func TestPrivateSourceBindingSurvivesSQLiteDeliveryApprovalAndRestart(t *testing.T) {
	ctx := context.Background()
	const sourceID = "meshy/creative-lab/keycap/prototype"
	const buildID = "meshy/creative-lab/keycap/build"
	var doc map[string]any
	if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t, sourceID, buildID), &doc); err != nil {
		t.Fatal(err)
	}
	for _, raw := range doc["offerings"].([]any) {
		o := raw.(map[string]any)
		if o["id"] == sourceID {
			o["parameters"] = []any{map[string]any{"name": "prompt", "type": "text", "semantic": "prompt", "required": true}}
		}
	}
	// The keycap stages are registered once the default Catalog enables them (#189).
	discovery := doc["application"].(map[string]any)["provider_discovery"].(map[string]any)
	registered := map[string]bool{}
	for _, model := range discovery["legacy_models"].([]any) {
		registered[model.(map[string]any)["model_id"].(string)] = true
	}
	for _, id := range []string{"creative-lab-keycap-prototype", "creative-lab-keycap-build"} {
		if !registered[id] {
			discovery["legacy_models"] = append(discovery["legacy_models"].([]any), map[string]any{"model_id": id, "provider_key": "meshy", "recommended": false})
		}
	}
	catalogtest.DropUnusedOfferingTexts(doc)
	raw, _ := json.Marshal(doc)
	c, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	gets, posts := 0, 0
	candidate := "candidate-A"
	transport := bindingWireTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"result":"build-1"}`
		status := http.StatusAccepted
		if r.Method == "GET" {
			gets++
			status = 200
			body = fmt.Sprintf(`{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","progress":100,"candidate_ids":[%q],"image_urls":["https://example.invalid/preview.png"]}`, candidate)
		} else {
			posts++
			if r.Method != "POST" || r.URL.Path != "/openapi/creative-lab/keycap/v1/build" {
				t.Fatalf("unexpected dispatch")
			}
			var sent map[string]any
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Fatal(err)
			}
			if sent["candidate_id"] != "candidate-A" || sent["input_task_id"] != "prototype-1" {
				t.Fatal("approved candidate changed")
			}
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	config := meshy.Config{BaseURL: "https://meshy.invalid", APIKey: "fixture", HTTPClient: &http.Client{Transport: transport}}
	for _, connection := range c.Application().Runtime.ProviderConnections {
		if connection.AdapterKey == "meshy.v2" {
			config.StatusMapping = connection.StatusMapping
			config.ErrorMapping = connection.ErrorMapping
		}
	}
	adapter, err := meshy.New(config)
	if err != nil {
		t.Fatal(err)
	}
	port := bindingWirePort{adapter}
	registry := providers.NewRegistry()
	if err := registry.Register("meshy.v2", port); err != nil {
		t.Fatal(err)
	}
	h := newConfiguredHarness(t, port, nil, func(d *generation.Dependencies) { d.Catalog = c; d.Providers = registry })
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner-a", OfferingID: sourceID, Capability: "creative-lab-prototype", Input: []byte(`{"prompt":"fixture"}`)})
	if err != nil {
		t.Fatal(err)
	}
	source := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if source.State != generation.TaskSucceeded || len(source.Assets) != 1 {
		t.Fatalf("source=%s failure=%+v", source.State, source.Failure)
	}
	aggregate, err := h.repository.Load(ctx, source.ID)
	if err != nil || len(aggregate.Claims) != 1 || len(aggregate.Claims[0].Metadata) == 0 {
		t.Fatal("claim identity was not persisted")
	}
	if aggregate.Claims[0].Locator != "" || len(aggregate.Claims[0].Inline) != 0 || aggregate.Claims[0].ExpiresAt != nil || aggregate.Claims[0].StoredAsset == nil || aggregate.Claims[0].StoredAsset.ID != source.Assets[0].ID || aggregate.Claims[0].StoredAsset.SHA256 != source.Assets[0].SHA256 {
		t.Fatal("retained identity kept locator or lost exact materialization")
	}
	body := []byte(fmt.Sprintf(`{"source_task":%q}`, source.ID))
	prepare := generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: source.SessionID, OfferingID: buildID, Capability: "creative-lab-build", Input: body}
	prepared, err := h.module.PrepareInput(ctx, prepare)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Dependencies) != 1 || !bytes.Contains(prepared.Dependencies[0].ProviderBinding, []byte("candidate-A")) || prepared.Dependencies[0].OutputAssets[0].SHA256 != source.Assets[0].SHA256 || prepared.Dependencies[0].SourceSessionID != source.SessionID {
		t.Fatal("private identity/preview/session not frozen")
	}
	cmd := authorizePrepared(t, ctx, h.repository, prepared, "frozen-candidate")
	if err := h.module.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	h.repository = reopened
	reopened.SetClock(h.clock.Now)
	h.module, err = generation.New(generation.Dependencies{Repository: reopened, Catalog: c, Providers: registry, Assets: h.assets, Inputs: h.inputs, Clock: h.clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.module.Close() })
	restored, err := h.module.GetPreparedInput(ctx, "owner-a", prepared.ID)
	if err != nil || restored.InputHash != prepared.InputHash || !bytes.Equal(restored.Dependencies[0].ProviderBinding, prepared.Dependencies[0].ProviderBinding) {
		t.Fatal("restart changed frozen binding")
	}
	// An unrelated fresh upstream candidate cannot replace the approved one.
	candidate = "candidate-B"
	before := gets
	build, err := h.module.SubmitPrepared(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	if gets != before || posts != 1 {
		t.Fatalf("candidate was fetched again: gets %d -> %d posts %d", before, gets, posts)
	}
	replay, err := h.module.SubmitPrepared(ctx, cmd)
	if err != nil || replay.TaskID != build.TaskID || !replay.Replayed || posts != 1 {
		t.Fatal("idempotent replay dispatched again")
	}
	if _, err := h.module.GetPreparedInput(ctx, "foreign", prepared.ID); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatal("foreign prepared visible")
	}
	bad, err := generation.New(generation.Dependencies{Repository: reopened, Catalog: c, Providers: registry, Assets: changedBindingReadStore{h.assets}, Inputs: h.inputs, Clock: h.clock})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.PrepareInput(ctx, prepare); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("changed actual preview bytes accepted: %v", err)
	}
	if posts != 1 {
		t.Fatal("byte rejection dispatched new work")
	}

}
