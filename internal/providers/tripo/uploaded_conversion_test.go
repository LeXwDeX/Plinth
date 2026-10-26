package tripo_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type uploadedModelInputs struct {
	body  []byte
	opens atomic.Int32
}

func (*uploadedModelInputs) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}
func (s *uploadedModelInputs) Open(context.Context, generation.InputAsset) (io.ReadCloser, error) {
	s.opens.Add(1)
	return io.NopCloser(bytes.NewReader(s.body)), nil
}

type uploadedConversionFixture struct {
	convertMode                 atomic.Value
	adapter                     *tripo.Adapter
	inputs                      *uploadedModelInputs
	request                     generation.StartRequest
	uploads, converts, gets     atomic.Int32
	body                        map[string]any
	uploadBytes                 []byte
	uploadName, uploadMediaType string
	server                      *httptest.Server
	paths                       []string
}

func uploadedConversionOffering() generation.Offering {
	minimum := 1
	return generation.Offering{ID: "tripo/format-conversion/uploaded-glb-to-stl", ProviderID: "tripo", AdapterKey: "tripo", Capability: generation.FormatConversion, PollInterval: time.Second,
		Cost:    &generation.CostDeclaration{BaseCredits: 5, FormatParameter: "format"},
		Outputs: []generation.AssetRequirement{{Role: "model", Variant: "converted.stl", Minimum: &minimum, MediaTypes: []string{"model/stl"}}},
		Parameters: []generation.Parameter{
			{Name: "source_asset", Type: "asset", Required: true, Semantic: generation.SemanticSourceAsset, WireName: "input", UploadedModel: &schema.UploadedModel{ProfileID: "static-glb-v1", NativeOnly: true}},
			{Name: "format", Type: "select", Required: true, Fixed: true, Default: json.RawMessage(`"stl"`), WireName: "format", WireValues: map[string]json.RawMessage{"stl": json.RawMessage(`"STL"`)}, Options: []generation.ParameterOption{{Value: "stl"}}},
		}, ProviderOptions: json.RawMessage(`{"endpoint":"models/convert","upload_endpoint":"files","observe_endpoint":"tasks/{task_id}","response_aliases":[{"key":"model_url","claim_id":"converted.stl","role":"model","variant":"converted.stl","fields":["model_url"]}]}`)}
}
func newUploadedConversionFixture(t *testing.T) *uploadedConversionFixture {
	t.Helper()
	return uploadedConversionFixtureFor(t, uploadedConversionOffering(), "")
}

func uploadedConversionFixtureFor(t *testing.T, offering generation.Offering, basePath string) *uploadedConversionFixture {
	t.Helper()
	f := &uploadedConversionFixture{inputs: &uploadedModelInputs{body: modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Interleaved: true, Textured: true})}}
	digest := sha256.Sum256(f.inputs.body)
	f.request = generation.StartRequest{TaskID: "task-uploaded-conversion", OwnerID: "owner-A", AttemptID: "attempt-uploaded-conversion", SubmissionKey: "uploaded-conversion-1", Offering: offering, Input: json.RawMessage(`{"source_asset":"owned-upload","format":"stl"}`), InputAssets: map[string][]generation.InputAsset{"source_asset": {{ID: "owned-upload", OwnerID: "owner-A", StorageKey: "private-content-key", MediaType: "model/gltf-binary", Size: int64(len(f.inputs.body)), SHA256: hex.EncodeToString(digest[:])}}}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.Method + " " + r.URL.Path
		f.paths = append(f.paths, path)
		switch path {
		case "POST " + basePath + "/files":
			f.uploads.Add(1)
			multipartReader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				http.Error(w, "invalid multipart", 400)
				return
			}
			part, err := multipartReader.NextPart()
			if err != nil {
				t.Error(err)
				http.Error(w, "invalid multipart", 400)
				return
			}
			f.uploadName = part.FileName()
			f.uploadMediaType = part.Header.Get("Content-Type")
			f.uploadBytes, _ = io.ReadAll(part)
			writeEnvelope(w, map[string]string{"file_token": "file_model_183"})
		case "POST " + basePath + "/models/convert":
			f.converts.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&f.body); err != nil {
				t.Error(err)
			}
			mode, _ := f.convertMode.Load().(string)
			if mode == "missing_id" {
				writeEnvelope(w, map[string]string{})
				return
			}
			if mode == "rejected" {
				_, _ = w.Write([]byte(`{"code":2000,"message":"PRIVATE_UPSTREAM_MESSAGE","data":{}}`))
				return
			}
			if mode == "lost_response" {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
				return
			}
			writeEnvelope(w, map[string]string{"task_id": "task_real_conversion_183"})
		case "GET " + basePath + "/tasks/task_real_conversion_183":
			f.gets.Add(1)
			writeEnvelope(w, map[string]any{"task_id": "task_real_conversion_183", "status": "success", "progress": 100, "credits_consumed": 5, "output": map[string]string{"model_url": f.server.URL + "/converted.stl"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	errorMapping, statusMapping := declaredTripoMappings(t)
	var err error
	f.adapter, err = tripo.New(tripo.Config{BaseURL: f.server.URL + basePath, APIKey: "fixture-key", HTTPClient: f.server.Client(), InputAssets: f.inputs, ErrorMapping: errorMapping, StatusMapping: statusMapping})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestDefaultUploadedConversionUsesVersionedBaseURL(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("tripo/format-conversion/uploaded-glb-to-stl")
	if !ok {
		t.Fatal("missing default uploaded conversion")
	}
	for _, basePath := range []string{"/v3", "/gateway/v3"} {
		t.Run(basePath, func(t *testing.T) {
			// Use the actual Catalog routes, not the handwritten fixture options.
			f := uploadedConversionFixtureFor(t, offering, basePath)
			uploadCP := &recordingCheckpointer{}
			prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(t.Context(), uploadCP), f.request)
			if err != nil {
				t.Fatalf("upload through versioned base failed (%v), requests=%v", err, f.paths)
			}
			if prepared.Phase != generation.UpstreamPreparing || prepared.Job != nil || uploadCP.count() != 1 || f.converts.Load() != 0 {
				t.Fatalf("upload must save a private token before conversion: %#v", prepared)
			}
			if !bytes.Equal(f.uploadBytes, f.inputs.body) || !strings.HasSuffix(f.uploadName, ".glb") || f.uploadMediaType != "model/gltf-binary" {
				t.Fatal("multipart payload differs from frozen GLB")
			}
			convertCP := &recordingCheckpointer{}
			accepted, err := uploadedResume(t, f, prepared.Opaque, convertCP)
			if err != nil || accepted.Job == nil || accepted.Job.RemoteID != "task_real_conversion_183" || convertCP.count() != 1 {
				t.Fatalf("conversion was not accepted: %#v %v", accepted, err)
			}
			if !reflect.DeepEqual(f.body, map[string]any{"input": "file_model_183", "format": "STL"}) {
				t.Fatalf("unexpected conversion body: %#v", f.body)
			}
			completed, err := f.adapter.Observe(t.Context(), generation.ObserveRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, Offering: offering, Input: f.request.Input, Job: *accepted.Job, Opaque: accepted.Opaque})
			if err != nil || completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 1 || completed.Artifacts[0].Variant != "stl" || completed.Artifacts[0].MediaType != "model/stl" {
				t.Fatalf("converted STL was not observed: %#v %v", completed, err)
			}
			want := []string{"POST " + basePath + "/files", "POST " + basePath + "/models/convert", "GET " + basePath + "/tasks/task_real_conversion_183"}
			if !reflect.DeepEqual(f.paths, want) || f.uploads.Load() != 1 || f.converts.Load() != 1 || f.gets.Load() != 1 {
				t.Fatalf("requests=%v want=%v", f.paths, want)
			}
		})
	}
}

type preparationResumer interface {
	ResumePreparation(context.Context, generation.PreparationRequest) (generation.Observation, error)
}

func uploadedResume(t *testing.T, f *uploadedConversionFixture, checkpoint json.RawMessage, cp generation.DispatchCheckpointer) (generation.Observation, error) {
	t.Helper()
	resumer, ok := any(f.adapter).(preparationResumer)
	if !ok {
		t.Fatal("uploaded model adapter must expose the private preparation continuation")
	}
	return resumer.ResumePreparation(generation.WithDispatchCheckpointer(context.Background(), cp), generation.PreparationRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, SubmissionKey: f.request.SubmissionKey, OwnerID: f.request.OwnerID, Offering: f.request.Offering, Input: f.request.Input, Opaque: checkpoint})
}
func TestUploadedConversionUsesExactGLBAndPrivateTwoStageWire(t *testing.T) {
	f := newUploadedConversionFixture(t)
	uploadCP := &recordingCheckpointer{}
	prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), uploadCP), f.request)
	if err != nil {
		t.Fatal(err)
	}
	if string(prepared.Phase) != "PREPARING" || prepared.Job != nil || prepared.Dispatch == nil || uploadCP.count() != 1 || f.uploads.Load() != 1 || f.converts.Load() != 0 {
		t.Fatalf("upload must persist preparation without claiming a conversion job: %#v", prepared)
	}
	if !bytes.Equal(f.uploadBytes, f.inputs.body) || !strings.HasSuffix(f.uploadName, ".glb") || f.uploadMediaType != "model/gltf-binary" {
		t.Fatal("uploaded payload or metadata differs from frozen source")
	}
	convertCP := &recordingCheckpointer{}
	accepted, err := uploadedResume(t, f, prepared.Opaque, convertCP)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Job == nil || accepted.Job.RemoteID != "task_real_conversion_183" || accepted.Phase != generation.UpstreamAccepted || accepted.Dispatch == nil || convertCP.count() != 1 {
		t.Fatalf("expected actual accepted convert job: %#v", accepted)
	}
	if !reflect.DeepEqual(f.body, map[string]any{"input": "file_model_183", "format": "STL"}) {
		t.Fatalf("non-basic or malformed wire: %#v", f.body)
	}
	if f.uploads.Load() != 1 || f.converts.Load() != 1 {
		t.Fatal("each private mutation must execute once")
	}
	completed, err := f.adapter.Observe(context.Background(), generation.ObserveRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, Offering: f.request.Offering, Input: f.request.Input, Job: *accepted.Job, Opaque: accepted.Opaque})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 1 || completed.Artifacts[0].Role != "model" || completed.Artifacts[0].Variant != "converted.stl" {
		t.Fatalf("missing required STL claim: %#v", completed)
	}
	if completed.Artifacts[0].MediaType != "model/stl" {
		t.Fatalf("wrong STL MIME: %q", completed.Artifacts[0].MediaType)
	}
	if f.uploads.Load() != 1 || f.converts.Load() != 1 || f.gets.Load() != 1 {
		t.Fatal("observing known job must never mutate")
	}
}
func TestUploadedConversionRequiresDispatchAuthorizationBeforeReadingOrNetwork(t *testing.T) {
	for _, kind := range []string{"missing", "denied", "pending"} {
		t.Run(kind, func(t *testing.T) {
			f := newUploadedConversionFixture(t)
			ctx := context.Background()
			if kind == "denied" {
				ctx = generation.WithDeniedDispatchCheckpointer(ctx)
			}
			if kind == "pending" {
				ctx = generation.WithDispatchCheckpointer(ctx, &recordingCheckpointer{pended: true})
			}
			_, err := f.adapter.Start(ctx, f.request)
			if err == nil {
				t.Fatal("missing execution fence accepted")
			}
			if kind == "pending" && !errors.Is(err, generation.ErrDispatchPending) {
				t.Fatalf("pending identity lost: %v", err)
			}
			if f.inputs.opens.Load() != 0 || f.uploads.Load() != 0 || f.converts.Load() != 0 {
				t.Fatal("unapproved adapter read or sent source")
			}
		})
	}
}
func TestUploadedConversionRejectsUnfrozenOrAdditionalInputWithoutNetwork(t *testing.T) {
	for _, kind := range []string{"missing", "different_id", "two_assets", "wrong_mime", "empty_owner", "bad_hash", "zero_size", "wrong_format", "advanced", "extra_task"} {
		t.Run(kind, func(t *testing.T) {
			f := newUploadedConversionFixture(t)
			switch kind {
			case "missing":
				f.request.InputAssets = nil
			case "different_id":
				f.request.InputAssets["source_asset"][0].ID = "another-upload"
			case "two_assets":
				f.request.InputAssets["source_asset"] = append(f.request.InputAssets["source_asset"], f.request.InputAssets["source_asset"][0])
			case "wrong_mime":
				f.request.InputAssets["source_asset"][0].MediaType = "model/gltf+json"
			case "empty_owner":
				f.request.InputAssets["source_asset"][0].OwnerID = ""
			case "bad_hash":
				f.request.InputAssets["source_asset"][0].SHA256 = "bad"
			case "zero_size":
				f.request.InputAssets["source_asset"][0].Size = 0
			case "wrong_format":
				f.request.Input = json.RawMessage(`{"source_asset":"owned-upload","format":"obj"}`)
			case "advanced":
				f.request.Input = json.RawMessage(`{"source_asset":"owned-upload","format":"stl","quad":true}`)
			case "extra_task":
				f.request.TaskDependencies = map[string]generation.TaskDependency{"source_task": {TaskID: "unapproved-task"}}
			}
			cp := &recordingCheckpointer{}
			_, err := f.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), cp), f.request)
			if err == nil || f.inputs.opens.Load() != 0 || f.uploads.Load() != 0 || f.converts.Load() != 0 || cp.count() != 0 {
				t.Fatalf("invalid frozen input read or dispatched (%v)", err)
			}
		})
	}
}
func TestUploadedConversionActualByteMismatchSettlesUnusedPermit(t *testing.T) {
	f := newUploadedConversionFixture(t)
	f.inputs.body = append([]byte(nil), f.inputs.body...)
	f.inputs.body[len(f.inputs.body)-1] ^= 1
	cp := &recordingCheckpointer{}
	observation, err := f.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), cp), f.request)
	if err != nil {
		t.Fatal("preflight failure must return unused-permit settlement, not strand a checkpoint", err)
	}
	if observation.Phase != generation.UpstreamFailed || observation.Failure == nil || observation.Dispatch == nil || observation.Dispatch.Result != generation.DispatchResultNotDispatched {
		t.Fatalf("mismatch did not fail closed with unused permit: %#v", observation)
	}
	if cp.count() != 1 || f.inputs.opens.Load() != 1 || f.uploads.Load() != 0 || f.converts.Load() != 0 {
		t.Fatal("mismatched actual bytes reached upstream")
	}
}

func requireUploadedUnknown(t *testing.T, err error) {
	t.Helper()
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil || !provider.Failure.Ambiguous || provider.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("expected non-resubmittable unknown outcome: %v", err)
	}
}
func TestUploadedConversionLostUploadReceiptNeverReuploadsOrConverts(t *testing.T) {
	f := newUploadedConversionFixture(t)
	cp := &recordingCheckpointer{}
	prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), cp), f.request)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Opaque) == 0 || bytes.Equal(prepared.Opaque, cp.intent) {
		t.Fatal("upload token must be a returned checkpoint distinct from pre-upload intent")
	}
	var intent map[string]any
	if err := json.Unmarshal(cp.intent, &intent); err != nil {
		t.Fatal(err)
	}
	if intent["phase"] != "upload_dispatching" || intent["file_token"] != nil {
		t.Fatal("token was fabricated before upload response")
	}
	for i := 0; i < 2; i++ {
		_, err = uploadedResume(t, f, cp.intent, &recordingCheckpointer{})
		requireUploadedUnknown(t, err)
	}
	if f.uploads.Load() != 1 || f.converts.Load() != 0 || f.inputs.opens.Load() != 1 {
		t.Fatal("unknown upload receipt repeated a mutation or source read")
	}
}
func TestUploadedConversionCrashBeforeConvertAndLostRemoteIdentityNeverRepost(t *testing.T) {
	for _, mode := range []string{"checkpoint_pending", "missing_id", "lost_response"} {
		t.Run(mode, func(t *testing.T) {
			f := newUploadedConversionFixture(t)
			uploadCP := &recordingCheckpointer{}
			prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), uploadCP), f.request)
			if err != nil {
				t.Fatal(err)
			}
			convertCP := &recordingCheckpointer{pended: mode == "checkpoint_pending"}
			f.convertMode.Store(mode)
			_, err = uploadedResume(t, f, prepared.Opaque, convertCP)
			if mode == "checkpoint_pending" {
				if !errors.Is(err, generation.ErrDispatchPending) {
					t.Fatalf("pending fence lost: %v", err)
				}
			} else {
				var provider *generation.ProviderError
				if !errors.As(err, &provider) || provider.Failure == nil || !provider.Failure.Ambiguous {
					t.Fatalf("accepted/unknown convert treated as safe retry: %v", err)
				}
			}
			expected := int32(1)
			if mode == "checkpoint_pending" {
				expected = 0
			}
			if f.converts.Load() != expected {
				t.Fatal("convert was sent without its permit or repeated by transport")
			}
			for i := 0; i < 2; i++ {
				_, err = uploadedResume(t, f, convertCP.intent, &recordingCheckpointer{})
				requireUploadedUnknown(t, err)
			}
			if f.uploads.Load() != 1 || f.converts.Load() != expected {
				t.Fatal("pending convert intent was resubmitted")
			}
		})
	}
}
func TestUploadedConversionResumeRejectsChangedLeaseIdentityBeforePermit(t *testing.T) {
	for _, field := range []string{"owner", "task", "attempt", "submission", "offering", "revision", "input", "duplicate", "extra"} {
		t.Run(field, func(t *testing.T) {
			f := newUploadedConversionFixture(t)
			prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), &recordingCheckpointer{}), f.request)
			if err != nil {
				t.Fatal(err)
			}
			request := generation.PreparationRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, SubmissionKey: f.request.SubmissionKey, OwnerID: f.request.OwnerID, Offering: f.request.Offering, Input: f.request.Input, Opaque: prepared.Opaque}
			switch field {
			case "owner":
				request.OwnerID = "owner-B"
			case "task":
				request.TaskID = "other-task"
			case "attempt":
				request.AttemptID = "other-attempt"
			case "submission":
				request.SubmissionKey = "other-submission"
			case "offering":
				request.Offering.ID = "another-offering"
			case "revision":
				request.Offering.Revision = "another-revision"
			case "input":
				request.Input = json.RawMessage(`{"source_asset":"other-upload","format":"stl"}`)
			case "duplicate":
				request.Opaque = bytes.Replace(request.Opaque, []byte(`"phase":"token_ready"`), []byte(`"phase":"submitted","phase":"token_ready"`), 1)
			case "extra":
				request.Opaque = append(append([]byte{}, request.Opaque[:len(request.Opaque)-1]...), []byte(`,"unknown":true}`)...)
			}
			cp := &recordingCheckpointer{}
			_, err = f.adapter.ResumePreparation(generation.WithDispatchCheckpointer(context.Background(), cp), request)
			if err == nil || cp.count() != 0 || f.converts.Load() != 0 || f.uploads.Load() != 1 {
				t.Fatal("changed preparation identity reached dispatch")
			}
		})
	}
}
func TestUploadedConversionKnownRemoteRestartOnlyObservesAndRetainsSourceCheckpoint(t *testing.T) {
	f := newUploadedConversionFixture(t)
	prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), &recordingCheckpointer{}), f.request)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := uploadedResume(t, f, prepared.Opaque, &recordingCheckpointer{})
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct the adapter, and make the local source unreadable/wrong after acceptance.
	f.inputs.body = []byte("source changed after paid task acceptance")
	mappings, statuses := declaredTripoMappings(t)
	restarted, err := tripo.New(tripo.Config{BaseURL: f.server.URL, APIKey: "fixture-key", HTTPClient: f.server.Client(), InputAssets: f.inputs, ErrorMapping: mappings, StatusMapping: statuses})
	if err != nil {
		t.Fatal(err)
	}
	opaque := accepted.Opaque
	for i := 0; i < 2; i++ {
		observation, err := restarted.Observe(generation.WithDeniedDispatchCheckpointer(context.Background()), generation.ObserveRequest{TaskID: f.request.TaskID, AttemptID: f.request.AttemptID, Offering: f.request.Offering, Input: f.request.Input, Job: *accepted.Job, Opaque: opaque})
		if err != nil {
			t.Fatal(err)
		}
		var state map[string]any
		if json.Unmarshal(observation.Opaque, &state) != nil || state["phase"] != "submitted" || state["file_token"] != "file_model_183" || state["remote_id"] != accepted.Job.RemoteID || state["source"] == nil {
			t.Fatal("normal observation erased private source identity")
		}
		opaque = observation.Opaque
	}
	if f.uploads.Load() != 1 || f.converts.Load() != 1 || f.gets.Load() != 2 || f.inputs.opens.Load() != 1 {
		t.Fatal("known paid task replay performed new preparation")
	}
}
func TestUploadedConversionDefinitiveRejectionSettlesItsDispatchedReceipt(t *testing.T) {
	f := newUploadedConversionFixture(t)
	prepared, err := f.adapter.Start(generation.WithDispatchCheckpointer(context.Background(), &recordingCheckpointer{}), f.request)
	if err != nil {
		t.Fatal(err)
	}
	f.convertMode.Store("rejected")
	observation, err := uploadedResume(t, f, prepared.Opaque, &recordingCheckpointer{})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamFailed || observation.Failure == nil || observation.Dispatch == nil || observation.Dispatch.Result != generation.DispatchResultDispatched || strings.Contains(observation.Failure.SafeDetail, "PRIVATE_") {
		t.Fatalf("definitive rejection lost its settled receipt: %#v", observation)
	}
	if f.converts.Load() != 1 || f.uploads.Load() != 1 {
		t.Fatal("rejection repeated a private request")
	}
}

func TestUploadedConversionRequiresExplicitNativeOnlySourceDeclaration(t *testing.T) {
	// This declaration boundary does not need status mapping or Catalog loading.
	// The mock accepts uploads so a missing native-only fence is observable.
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	inputs := &uploadedModelInputs{body: raw}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeEnvelope(w, map[string]string{"file_token": "file_native_test"})
	}))
	defer server.Close()
	port, err := tripo.New(tripo.Config{BaseURL: server.URL, APIKey: "fixture-key", HTTPClient: server.Client(), InputAssets: inputs})
	if err != nil {
		t.Fatal(err)
	}
	route := uploadedConversionOffering()
	route.Parameters[0].UploadedModel.NativeOnly = false
	sum := sha256.Sum256(raw)
	request := generation.StartRequest{OwnerID: "owner", TaskID: "native-task", AttemptID: "native-attempt", SubmissionKey: "native-submission", Offering: route, Input: json.RawMessage(`{"source_asset":"upload","format":"stl"}`), InputAssets: map[string][]generation.InputAsset{"source_asset": {{ID: "upload", OwnerID: "owner", MediaType: "model/gltf-binary", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))}}}}
	cp := &recordingCheckpointer{}
	_, err = port.Start(generation.WithDispatchCheckpointer(context.Background(), cp), request)
	if err == nil || cp.count() != 0 || inputs.opens.Load() != 0 || requests.Load() != 0 {
		t.Fatal("non-native source declaration reached upload dispatch")
	}
}
