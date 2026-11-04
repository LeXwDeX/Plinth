package meshy_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	meshyopenapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v1"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
)

// #93: the Meshy extension steps (UV unwrap, resize, the three print
// endpoints) name a successful Meshy task of the same connection by
// input_task_id and upload nothing; a saved model goes inline as a data URI.
// Exercised against the fake meshy/openapi-v1 upstream with the shipped
// Offerings.

const fakeMeshyKey = "test-key"

type recordedMeshy struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	bodies map[string]map[string]any
}

func newRecordedMeshy(t *testing.T) *recordedMeshy {
	t.Helper()
	fake, err := fakeupstream.New(fakeupstream.Config{}, meshyopenapiv1.Version(), meshyopenapiv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	r := &recordedMeshy{t: t, bodies: map[string]map[string]any{}}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			raw, _ := io.ReadAll(request.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			r.mu.Lock()
			r.bodies[request.URL.Path] = body
			r.mu.Unlock()
			request.Body = io.NopCloser(bytes.NewReader(raw))
		}
		fake.ServeHTTP(w, request)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *recordedMeshy) body(path string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodies["/meshy/openapi/v1/"+path]
}

// finishedSource creates a Meshy task in the fake and polls it to SUCCEEDED,
// returning its upstream ID.
func (r *recordedMeshy) finishedSource(path string, body map[string]any) string {
	r.t.Helper()
	raw, _ := json.Marshal(body)
	request, _ := http.NewRequest(http.MethodPost, r.server.URL+"/meshy/openapi/v1/"+path, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+fakeMeshyKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.server.Client().Do(request)
	if err != nil {
		r.t.Fatal(err)
	}
	var created struct {
		Result string `json:"result"`
	}
	_ = json.NewDecoder(response.Body).Decode(&created)
	_ = response.Body.Close()
	if created.Result == "" {
		r.t.Fatalf("create %s = %d", path, response.StatusCode)
	}
	for i := 0; i < 10; i++ {
		poll, _ := http.NewRequest(http.MethodGet, r.server.URL+"/meshy/openapi/v1/"+path+"/"+created.Result, nil)
		poll.Header.Set("Authorization", "Bearer "+fakeMeshyKey)
		answer, err := r.server.Client().Do(poll)
		if err != nil {
			r.t.Fatal(err)
		}
		var state struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(answer.Body).Decode(&state)
		_ = answer.Body.Close()
		if state.Status == "SUCCEEDED" {
			return created.Result
		}
	}
	r.t.Fatalf("source %s never succeeded", path)
	return ""
}

func (r *recordedMeshy) adapter(t *testing.T, content []byte) *meshy.Adapter {
	return newMeshyAdapter(t, meshy.Config{BaseURL: r.server.URL + "/meshy", APIKey: fakeMeshyKey,
		HTTPClient: r.server.Client(), InputAssets: memoryInputAssets{content: content}})
}

var extensionSteps = []struct {
	id, endpoint, extra string
}{
	{"meshy/processing/uv-unwrap", "uv-unwrap", ``},
	{"meshy/processing/resize", "resize", `,"resize_height":0.2,"origin_at":"bottom"`},
	{"meshy/processing/analyze-printability", "print/analyze", ``},
	{"meshy/processing/repair-printability", "print/repair", ``},
	{"meshy/processing/multi-color-print", "print/multi-color", `,"style":"realistic","max_colors":16,"max_depth":6,"printer_brand":"bambu"`},
}

func shippedOffering(t *testing.T, id string) generation.Offering {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get(generation.OfferingID(id))
	if !ok {
		t.Fatalf("%s missing", id)
	}
	return offering
}

func TestMeshyExtensionStepsSendTheSourceTaskID(t *testing.T) {
	upstream := newRecordedMeshy(t)
	source := upstream.finishedSource("image-to-3d", map[string]any{"image_url": "https://example.test/a.png", "target_polycount": 10000})
	for _, step := range extensionSteps {
		if step.endpoint == "print/repair" {
			continue // the fake has no repair route; the wire shape is the same as analyze
		}
		t.Run(step.endpoint, func(t *testing.T) {
			adapter := upstream.adapter(t, nil)
			started, err := adapter.Start(context.Background(), generation.StartRequest{
				OwnerID: "owner-a", TaskID: "task", AttemptID: "attempt",
				Offering: shippedOffering(t, step.id),
				Input:    json.RawMessage(`{"source_task":"task-local"` + step.extra + `}`),
				TaskDependencies: map[string]generation.TaskDependency{"source_task": {
					TaskID: "task-local", Capability: generation.ImageTo3D, ProviderID: "meshy",
					UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: source},
				}},
			})
			if err != nil || started.Job == nil || started.Job.RemoteID == "" {
				t.Fatalf("start = %+v, %v", started, err)
			}
			body := upstream.body(step.endpoint)
			if body["input_task_id"] != source {
				t.Fatalf("input_task_id = %#v", body["input_task_id"])
			}
			for _, field := range []string{"model_url", "source_task", "source_asset"} {
				if _, present := body[field]; present {
					t.Errorf("task route also sent %s: %#v", field, body)
				}
			}
		})
	}
}

func TestMeshyExtensionStepsInlineASavedModel(t *testing.T) {
	upstream := newRecordedMeshy(t)
	model := modelinputtest.Triangle(modelinputtest.Options{Textured: true})
	for _, step := range extensionSteps {
		if step.endpoint == "print/repair" {
			continue
		}
		t.Run(step.endpoint, func(t *testing.T) {
			offering := shippedOffering(t, step.id)
			var options struct {
				Inline struct {
					MediaType string `json:"media_type"`
				} `json:"inline_model_input"`
			}
			_ = json.Unmarshal(offering.ProviderOptions, &options)
			asset := ownedModel(model, "model")
			_, err := upstream.adapter(t, model).Start(context.Background(), generation.StartRequest{
				OwnerID: "owner-a", TaskID: "task", AttemptID: "attempt", Offering: offering,
				Input:       json.RawMessage(`{"source_asset":"asset-model"` + step.extra + `}`),
				InputAssets: map[string][]generation.InputAsset{"source_asset": {asset}},
			})
			// Multi-color needs a single base color texture; the fixture's
			// textured triangle is the documented shape, so all four start.
			if err != nil {
				t.Fatal(err)
			}
			body := upstream.body(step.endpoint)
			want := "data:" + options.Inline.MediaType + ";base64," + base64.StdEncoding.EncodeToString(model)
			if body["model_url"] != want {
				t.Fatalf("model_url = %.80v", body["model_url"])
			}
			if _, present := body["input_task_id"]; present {
				t.Fatal("asset route also sent input_task_id")
			}
		})
	}
}

// #325 still applies: a Meshy task the provider no longer keeps answers 404
// "Task not found", which is a definite source_expired failure.
func TestMeshyExtensionStepOfAnExpiredTaskFailsAsSourceExpired(t *testing.T) {
	upstream := newRecordedMeshy(t)
	known := upstream.finishedSource("image-to-3d", map[string]any{"image_url": "https://example.test/a.png"})
	gone := known[:len(known)-6] + "999999"
	_, err := upstream.adapter(t, nil).Start(context.Background(), generation.StartRequest{
		OwnerID: "owner-a", TaskID: "task", AttemptID: "attempt",
		Offering: shippedOffering(t, "meshy/processing/uv-unwrap"),
		Input:    json.RawMessage(`{"source_task":"task-local"}`),
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {
			TaskID: "task-local", Capability: generation.ImageTo3D, ProviderID: "meshy",
			UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: gone},
		}},
	})
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil || provider.Failure.Class != generation.FailureSourceExpired || provider.Failure.Retryable {
		t.Fatalf("err = %v; want a non-retryable source_expired", err)
	}
}

// Meshy refuses a UV source over 40,000 faces at create. A Remesh result kept
// under the limit goes through by its task ID, the Remesh -> UV plan route.
func TestMeshyUVUnwrapOfARemeshResultUsesItsTaskID(t *testing.T) {
	upstream := newRecordedMeshy(t)
	generated := upstream.finishedSource("image-to-3d", map[string]any{"image_url": "https://example.test/a.png", "target_polycount": 300000})
	start := func(remote string) error {
		_, err := upstream.adapter(t, nil).Start(context.Background(), generation.StartRequest{
			OwnerID: "owner-a", TaskID: "task", AttemptID: "attempt",
			Offering: shippedOffering(t, "meshy/processing/uv-unwrap"),
			Input:    json.RawMessage(`{"source_task":"task-local"}`),
			TaskDependencies: map[string]generation.TaskDependency{"source_task": {
				TaskID: "task-local", Capability: generation.Remesh, ProviderID: "meshy",
				UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: remote},
			}},
		})
		return err
	}
	var provider *generation.ProviderError
	if err := start(generated); !errors.As(err, &provider) || provider.Failure == nil || provider.Failure.Class == generation.FailureSourceExpired || provider.Failure.UpstreamStatus != http.StatusBadRequest {
		t.Fatalf("over-limit source: err = %v; want the upstream 400", err)
	}
	remeshed := upstream.finishedSource("remesh", map[string]any{"input_task_id": generated, "topology": "quad", "target_polycount": 20000})
	if err := start(remeshed); err != nil {
		t.Fatalf("remeshed source: %v", err)
	}
	if body := upstream.body("uv-unwrap"); body["input_task_id"] != remeshed || strings.Contains(string(mustJSON(body)), "model_url") {
		t.Fatalf("UV body = %#v", body)
	}
}

func ownedModel(raw []byte, role string) generation.InputAsset {
	return generation.InputAsset{ID: "asset-model", OwnerID: "owner-a", StorageKey: "stored-model", AssetType: "model",
		MediaType: "model/gltf-binary", Size: int64(len(raw)), SHA256: sha256Hex(raw), Role: role}
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
