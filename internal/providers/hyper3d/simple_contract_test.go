package hyper3d_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Official multipart and response contracts checked 2026-10-17:
// https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5
// https://docs.hyper3d.ai/en/api-specification/bang
// https://docs.hyper3d.ai/en/api-specification/generate-texture
// https://docs.hyper3d.ai/en/api-specification/check-status
// https://docs.hyper3d.ai/en/api-specification/download-results
// All business text, UUIDs, subscription keys, file bytes and URLs are synthetic.
// These are Adapter wire tests, not real generation or asset-format qualification.
func simpleHyperRequest(kind, prompt, uuid string) generation.StartRequest {
	req := generation.StartRequest{OwnerID: "owner", TaskID: "simple-task", AttemptID: "simple-attempt", SubmissionKey: "simple-submission"}
	switch kind {
	case "rodin-text":
		req.Offering = offering(generation.TextTo3D)
		req.Input, _ = json.Marshal(map[string]any{"prompt": prompt})
	case "rodin-image":
		req.Offering = offering(generation.ImageTo3D)
		req.Input = json.RawMessage(`{"reference_assets":["image"]}`)
		req.InputAssets = map[string][]generation.InputAsset{"reference_assets": {{ID: "image", OwnerID: "owner", OriginalFilename: "reference.png", MediaType: "image/png"}}}
	case "bang-native":
		req.Offering = processingOffering("bang", generation.Capability("mesh-segmentation"))
		req.Input = json.RawMessage(`{"source_task":"source"}`)
		req.TaskDependencies = map[string]generation.TaskDependency{"source_task": {TaskID: "source", Capability: generation.TextTo3D, OfferingID: "rodin", ModelID: "rodin-gen-2.5", ProviderID: "hyper3d", UpstreamJob: generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: uuid}}}
	default:
		endpoint, ref := "bang", "references"
		capability := generation.Capability("mesh-segmentation")
		if kind == "texture" {
			endpoint, ref = "rodin_texture_only", "reference"
			capability = generation.Retexture
		}
		req.Offering = processingOffering(endpoint, capability)
		req.Input, _ = json.Marshal(map[string]any{"model_file": []string{"model"}, ref: []string{"image"}})
		req.InputAssets = map[string][]generation.InputAsset{
			"model_file": {{ID: "model", OwnerID: "owner", OriginalFilename: "model.glb", MediaType: "model/gltf-binary"}},
			ref:          {{ID: "image", OwnerID: "owner", OriginalFilename: "reference.png", MediaType: "image/png"}},
		}
		if kind == "bang-upload" {
			req.Input, _ = json.Marshal(map[string]any{"model_file": []string{"model"}, ref: []string{"image"}, "prompt": prompt})
		}
	}
	return req
}

var simpleHyperKinds = []string{"rodin-text", "rodin-image", "bang-native", "bang-upload", "texture"}

func TestHyperSimpleContractAcceptsLegalContentVariants(t *testing.T) {
	for _, kind := range simpleHyperKinds {
		for i, prompt := range []string{"wooden chair", "a red vase", "蓝色石像"} {
			t.Run(fmt.Sprintf("%s/%d", kind, i), func(t *testing.T) {
				uuid := fmt.Sprintf("123e4567-e89b-42d3-a456-42661417400%d", i)
				jobUUID := fmt.Sprintf("223e4567-e89b-42d3-a456-42661417400%d", i)
				key := fmt.Sprintf("synthetic-subscription-%d", i)
				locator := fmt.Sprintf("https://assets.example.test/%d/model.glb?token=synthetic%d", i, i)
				req := simpleHyperRequest(kind, prompt, uuid)
				// Independent official routes: never derive the oracle from the Offering.
				endpoint := map[string]string{
					"rodin-text": "/api/v2/rodin", "rodin-image": "/api/v2/rodin",
					"bang-native": "/api/v2/bang", "bang-upload": "/api/v2/bang",
					"texture": "/api/v2/rodin_texture_only",
				}[kind]
				if endpoint == "" {
					t.Fatalf("missing official route for %q", kind)
				}
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodPost {
						t.Errorf("method=%s", r.Method)
					}
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case endpoint:
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							http.Error(w, "multipart required", 400)
							return
						}
						defer r.MultipartForm.RemoveAll()
						if r.FormValue("geometry_file_format") != "glb" {
							t.Errorf("format=%q", r.FormValue("geometry_file_format"))
						}
						switch kind {
						case "rodin-text", "rodin-image":
							if r.FormValue("tier") != "Gen-2.5-Medium" {
								t.Errorf("tier=%q", r.FormValue("tier"))
							}
							if kind == "rodin-text" {
								if r.FormValue("prompt") != prompt || len(r.MultipartForm.File) != 0 {
									t.Errorf("text shape: %+v", r.MultipartForm)
								}
							} else if len(r.MultipartForm.File["images"]) != 1 {
								t.Errorf("images shape: %+v", r.MultipartForm.File)
							}
						case "bang-native":
							if r.FormValue("asset_id") != uuid || r.FormValue("prompt") != "" || len(r.MultipartForm.File) != 0 {
								t.Errorf("native shape: %+v", r.MultipartForm)
							}
						default:
							if len(r.MultipartForm.File["model"]) != 1 || len(r.MultipartForm.File["image"]) != 1 || len(r.MultipartForm.File) != 2 || r.FormValue("asset_id") != "" {
								t.Errorf("upload shape: %+v", r.MultipartForm)
							}
							if kind == "bang-upload" && r.FormValue("prompt") != prompt {
								t.Errorf("prompt=%q", r.FormValue("prompt"))
							}
						}
						w.WriteHeader(http.StatusCreated)
						_ = json.NewEncoder(w).Encode(map[string]any{"message": "Submitted.", "uuid": uuid, "jobs": map[string]any{"subscription_key": key, "uuids": []string{jobUUID}}})
					case "/api/v2/status":
						var body map[string]string
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if len(body) != 1 || body["subscription_key"] != key {
							t.Errorf("status identity: %#v", body)
						}
						w.WriteHeader(http.StatusCreated)
						_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []any{map[string]any{"uuid": jobUUID, "status": "Done"}}})
					case "/api/v2/download":
						var body map[string]string
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if len(body) != 1 || body["task_uuid"] != uuid {
							t.Errorf("download identity: %#v", body)
						}
						w.WriteHeader(http.StatusCreated)
						_ = json.NewEncoder(w).Encode(map[string]any{"list": []any{map[string]any{"name": "model.glb", "url": locator}}})
					default:
						t.Errorf("unexpected route %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
				accepted, err := port.Start(context.Background(), req)
				if err != nil || accepted.Job == nil || accepted.Job.RemoteID != uuid {
					t.Fatalf("Start: %+v %v", accepted, err)
				}
				done, err := port.Observe(context.Background(), generation.ObserveRequest{TaskID: req.TaskID, AttemptID: req.AttemptID, Offering: req.Offering, Input: req.Input, Job: *accepted.Job, Opaque: accepted.Opaque})
				if err != nil || done.Phase != generation.UpstreamCompleted || len(done.Artifacts) != 1 || done.Artifacts[0].Locator != locator || calls != 3 {
					t.Fatalf("Observe: %+v %v calls=%d", done, err, calls)
				}
			})
		}
	}
}

func TestHyperSimpleContractRejectsMalformedSubmission(t *testing.T) {
	for _, kind := range simpleHyperKinds {
		for _, response := range []string{
			`{"jobs":{"subscription_key":"example"}}`,
			`{"uuid":42,"jobs":{"subscription_key":"example"}}`,
			`{"uuid":"123e4567-e89b-42d3-a456-426614174000","jobs":{}}`,
			`{"uuid":"123e4567-e89b-42d3-a456-426614174000","jobs":{"subscription_key":42}}`,
		} {
			t.Run(kind+"/"+response, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, response)
				}))
				defer server.Close()
				req := simpleHyperRequest(kind, "a chair", "123e4567-e89b-42d3-a456-426614174000")
				got, err := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{}).Start(context.Background(), req)
				if err == nil || got.Job != nil || calls != 1 {
					t.Fatalf("invalid response accepted: %+v %v", got, err)
				}
			})
		}
	}
}

func TestHyperSimpleContractRejectsMissingOrWrongTypeRequiredInput(t *testing.T) {
	for _, kind := range simpleHyperKinds {
		t.Run(kind, func(t *testing.T) {
			req := simpleHyperRequest(kind, "a chair", "123e4567-e89b-42d3-a456-426614174000")
			req.Input = json.RawMessage(`{}`)
			req.InputAssets = nil
			req.TaskDependencies = nil
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "must not dispatch", 500) }))
			defer server.Close()
			port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
			if _, err := port.Start(context.Background(), req); err == nil {
				t.Fatal("missing required input accepted")
			}
			if kind == "rodin-text" {
				req.Input = json.RawMessage(`{"prompt":123}`)
				if _, err := port.Start(context.Background(), req); err == nil {
					t.Fatal("numeric prompt accepted")
				}
			}
			if calls != 0 {
				t.Fatalf("invalid request reached upstream: %d", calls)
			}
		})
	}
}
