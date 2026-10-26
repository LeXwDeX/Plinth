package tripo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

type nativeDeliveryClock struct{ now time.Time }

func (c *nativeDeliveryClock) Now() time.Time { return c.now }

type nativeDeliveryCatalog []generation.Offering

func (c nativeDeliveryCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	for _, o := range c {
		if o.ID == id {
			return o, true
		}
	}
	return generation.Offering{}, false
}
func (c nativeDeliveryCatalog) List(cap generation.Capability) []generation.Offering {
	var out []generation.Offering
	for _, o := range c {
		if cap == "" || cap == o.Capability {
			out = append(out, o)
		}
	}
	return out
}
func (c nativeDeliveryCatalog) Normalize(cap generation.Capability, id generation.OfferingID, raw json.RawMessage) (generation.Offering, json.RawMessage, error) {
	o, ok := c.Get(id)
	if !ok || o.Capability != cap {
		return o, nil, errors.New("unknown offering")
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return o, nil, errors.New("bad input")
	}
	normalized, _ := json.Marshal(values)
	return o, normalized, nil
}
func nativeCoordinatorGLB() []byte {
	raw := []byte(`{"asset":{"version":"2.0"},"scenes":[{"nodes":[]}],"scene":0}`)
	for len(raw)%4 != 0 {
		raw = append(raw, ' ')
	}
	out := make([]byte, 20+len(raw))
	copy(out, "glTF")
	binary.LittleEndian.PutUint32(out[4:], 2)
	binary.LittleEndian.PutUint32(out[8:], uint32(len(out)))
	binary.LittleEndian.PutUint32(out[12:], uint32(len(raw)))
	copy(out[16:], "JSON")
	copy(out[20:], raw)
	return out
}

type nativeDeliverySourcePort struct{}

func (nativeDeliverySourcePort) Start(_ context.Context, r generation.StartRequest) (generation.Observation, error) {
	return generation.Observation{Phase: generation.UpstreamCompleted, Progress: 100, Job: &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "task_native"}, Artifacts: []generation.ArtifactClaim{{ID: "source", Role: "model", Variant: "glb", MediaType: "model/gltf-binary", Inline: nativeCoordinatorGLB()}}}, nil
}
func (nativeDeliverySourcePort) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected source poll")
}
func (nativeDeliverySourcePort) OpenAsset(_ context.Context, r generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(r.Claim.Inline)), generation.AssetMetadata{MediaType: r.Claim.MediaType}, nil
}

type nativeDeliveryCoordinatorPort struct {
	*Adapter
	expire *nativeDeliveryClock
	lose   bool
}

func (p *nativeDeliveryCoordinatorPort) Observe(ctx context.Context, r generation.ObserveRequest) (generation.Observation, error) {
	obs, err := p.Adapter.Observe(ctx, r)
	if obs.Dispatch != nil {
		if p.expire != nil {
			p.expire.now = p.expire.now.Add(2 * time.Minute)
			p.expire = nil
		}
		if p.lose {
			p.lose = false
			return generation.Observation{}, submissionUnknown("synthetic lost conversion receipt")
		}
	}
	return obs, err
}

func TestNativeDeliveryRealCoordinatorSQLiteRestartReceiptAndCredits(t *testing.T) {
	for _, kind := range []string{"success", "lost-receipt", "expired-lease"} {
		t.Run(kind, func(t *testing.T) { runNativeDeliveryCoordinator(t, kind) })
	}
}
func runNativeDeliveryCoordinator(t *testing.T, kind string) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "native.db")
	clock := &nativeDeliveryClock{time.Now().UTC()}
	basePosts, childPosts, childPolls, basePolls := 0, 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "animations/rig"):
			basePosts++
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "models/convert"):
			childPosts++
			io.WriteString(w, `{"code":0,"data":{"task_id":"child_fbx"}}`)
		case strings.HasSuffix(r.URL.Path, "task_processed"):
			basePolls++
			status := "running"
			if basePolls >= 2 {
				status = "success"
			}
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed","status":"`+status+`","progress":100,"credits_consumed":25.5,"output":{"model_url":"https://assets.example/original.glb"}}}`)
		case strings.HasSuffix(r.URL.Path, "child_fbx"):
			childPolls++
			if childPolls <= 2 {
				credit := ""
				if childPolls == 2 {
					credit = `,"credits_consumed":3.75`
				}
				io.WriteString(w, `{"code":0,"data":{"task_id":"child_fbx","status":"running","progress":50`+credit+`}}`)
			} else {
				io.WriteString(w, `{"code":0,"data":{"task_id":"child_fbx","status":"success","output":{"model_url":"https://assets.example/rig.fbx"}}}`)
			}
		default:
			t.Errorf("unexpected fixture request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	fixture := nativeDeliveryFixture(generation.Rigging)
	processing := fixture.Offering
	processing.Enabled = true
	processing.AdapterKey = "native.delivery"
	processing.Domain = generation.DomainProcessing
	processing.PollInterval = time.Second
	processing.Timeout = time.Hour
	processing.MaterializeAttempts = 3
	processing.EstimatedCredits = 25
	one := 1
	source := generation.Offering{ID: processing.Parameters[0].TaskOfferings[0], Revision: "source.v1", ProviderID: "tripo", Capability: generation.TextTo3D, Domain: generation.DomainModel3D, AdapterKey: "native.source", Enabled: true, Timeout: time.Hour, MaterializeAttempts: 3, Outputs: []generation.AssetRequirement{{Role: "model", Variant: "glb", Minimum: &one, MediaTypes: []string{"model/gltf-binary"}, MaxBytes: 1 << 20}}}
	var repository *sqlite.Repository
	var module *generation.Module
	open := func() {
		var err error
		repository, err = sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		repository.SetClock(clock.Now)
		store, err := local.New(filepath.Join(root, "assets"))
		if err != nil {
			t.Fatal(err)
		}
		a := rigAdapter(t, server)
		a.nativeSourceClient = &http.Client{Transport: nativeRoundTrip(func(*http.Request) (*http.Response, error) {
			b := nativeCoordinatorGLB()
			return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: int64(len(b)), Body: io.NopCloser(bytes.NewReader(b))}, nil
		})}
		port := &nativeDeliveryCoordinatorPort{Adapter: a}
		if kind == "expired-lease" {
			port.expire = clock
		}
		if kind == "lost-receipt" {
			port.lose = true
		}
		registry := providers.NewRegistry()
		if err := registry.Register(source.AdapterKey, nativeDeliverySourcePort{}); err != nil {
			t.Fatal(err)
		}
		if err := registry.Register(processing.AdapterKey, port); err != nil {
			t.Fatal(err)
		}
		module, err = generation.New(generation.Dependencies{Repository: repository, Catalog: nativeDeliveryCatalog{source, processing}, Providers: registry, Assets: store, Clock: clock, SchemaVersion: "native.delivery"})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { module.Close() }()
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", Title: "native delivery", CreatedAt: clock.Now(), UpdatedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	submit := func(o generation.Offering, input json.RawMessage, key string) generation.TaskID {
		prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "session", Capability: o.Capability, OfferingID: o.ID, Input: input})
		if err != nil {
			t.Fatal(err)
		}
		if o.ID == processing.ID && prepared.ExtraCredits != 10 {
			t.Fatal("extra price not frozen")
		}
		service := executiongrant.Service{Grants: repository, Prepared: repository}
		principal := executiongrant.Principal{OwnerID: "owner", ActorID: "human", Source: executiongrant.HumanBrowser}
		grant, err := service.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal, SessionID: "session", OfferingID: string(o.ID), OfferingRevision: o.Revision, PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, IdempotencyKey: key, OperationID: key})
		if err != nil {
			t.Fatal(err)
		}
		grant, err = service.Decide(ctx, principal, grant.RequestID, executiongrant.Approve)
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: "session", PreparedInputID: prepared.ID, IdempotencyKey: key, OperationID: key, AuthorizationRef: grant.RequestID})
		if err != nil {
			t.Fatal(err)
		}
		return accepted.TaskID
	}
	tick := func() {
		_, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "native-delivery", LeaseFor: time.Minute, BatchSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(2 * time.Second)
	}
	id := submit(source, json.RawMessage(`{}`), "source")
	for range 4 {
		tick()
	}
	sourceTask, err := module.Get(ctx, "owner", id)
	if err != nil || sourceTask.State != generation.TaskSucceeded {
		t.Fatalf("source not delivered %+v %v", sourceTask, err)
	}
	var input map[string]any
	json.Unmarshal(fixture.Input, &input)
	input["source_task"] = id
	raw, _ := json.Marshal(input)
	id = submit(processing, raw, "rig")
	load := func() generation.Aggregate {
		a, err := repository.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	tick()
	tick()
	before := load()
	if before.Task.Progress != 80 {
		t.Fatalf("base progress not capped %d", before.Task.Progress)
	}
	tick()
	submitted := load()
	if kind != "success" {
		if err := module.Close(); err != nil {
			t.Fatal(err)
		}
		open()
		for range 3 {
			tick()
		}
		a := load()
		if basePosts != 1 || childPosts != 1 || a.Task.State != generation.TaskFailed || a.Task.Failure == nil || a.Task.Failure.Class != generation.FailureSubmissionUnknown {
			t.Fatalf("uncertain paid POST replay/state posts=%d/%d state=%s failure=%+v opaque=%s", basePosts, childPosts, a.Task.State, a.Task.Failure, a.CurrentAttempt.Opaque)
		}
		return
	}
	var state nativeProcessingState
	if json.Unmarshal(submitted.CurrentAttempt.Opaque, &state) != nil || state.Conversions["FBX"].RemoteID != "child_fbx" || submitted.Task.Progress <= before.Task.Progress || submitted.CurrentAttempt.LastObservationKey == before.CurrentAttempt.LastObservationKey || submitted.CurrentAttempt.UpstreamJob.RemoteID != "task_processed" {
		t.Fatalf("receipt/remote lost %+v opaque%s", submitted.Task, submitted.CurrentAttempt.Opaque)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint string
	err = db.QueryRow(`SELECT dispatch_checkpoint_json FROM generation_attempts WHERE id=?`, string(submitted.CurrentAttempt.ID)).Scan(&checkpoint)
	db.Close()
	if err != nil || !strings.Contains(checkpoint, `"state":"received"`) {
		t.Fatalf("receipt not settled %s %v", checkpoint, err)
	}
	if err := module.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	tick()
	baseline := load()
	tick()
	billed := load()
	if billed.Task.Progress != baseline.Task.Progress || !bytes.Contains(billed.CurrentAttempt.Opaque, []byte(`"credits_consumed":3.75`)) || billed.CurrentAttempt.LastObservationKey == baseline.CurrentAttempt.LastObservationKey {
		t.Fatalf("same-stage credits lost %s", billed.CurrentAttempt.Opaque)
	}
	if err := module.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	tick()
	tick()
	a := load()
	if a.Task.State != generation.TaskMaterializing || !bytes.Contains(a.CurrentAttempt.Opaque, []byte(`"credits_consumed":3.75`)) || basePosts != 1 || childPosts != 1 {
		t.Fatalf("completion/restart evidence state=%s posts=%d/%d opaque=%s", a.Task.State, basePosts, childPosts, a.CurrentAttempt.Opaque)
	}
}
