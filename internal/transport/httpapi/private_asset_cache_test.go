package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestPrivateTaskAssetDownloadReauthorizesConditionalRequests(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	task := createSucceededImageTask(t, module, generation.OwnerID("owner-a"))
	if len(task.Assets) != 1 {
		t.Fatalf("task assets = %#v", task.Assets)
	}
	url := "/api/v1/tasks/" + string(task.ID) + "/assets/" +
		string(task.Assets[0].ID)

	request := func(owner, ifNoneMatch string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("X-User-ID", owner)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	first := request("owner-a", "")
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || first.Body.Len() == 0 || etag == "" {
		t.Fatalf("first response = status %d headers %v body %q",
			first.Code, first.Header(), first.Body.String())
	}
	if policy := first.Header().Get("Cache-Control"); policy != "private, no-store" {
		t.Fatalf("first cache policy = %q", policy)
	}

	// no-store responses cannot be reused by a browser. A validator sent by a
	// client still goes through authorization and gets fresh bytes, not 304.
	conditional := request("owner-a", etag)
	if conditional.Code != http.StatusOK ||
		conditional.Header().Get("ETag") != etag ||
		conditional.Header().Get("Cache-Control") != "private, no-store" ||
		conditional.Body.String() != first.Body.String() {
		t.Fatalf("conditional response = status %d headers %v body %q",
			conditional.Code, conditional.Header(), conditional.Body.String())
	}

	otherOwner := request("owner-b", etag)
	if otherOwner.Code != http.StatusNotFound ||
		otherOwner.Header().Get("ETag") != "" ||
		otherOwner.Header().Get("Cache-Control") != "private, no-store" ||
		otherOwner.Body.String() == first.Body.String() {
		t.Fatalf("cross-owner response = status %d headers %v body %q",
			otherOwner.Code, otherOwner.Header(), otherOwner.Body.String())
	}
}

func TestPrivateInputAssetRoutesDoNotCacheAcrossOwners(t *testing.T) {
	t.Parallel()
	_, handler, inputs := newHTTPHarnessWithRoles(t, []string{"user"})
	asset, err := inputs.UploadAsset(
		context.Background(),
		"owner-a",
		generation.InputAssetUpload{
			Body:             strings.NewReader("private input"),
			OriginalFilename: "private.png",
			MediaType:        "image/png",
			AssetType:        "image",
			MaxBytes:         1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v1/assets/" + string(asset.ID),
		"/api/assets/" + string(asset.ID),
		"/api/assets/" + string(asset.ID) + "?format=json",
	} {
		t.Run(path, func(t *testing.T) {
			request := func(owner, ifNoneMatch string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("X-User-ID", owner)
				if ifNoneMatch != "" {
					req.Header.Set("If-None-Match", ifNoneMatch)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				return response
			}
			owned := request("owner-a", "")
			if owned.Code != http.StatusOK || owned.Body.Len() == 0 ||
				owned.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("owned response = status %d headers %v body %q",
					owned.Code, owned.Header(), owned.Body.String())
			}
			etag := owned.Header().Get("ETag")
			if etag == "" {
				etag = `"` + asset.SHA256 + `"`
			}
			conditional := request("owner-a", etag)
			if conditional.Code != http.StatusOK ||
				conditional.Header().Get("Cache-Control") != "private, no-store" ||
				conditional.Body.String() != owned.Body.String() {
				t.Fatalf("conditional response = status %d headers %v body %q",
					conditional.Code, conditional.Header(), conditional.Body.String())
			}
			otherOwner := request("owner-b", etag)
			if otherOwner.Code != http.StatusForbidden ||
				otherOwner.Header().Get("ETag") != "" ||
				otherOwner.Header().Get("Cache-Control") != "private, no-store" ||
				strings.Contains(otherOwner.Body.String(), "private input") {
				t.Fatalf("cross-owner response = status %d headers %v body %q",
					otherOwner.Code, otherOwner.Header(), otherOwner.Body.String())
			}
		})
	}
}

// #225: thumbnails may be reused by the owner's browser for a short, Schema-
// declared time and revalidate with 304 — but only after authorization. Full
// model bytes stay no-store, and another owner never sees an ETag.
func TestThumbnailAssetsAreBrowserCacheableOnlyAfterAuthorization(t *testing.T) {
	t.Parallel()
	// The mock provider emits no thumbnail asset, so this test declares the
	// fixture's "model" role as the cacheable one; the default Schema list
	// (thumbnail only) is pinned by the catalog test.
	module, handler, _ := newHTTPHarnessWithApplication(t, []string{"user"}, func(application *catalog.ApplicationSchema) {
		application.Runtime.HTTP.ThumbnailAssetRoles = []string{"model"}
	})
	task := createSucceededTripoModelTask(t, module, generation.OwnerID("owner-a"))
	var thumbnail, model generation.Asset
	for _, asset := range task.Assets {
		switch asset.Role {
		case "model":
			thumbnail = asset
		case "converted_model":
			model = asset
		}
	}
	if thumbnail.ID == "" || model.ID == "" {
		t.Fatalf("fixture lacks thumbnail/model assets: %#v", task.Assets)
	}
	request := func(asset generation.Asset, owner, ifNoneMatch string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+string(task.ID)+"/assets/"+string(asset.ID), nil)
		req.Header.Set("X-User-ID", owner)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := request(thumbnail, "owner-a", "")
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" || first.Body.Len() == 0 ||
		first.Header().Get("Cache-Control") != "private, max-age=3600" ||
		first.Header().Get("Vary") != "Cookie, Authorization" {
		t.Fatalf("thumbnail = %d %v", first.Code, first.Header())
	}
	revalidated := request(thumbnail, "owner-a", etag)
	if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 ||
		revalidated.Header().Get("ETag") != etag || revalidated.Header().Get("Cache-Control") != "private, max-age=3600" ||
		revalidated.Header().Get("Vary") != "Cookie, Authorization" {
		t.Fatalf("revalidation = %d %v %q", revalidated.Code, revalidated.Header(), revalidated.Body.String())
	}
	if stale := request(thumbnail, "owner-a", `"other"`); stale.Code != http.StatusOK || stale.Body.String() != first.Body.String() {
		t.Fatalf("stale validator = %d", stale.Code)
	}
	otherOwner := request(thumbnail, "owner-b", etag)
	if otherOwner.Code != http.StatusNotFound || otherOwner.Header().Get("ETag") != "" ||
		otherOwner.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cross-owner thumbnail = %d %v", otherOwner.Code, otherOwner.Header())
	}
	full := request(model, "owner-a", "")
	if full.Code != http.StatusOK || full.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("model bytes = %d %v", full.Code, full.Header())
	}
	if again := request(model, "owner-a", full.Header().Get("ETag")); again.Code != http.StatusOK || again.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("model conditional = %d %v", again.Code, again.Header())
	}
}
