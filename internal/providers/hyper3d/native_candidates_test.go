package hyper3d_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// These tests call the frozen Schema declaration directly against httptest,
// qualifying its wire only. They do not establish real upstream delivery.
func nativeCandidate(t *testing.T, id generation.OfferingID) generation.Offering {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get(id)
	// Rodin Gen-2.5 is enabled under #188; Bang and Texture under #187.
	if !ok || !o.Enabled {
		t.Fatalf("unexpected enablement for candidate %s", id)
	}
	return o
}
func TestNativeRodinCandidatesUseHighestFrozenWireAndOrderedImages(t *testing.T) {
	for _, count := range []int{0, 1, 3, 5} {
		t.Run(fmt.Sprintf("images_%d", count), func(t *testing.T) {
			kind := "text-to-3d"
			if count > 0 {
				kind = "image-to-3d"
			}
			step := nativeCandidate(t, generation.OfferingID("hyper3d/rodin-gen-2.5/"+kind))
			checkpointer := &recordingCheckpointer{}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v2/rodin" || r.Method != "POST" || checkpointer.calls.Load() != 1 {
					t.Error("missing durable intent or wrong endpoint")
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				expected := map[string][]string{"tier": {"Gen-2.5-Extreme-High"}, "geometry_file_format": {"glb"}, "mesh_mode": {"Raw"}, "quality": {"high"}, "quality_override": {"2000000"}, "material": {"PBR"}, "texture_mode": {"extreme-high"}}
				if count == 0 {
					expected["prompt"] = []string{"original prompt"}
				}
				if !reflect.DeepEqual(r.MultipartForm.Value, expected) {
					t.Fatalf("highest-tier wire or consent leakage: %#v", r.MultipartForm.Value)
				}
				if len(r.MultipartForm.File["images"]) != count {
					t.Fatal("image count changed")
				}
				for i, file := range r.MultipartForm.File["images"] {
					body, err := file.Open()
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(body)
					body.Close()
					if err != nil || string(data) != fmt.Sprintf("image-view-%d", i) {
						t.Fatal("image identity/order changed")
					}
				}
				_, _ = io.WriteString(w, `{"uuid":"created","jobs":{"subscription_key":"subscription"}}`)
			}))
			defer server.Close()
			input := map[string]any{"authorize_extra_credits": true}
			assets := map[string][]generation.InputAsset{}
			if count == 0 {
				input["prompt"] = "original prompt"
			} else {
				ids := []string{}
				for i := 0; i < count; i++ {
					id := fmt.Sprintf("view-%d", i)
					ids = append(ids, id)
					assets["reference_assets"] = append(assets["reference_assets"], generation.InputAsset{ID: generation.AssetID(id), OwnerID: "owner", MediaType: "image/png", OriginalFilename: id + ".png"})
				}
				input["reference_assets"] = ids
			}
			raw, _ := json.Marshal(input)
			q, err := step.EvaluateNativeQuote(raw)
			if err != nil || q.TotalAmount != "3" || q.ExtraAmount != "2.5" {
				t.Fatalf("quote=%+v err=%v", q, err)
			}
			port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
			result, err := port.Start(generation.WithDispatchCheckpointer(context.Background(), checkpointer), generation.StartRequest{OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: step, Input: raw, InputAssets: assets})
			if err != nil || result.Job == nil || result.Dispatch == nil || calls != 1 {
				t.Fatalf("candidate wire: %+v %v calls=%d", result, err, calls)
			}
		})
	}
}

func TestNativeBangCandidatesSeparateSourceModesAndKeepHalfCredit(t *testing.T) {
	for _, mode := range []string{"rodin-task", "local-model"} {
		t.Run(mode, func(t *testing.T) {
			step := nativeCandidate(t, generation.OfferingID("hyper3d/bang/"+mode))
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v2/bang" {
					t.Fatal("wrong route")
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				expected := map[string][]string{"geometry_file_format": {"glb"}, "material": {"PBR"}, "resolution": {"High"}, "mesh_mode": {"Raw"}, "quality_override": {"2000000"}, "strength": {"5"}}
				if mode == "rodin-task" {
					expected["asset_id"] = []string{"private-rodin-uuid"}
					if len(r.MultipartForm.File) != 0 {
						t.Fatal("native mode mixed files")
					}
				} else {
					if len(r.MultipartForm.File["model"]) != 1 || len(r.MultipartForm.File["image"]) != 1 || len(r.MultipartForm.File) != 2 {
						t.Fatal("owned file mode changed")
					}
				}
				if !reflect.DeepEqual(expected, r.MultipartForm.Value) {
					t.Fatalf("unexpected Bang fields: %#v", r.MultipartForm.Value)
				}
				_, _ = io.WriteString(w, `{"uuid":"bang-created","jobs":{"subscription_key":"bang-subscription"}}`)
			}))
			defer server.Close()
			request := generation.StartRequest{OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: step}
			if mode == "rodin-task" {
				request.Input = json.RawMessage(`{"source_task":"source"}`)
				request.TaskDependencies = map[string]generation.TaskDependency{"source_task": {TaskID: "source", ProviderID: "hyper3d", Capability: generation.TextTo3D, OfferingID: "hyper3d/rodin-gen-2.5/text-to-3d", ModelID: "business-alias-not-a-wire-version", UpstreamJob: generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: "private-rodin-uuid"}}}
			} else {
				request.Input = json.RawMessage(`{"source_asset":"model-owned","reference_image":"image-owned"}`)
				request.InputAssets = map[string][]generation.InputAsset{"source_asset": {{ID: "model-owned", OwnerID: "owner", OriginalFilename: "model.glb", MediaType: "model/gltf-binary"}}, "reference_image": {{ID: "image-owned", OwnerID: "owner", OriginalFilename: "reference.png", MediaType: "image/png"}}}
			}
			q, err := step.EvaluateNativeQuote(request.Input)
			if err != nil || q.TotalAmount != "0.5" || q.ExtraAmount != "0" {
				t.Fatalf("quote=%+v err=%v", q, err)
			}
			port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
			result, err := port.Start(context.Background(), request)
			if err != nil || result.Job == nil || calls != 1 {
				t.Fatalf("candidate wire: %+v %v calls=%d", result, err, calls)
			}
		})
	}
}

// TestNativeTextureCandidateSendsFrozenWireAndOwnedFiles verifies the frozen
// High candidate and its current estimate; live High qualification is separate.
func TestNativeTextureCandidateSendsFrozenWireAndOwnedFiles(t *testing.T) {
	step := nativeCandidate(t, "hyper3d/texture/local-model")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v2/rodin_texture_only" || r.Method != http.MethodPost {
			t.Fatalf("wrong route %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		expected := map[string][]string{"geometry_file_format": {"glb"}, "material": {"PBR"}, "resolution": {"High"}, "texture_mode": {"extreme-high"}, "hd_texture": {"true"}, "prompt": {"weathered oak"}}
		if !reflect.DeepEqual(expected, r.MultipartForm.Value) {
			t.Fatalf("unexpected Texture fields: %#v", r.MultipartForm.Value)
		}
		if len(r.MultipartForm.File["model"]) != 1 || len(r.MultipartForm.File["image"]) != 1 || len(r.MultipartForm.File) != 2 {
			t.Fatal("Texture must upload exactly one model and one image")
		}
		_, _ = io.WriteString(w, `{"uuid":"texture-created","jobs":{"subscription_key":"texture-subscription"}}`)
	}))
	defer server.Close()
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	ownedModel := generation.InputAsset{ID: "model-owned", OwnerID: "owner", OriginalFilename: "model.glb", MediaType: "model/gltf-binary"}
	ownedImage := generation.InputAsset{ID: "image-owned", OwnerID: "owner", OriginalFilename: "reference.png", MediaType: "image/png"}
	request := func(input string, assets map[string][]generation.InputAsset, dependencies map[string]generation.TaskDependency) generation.StartRequest {
		return generation.StartRequest{OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: step,
			Input: json.RawMessage(input), InputAssets: assets, TaskDependencies: dependencies}
	}
	valid := `{"source_asset":"model-owned","reference_image":"image-owned","prompt":"weathered oak"}`
	q, err := step.EvaluateNativeQuote(json.RawMessage(valid))
	if err != nil || q.TotalAmount != "2" || q.ExtraAmount != "0" {
		t.Fatalf("quote=%+v err=%v", q, err)
	}
	rejected := map[string]generation.StartRequest{
		"missing reference image": request(`{"source_asset":"model-owned"}`, map[string][]generation.InputAsset{"source_asset": {ownedModel}}, nil),
		"other owner's model": request(valid, map[string][]generation.InputAsset{
			"source_asset": {{ID: "model-owned", OwnerID: "other", OriginalFilename: "model.glb", MediaType: "model/gltf-binary"}}, "reference_image": {ownedImage}}, nil),
		"undeclared model format": request(valid, map[string][]generation.InputAsset{
			"source_asset": {{ID: "model-owned", OwnerID: "owner", OriginalFilename: "model.obj", MediaType: "model/obj"}}, "reference_image": {ownedImage}}, nil),
		"task source instead of bytes": request(valid, map[string][]generation.InputAsset{"source_asset": {ownedModel}, "reference_image": {ownedImage}},
			map[string]generation.TaskDependency{"source_asset": {TaskID: "source", ProviderID: "hyper3d", UpstreamJob: generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: "private-rodin-uuid"}}}),
	}
	for name, invalid := range rejected {
		if result, err := port.Start(context.Background(), invalid); err == nil || result.Job != nil {
			t.Fatalf("%s reached submission: %+v", name, result)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid Texture input reached upstream %d times", calls)
	}
	result, err := port.Start(context.Background(), request(valid, map[string][]generation.InputAsset{"source_asset": {ownedModel}, "reference_image": {ownedImage}}, nil))
	if err != nil || result.Job == nil || calls != 1 {
		t.Fatalf("candidate wire: %+v %v calls=%d", result, err, calls)
	}
}
