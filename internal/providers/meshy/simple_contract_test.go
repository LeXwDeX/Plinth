package meshy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// Synthetic fixed responses follow https://docs.meshy.ai/en/api/multi-image-to-3d
// (v1, Meshy 7.1; checked 2026-10-17).
// The source/job/asset values differ deliberately; their contents are not protocol constants.
func TestSimpleMultiImageContractAcceptsDifferentBusinessValues(t *testing.T) {
	for _, tc := range []struct{ source, created, asset string }{
		{"source-A", "generated-A", "first.glb"},
		{"source-B_27", "generated-B_27", "second.glb"},
	} {
		t.Run(tc.created, func(t *testing.T) {
			posts, gets := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPost:
					posts++
					if r.URL.Path != "/openapi/v1/multi-image-to-3d" {
						t.Errorf("POST path = %s", r.URL.Path)
					}
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "invalid JSON", http.StatusBadRequest)
						return
					}
					var source, model string
					if err := json.Unmarshal(body["input_task_id"], &source); err != nil || source != tc.source {
						t.Errorf("input_task_id = %s: %v", body["input_task_id"], err)
					}
					if err := json.Unmarshal(body["ai_model"], &model); err != nil || model != "meshy-7.1" {
						t.Errorf("ai_model = %s: %v", body["ai_model"], err)
					}
					if _, exists := body["image_urls"]; exists {
						t.Error("task source also sent image_urls")
					}
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(map[string]any{"result": tc.created})
				case http.MethodGet:
					gets++
					if r.URL.Path != "/openapi/v1/multi-image-to-3d/"+tc.created {
						t.Errorf("GET path = %s", r.URL.Path)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": tc.created, "status": "SUCCEEDED", "progress": 100, "model_urls": map[string]string{"glb": "https://assets.meshy.ai/simple/" + tc.asset}})
				default:
					t.Errorf("method = %s", r.Method)
				}
			}))
			defer server.Close()
			adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "fixture", HTTPClient: server.Client()})
			request := generation.StartRequest{TaskID: "public-task", AttemptID: "attempt", SubmissionKey: "one-create", OwnerID: "owner", Offering: multiImageOffering(), Input: json.RawMessage(`{"source_task":"local-source"}`), TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "local-source", Capability: generation.ImageToImage, OfferingID: "meshy/test/image-to-image", ProviderID: "meshy", UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: tc.source}}}}
			started, err := adapter.Start(context.Background(), request)
			if err != nil || started.Job == nil || started.Job.RemoteID != tc.created {
				t.Fatalf("start = %+v, %v", started, err)
			}
			observed, err := adapter.Observe(context.Background(), generation.ObserveRequest{TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: request.Offering, Job: *started.Job})
			if err != nil || observed.Phase != generation.UpstreamCompleted || len(observed.Artifacts) != 1 {
				t.Fatalf("observe = %+v, %v", observed, err)
			}
			claim := observed.Artifacts[0]
			if claim.Role != "model" || claim.Variant != "glb" || claim.Locator != "https://assets.meshy.ai/simple/"+tc.asset || posts != 1 || gets != 1 {
				t.Fatalf("claim = %+v; calls = %d/%d", claim, posts, gets)
			}
		})
	}
}

func TestSimpleMultiImageContractRejectsMissingOrWrongTypeBeforePOST(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts++; w.WriteHeader(http.StatusAccepted) }))
	defer server.Close()
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: server.URL, APIKey: "fixture", HTTPClient: server.Client()})
	for _, input := range []string{`{}`, `{"source_task":42}`} {
		_, err := adapter.Start(context.Background(), generation.StartRequest{TaskID: "task", AttemptID: "attempt", SubmissionKey: "one-create", OwnerID: "owner", Offering: multiImageOffering(), Input: json.RawMessage(input)})
		if err == nil {
			t.Errorf("accepted invalid source %s", input)
		}
	}
	if posts != 0 {
		t.Fatalf("invalid source made %d POSTs", posts)
	}
}
