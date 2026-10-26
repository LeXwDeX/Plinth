package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// #330 second follow-ups, observation 5: a single OBJ download answered
// application/x-tgif (the host's mime table) and a USDZ application/zip. A
// model file is now served with its format's media type from the Product
// Schema (IANA registrations; FBX has none and stays octet-stream), while
// nosniff and the attachment disposition are unchanged.
func TestModelDownloadContentTypeFollowsTheFormat330(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	task := createSucceededMeshyModelTask(t, module, "owner-a")
	want := map[string]string{
		"glb": "model/gltf-binary", "fbx": "application/octet-stream", "obj": "model/obj",
		"stl": "model/stl", "usdz": "model/vnd.usdz+zip", "3mf": "model/3mf",
	}
	seen := 0
	for _, asset := range task.Assets {
		expected, listed := want[asset.Variant]
		if asset.Role != "model" || !listed {
			continue
		}
		seen++
		response := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(task.ID)+"/assets/"+string(asset.ID), "owner-a")
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != expected {
			t.Errorf("%s (saved %q) = %d %q; want %q", asset.Variant, asset.MediaType, response.Code, response.Header().Get("Content-Type"), expected)
		}
		if response.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment;") {
			t.Errorf("%s lost nosniff or attachment: %v", asset.Variant, response.Header())
		}
	}
	if seen != len(want) {
		t.Fatalf("fixture has %d of %d formats: %#v", seen, len(want), task.Assets)
	}
}
