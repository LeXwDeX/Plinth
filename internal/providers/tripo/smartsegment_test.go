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
	"os"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

type smartSegmentFixture struct {
	adapter                                      *tripo.Adapter
	input                                        *uploadedModelInputs
	request                                      generation.StartRequest
	uploads, creates, polls                      atomic.Int32
	uploaded                                     []byte
	uploadContentType, uploadName, authorization string
	createBody                                   map[string]any
	createMode                                   string
	uploadMode                                   string
	remoteType                                   string
	outputMode                                   string
}

func newSmartSegmentFixture(t *testing.T, kind string, configure ...func(*tripo.Config)) *smartSegmentFixture {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get(generation.OfferingID("tripo/geometry/smartsegment-" + kind))
	if !ok {
		t.Fatal("Smart Segmentation declaration missing")
	}
	f := &smartSegmentFixture{input: &uploadedModelInputs{}, remoteType: "smartsegment_" + kind}
	name, id, media := "image", "owned-image", "image/png"
	if kind == "image" {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		f.input.body = b.Bytes()
	} else {
		name, id, media = "source_asset", "owned-glb", "model/gltf-binary"
		f.input.body = modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Interleaved: true, Textured: true})
	}
	sum := sha256.Sum256(f.input.body)
	input := map[string]any{name: id, "seg_type": kind, "granularity": "fine", "hint": "separate arms", "output_formats": []string{"FBX", "OBJ", "STL", "USDZ", "3MF"}, "authorize_extra_credits": true}
	raw, _ := json.Marshal(input)
	f.request = generation.StartRequest{TaskID: "public-smartseg", AttemptID: "attempt-smartseg", OwnerID: "owner", SubmissionKey: "smartseg-submit", Offering: o, Input: raw, InputAssets: map[string][]generation.InputAsset{name: {{ID: generation.AssetID(id), OwnerID: "owner", StorageKey: "owned", Size: int64(len(f.input.body)), SHA256: hex.EncodeToString(sum[:]), MediaType: media}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.authorization = r.Header.Get("Authorization")
		switch r.Method + " " + r.URL.Path {
		case "POST /v3/files":
			f.uploads.Add(1)
			if f.uploadMode == "lost" {
				conn, _, _ := w.(http.Hijacker).Hijack()
				if conn != nil {
					conn.Close()
				}
				return
			}
			m, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			part, err := m.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			f.uploadName, f.uploadContentType = part.FileName(), part.Header.Get("Content-Type")
			f.uploaded, _ = io.ReadAll(part)
			writeEnvelope(w, map[string]any{"file_token": "file_smartseg"})
		case "POST /v3/mesh/smartsegment":
			f.creates.Add(1)
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("create is not JSON")
			}
			if err := json.NewDecoder(r.Body).Decode(&f.createBody); err != nil {
				t.Error(err)
			}
			if f.createMode == "lost" {
				conn, _, _ := w.(http.Hijacker).Hijack()
				if conn != nil {
					conn.Close()
				}
				return
			}
			if f.createMode == "missing" {
				writeEnvelope(w, map[string]any{})
				return
			}
			writeEnvelope(w, map[string]any{"task_id": "remote-smartseg"})
		case "GET /v3/tasks/remote-smartseg":
			f.polls.Add(1)
			output := map[string]any{"mask_url": "https://cdn.tripo3d.ai/mask.png", "seg_model_url": "https://cdn.tripo3d.ai/seg.glb", "seg_task_id": "private-seg", "model_task_id": "private-model"}
			if f.outputMode == "missing-mask" {
				delete(output, "mask_url")
			}
			writeEnvelope(w, map[string]any{"task_id": "remote-smartseg", "type": f.remoteType, "status": "success", "progress": 100, "credits_consumed": 85.25, "output": output})
		default:
			t.Errorf("unexpected wire %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	errors, statuses := declaredTripoMappings(t)
	config := tripo.Config{BaseURL: server.URL + "/v3", APIKey: "fixture-key", HTTPClient: server.Client(), InputAssets: f.input, ErrorMapping: errors, StatusMapping: statuses}
	for _, change := range configure {
		change(&config)
	}
	f.adapter, err = tripo.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *smartSegmentFixture) start(t *testing.T) (generation.Observation, error) {
	t.Helper()
	return f.adapter.Start(generation.WithDispatchCheckpointer(t.Context(), &recordingCheckpointer{}), f.request)
}
func (f *smartSegmentFixture) resume(t *testing.T, opaque json.RawMessage) (generation.Observation, error) {
	t.Helper()
	return f.resumeWith(t, opaque, &recordingCheckpointer{})
}
func (f *smartSegmentFixture) resumeWith(t *testing.T, opaque json.RawMessage, cp *recordingCheckpointer) (generation.Observation, error) {
	t.Helper()
	return f.adapter.ResumePreparation(generation.WithDispatchCheckpointer(t.Context(), cp), generation.PreparationRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, OwnerID: f.request.OwnerID, SubmissionKey: f.request.SubmissionKey, Offering: f.request.Offering, Input: f.request.Input, Opaque: opaque})
}

func TestSmartSegmentOfficialWireAndPrivateResults(t *testing.T) {
	for _, kind := range []string{"image", "model"} {
		t.Run(kind, func(t *testing.T) {
			f := newSmartSegmentFixture(t, kind)
			prepared, err := f.start(t)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Phase != generation.UpstreamPreparing || prepared.Job != nil || f.uploads.Load() != 1 || f.creates.Load() != 0 || !bytes.Equal(f.uploaded, f.input.body) || f.authorization != "Bearer fixture-key" {
				t.Fatalf("upload: %+v", prepared)
			}
			if kind == "image" && (f.uploadName != "input.png" || f.uploadContentType != "image/png") {
				t.Fatalf("image multipart: %s %s", f.uploadName, f.uploadContentType)
			}
			if kind == "model" && (f.uploadName != "input.glb" || f.uploadContentType != "model/gltf-binary") {
				t.Fatalf("model multipart: %s %s", f.uploadName, f.uploadContentType)
			}
			accepted, err := f.resume(t, prepared.Opaque)
			if err != nil {
				t.Fatal(err)
			}
			if accepted.Phase != generation.UpstreamAccepted || accepted.Job == nil || accepted.Job.RemoteID != "remote-smartseg" || f.creates.Load() != 1 {
				t.Fatalf("create: %+v", accepted)
			}
			want := map[string]any{"seg_type": kind, "input": "file_smartseg", "granularity": "fine", "hint": "separate arms"}
			if kind == "model" {
				want["transform"] = []any{float64(1), float64(0), float64(0), float64(0), float64(0), float64(1), float64(0), float64(0), float64(0), float64(0), float64(1), float64(0), float64(0), float64(0), float64(0), float64(1)}
			}
			if !reflect.DeepEqual(f.createBody, want) {
				t.Fatalf("body=%v want=%v", f.createBody, want)
			}
			// Result slots, private child IDs and the five required exports are
			// covered by the in-package delivery tests (smartsegment_delivery_test.go).
		})
	}
}

func TestSmartSegmentRejectsWrongSourceBeforeChargeAndNeverReplaysUnknownCreate(t *testing.T) {
	for _, kind := range []string{"image", "model"} {
		t.Run(kind, func(t *testing.T) {
			for _, mutate := range []func(*smartSegmentFixture){
				func(f *smartSegmentFixture) { f.request.OwnerID = "another-owner" },
				func(f *smartSegmentFixture) { f.input.body = append(f.input.body, 0) },
				func(f *smartSegmentFixture) {
					name := "image"
					wrongMedia := "model/gltf-binary"
					if kind == "model" {
						name, wrongMedia = "source_asset", "image/png"
					}
					asset := f.request.InputAssets[name][0]
					asset.MediaType = wrongMedia
					f.request.InputAssets[name] = []generation.InputAsset{asset}
				},
				func(f *smartSegmentFixture) {
					wrong := "model"
					if kind == "model" {
						wrong = "image"
					}
					var values map[string]any
					_ = json.Unmarshal(f.request.Input, &values)
					values["seg_type"] = wrong
					f.request.Input, _ = json.Marshal(values)
				},
				func(f *smartSegmentFixture) {
					f.request.Input = json.RawMessage(`{"seg_type":"` + kind + `","granularity":"fine","hint":"separate arms","` + map[bool]string{true: "image", false: "source_asset"}[kind == "image"] + `":"forged"}`)
				},
			} {
				f := newSmartSegmentFixture(t, kind)
				mutate(f)
				if _, err := f.start(t); err == nil || f.uploads.Load() != 0 || f.creates.Load() != 0 {
					t.Fatalf("invalid source charged: %v", err)
				}
			}
			f := newSmartSegmentFixture(t, kind)
			if _, err := f.adapter.Start(context.Background(), f.request); err == nil || f.uploads.Load() != 0 {
				t.Fatal("missing durable permit dispatched")
			}
			prepared, err := f.start(t)
			if err != nil {
				t.Fatal(err)
			}
			f.createMode = "lost"
			cp := &recordingCheckpointer{}
			if _, err = f.resumeWith(t, prepared.Opaque, cp); err == nil {
				t.Fatal("unknown create accepted")
			}
			if f.creates.Load() != 1 {
				t.Fatal("create count")
			}
			if _, err = f.resume(t, cp.intent); err == nil || f.creates.Load() != 1 {
				t.Fatal("unknown create intent was replayed")
			}
		})
	}
}

func TestSmartSegmentUnknownUploadAndOldCheckpointNeverReplay(t *testing.T) {
	for _, kind := range []string{"image", "model"} {
		t.Run(kind, func(t *testing.T) {
			f := newSmartSegmentFixture(t, kind)
			f.uploadMode = "lost"
			cp := &recordingCheckpointer{}
			if _, err := f.adapter.Start(generation.WithDispatchCheckpointer(t.Context(), cp), f.request); err == nil || f.uploads.Load() != 1 || f.creates.Load() != 0 {
				t.Fatal("unknown upload did not stop")
			}
			if len(cp.intent) == 0 {
				t.Fatal("upload intent was not durably captured")
			}
			if _, err := f.resume(t, cp.intent); err == nil || f.uploads.Load() != 1 || f.creates.Load() != 0 {
				t.Fatal("unknown upload was replayed")
			}
			var old map[string]any
			if err := json.Unmarshal(cp.intent, &old); err != nil {
				t.Fatal(err)
			}
			delete(old, "upload_sha256")
			delete(old, "upload_media_type")
			delete(old, "upload_size")
			oldRaw, _ := json.Marshal(old)
			if _, err := f.resume(t, oldRaw); err == nil || f.uploads.Load() != 1 || f.creates.Load() != 0 {
				t.Fatal("legacy checkpoint was replayed")
			}
		})
	}
}

func TestSmartSegmentRejectsInvalidModelTransformBeforeUpload(t *testing.T) {
	f := newSmartSegmentFixture(t, "model")
	var options map[string]any
	if err := json.Unmarshal(f.request.Offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	options["transform"] = []any{float64(1), float64(0)}
	f.request.Offering.ProviderOptions, _ = json.Marshal(options)
	if err := tripo.ValidateOffering(f.request.Offering); err == nil {
		t.Fatal("invalid matrix declaration accepted")
	}
	if _, err := f.start(t); err == nil || f.uploads.Load() != 0 || f.creates.Load() != 0 {
		t.Fatal("invalid matrix reached upstream")
	}
}

func TestSmartSegmentCheckpointBindsFrozenOwnerOfferingAndInput(t *testing.T) {
	for _, kind := range []string{"image", "model"} {
		for _, field := range []string{"task_id", "attempt_id", "owner_id", "offering_id", "offering_revision", "offering_sha256", "input_sha256", "submission_key", "upload_sha256", "upload_media_type", "upload_size", "version", "kind"} {
			t.Run(kind+"/"+field, func(t *testing.T) {
				f := newSmartSegmentFixture(t, kind)
				prepared, err := f.start(t)
				if err != nil {
					t.Fatal(err)
				}
				var checkpoint map[string]any
				if err := json.Unmarshal(prepared.Opaque, &checkpoint); err != nil {
					t.Fatal(err)
				}
				checkpoint[field] = "forged"
				forged, _ := json.Marshal(checkpoint)
				if _, err := f.resume(t, forged); err == nil || f.creates.Load() != 0 {
					t.Fatal("forged checkpoint dispatched")
				}
			})
		}
	}
}

func TestSmartSegmentWebPSourceUploadsPNGAndFreezesDerivedBytes(t *testing.T) {
	f := newSmartSegmentFixture(t, "image")
	raw, err := os.ReadFile("../../../public/gpt-skills/_placeholder.webp")
	if err != nil {
		t.Fatal(err)
	}
	f.input.body = raw
	asset := f.request.InputAssets["image"][0]
	asset.Size = int64(len(raw))
	sum := sha256.Sum256(raw)
	asset.SHA256 = hex.EncodeToString(sum[:])
	asset.MediaType = "image/webp"
	f.request.InputAssets["image"] = []generation.InputAsset{asset}
	prepared, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if f.uploads.Load() != 1 || f.uploadName != "input.png" || f.uploadContentType != "image/png" || bytes.Equal(f.uploaded, raw) || len(f.uploaded) > 20_000_000 {
		t.Fatal("WebP was not uploaded as a bounded PNG")
	}
	original, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	derived, format, err := image.Decode(bytes.NewReader(f.uploaded))
	if err != nil || format != "png" || !derived.Bounds().Eq(original.Bounds()) {
		t.Fatal("derived PNG dimensions differ")
	}
	for y := original.Bounds().Min.Y; y < original.Bounds().Max.Y; y++ {
		for x := original.Bounds().Min.X; x < original.Bounds().Max.X; x++ {
			or, og, ob, oa := original.At(x, y).RGBA()
			dr, dg, db, da := derived.At(x, y).RGBA()
			if or != dr || og != dg || ob != db || oa != da {
				t.Fatal("derived PNG changed pixel or alpha")
			}
		}
	}
	var checkpoint map[string]any
	if err := json.Unmarshal(prepared.Opaque, &checkpoint); err != nil {
		t.Fatal(err)
	}
	derivedSum := sha256.Sum256(f.uploaded)
	if checkpoint["upload_media_type"] != "image/png" || checkpoint["upload_sha256"] != hex.EncodeToString(derivedSum[:]) || checkpoint["upload_size"] != float64(len(f.uploaded)) {
		t.Fatal("derived upload is not frozen in checkpoint")
	}
	if _, err := f.resume(t, prepared.Opaque); err != nil || f.creates.Load() != 1 {
		t.Fatalf("create after WebP upload: %v", err)
	}
}
