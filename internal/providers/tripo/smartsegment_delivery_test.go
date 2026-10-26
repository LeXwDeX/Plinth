package tripo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// smartSegmentDeliveryInputs serves one owned source asset to the adapter.
type smartSegmentDeliveryInputs struct{ body []byte }

func (*smartSegmentDeliveryInputs) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}
func (s *smartSegmentDeliveryInputs) Open(context.Context, generation.InputAsset) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.body)), nil
}

type smartSegmentDeliveryFixture struct {
	mu                       sync.Mutex
	adapter                  *Adapter
	request                  generation.StartRequest
	uploads, creates         int
	converts                 []map[string]any
	convertMode              string
	missingMask              bool
	remoteType               string
	sourceBytes              []byte
	sourceDownloads          int
	latestIntent             json.RawMessage
	permit                   *nativeDeliveryPermit
	childStatus              string
	unexpectedWire           []string
	segURL, maskURL, outBase string
}

func newSmartSegmentDeliveryFixture(t *testing.T, kind string) *smartSegmentDeliveryFixture {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get(generation.OfferingID("tripo/geometry/smartsegment-" + kind))
	if !ok {
		t.Fatal("Smart Segmentation declaration missing")
	}
	f := &smartSegmentDeliveryFixture{remoteType: "smartsegment_" + kind, sourceBytes: nativeTestGLB(), childStatus: "success",
		segURL: "https://cdn.tripo3d.ai/output/seg_model.glb", maskURL: "https://cdn.tripo3d.ai/output/mask.png", outBase: "https://cdn.tripo3d.ai/output/converted."}
	inputs := &smartSegmentDeliveryInputs{}
	name, id, media := "image", "owned-image", "image/png"
	if kind == "image" {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		inputs.body = b.Bytes()
	} else {
		name, id, media = "source_asset", "owned-glb", "model/gltf-binary"
		inputs.body = modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Interleaved: true, Textured: true})
	}
	sum := sha256.Sum256(inputs.body)
	raw, _ := json.Marshal(map[string]any{name: id, "seg_type": kind, "granularity": "fine", "output_formats": []string{"FBX", "OBJ", "STL", "USDZ", "3MF"}, "authorize_extra_credits": true})
	f.request = generation.StartRequest{TaskID: "public-smartseg", AttemptID: "attempt-smartseg", OwnerID: "owner", SubmissionKey: "smartseg-submit", Offering: o, Input: raw,
		InputAssets: map[string][]generation.InputAsset{name: {{ID: generation.AssetID(id), OwnerID: "owner", StorageKey: "owned", Size: int64(len(inputs.body)), SHA256: hex.EncodeToString(sum[:]), MediaType: media}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/files":
			f.uploads++
			writeTestEnvelope(w, map[string]any{"file_token": "file_smartseg"})
		case r.Method == http.MethodPost && r.URL.Path == "/v3/mesh/smartsegment":
			f.creates++
			writeTestEnvelope(w, map[string]any{"task_id": "remote-smartseg"})
		case r.Method == http.MethodGet && r.URL.Path == "/v3/tasks/remote-smartseg":
			output := map[string]any{"prompt": "head, body", "seg_model_url": f.segURL, "mask_url": f.maskURL, "seg_task_id": "private-seg", "model_task_id": "private-model"}
			if f.missingMask {
				delete(output, "mask_url")
			}
			writeTestEnvelope(w, map[string]any{"task_id": "remote-smartseg", "type": f.remoteType, "status": "success", "progress": 100, "credits_consumed": 55, "output": output})
		case r.Method == http.MethodPost && r.URL.Path == "/v3/models/convert":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.converts = append(f.converts, body)
			if !strings.Contains(string(f.latestIntent), `"status":"dispatching"`) {
				f.unexpectedWire = append(f.unexpectedWire, "convert without durable dispatching intent")
			}
			if f.convertMode == "lost" {
				if conn, _, _ := w.(http.Hijacker).Hijack(); conn != nil {
					conn.Close()
				}
				return
			}
			writeTestEnvelope(w, map[string]any{"task_id": "child_" + strings.ToLower(body["format"].(string))})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v3/tasks/child_"):
			child := strings.TrimPrefix(r.URL.Path, "/v3/tasks/")
			writeTestEnvelope(w, map[string]any{"task_id": child, "status": f.childStatus, "progress": 100, "credits_consumed": 10, "output": map[string]any{"model_url": f.outBase + strings.TrimPrefix(child, "child_")}})
		default:
			f.unexpectedWire = append(f.unexpectedWire, r.Method+" "+r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	f.adapter, err = New(Config{BaseURL: server.URL + "/v3", APIKey: "fixture-key", HTTPClient: server.Client(), InputAssets: inputs,
		StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]generation.StatusRule{"success": {Phase: "completed"}, "running": {Phase: "running"}, "failed": {Phase: "failed"}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.adapter.nativeSourceClient = &http.Client{Transport: nativeRoundTrip(func(q *http.Request) (*http.Response, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if q.Header.Get("Authorization") != "" || q.URL.String() != f.segURL {
			f.unexpectedWire = append(f.unexpectedWire, "source download "+q.URL.String())
		}
		f.sourceDownloads++
		return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: int64(len(f.sourceBytes)), Body: io.NopCloser(bytes.NewReader(f.sourceBytes))}, nil
	})}
	f.permit = &nativeDeliveryPermit{capture: func(b json.RawMessage) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.latestIntent = append(json.RawMessage(nil), b...)
	}}
	return f
}

// The fake upstream runs on server goroutines; a hijacked (lost) response has
// no happens-before edge, so the test reads and changes shared state under f.mu.
type smartSegmentWire struct {
	uploads, creates, downloads int
	converts                    []map[string]any
	unexpected                  []string
	intent                      json.RawMessage
}

func (f *smartSegmentDeliveryFixture) wire() smartSegmentWire {
	f.mu.Lock()
	defer f.mu.Unlock()
	return smartSegmentWire{f.uploads, f.creates, f.sourceDownloads, append([]map[string]any(nil), f.converts...), append([]string(nil), f.unexpectedWire...), append(json.RawMessage(nil), f.latestIntent...)}
}

func (f *smartSegmentDeliveryFixture) set(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
}

func writeTestEnvelope(w http.ResponseWriter, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
}

func (f *smartSegmentDeliveryFixture) ctx(t *testing.T) context.Context {
	return generation.WithDispatchCheckpointer(t.Context(), f.permit)
}

// accept runs the private upload and create, returning the observe request.
func (f *smartSegmentDeliveryFixture) accept(t *testing.T) generation.ObserveRequest {
	t.Helper()
	prepared, err := f.adapter.Start(f.ctx(t), f.request)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := f.adapter.ResumePreparation(f.ctx(t), generation.PreparationRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, OwnerID: f.request.OwnerID, SubmissionKey: f.request.SubmissionKey, Offering: f.request.Offering, Input: f.request.Input, Opaque: prepared.Opaque})
	if err != nil || accepted.Job == nil {
		t.Fatalf("create: %+v %v", accepted, err)
	}
	return generation.ObserveRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, Offering: f.request.Offering, Input: f.request.Input, Opaque: accepted.Opaque, Job: *accepted.Job}
}

func (f *smartSegmentDeliveryFixture) observeUntilDone(t *testing.T, q generation.ObserveRequest) generation.Observation {
	t.Helper()
	var obs generation.Observation
	var err error
	for i := 0; i < 20; i++ {
		obs, err = f.adapter.Observe(f.ctx(t), q)
		if err != nil {
			t.Fatal(err)
		}
		if obs.Phase != generation.UpstreamRunning {
			return obs
		}
		if obs.Progress < 80 || obs.Progress > 98 {
			t.Fatalf("export stage progress %d outside 80..98", obs.Progress)
		}
		q.Opaque = obs.Opaque
	}
	t.Fatal("exports never completed")
	return obs
}

func TestSmartSegmentDeliversSegmentedGLBMaskAndFiveRequiredExports(t *testing.T) {
	for _, kind := range []string{"image", "model"} {
		t.Run(kind, func(t *testing.T) {
			f := newSmartSegmentDeliveryFixture(t, kind)
			obs := f.observeUntilDone(t, f.accept(t))
			w := f.wire()
			if obs.Phase != generation.UpstreamCompleted || len(w.unexpected) != 0 {
				t.Fatalf("completion %+v unexpected=%v", obs, w.unexpected)
			}
			if w.uploads != 1 || w.creates != 1 || len(w.converts) != 5 || w.downloads < 2 {
				t.Fatalf("wire counts uploads=%d creates=%d converts=%d downloads=%d", w.uploads, w.creates, len(w.converts), w.downloads)
			}
			for i, format := range []string{"FBX", "OBJ", "STL", "USDZ", "3MF"} {
				want := map[string]any{"input": f.segURL, "format": format, "texture_size": float64(8192), "texture_format": "PNG", "with_animation": false, "bake": true, "fbx_preset": "blender"}
				if !reflect.DeepEqual(w.converts[i], want) {
					t.Fatalf("export %s wire %v", format, w.converts[i])
				}
			}
			slots := map[string]generation.ArtifactClaim{}
			for _, claim := range obs.Artifacts {
				slots[claim.Role+":"+claim.Variant] = claim
			}
			want := map[string]string{"model:glb": "model/gltf-binary", "mask:png": "image/png", "converted_model:fbx": "model/fbx", "converted_model:obj": "application/zip", "converted_model:stl": "model/stl", "converted_model:usdz": "model/vnd.usdz+zip", "converted_model:3mf": "model/3mf"}
			if len(slots) != len(want) || len(obs.Artifacts) != len(want) {
				t.Fatalf("slots %+v", obs.Artifacts)
			}
			for key, media := range want {
				if slots[key].MediaType != media {
					t.Fatalf("slot %s media %q", key, slots[key].MediaType)
				}
			}
			// The segmented GLB stays its own slot and is bound to the bytes that
			// were exported; the exports never replace it.
			if slots["model:glb"].Locator != f.segURL || !strings.Contains(string(slots["model:glb"].Metadata), "native_conversion_source") || len(slots["mask:png"].Metadata) != 0 {
				t.Fatalf("source binding %+v", slots)
			}
			for _, private := range []string{"private-seg", "private-model"} {
				if strings.Contains(string(obs.Opaque), private) {
					t.Fatal("private child ID promoted into checkpoint")
				}
			}
			if !strings.Contains(string(obs.Opaque), `"conversion_source"`) || !strings.Contains(string(obs.Opaque), `"consumed_credit":55`) {
				t.Fatalf("checkpoint lost export or credit evidence: %s", obs.Opaque)
			}
		})
	}
}

// Model mode has no official result example, so a missing mask does not fail
// an otherwise complete delivery; image mode keeps the documented mask slot.
func TestSmartSegmentMaskPresenceFollowsDeclaredSlot(t *testing.T) {
	model := newSmartSegmentDeliveryFixture(t, "model")
	model.set(func() { model.missingMask = true })
	obs := model.observeUntilDone(t, model.accept(t))
	if obs.Phase != generation.UpstreamCompleted || len(obs.Artifacts) != 6 {
		t.Fatalf("model without mask: %+v", obs)
	}
	for _, claim := range obs.Artifacts {
		if claim.Role == "mask" {
			t.Fatal("absent mask fabricated")
		}
	}
	image := newSmartSegmentDeliveryFixture(t, "image")
	image.set(func() { image.missingMask = true })
	if _, err := image.adapter.Observe(image.ctx(t), image.accept(t)); err == nil || len(image.wire().converts) != 0 {
		t.Fatalf("image result without required mask started exports: %v", err)
	}
}

func TestSmartSegmentWrongResultTypeStartsNoExport(t *testing.T) {
	f := newSmartSegmentDeliveryFixture(t, "model")
	f.set(func() { f.remoteType = "smartsegment_image" })
	if _, err := f.adapter.Observe(f.ctx(t), f.accept(t)); err == nil || len(f.wire().converts) != 0 {
		t.Fatalf("wrong upstream type exported: %v", err)
	}
}

func TestSmartSegmentUnknownExportIsNeverReplayed(t *testing.T) {
	f := newSmartSegmentDeliveryFixture(t, "model")
	q := f.accept(t)
	f.set(func() { f.convertMode = "lost" })
	if _, err := f.adapter.Observe(f.ctx(t), q); err == nil {
		t.Fatal("lost export response accepted")
	}
	w := f.wire()
	if len(w.converts) != 1 || !strings.Contains(string(w.intent), `"dispatching"`) {
		t.Fatalf("export intent not durable: converts=%d", len(w.converts))
	}
	f.set(func() { f.convertMode = "" })
	q.Opaque = w.intent
	if _, err := f.adapter.Observe(f.ctx(t), q); err == nil || len(f.wire().converts) != 1 {
		t.Fatalf("unknown export replayed on observe: %v", err)
	}
	if _, err := f.adapter.ResumePreparation(f.ctx(t), generation.PreparationRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, OwnerID: f.request.OwnerID, SubmissionKey: f.request.SubmissionKey, Offering: f.request.Offering, Input: f.request.Input, Opaque: w.intent}); err == nil || len(f.wire().converts) != 1 || f.wire().creates != 1 {
		t.Fatalf("unknown export replayed on resume: %v", err)
	}
}

func TestSmartSegmentFailedExportFailsTheDelivery(t *testing.T) {
	f := newSmartSegmentDeliveryFixture(t, "image")
	f.set(func() { f.childStatus = "failed" })
	q := f.accept(t)
	var obs generation.Observation
	var err error
	for i := 0; i < 4; i++ {
		obs, err = f.adapter.Observe(f.ctx(t), q)
		if err != nil {
			t.Fatal(err)
		}
		q.Opaque = obs.Opaque
		if obs.Phase == generation.UpstreamFailed {
			break
		}
	}
	if obs.Phase != generation.UpstreamFailed || len(obs.Artifacts) != 0 || len(f.wire().converts) != 1 {
		t.Fatalf("required export failure not terminal: %+v", obs)
	}
}

// The segmented GLB is downloaded at materialization through the bound reader,
// so bytes replaced after export cannot become the delivered model.
func TestSmartSegmentMaterializationBindsExportedSourceBytes(t *testing.T) {
	f := newSmartSegmentDeliveryFixture(t, "model")
	obs := f.observeUntilDone(t, f.accept(t))
	var source, mask generation.ArtifactClaim
	for _, claim := range obs.Artifacts {
		switch claim.Role + ":" + claim.Variant {
		case "model:glb":
			source = claim
		case "mask:png":
			mask = claim
		}
	}
	if c, err := nativeSourceMaterializationOptions(f.request.Offering, mask); err != nil || c != nil {
		t.Fatalf("mask treated as export source: %v", err)
	}
	read := func() error {
		body, _, err := f.adapter.OpenAsset(t.Context(), generation.OpenAssetRequest{Offering: f.request.Offering, Claim: source})
		if err != nil {
			return err
		}
		defer body.Close()
		_, err = io.ReadAll(body)
		return err
	}
	if err := read(); err != nil {
		t.Fatalf("bound source rejected: %v", err)
	}
	f.set(func() {
		changed := append([]byte(nil), f.sourceBytes...)
		changed[len(changed)-1] ^= 0xff
		f.sourceBytes = changed
	})
	if err := read(); err == nil {
		t.Fatal("replaced segmented GLB bytes materialized")
	}
}

func TestSmartSegmentRequiresFullDeliveryConsentBeforeUpload(t *testing.T) {
	for _, input := range []string{
		`{"source_asset":"owned-glb","seg_type":"model","output_formats":["FBX","OBJ","STL","USDZ","3MF"],"authorize_extra_credits":false}`,
		`{"source_asset":"owned-glb","seg_type":"model","output_formats":["FBX","OBJ","STL","USDZ","3MF"]}`,
		`{"source_asset":"owned-glb","seg_type":"model","output_formats":["FBX"],"authorize_extra_credits":true}`,
	} {
		f := newSmartSegmentDeliveryFixture(t, "model")
		f.request.Input = json.RawMessage(input)
		_, err := f.adapter.Start(f.ctx(t), f.request)
		if w := f.wire(); err == nil || w.uploads != 0 || w.creates != 0 {
			t.Fatalf("partial/unconsented delivery reached upstream: %s", input)
		}
	}
}

// Snapshots frozen by the 2026-10-17 candidate (GLB + mask, no exports) keep
// their original closed protocol; the shape is not mixed with the new one.
func TestSmartSegmentDeclarationShapes(t *testing.T) {
	for _, kind := range []string{"image", "model"} {
		f := newSmartSegmentDeliveryFixture(t, kind)
		if err := ValidateOffering(f.request.Offering); err != nil {
			t.Fatalf("%s current declaration: %v", kind, err)
		}
		legacy := f.request.Offering
		legacy.Cost, legacy.CostPolicy = nil, nil
		legacy.Parameters = nil
		for _, p := range f.request.Offering.Parameters {
			if p.Name != "output_formats" && p.Name != "authorize_extra_credits" {
				legacy.Parameters = append(legacy.Parameters, p)
			}
		}
		legacy.Outputs = nil
		one := 1
		for _, out := range f.request.Offering.Outputs {
			if out.Role == "model" || out.Role == "mask" {
				out.Minimum = &one
				legacy.Outputs = append(legacy.Outputs, out)
			}
		}
		var options map[string]any
		_ = json.Unmarshal(f.request.Offering.ProviderOptions, &options)
		delete(options, "convert_endpoint")
		delete(options, "native_conversion")
		legacy.ProviderOptions, _ = json.Marshal(options)
		if err := ValidateOffering(legacy); err != nil {
			t.Fatalf("%s legacy snapshot: %v", kind, err)
		}
		mixed := legacy
		mixed.Cost = f.request.Offering.Cost
		if err := ValidateOffering(mixed); err == nil {
			t.Fatalf("%s cost without exports accepted", kind)
		}
		for _, mutate := range []func(map[string]any){
			func(o map[string]any) { o["native_conversion"].(map[string]any)["source_alias"] = "mask_url" },
			func(o map[string]any) { delete(o, "convert_endpoint") },
		} {
			var bad map[string]any
			_ = json.Unmarshal(f.request.Offering.ProviderOptions, &bad)
			mutate(bad)
			broken := f.request.Offering
			broken.ProviderOptions, _ = json.Marshal(bad)
			if err := ValidateOffering(broken); err == nil {
				t.Fatalf("%s invalid export declaration accepted", kind)
			}
		}
	}
}
