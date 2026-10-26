package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #264 follow-up: a model file downloaded on its own is saved under the
// Schema's format name (model-<task>.<extension>), not as asset_<id>.<guess>.
// Authorization and caching headers stay as they were, and images that the UI
// previews keep their inline delivery.
func TestTaskModelAssetDownloadNamesTheFileByItsFormat(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	task := createSucceededMeshyModelTask(t, module, "owner-a")
	base := "/api/v1/tasks/" + string(task.ID) + "/assets/"
	var model, usdz generation.Asset
	for _, asset := range task.Assets {
		switch {
		case asset.Role == "model" && asset.Variant == "glb":
			model = asset
		case asset.Role == "model" && asset.Variant == "usdz":
			usdz = asset
		}
	}
	if model.ID == "" || usdz.ID == "" {
		t.Fatalf("fixture assets = %#v", task.Assets)
	}

	response := modelDeliveryRequest(handler, http.MethodGet, base+string(model.ID), "owner-a")
	name := "model-" + string(task.ID) + ".glb"
	want := `attachment; filename="` + name + `"; filename*=UTF-8''` + name
	if response.Code != http.StatusOK || response.Header().Get("Content-Disposition") != want {
		t.Fatalf("model download = %d %q; want %q", response.Code, response.Header().Get("Content-Disposition"), want)
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" ||
		response.Header().Get("ETag") == "" || response.Header().Get("Content-Type") != model.MediaType {
		t.Fatalf("model download headers changed: %v", response.Header())
	}
	if foreign := modelDeliveryRequest(handler, http.MethodGet, base+string(model.ID), "owner-b"); foreign.Code != http.StatusNotFound ||
		foreign.Header().Get("Content-Disposition") != "" {
		t.Fatalf("foreign download = %d %v", foreign.Code, foreign.Header())
	}

	other := modelDeliveryRequest(handler, http.MethodGet, base+string(usdz.ID), "owner-a")
	if name := "model-" + string(task.ID) + ".usdz"; other.Header().Get("Content-Disposition") != `attachment; filename="`+name+`"; filename*=UTF-8''`+name {
		t.Fatalf("usdz download = %q", other.Header().Get("Content-Disposition"))
	}

	image := createSucceededImageTask(t, module, "owner-a")
	picture := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(image.ID)+"/assets/"+string(image.Assets[0].ID), "owner-a")
	if picture.Code != http.StatusOK || strings.HasPrefix(picture.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("image lost inline delivery: %d %v", picture.Code, picture.Header())
	}
}
