package tripo_test

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
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type splatFixture struct {
	adapter                      *tripo.Adapter
	input                        *uploadedModelInputs
	request                      generation.StartRequest
	uploads, creates, polls      atomic.Int32
	body                         atomic.Value
	uploaded                     atomic.Value
	createMode, status, taskType atomic.Value
}

func newSplatFixture(t *testing.T) *splatFixture {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get("tripo/image-to-splat")
	if !ok {
		t.Fatal("candidate missing")
	}
	f := &splatFixture{input: &uploadedModelInputs{}}
	f.status.Store("success")
	f.taskType.Store("image_to_splat")
	f.createMode.Store("")
	var b bytes.Buffer
	if err = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	f.input.body = b.Bytes()
	sum := sha256.Sum256(b.Bytes())
	f.request = generation.StartRequest{TaskID: "public-splat", AttemptID: "attempt-splat", OwnerID: "owner", SubmissionKey: "submit-splat", Offering: o, Input: json.RawMessage(`{"image":"asset-image","model_seed":42}`), InputAssets: map[string][]generation.InputAsset{"image": {{ID: "asset-image", OwnerID: "owner", StorageKey: "owned-image", Size: int64(b.Len()), SHA256: hex.EncodeToString(sum[:]), MediaType: "image/png"}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v3/files":
			f.uploads.Add(1)
			m, e := r.MultipartReader()
			if e != nil {
				t.Error(e)
				return
			}
			p, e := m.NextPart()
			if e != nil {
				t.Error(e)
				return
			}
			uploaded, _ := io.ReadAll(p)
			f.uploaded.Store(uploaded)
			writeEnvelope(w, map[string]string{"file_token": "splat-file-token"})
		case "POST /v3/generation/image-to-splat":
			f.creates.Add(1)
			var body map[string]any
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			f.body.Store(body)
			if f.createMode.Load() == "lost" {
				conn, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					_ = conn.Close()
				}
				return
			}
			if f.createMode.Load() == "missing" {
				writeEnvelope(w, map[string]string{})
				return
			}
			writeEnvelope(w, map[string]string{"task_id": "remote-splat"})
		case "GET /v3/tasks/remote-splat":
			f.polls.Add(1)
			writeEnvelope(w, map[string]any{"task_id": "remote-splat", "type": f.taskType.Load(), "status": f.status.Load(), "progress": 100, "credits_consumed": 30, "output": map[string]any{"model_url": "https://cdn.tripo3d.ai/asset.splat"}})
		default:
			t.Errorf("unexpected wire %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	errors, statuses := declaredTripoMappings(t)
	f.adapter, err = tripo.New(tripo.Config{BaseURL: server.URL + "/v3", APIKey: "fixture", HTTPClient: server.Client(), InputAssets: f.input, ErrorMapping: errors, StatusMapping: statuses})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *splatFixture) resume(t *testing.T, opaque json.RawMessage, cp generation.DispatchCheckpointer) (generation.Observation, error) {
	t.Helper()
	return f.adapter.ResumePreparation(generation.WithDispatchCheckpointer(t.Context(), cp), generation.PreparationRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, OwnerID: f.request.OwnerID, SubmissionKey: f.request.SubmissionKey, Offering: f.request.Offering, Input: f.request.Input, Opaque: opaque})
}
func (f *splatFixture) observe(t *testing.T, accepted generation.Observation) (generation.Observation, error) {
	t.Helper()
	return f.adapter.Observe(t.Context(), generation.ObserveRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, Offering: f.request.Offering, Input: f.request.Input, Opaque: accepted.Opaque, Job: *accepted.Job})
}
func TestSplatPrivateUploadCreateObserve(t *testing.T) {
	f := newSplatFixture(t)
	uploadCP := &recordingCheckpointer{}
	prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(t.Context(), uploadCP), f.request)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Phase != generation.UpstreamPreparing || prepared.Job != nil || uploadCP.count() != 1 || f.creates.Load() != 0 || !bytes.Equal(f.uploaded.Load().([]byte), f.input.body) {
		t.Fatalf("upload boundary: %+v", prepared)
	}
	createCP := &recordingCheckpointer{}
	accepted, err := f.resume(t, prepared.Opaque, createCP)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Phase != generation.UpstreamAccepted || accepted.Job == nil || accepted.Job.RemoteID != "remote-splat" || createCP.count() != 1 || !reflect.DeepEqual(f.body.Load(), map[string]any{"input": "splat-file-token", "model_seed": float64(42)}) {
		t.Fatalf("create boundary: %+v %+v", accepted, f.body.Load())
	}
	completed, err := f.observe(t, accepted)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 1 {
		t.Fatalf("completion: %+v", completed)
	}
	a := completed.Artifacts[0]
	if a.Role != schema.GaussianSplatRole || a.Variant != schema.GaussianSplatVariant || a.MediaType != schema.GaussianSplatMediaType {
		t.Fatalf("identity: %+v", a)
	}
	if !bytes.Contains(completed.Opaque, []byte(`"consumed_credit":30`)) || f.uploads.Load() != 1 || f.creates.Load() != 1 || f.polls.Load() != 1 {
		t.Fatal("private usage or wire count lost")
	}
	f.status.Store("failed")
	failed, err := f.observe(t, accepted)
	if err != nil || failed.Phase != generation.UpstreamFailed {
		t.Fatalf("failure: %+v %v", failed, err)
	}
	f.status.Store("success")
	f.taskType.Store("image_to_model")
	if _, err = f.observe(t, accepted); err == nil {
		t.Fatal("wrong task type accepted")
	}
}
func TestSplatRejectsBeforeUpload(t *testing.T) {
	cases := map[string]func(*splatFixture){
		"cross owner": func(f *splatFixture) { f.request.OwnerID = "other" },
		"extra input": func(f *splatFixture) { f.request.Input = json.RawMessage(`{"image":"asset-image","model":"v3"}`) },
		"duplicate input": func(f *splatFixture) {
			f.request.Input = json.RawMessage(`{"image":"asset-image","image":"asset-image"}`)
		},
		"fractional seed": func(f *splatFixture) { f.request.Input = json.RawMessage(`{"image":"asset-image","model_seed":1.5}`) },
		"forged ID":       func(f *splatFixture) { f.request.Input = json.RawMessage(`{"image":"other-image"}`) },
		"bad hash":        func(f *splatFixture) { f.input.body = append(f.input.body, 0) },
		"actual MIME": func(f *splatFixture) {
			a := f.request.InputAssets["image"][0]
			a.MediaType = "image/webp"
			f.request.InputAssets["image"][0] = a
		},
		"oversize": func(f *splatFixture) {
			a := f.request.InputAssets["image"][0]
			a.Size = 20_000_001
			f.request.InputAssets["image"][0] = a
		},
		"second asset": func(f *splatFixture) {
			f.request.InputAssets["image"] = append(f.request.InputAssets["image"], f.request.InputAssets["image"][0])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSplatFixture(t)
			mutate(f)
			cp := &recordingCheckpointer{}
			_, err := f.adapter.Start(generation.WithDispatchCheckpointer(t.Context(), cp), f.request)
			if err == nil || f.uploads.Load() != 0 || f.creates.Load() != 0 || cp.count() != 0 {
				t.Fatalf("invalid input dispatched: %v", err)
			}
		})
	}
	f := newSplatFixture(t)
	if _, err := f.adapter.Start(context.Background(), f.request); err == nil || f.uploads.Load() != 0 {
		t.Fatal("missing permit dispatched")
	}
}
func TestSplatAmbiguousDispatchNeverRepeats(t *testing.T) {
	for _, mode := range []string{"lost", "missing"} {
		t.Run(mode, func(t *testing.T) {
			f := newSplatFixture(t)
			u := &recordingCheckpointer{}
			prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(t.Context(), u), f.request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.resume(t, u.intent, &recordingCheckpointer{}); err == nil || f.uploads.Load() != 1 || f.creates.Load() != 0 {
				t.Fatal("unknown upload replayed")
			}
			f.createMode.Store(mode)
			cp := &recordingCheckpointer{}
			if _, err = f.resume(t, prepared.Opaque, cp); err == nil {
				t.Fatal("ambiguous create accepted")
			}
			if _, err = f.resume(t, cp.intent, &recordingCheckpointer{}); err == nil || f.creates.Load() != 1 || f.uploads.Load() != 1 {
				t.Fatal("unknown create replayed")
			}
		})
	}
}
func TestSplatCheckpointBindsFrozenIdentity(t *testing.T) {
	for _, field := range []string{"task_id", "attempt_id", "submission_key", "offering_id", "offering_revision", "input_sha256"} {
		t.Run(field, func(t *testing.T) {
			f := newSplatFixture(t)
			prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(t.Context(), &recordingCheckpointer{}), f.request)
			if err != nil {
				t.Fatal(err)
			}
			var state map[string]any
			if err = json.Unmarshal(prepared.Opaque, &state); err != nil {
				t.Fatal(err)
			}
			state[field] = "changed"
			raw, _ := json.Marshal(state)
			if _, err = f.resume(t, raw, &recordingCheckpointer{}); err == nil || f.creates.Load() != 0 {
				t.Fatal("changed checkpoint dispatched")
			}
		})
	}
}
