package httpapi_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #290: small views ask for ?size=preview and get a downscaled PNG the owner's browser may keep for a while;
// the original stays no-store, other owners get nothing, and assets without a preview fall back to the original.
func TestAssetPreviewIsSmallCacheableAndOwnerScoped(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	task := createSucceededImageTask(t, module, generation.OwnerID("owner-a"))
	if len(task.Assets) != 1 {
		t.Fatalf("task assets = %#v", task.Assets)
	}
	url := "/api/v1/tasks/" + string(task.ID) + "/assets/" + string(task.Assets[0].ID)
	get := func(path, owner, ifNoneMatch string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-User-ID", owner)
		if ifNoneMatch != "" {
			request.Header.Set("If-None-Match", ifNoneMatch)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	original := get(url, "owner-a", "")
	if original.Code != http.StatusOK || original.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("original = %d %v", original.Code, original.Header())
	}
	originalSize, _, err := image.DecodeConfig(bytes.NewReader(original.Body.Bytes()))
	if err != nil || max(originalSize.Width, originalSize.Height) <= 512 {
		t.Fatalf("fixture picture %dx%d (%v) must be larger than a preview", originalSize.Width, originalSize.Height, err)
	}

	preview := get(url+"?size=preview", "owner-a", "")
	if preview.Code != http.StatusOK || preview.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("preview = %d %v", preview.Code, preview.Header())
	}
	size, format, err := image.DecodeConfig(bytes.NewReader(preview.Body.Bytes()))
	if err != nil || format != "png" || max(size.Width, size.Height) != 512 {
		t.Fatalf("preview picture = %s %dx%d (%v)", format, size.Width, size.Height, err)
	}
	if preview.Body.Len() >= original.Body.Len() {
		t.Fatalf("preview %d bytes is not smaller than the original %d", preview.Body.Len(), original.Body.Len())
	}
	etag := preview.Header().Get("ETag")
	if preview.Header().Get("Cache-Control") != "private, max-age=3600" ||
		preview.Header().Get("Vary") != "Cookie, Authorization" ||
		!strings.HasSuffix(etag, `-preview-512"`) || etag == original.Header().Get("ETag") {
		t.Fatalf("preview caching headers = %v", preview.Header())
	}

	again := get(url+"?size=preview", "owner-a", "")
	if again.Code != http.StatusOK || !bytes.Equal(again.Body.Bytes(), preview.Body.Bytes()) {
		t.Fatal("a second request is answered with the same (cached) preview")
	}
	revalidated := get(url+"?size=preview", "owner-a", etag)
	if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 || revalidated.Header().Get("ETag") != etag {
		t.Fatalf("revalidation = %d %v", revalidated.Code, revalidated.Header())
	}
	// The validator does not skip authorization: another owner gets the same 404 as for the original.
	other := get(url+"?size=preview", "owner-b", etag)
	if other.Code != http.StatusNotFound || other.Header().Get("ETag") != "" ||
		other.Header().Get("Cache-Control") != "private, no-store" || other.Body.Len() == preview.Body.Len() {
		t.Fatalf("cross-owner preview = %d %v", other.Code, other.Header())
	}
	// Any other size value is the original.
	unknown := get(url+"?size=huge", "owner-a", "")
	if unknown.Code != http.StatusOK || !bytes.Equal(unknown.Body.Bytes(), original.Body.Bytes()) ||
		unknown.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unknown size = %d %v", unknown.Code, unknown.Header())
	}
}

func TestAssetPreviewFallsBackToTheOriginalForNonPictures(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	task := createSucceededTripoModelTask(t, module, generation.OwnerID("owner-a"))
	var model generation.Asset
	for _, asset := range task.Assets {
		if asset.Role == "converted_model" {
			model = asset
		}
	}
	if model.ID == "" {
		t.Fatalf("fixture lacks a model asset: %#v", task.Assets)
	}
	url := "/api/v1/tasks/" + string(task.ID) + "/assets/" + string(model.ID)
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-User-ID", "owner-a")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	original := get(url)
	asked := get(url + "?size=preview")
	if asked.Code != http.StatusOK || !bytes.Equal(asked.Body.Bytes(), original.Body.Bytes()) ||
		asked.Header().Get("Cache-Control") != "private, no-store" || asked.Header().Get("ETag") != original.Header().Get("ETag") {
		t.Fatalf("model asked for a preview = %d %v", asked.Code, asked.Header())
	}
}

func TestInputAssetPreviewIsSmallAndOwnerScoped(t *testing.T) {
	t.Parallel()
	_, handler, inputs := newHTTPHarnessWithRoles(t, []string{"user"})
	picture := image.NewNRGBA(image.Rect(0, 0, 1600, 900))
	for index := range picture.Pix {
		picture.Pix[index] = uint8(index * 7)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	asset, err := inputs.UploadAsset(context.Background(), "owner-a", generation.InputAssetUpload{
		Body: bytes.NewReader(encoded.Bytes()), OriginalFilename: "board.png", MediaType: "image/png", AssetType: "image",
		MaxBytes: int64(encoded.Len()) + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, owner string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-User-ID", owner)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	url := "/api/v1/assets/" + string(asset.ID)
	preview := get(url+"?size=preview", "owner-a")
	size, _, err := image.DecodeConfig(bytes.NewReader(preview.Body.Bytes()))
	if preview.Code != http.StatusOK || err != nil || size.Width != 512 || size.Height != 288 ||
		preview.Header().Get("Cache-Control") != "private, max-age=3600" || preview.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("input preview = %d %dx%d (%v) %v", preview.Code, size.Width, size.Height, err, preview.Header())
	}
	original := get(url, "owner-a")
	if original.Code != http.StatusOK || !bytes.Equal(original.Body.Bytes(), encoded.Bytes()) || original.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("input original = %d %v", original.Code, original.Header())
	}
	if other := get(url+"?size=preview", "owner-b"); other.Code != http.StatusForbidden || other.Header().Get("ETag") != "" {
		t.Fatalf("cross-owner input preview = %d %v", other.Code, other.Header())
	}
}
