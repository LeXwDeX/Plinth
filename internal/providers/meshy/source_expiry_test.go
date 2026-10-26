package meshy_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// #325: Meshy keeps API tasks at most three days; a create that names a
// deleted task by input_task_id answers 404 "Task not found"
// (docs.meshy.ai/en/api/retexture, /asset-retention). The saved model file
// goes as model_url instead.

func shippedRetexture(t *testing.T) generation.Offering {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get("meshy/processing/retexture")
	if !ok {
		t.Fatal("meshy/processing/retexture missing")
	}
	return offering
}

func retextureUpstream(t *testing.T, handle func(body map[string]any, writer http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/openapi/v1/retexture" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("body: %v", err)
		}
		handle(body, writer)
	}))
}

func TestRetextureOfAnExpiredMeshyTaskFailsAsSourceExpired(t *testing.T) {
	upstream := retextureUpstream(t, func(body map[string]any, writer http.ResponseWriter) {
		if body["input_task_id"] != "remote-text-to-3d" {
			t.Errorf("input_task_id = %#v", body["input_task_id"])
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"message":"Task not found"}`))
	})
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.URL, APIKey: "meshy-key", HTTPClient: upstream.Client()})
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: shippedRetexture(t),
		Input:    []byte(`{"source_task":"task-local","prompt":"walnut","target_formats":["glb","obj","fbx","stl","usdz","3mf"]}`),
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {
			TaskID: "task-local", Capability: generation.TextTo3D, ProviderID: "meshy",
			UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote-text-to-3d"},
		}},
	})
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("err = %v; want a ProviderError", err)
	}
	failure := provider.Failure
	if failure.Class != generation.FailureSourceExpired || failure.Retryable || failure.Ambiguous || failure.UpstreamStatus != http.StatusNotFound {
		t.Fatalf("failure = %+v; want a definite, non-retryable source_expired 404", failure)
	}
}

func TestRetextureSendsTheSavedModelAsModelURL(t *testing.T) {
	model := []byte("glTF-saved-model-bytes")
	want := "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(model)
	upstream := retextureUpstream(t, func(body map[string]any, writer http.ResponseWriter) {
		if _, present := body["input_task_id"]; present {
			t.Errorf("input_task_id sent with a saved file: %#v", body)
		}
		if _, present := body["source_asset"]; present {
			t.Errorf("platform parameter name leaked: %#v", body)
		}
		if body["model_url"] != want {
			t.Errorf("model_url = %.60v", body["model_url"])
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"result": "retexture-remote-1"})
	})
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.URL, APIKey: "meshy-key", HTTPClient: upstream.Client(),
		InputAssets: memoryInputAssets{content: model}})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: shippedRetexture(t),
		Input:    []byte(`{"source_asset":"asset-model","prompt":"walnut","target_formats":["glb","obj","fbx","stl","usdz","3mf"]}`),
		InputAssets: map[string][]generation.InputAsset{"source_asset": {{
			ID: "asset-model", OwnerID: "owner-a", MediaType: "model/gltf-binary",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "retexture-remote-1" {
		t.Fatalf("started = %#v", started)
	}
}

func TestA404WithoutATaskReferenceIsNotASourceExpiry(t *testing.T) {
	upstream := retextureUpstream(t, func(_ map[string]any, writer http.ResponseWriter) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"message":"Not found"}`))
	})
	defer upstream.Close()
	adapter := newMeshyAdapter(t, meshy.Config{BaseURL: upstream.URL, APIKey: "meshy-key", HTTPClient: upstream.Client(),
		InputAssets: memoryInputAssets{content: []byte("glb")}})
	_, err := adapter.Start(context.Background(), generation.StartRequest{
		Offering: shippedRetexture(t),
		Input:    []byte(`{"source_asset":"asset-model","prompt":"walnut","target_formats":["glb","obj","fbx","stl","usdz","3mf"]}`),
		InputAssets: map[string][]generation.InputAsset{"source_asset": {{
			ID: "asset-model", OwnerID: "owner-a", MediaType: "model/gltf-binary",
		}}},
	})
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil || provider.Failure.Class == generation.FailureSourceExpired {
		t.Fatalf("err = %v; want a provider failure other than source_expired", err)
	}
}
