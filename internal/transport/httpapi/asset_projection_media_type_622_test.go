package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #622: the task projection listed a Meshy OBJ as application/x-tgif (the
// label the CDN's host mime table gave the bytes) while the same file was
// downloaded as model/obj (#330). Every asset of a task now projects the
// media type its own download answers with, for each model format and for
// files of no format alike, so a reader never sees two types for one file.
func TestTaskProjectionMediaTypeMatchesDownload622(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	for _, task := range []generation.Task{
		createSucceededMeshyModelTask(t, module, "owner-a"),
		createSucceededTripoModelTask(t, module, "owner-a"),
	} {
		response := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(task.ID), "owner-a")
		if response.Code != http.StatusOK {
			t.Fatalf("task %s = %d %s", task.ID, response.Code, response.Body.String())
		}
		var projected struct {
			Assets []struct {
				ID        string `json:"id"`
				Role      string `json:"role"`
				Variant   string `json:"variant"`
				MediaType string `json:"media_type"`
				URL       string `json:"url"`
			} `json:"assets"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &projected); err != nil {
			t.Fatal(err)
		}
		if len(projected.Assets) == 0 {
			t.Fatalf("task %s projected no assets", task.ID)
		}
		for _, asset := range projected.Assets {
			download := modelDeliveryRequest(handler, http.MethodGet, asset.URL, "owner-a")
			if download.Code != http.StatusOK {
				t.Fatalf("%s/%s download = %d", asset.Role, asset.Variant, download.Code)
			}
			if got := download.Header().Get("Content-Type"); asset.MediaType != got {
				t.Errorf("%s %s/%s projected %q, downloaded as %q", task.Offering.ProviderID, asset.Role, asset.Variant, asset.MediaType, got)
			}
		}
	}
}

// The stored label a CDN gave an OBJ (application/x-tgif) is projected as the
// OBJ format's type in the task view as in its download.
func TestTaskProjectionNormalizesHostMimeLabel622(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	task := createSucceededMeshyModelTask(t, module, "owner-a")
	response := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(task.ID), "owner-a")
	var projected struct {
		Assets []struct {
			Role      string `json:"role"`
			Variant   string `json:"variant"`
			MediaType string `json:"media_type"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &projected); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"glb": "model/gltf-binary", "fbx": "application/octet-stream", "obj": "model/obj",
		"stl": "model/stl", "usdz": "model/vnd.usdz+zip", "3mf": "model/3mf",
	}
	seen := 0
	for _, asset := range projected.Assets {
		expected, listed := want[asset.Variant]
		if asset.Role != "model" || !listed {
			continue
		}
		seen++
		if asset.MediaType != expected {
			t.Errorf("%s projected %q; want %q", asset.Variant, asset.MediaType, expected)
		}
	}
	if seen != len(want) {
		t.Fatalf("fixture has %d of %d formats: %#v", seen, len(want), projected.Assets)
	}
}
