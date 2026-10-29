package httpapi_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

type requireExplicitTestIdentity struct {
	fallback authentication.FallbackIdentity
}

func (i requireExplicitTestIdentity) Identify(request *http.Request) (access.Principal, error) {
	if request.Header.Get("X-User-ID") == "" {
		return access.Principal{}, authentication.ErrUnauthenticated
	}
	return i.fallback.Identify(request)
}

type thumbnailFixtureAdapter struct {
	*httpMeshyFormatFixtureAdapter
	png []byte
}

func (a *thumbnailFixtureAdapter) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	observation, err := a.httpMeshyFormatFixtureAdapter.Observe(ctx, request)
	if err == nil && observation.Phase == generation.UpstreamCompleted && request.Offering.ID == "tripo/tripo-v3.1-full/text-to-3d" && !request.PostSuccess {
		observation.Artifacts = append(observation.Artifacts, generation.ArtifactClaim{ID: string(request.AttemptID) + ":thumbnail", Role: "thumbnail", Variant: "rendered_image", MediaType: "image/png", Inline: a.png})
	}
	return observation, err
}

func TestThumbnailConditionalRejectsCorruptedCAS(t *testing.T) {
	var root string
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	adapter := &thumbnailFixtureAdapter{httpMeshyFormatFixtureAdapter: &httpMeshyFormatFixtureAdapter{Adapter: mock.New()}, png: imageBytes.Bytes()}
	module, handler, _, _ := newHTTPHarnessWithCatalogAndProviders(t, []string{"user"}, nil,
		func(config *authentication.Config) { config.Fallback = requireExplicitTestIdentity{config.Fallback} }, nil, nil, nil,
		map[string]generation.ProviderPort{"tripo.v3": adapter},
		func(t *testing.T, database string) { root = filepath.Dir(database) })
	task := createSucceededTripoModelTask(t, module, "owner-a")
	var thumbnail generation.Asset
	for _, asset := range task.Assets {
		if asset.Role == "thumbnail" {
			thumbnail = asset
		}
	}
	if thumbnail.ID == "" {
		t.Fatalf("fixture missing real thumbnail: %+v", task.Assets)
	}
	url := "/api/v1/tasks/" + string(task.ID) + "/assets/" + string(thumbnail.ID)
	request := func(owner, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if owner != "" {
			req.Header.Set("X-User-ID", owner)
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := request("owner-a", "")
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" || !bytes.Equal(first.Body.Bytes(), imageBytes.Bytes()) {
		t.Fatalf("healthy thumbnail: %d", first.Code)
	}
	if response := request("owner-a", etag); response.Code != http.StatusNotModified || response.Header().Get("ETag") != etag {
		t.Fatalf("healthy conditional: %d", response.Code)
	}
	for _, tc := range []struct {
		owner  string
		status int
	}{{"owner-b", http.StatusNotFound}, {"", http.StatusUnauthorized}} {
		response := request(tc.owner, etag)
		if response.Code != tc.status || response.Header().Get("ETag") != "" {
			t.Fatalf("authorization %q: %d", tc.owner, response.Code)
		}
	}
	corrupt := bytes.Repeat([]byte("x"), int(thumbnail.Size))
	if err := os.WriteFile(filepath.Join(root, "storage", thumbnail.StorageKey), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	response := request("owner-a", etag)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("ETag") != "" || bytes.Contains(response.Body.Bytes(), corrupt) {
		t.Fatalf("corrupted conditional status=%d headers=%v", response.Code, response.Header())
	}
}

func TestTaskAssetCorruptionNeverCrossesOwnerOrAnonymousBoundary(t *testing.T) {
	var root string
	module, handler, _, _ := newHTTPHarnessWithServices(t, []string{"user"}, nil,
		func(config *authentication.Config) { config.Fallback = requireExplicitTestIdentity{config.Fallback} }, nil, nil,
		func(t *testing.T, database string) { root = filepath.Dir(database) })
	task := createSucceededImageTask(t, module, "owner-a")
	asset := task.Assets[0]
	url := "/api/v1/tasks/" + string(task.ID) + "/assets/" + string(asset.ID)
	request := func(owner string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if owner != "" {
			req.Header.Set("X-User-ID", owner)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request("owner-a"); response.Code != http.StatusOK || response.Body.Len() == 0 {
		t.Fatalf("owner healthy: %d", response.Code)
	}
	corrupt := bytes.Repeat([]byte("x"), int(asset.Size))
	if err := os.WriteFile(filepath.Join(root, "storage", asset.StorageKey), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"owner-a", "owner-b", ""} {
		response := request(owner)
		if response.Code == http.StatusOK || bytes.Contains(response.Body.Bytes(), corrupt) {
			t.Fatalf("corrupt asset delivered to %q: status %d", owner, response.Code)
		}
		if owner == "owner-b" && response.Code != http.StatusNotFound {
			t.Fatalf("cross-owner status = %d", response.Code)
		}
		if owner == "" && response.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous status = %d", response.Code)
		}
	}
}

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
			if otherOwner.Code != http.StatusNotFound ||
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
