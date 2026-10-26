package httpapi_test

import (
	"mime"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

var safeDownloadName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// #315 (DLV-04): every stored product byte a person downloads is served with
// nosniff and CSP sandbox. Images the UI previews (Schema inline_media_types)
// stay inline; every other file is an attachment with a safe file name, so
// an MTL or any text an upstream returned never renders on this origin.
func TestTaskAssetsAreDeliveredWithSandboxAndSafeDisposition(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	model := createSucceededMeshyModelTask(t, module, "owner-a")
	image := createSucceededImageTask(t, module, "owner-a")
	inline := map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true}
	seen := map[string]bool{}
	check := func(taskID, assetID, role, variant, mediaType string) {
		t.Helper()
		response := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+taskID+"/assets/"+assetID, "owner-a")
		header := response.Header()
		if response.Code != http.StatusOK || header.Get("X-Content-Type-Options") != "nosniff" || header.Get("Content-Security-Policy") != "sandbox" {
			t.Fatalf("%s/%s: %d %v", role, variant, response.Code, header)
		}
		disposition, params, err := mime.ParseMediaType(header.Get("Content-Disposition"))
		if err != nil {
			t.Fatalf("%s/%s: disposition %q: %v", role, variant, header.Get("Content-Disposition"), err)
		}
		if inline[mediaType] {
			if disposition != "inline" {
				t.Fatalf("%s/%s (%s) should stay inline: %q", role, variant, mediaType, header.Get("Content-Disposition"))
			}
			return
		}
		if disposition != "attachment" || !safeDownloadName.MatchString(params["filename"]) || !strings.Contains(params["filename"], taskID) {
			t.Fatalf("%s/%s (%s): %q", role, variant, mediaType, header.Get("Content-Disposition"))
		}
		seen[variant] = true
	}
	for _, asset := range model.Assets {
		check(string(model.ID), string(asset.ID), asset.Role, asset.Variant, asset.MediaType)
	}
	for _, asset := range image.Assets {
		check(string(image.ID), string(asset.ID), asset.Role, asset.Variant, asset.MediaType)
	}
	if !seen["glb"] || !seen["obj"] {
		t.Fatalf("fixture did not exercise model attachments: %v", seen)
	}
	// The model keeps its Schema format name (#264).
	glb := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(model.ID)+"/assets/"+string(assetOf(t, model.Assets, "model", "glb")), "owner-a")
	if _, params, _ := mime.ParseMediaType(glb.Header().Get("Content-Disposition")); params["filename"] != "model-"+string(model.ID)+".glb" {
		t.Fatalf("glb name = %q", glb.Header().Get("Content-Disposition"))
	}
	archive := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(model.ID)+"/archive", "owner-a")
	if archive.Code != http.StatusOK || archive.Header().Get("Content-Security-Policy") != "sandbox" || archive.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(archive.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("archive headers = %d %v", archive.Code, archive.Header())
	}
}

func assetOf(t *testing.T, assets []generation.Asset, role, variant string) generation.AssetID {
	t.Helper()
	for _, asset := range assets {
		if asset.Role == role && asset.Variant == variant {
			return asset.ID
		}
	}
	t.Fatalf("no %s/%s asset in %v", role, variant, assets)
	return ""
}
