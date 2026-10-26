package hyper3d_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
)

func offering(capability generation.Capability) generation.Offering {
	result := generation.Offering{
		ProviderID: "hyper3d", ModelID: "rodin-gen-2.5", Capability: capability,
		PollInterval:    5 * time.Second,
		ProviderOptions: json.RawMessage(`{"endpoint":"rodin","observe_endpoint":"status","download_endpoint":"download","request_template":{"tier":"Gen-2.5-Medium","geometry_file_format":"glb"}}`),
		Parameters:      []generation.Parameter{{Name: "prompt", Type: "text", Required: capability == generation.TextTo3D}},
		Outputs:         []generation.AssetRequirement{{Role: "model", MediaTypes: []string{"model/gltf-binary"}}},
	}
	if capability == generation.ImageTo3D {
		result.Parameters = append(result.Parameters, generation.Parameter{
			Name: "reference_assets", Type: "asset-array", WireName: "images", Required: true,
			MinItems: 1, MaxItems: 5, MaxBytes: 10 << 20,
		})
	}
	return result
}

func mapping() *generation.StatusMapping {
	return &generation.StatusMapping{UpstreamStatus: map[string]generation.StatusRule{
		"waiting": {Phase: "accepted"}, "generating": {Phase: "running"},
		"done": {Phase: "completed"}, "failed": {Phase: "failed"},
	}}
}

func adapter(t *testing.T, baseURL string, client *http.Client, inputs generation.InputAssetResolver) *hyper3d.Adapter {
	t.Helper()
	value, err := hyper3d.New(hyper3d.Config{
		BaseURL: baseURL, APIKey: "test-key", StatusMapping: mapping(),
		HTTPClient: client, AllowHTTPAssets: true, InputAssets: inputs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type recordingCheckpointer struct {
	calls   atomic.Int32
	pending bool
}

func (c *recordingCheckpointer) Checkpoint(_ context.Context, intent generation.DispatchIntent) (generation.DispatchAck, error) {
	c.calls.Add(1)
	if c.pending {
		return generation.DispatchAck{}, generation.ErrDispatchPending
	}
	if !bytes.Contains(intent.Bytes, []byte(`"stage":"rodin.create"`)) {
		return generation.DispatchAck{}, errors.New("missing Rodin dispatch stage")
	}
	return generation.DispatchAck{Sequence: 7, AckToken: "hyper3d-test-ack", Opaque: intent.Bytes}, nil
}

func TestRodinTextFlowUsesBothIdentifiersAndMaterializesOnlyAfterAllJobsDone(t *testing.T) {
	var statusCalls, downloadCalls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v2/") && r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v2/rodin":
			if r.Method != http.MethodPost {
				t.Errorf("submit method = %s", r.Method)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			if got := r.FormValue("tier"); got != "Gen-2.5-Medium" {
				t.Errorf("tier = %q", got)
			}
			if got := r.FormValue("prompt"); got != "wooden chair" {
				t.Errorf("prompt = %q", got)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"uuid":"task-uuid","jobs":{"subscription_key":"status-secret"}}`)
		case "/api/v2/status":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["subscription_key"] != "status-secret" {
				t.Errorf("status identity = %#v", body)
			}
			w.WriteHeader(http.StatusCreated)
			if statusCalls.Add(1) == 1 {
				_, _ = io.WriteString(w, `{"jobs":[{"status":"Done"},{"status":"Generating"}]}`)
			} else {
				_, _ = io.WriteString(w, `{"jobs":[{"status":"Done"},{"status":"Done"}]}`)
			}
		case "/api/v2/download":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["task_uuid"] != "task-uuid" {
				t.Errorf("download identity = %#v", body)
			}
			downloadCalls.Add(1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"list": []map[string]string{
				{"name": "asset.glb", "url": server.URL + "/files/asset.glb"},
				{"name": "supplement.obj", "url": server.URL + "/files/supplement.obj"},
				{"name": "preview.png", "url": server.URL + "/files/preview.png"},
			}})
		case "/files/asset.glb":
			w.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = w.Write(testGLB())
		case "/files/error.glb":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, "<html>expired signed URL</html>")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), nil)
	step := offering(generation.TextTo3D)
	step.Parameters[0].Name = "description"
	step.Parameters[0].WireName = "prompt"
	optionalMinimum := 0
	step.Outputs = append(step.Outputs, generation.AssetRequirement{
		Role: "model", Minimum: &optionalMinimum, MediaTypes: []string{"model/obj"},
	})
	step.AssetHosts = []string{"127.0.0.1"}
	checkpointer := &recordingCheckpointer{}
	start, err := port.Start(generation.WithDispatchCheckpointer(context.Background(), checkpointer), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", SubmissionKey: "submission", Offering: step,
		Input: json.RawMessage(`{"description":"wooden chair"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if start.Job == nil || start.Job.RemoteID != "task-uuid" || start.Dispatch == nil || start.Dispatch.Sequence != 7 || checkpointer.calls.Load() != 1 {
		t.Fatalf("start = %#v, checkpoints = %d", start, checkpointer.calls.Load())
	}
	var private map[string]any
	if err := json.Unmarshal(start.Opaque, &private); err != nil || private["subscription_key"] != "status-secret" {
		t.Fatalf("private checkpoint = %s, %v", start.Opaque, err)
	}
	observe := generation.ObserveRequest{TaskID: "task", AttemptID: "attempt", Offering: step, Job: *start.Job, Opaque: start.Opaque}
	first, err := port.Observe(context.Background(), observe)
	if err != nil {
		t.Fatal(err)
	}
	if first.Phase != generation.UpstreamRunning || downloadCalls.Load() != 0 || first.PollAfter < 5*time.Second {
		t.Fatalf("premature completion = %#v", first)
	}
	observe.Opaque = first.Opaque // durable round-trip across process restarts
	final, err := port.Observe(context.Background(), observe)
	if err != nil {
		t.Fatal(err)
	}
	if final.Phase != generation.UpstreamCompleted || downloadCalls.Load() != 1 || len(final.Artifacts) != 3 {
		t.Fatalf("final = %#v, download calls = %d", final, downloadCalls.Load())
	}
	var model, optionalModel generation.ArtifactClaim
	for _, claim := range final.Artifacts {
		if claim.Role == "model" && claim.Variant == "glb" {
			model = claim
		} else if claim.Role == "model" && claim.Variant == "obj" {
			optionalModel = claim
		}
	}
	if model.ID == "" || model.MediaType != "model/gltf-binary" {
		t.Fatalf("model = %#v", model)
	}
	if optionalModel.ID == "" || optionalModel.MediaType != "model/obj" {
		t.Fatalf("optional model output = %#v", optionalModel)
	}
	content, _, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: step, Claim: model})
	if err != nil {
		t.Fatal(err)
	}
	defer content.Close()
	payload, _ := io.ReadAll(content)
	if !bytes.Equal(payload, testGLB()) {
		t.Fatalf("model bytes = %q", payload)
	}
	bad := model
	bad.Locator = server.URL + "/files/error.glb"
	if _, _, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: step, Claim: bad}); err == nil {
		t.Fatal("HTML error page was accepted as a GLB model")
	}
	step.AssetHosts = nil
	if _, _, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: step, Claim: model}); err == nil {
		t.Fatal("asset host allowlist absent: expected fail-closed download")
	}
}

func testGLB() []byte {
	jsonChunk := []byte(`{"asset":{"version":"2.0"}}`)
	for len(jsonChunk)%4 != 0 {
		jsonChunk = append(jsonChunk, ' ')
	}
	blob := make([]byte, 20+len(jsonChunk))
	copy(blob, "glTF")
	binary.LittleEndian.PutUint32(blob[4:8], 2)
	binary.LittleEndian.PutUint32(blob[8:12], uint32(len(blob)))
	binary.LittleEndian.PutUint32(blob[12:16], uint32(len(jsonChunk)))
	copy(blob[16:20], "JSON")
	copy(blob[20:], jsonChunk)
	return blob
}

func TestRodinChunkedGLBLengthMatchesDeclaredBytes(t *testing.T) {
	valid := testGLB()
	cases := []struct {
		name    string
		body    []byte
		wantErr bool
	}{
		{name: "valid", body: valid},
		{name: "truncated", body: valid[:len(valid)-2], wantErr: true},
		{name: "trailing", body: append(append([]byte(nil), valid...), 0, 0), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "model/gltf-binary")
				_, _ = w.Write(tc.body[:20])
				w.(http.Flusher).Flush() // force unknown Content-Length
				_, _ = w.Write(tc.body[20:])
			}))
			t.Cleanup(server.Close)
			step := offering(generation.TextTo3D)
			step.AssetHosts = []string{"127.0.0.1"}
			port := adapter(t, server.URL+"/api/v2", server.Client(), nil)
			body, metadata, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{
				Offering: step,
				Claim:    generation.ArtifactClaim{Role: "model", Variant: "glb", Locator: server.URL + "/model.glb"},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			if metadata.Size != -1 {
				t.Fatalf("chunked Content-Length = %d", metadata.Size)
			}
			got, err := io.ReadAll(body)
			if tc.wantErr {
				var providerErr *generation.ProviderError
				if !errors.As(err, &providerErr) || providerErr.Failure.Class != generation.FailureIntegrity {
					t.Fatalf("expected integrity failure, got %v", err)
				}
			} else if err != nil || !bytes.Equal(got, valid) {
				t.Fatalf("valid GLB read = %v, %x", err, got)
			}
		})
	}
}

type imageInputs struct{}

func (imageInputs) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}
func (imageInputs) Open(_ context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("image-" + string(asset.ID))), nil
}

func TestRodinImageFlowPreservesMultipartImageOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/rodin" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		files := r.MultipartForm.File["images"]
		if len(files) != 2 || files[0].Filename != "front.png" || files[1].Filename != "back.png" {
			t.Errorf("image fields = %#v", files)
		}
		for index, file := range files {
			body, err := file.Open()
			if err != nil {
				t.Error(err)
				continue
			}
			content, _ := io.ReadAll(body)
			body.Close()
			if string(content) != []string{"image-first", "image-second"}[index] {
				t.Errorf("image %d = %q", index, content)
			}
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"uuid":"uuid-images","jobs":{"subscription_key":"images-key"}}`)
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	_, err := port.Start(context.Background(), generation.StartRequest{
		AttemptID: "attempt", Offering: offering(generation.ImageTo3D),
		Input: json.RawMessage(`{"reference_assets":["first","second"]}`),
		InputAssets: map[string][]generation.InputAsset{"reference_assets": {
			{ID: "first", MediaType: "image/png", OriginalFilename: "front.png"},
			{ID: "second", MediaType: "image/png", OriginalFilename: "back.png"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	implicit := offering(generation.ImageTo3D)
	implicit.Parameters[1].Name = "images"
	implicit.Parameters[1].WireName = ""
	_, err = port.Start(context.Background(), generation.StartRequest{
		AttemptID: "implicit-wire", Offering: implicit,
		Input: json.RawMessage(`{"images":["first","second"]}`),
		InputAssets: map[string][]generation.InputAsset{"images": {
			{ID: "first", MediaType: "image/png", OriginalFilename: "front.png"},
			{ID: "second", MediaType: "image/png", OriginalFilename: "back.png"},
		}},
	})
	if err != nil {
		t.Fatalf("implicit images wire name: %v", err)
	}
}

func TestRodinImageCountBoundary(t *testing.T) {
	for _, count := range []int{0, 1, 5, 6} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var submits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				submits.Add(1)
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
				}
				if got := len(r.MultipartForm.File["images"]); got != count {
					t.Errorf("images = %d, want %d", got, count)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"uuid":"uuid-images","jobs":{"subscription_key":"images-key"}}`)
			}))
			t.Cleanup(server.Close)
			port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
			ids := make([]string, 0, count)
			assets := make([]generation.InputAsset, 0, count)
			for index := range count {
				id := fmt.Sprintf("image-%d", index)
				ids = append(ids, id)
				assets = append(assets, generation.InputAsset{ID: generation.AssetID(id), MediaType: "image/png"})
			}
			encoded, err := json.Marshal(map[string]any{"reference_assets": ids})
			if err != nil {
				t.Fatal(err)
			}
			_, err = port.Start(context.Background(), generation.StartRequest{
				Offering: offering(generation.ImageTo3D), Input: encoded,
				InputAssets: map[string][]generation.InputAsset{"reference_assets": assets},
			})
			if count == 0 || count == 6 {
				if err == nil || submits.Load() != 0 {
					t.Fatalf("count %d should fail before submission: err=%v submits=%d", count, err, submits.Load())
				}
			} else if err != nil || submits.Load() != 1 {
				t.Fatalf("count %d submission: err=%v submits=%d", count, err, submits.Load())
			}
		})
	}
}

func TestRodinRejectsBodyErrorAndDoesNotReplayPendingDispatch(t *testing.T) {
	var submits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		submits.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"error":"IMAGE_CONTENT_VIOLATION","uuid":"must-ignore"}`)
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), nil)
	request := generation.StartRequest{Offering: offering(generation.TextTo3D), Input: json.RawMessage(`{"prompt":"chair"}`)}
	_, err := port.Start(context.Background(), request)
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure.Class != generation.FailureRejected || providerErr.Failure.Ambiguous {
		t.Fatalf("201 body error = %v", err)
	}
	if submits.Load() != 1 {
		t.Fatalf("submits = %d", submits.Load())
	}
	pending := &recordingCheckpointer{pending: true}
	_, err = port.Start(generation.WithDispatchCheckpointer(context.Background(), pending), request)
	if !errors.Is(err, generation.ErrDispatchPending) || submits.Load() != 1 {
		t.Fatalf("pending dispatch = %v, submits = %d", err, submits.Load())
	}
}

func TestRodinOfferingRequiresExplicitGen25Tier(t *testing.T) {
	step := offering(generation.TextTo3D)
	step.ProviderOptions = json.RawMessage(`{"endpoint":"rodin","observe_endpoint":"status","download_endpoint":"download","request_template":{"mesh_mode":"Raw"}}`)
	if err := hyper3d.ValidateOffering(step); err == nil {
		t.Fatal("missing Gen-2.5 tier silently falls back to older Rodin")
	}
}

func TestRodinAcceptedResponseWithoutBothIdentifiersStopsAsUnknown(t *testing.T) {
	for _, response := range []string{
		`{"uuid":"task-only","jobs":{}}`,
		`{"jobs":{"subscription_key":"key-only"}}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, response)
			}))
			t.Cleanup(server.Close)
			port := adapter(t, server.URL+"/api/v2", server.Client(), nil)
			_, err := port.Start(context.Background(), generation.StartRequest{
				Offering: offering(generation.TextTo3D), Input: json.RawMessage(`{"prompt":"chair"}`),
			})
			var providerErr *generation.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Failure.Class != generation.FailureSubmissionUnknown || !providerErr.Failure.Ambiguous {
				t.Fatalf("missing identifier = %v", err)
			}
		})
	}
}
