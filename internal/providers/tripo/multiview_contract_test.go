package tripo_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

func TestMultiviewEditPromptUsesSchemaCharacterLimit(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &document); err != nil {
		t.Fatal(err)
	}
	for _, item := range document["offerings"].([]any) {
		o := item.(map[string]any)
		if o["id"] == "tripo/edit-multiview" {
			o["enabled"] = true
		}
	}
	raw, _ := json.Marshal(document)
	c, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	a, err := tripo.New(tripo.Config{BaseURL: "https://api.example.test/v3", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	source := generation.TaskDependency{TaskID: "source-public", Capability: generation.ImageToMultiview, OfferingID: "tripo/image-to-multiview", ProviderID: "tripo", SessionID: "session", UpstreamJob: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "11111111-2222-3333-4444-555555555555"}, ProviderBinding: json.RawMessage(`{"task_id":"source-public","attempt_id":"source-attempt","remote_id":"11111111-2222-3333-4444-555555555555"}`)}
	for _, tc := range []struct{ name, prompt string }{
		{"ASCII1024", strings.Repeat("a", 1024)},
		{"CJK342", strings.Repeat("绿", 342)}, // 342 characters, 1026 UTF-8 bytes.
		{"CJK1024", strings.Repeat("绿", 1024)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{"source_task": "source-public", "front_prompt": tc.prompt, "authorize_extra_credits": true})
			offering, normalized, err := c.Normalize(generation.EditMultiview, "tripo/edit-multiview", input)
			if err != nil {
				t.Fatalf("Schema rejected %d characters: %v", len([]rune(tc.prompt)), err)
			}
			if err := a.ValidateInput(context.Background(), generation.InputValidationRequest{OwnerID: "owner", SessionID: "session", Offering: offering, Input: normalized, TaskDependencies: map[string]generation.TaskDependency{"source_task": source}}); err != nil {
				t.Fatalf("Adapter rejected %d characters: %v", len([]rune(tc.prompt)), err)
			}
		})
	}
	tooLong, _ := json.Marshal(map[string]any{"source_task": "source-public", "front_prompt": strings.Repeat("绿", 1025), "authorize_extra_credits": true})
	if _, _, err := c.Normalize(generation.EditMultiview, "tripo/edit-multiview", tooLong); err == nil {
		t.Fatal("Schema accepted 1025 characters")
	}
}

func TestMultiviewGenerationUploadCreateAndFourExactClaims(t *testing.T) {
	// The nested output, UUID and 10-credit usage are redacted 2026-10-17
	// v3 qualification observations, not the older flat documentation example.
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get("tripo/image-to-multiview")
	if !ok {
		t.Fatal("missing candidate")
	}
	var file bytes.Buffer
	if err := png.Encode(&file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(file.Bytes())
	inputs := &uploadedModelInputs{body: file.Bytes()}
	posts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		if r.Method == "POST" {
			posts[key]++
		}
		switch key {
		case "POST /v3/files":
			if r.Header.Get("Authorization") != "Bearer test-key" {
				t.Errorf("auth=%v", r.Header)
			}
			part, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			item, err := part.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			body, err := io.ReadAll(item)
			if err != nil || item.FormName() != "file" || item.Header.Get("Content-Type") != "image/png" || !bytes.Equal(body, file.Bytes()) {
				t.Errorf("upload part=%v body=%d err=%v", item.Header, len(body), err)
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"file_token": "uploaded-image-token"}})
		case "POST /v3/generation/image-to-multiview":
			if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("create headers=%v", r.Header)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if !reflect.DeepEqual(body, map[string]any{"input": "uploaded-image-token"}) {
				t.Errorf("create body=%v", body)
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "type": "image_to_multiview", "status": "queued"}})
		case "GET /v3/tasks/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee":
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "type": "image_to_multiview", "status": "success", "credits_consumed": 10, "output": map[string]any{"generate_multiview_image": map[string]any{"front_view_url": "https://cdn.tripo3d.ai/front.jpg", "left_view_url": "https://cdn.tripo3d.ai/left.jpg", "back_view_url": "https://cdn.tripo3d.ai/back.jpg", "right_view_url": "https://cdn.tripo3d.ai/right.jpg"}}}})
		default:
			t.Errorf("unexpected wire %s", key)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	em, sm := declaredTripoMappings(t)
	a, err := tripo.New(tripo.Config{BaseURL: server.URL + "/v3", APIKey: "test-key", HTTPClient: server.Client(), InputAssets: inputs, ErrorMapping: em, StatusMapping: sm})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"image":"asset-image"}`)
	request := generation.StartRequest{TaskID: "public-image-group", AttemptID: "attempt-image-group", OwnerID: "owner", SessionID: "session", SubmissionKey: "submit-image-group", Offering: o, Input: input, InputAssets: map[string][]generation.InputAsset{"image": {{ID: "asset-image", OwnerID: "owner", StorageKey: "private-image", Size: int64(file.Len()), SHA256: hex.EncodeToString(sum[:]), MediaType: "image/png"}}}}
	uploadCheckpoint := &recordingCheckpointer{}
	prepared, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), uploadCheckpoint), request)
	if err != nil || prepared.Phase != generation.UpstreamPreparing || posts["POST /v3/files"] != 1 || posts["POST /v3/generation/image-to-multiview"] != 0 {
		t.Fatalf("prepare=%+v err=%v posts=%v", prepared, err, posts)
	}
	preparation := generation.PreparationRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, OwnerID: request.OwnerID, SubmissionKey: request.SubmissionKey, Offering: o, Input: input, Opaque: uploadCheckpoint.intent}
	if _, err := a.ResumePreparation(generation.WithDispatchCheckpointer(context.Background(), &recordingCheckpointer{}), preparation); err == nil || posts["POST /v3/files"] != 1 || posts["POST /v3/generation/image-to-multiview"] != 0 {
		t.Fatalf("uncertain upload replayed: %v %v", err, posts)
	}
	preparation.Opaque = prepared.Opaque
	createCheckpoint := &recordingCheckpointer{}
	accepted, err := a.ResumePreparation(generation.WithDispatchCheckpointer(context.Background(), createCheckpoint), preparation)
	if err != nil || accepted.Phase != generation.UpstreamAccepted || posts["POST /v3/generation/image-to-multiview"] != 1 {
		t.Fatalf("create=%+v err=%v posts=%v", accepted, err, posts)
	}
	preparation.Opaque = createCheckpoint.intent
	if _, err := a.ResumePreparation(generation.WithDispatchCheckpointer(context.Background(), &recordingCheckpointer{}), preparation); err == nil || posts["POST /v3/generation/image-to-multiview"] != 1 {
		t.Fatalf("uncertain create replayed: %v %v", err, posts)
	}
	completed, err := a.Observe(context.Background(), generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: o, Input: input, Opaque: accepted.Opaque, Job: *accepted.Job})
	if err != nil || completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 4 {
		t.Fatalf("complete=%+v err=%v", completed, err)
	}
	for i, view := range []string{"front", "left", "back", "right"} {
		if completed.Artifacts[i].Role != "image" || completed.Artifacts[i].Variant != view {
			t.Errorf("claim[%d]=%+v", i, completed.Artifacts[i])
		}
	}
	if !bytes.Contains(completed.Opaque, []byte(`"consumed_credit":10`)) {
		t.Fatalf("usage missing: %s", completed.Opaque)
	}
}

func TestMultiviewEditContractUsesSelectedViewsOnly(t *testing.T) {
	// The one-view nested output and 5-credit usage are redacted 2026-10-17
	// v3 qualification observations. The flat branch below is the same day's
	// official API documentation example; no recursive URL search is allowed.
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get("tripo/edit-multiview")
	if !ok {
		t.Fatal("missing candidate")
	}
	var received map[string]any
	usage := 5
	terminalOutput := map[string]any{"generate_multiview_image": map[string]any{"front_view_url": "https://cdn.tripo3d.ai/front.jpg"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v3/generation/edit-multiview":
			if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer test-key" {
				t.Errorf("headers: %v", r.Header)
			}
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "type": "edit_multiview", "status": "queued"}})
		case "GET /v3/tasks/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee":
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"task_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "type": "edit_multiview", "status": "success", "credits_consumed": usage, "output": terminalOutput}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	em, sm := declaredTripoMappings(t)
	a, err := tripo.New(tripo.Config{BaseURL: server.URL + "/v3", APIKey: "test-key", HTTPClient: server.Client(), ErrorMapping: em, StatusMapping: sm})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"source_task":"source-public","front_prompt":"make green","left_prompt":"","back_prompt":"","right_prompt":"","authorize_extra_credits":true}`)
	source := generation.TaskDependency{TaskID: "source-public", Capability: generation.ImageToMultiview, OfferingID: "tripo/image-to-multiview", ProviderID: "tripo", SessionID: "session", UpstreamJob: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "11111111-2222-3333-4444-555555555555"}, ProviderBinding: json.RawMessage(`{"task_id":"source-public","attempt_id":"source-attempt","remote_id":"11111111-2222-3333-4444-555555555555"}`)}
	request := generation.StartRequest{TaskID: "edit-public", AttemptID: "edit-attempt", OwnerID: "owner", SessionID: "session", SubmissionKey: "edit-submit", Offering: o, Input: input, TaskDependencies: map[string]generation.TaskDependency{"source_task": source}}
	for name, mutate := range map[string]func(*generation.InputValidationRequest){
		"cross session": func(r *generation.InputValidationRequest) {
			r.TaskDependencies["source_task"] = generation.TaskDependency{TaskID: source.TaskID, Capability: source.Capability, OfferingID: source.OfferingID, ProviderID: source.ProviderID, SessionID: "other-session", UpstreamJob: source.UpstreamJob, ProviderBinding: source.ProviderBinding}
		},
		"wrong offering": func(r *generation.InputValidationRequest) {
			d := source
			d.OfferingID = "tripo/image-to-splat"
			r.TaskDependencies["source_task"] = d
		},
		"wrong remote binding": func(r *generation.InputValidationRequest) {
			d := source
			d.UpstreamJob.RemoteID = "different-remote"
			r.TaskDependencies["source_task"] = d
		},
	} {
		t.Run(name, func(t *testing.T) {
			validation := generation.InputValidationRequest{OwnerID: request.OwnerID, SessionID: request.SessionID, Offering: o, Input: input, TaskDependencies: map[string]generation.TaskDependency{"source_task": source}}
			mutate(&validation)
			if err := a.ValidateInput(context.Background(), validation); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
	obs, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), &recordingCheckpointer{}), request)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"input": "11111111-2222-3333-4444-555555555555", "prompts": []any{map[string]any{"view": "front", "prompt": "make green"}}}
	if !reflect.DeepEqual(received, want) {
		t.Fatalf("body=%v want=%v", received, want)
	}
	completed, err := a.Observe(context.Background(), generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: o, Input: input, Opaque: obs.Opaque, Job: *obs.Job})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != generation.UpstreamCompleted || len(completed.Artifacts) != 1 || completed.Artifacts[0].Role != "image" || completed.Artifacts[0].Variant != "front" {
		t.Fatalf("completion=%+v", completed)
	}
	for name, output := range map[string]map[string]any{
		"missing front":         {"generate_multiview_image": map[string]any{}},
		"unrequested left":      {"generate_multiview_image": map[string]any{"front_view_url": "https://cdn.tripo3d.ai/front.jpg", "left_view_url": "https://cdn.tripo3d.ai/left.jpg"}},
		"unknown top":           {"generate_multiview_image": map[string]any{"front_view_url": "https://cdn.tripo3d.ai/front.jpg", "top_view_url": "https://cdn.tripo3d.ai/top.jpg"}},
		"ambiguous flat nested": {"front_view_url": "https://cdn.tripo3d.ai/front.jpg", "generate_multiview_image": map[string]any{"front_view_url": "https://cdn.tripo3d.ai/front.jpg"}},
		"non-string front":      {"generate_multiview_image": map[string]any{"front_view_url": 2}},
	} {
		t.Run(name, func(t *testing.T) {
			terminalOutput = output
			if _, err := a.Observe(context.Background(), generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: o, Input: input, Opaque: obs.Opaque, Job: *obs.Job}); err == nil {
				t.Fatal("invalid selected-view response accepted")
			}
		})
	}
	terminalOutput = map[string]any{"front_view_url": "https://cdn.tripo3d.ai/front.jpg"}
	flat, err := a.Observe(context.Background(), generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: o, Input: input, Opaque: obs.Opaque, Job: *obs.Job})
	if err != nil || len(flat.Artifacts) != 1 || flat.Artifacts[0].Variant != "front" {
		t.Fatalf("official flat example rejected: %+v %v", flat, err)
	}
	terminalOutput = map[string]any{"generate_multiview_image": map[string]any{
		// Four edited views and 20 credits were observed in a separate
		// redacted 2026-10-17 v3 qualification response.
		"front_view_url": "https://cdn.tripo3d.ai/front.jpg",
		"left_view_url":  "https://cdn.tripo3d.ai/left.jpg",
		"back_view_url":  "https://cdn.tripo3d.ai/back.jpg",
		"right_view_url": "https://cdn.tripo3d.ai/right.jpg",
	}}
	usage = 20
	input = json.RawMessage(`{"source_task":"source-public","front_prompt":"green","left_prompt":"green","back_prompt":"green","right_prompt":"green","authorize_extra_credits":true}`)
	request.TaskID, request.AttemptID, request.SubmissionKey, request.Input = "edit-four-public", "edit-four-attempt", "edit-four-submit", input
	fourStart, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), &recordingCheckpointer{}), request)
	if err != nil {
		t.Fatal(err)
	}
	wantPrompts := []any{map[string]any{"view": "front", "prompt": "green"}, map[string]any{"view": "left", "prompt": "green"}, map[string]any{"view": "back", "prompt": "green"}, map[string]any{"view": "right", "prompt": "green"}}
	if !reflect.DeepEqual(received, map[string]any{"input": "11111111-2222-3333-4444-555555555555", "prompts": wantPrompts}) {
		t.Fatalf("four-view body=%v", received)
	}
	fourDone, err := a.Observe(context.Background(), generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: o, Input: input, Opaque: fourStart.Opaque, Job: *fourStart.Job})
	if err != nil || len(fourDone.Artifacts) != 4 {
		t.Fatalf("four-view output=%+v %v", fourDone, err)
	}
	if !bytes.Contains(fourDone.Opaque, []byte(`"consumed_credit":20`)) {
		t.Fatalf("four-view usage missing: %s", fourDone.Opaque)
	}
	for i, view := range []string{"front", "left", "back", "right"} {
		if fourDone.Artifacts[i].Variant != view {
			t.Fatalf("four-view claim order=%+v", fourDone.Artifacts)
		}
	}
}

func TestMultiviewImageClaimSniffsJPEGWhenCDNUsesOctetStream(t *testing.T) {
	var expected bytes.Buffer
	if err := jpeg.Encode(&expected, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/front.jpg" {
			t.Errorf("asset request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(expected.Bytes())
	}))
	defer server.Close()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, _ := c.Get("tripo/image-to-multiview")
	parsed, _ := url.Parse(server.URL)
	o.AssetHosts = []string{parsed.Hostname()}
	a, err := tripo.New(tripo.Config{BaseURL: server.URL + "/v3", APIKey: "test-key", HTTPClient: server.Client(), AllowHTTPAssets: true})
	if err != nil {
		t.Fatal(err)
	}
	opened, meta, err := a.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: o, Claim: generation.ArtifactClaim{Role: "image", Variant: "front", Locator: server.URL + "/front.jpg"}})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	actual, err := io.ReadAll(opened)
	if err != nil || meta.MediaType != "image/jpeg" || !bytes.Equal(actual, expected.Bytes()) {
		t.Fatalf("asset media=%q bytes=%d err=%v", meta.MediaType, len(actual), err)
	}
}
