package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type modelPreviewTestIdentity struct{}

func (modelPreviewTestIdentity) Identify(request *http.Request) (access.Principal, error) {
	owner := strings.TrimSpace(request.Header.Get("X-User-ID"))
	if owner == "" {
		return access.Principal{}, errors.New("unauthenticated")
	}
	return access.Principal{Claims: access.Claims{ID: owner}}, nil
}

type modelPreviewFixture struct {
	handler       http.Handler
	app           catalog.ApplicationSchema
	uploadProfile *catalog.ModelInputProfile
	native        *inputassets.Service
	root          string
}

func newModelPreviewFixture(t *testing.T, customize func(*catalog.ApplicationSchema), readerEnabled bool) modelPreviewFixture {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	profile := app.Runtime.InputAssets.ModelProfile
	if profile == nil {
		t.Fatal("default catalog requires a model input profile")
	}
	profileCopy := *profile
	profileCopy.PreviewMediaTypes = append([]string(nil), profile.PreviewMediaTypes...)
	if customize != nil {
		customize(&app)
	}
	root := t.TempDir()
	repository, err := OpenMigratedTestRepository(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	blobs, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	native, err := inputassets.New(repository, blobs)
	if err != nil {
		t.Fatal(err)
	}
	policy := modelinput.Policy{ProfileID: profile.ID, Revision: profile.Revision, Limits: modelinput.Limits{
		MaxBytes: profile.Limits.MaxBytes, MaxJSONBytes: profile.Limits.MaxJSONBytes,
		MaxDepth: profile.Limits.MaxDepth, MaxElements: profile.Limits.MaxElements,
		MaxVertices: profile.Limits.MaxVertices, MaxTriangles: profile.Limits.MaxTriangles,
		MaxNodes: profile.Limits.MaxNodes, MaxWork: profile.Limits.MaxWork,
		MaxImagePixels: profile.Limits.MaxImagePixels, MaxTotalImagePixels: profile.Limits.MaxTotalImagePixels,
	}}
	models, err := modelsource.New(native, policy)
	if err != nil {
		t.Fatal(err)
	}
	auditSchema := app.Administration.AuditLogs
	audit, err := administration.NewAuditService(administration.AuditServiceConfig{
		Repository: repository, DefaultPage: auditSchema.Limits.DefaultPage,
		DefaultPageSize: auditSchema.Limits.DefaultPageSize, MaximumPageSize: auditSchema.Limits.MaximumPageSize,
		IDPrefix: auditSchema.Defaults.IDPrefix, WriteMaximumAttempts: auditSchema.WritePolicy.MaximumAttempts,
		WriteRetryDelay: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	var previewReader ModelPreviewReader
	if readerEnabled {
		previewReader = models
	}
	server := &Server{
		identity: modelPreviewTestIdentity{}, application: app, modelPreviews: previewReader,
		administrationAudit: audit, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mux: http.NewServeMux(),
	}
	if route := app.Runtime.HTTP.Routes.AssetModelPreview; route != "" {
		server.mux.HandleFunc("GET "+route, server.withIdentity(server.previewInputModel))
	}
	return modelPreviewFixture{handler: server.mux, app: app, uploadProfile: &profileCopy, native: native, root: root}
}

func uploadPreviewFixture(t *testing.T, fixture modelPreviewFixture, owner string, raw []byte) generation.InputAsset {
	t.Helper()
	profile := fixture.uploadProfile
	asset, err := fixture.native.UploadAsset(context.Background(), generation.OwnerID(owner), generation.InputAssetUpload{
		Body: bytes.NewReader(raw), OriginalFilename: "preview.glb", AssetType: profile.AssetType,
		MediaType: profile.UploadMediaType, MaxBytes: profile.Limits.MaxBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func previewRequest(fixture modelPreviewFixture, owner string, id generation.AssetID) *httptest.ResponseRecorder {
	path := strings.ReplaceAll(fixture.app.Runtime.HTTP.Routes.AssetModelPreview, "{assetID}", string(id))
	if path == "" {
		path = "/api/v1/assets/" + string(id) + "/model-preview"
	}
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if owner != "" {
		request.Header.Set("X-User-ID", owner)
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	return response
}

func TestModelPreviewHTTPReturnsExactVerifiedOwnerBytesAndHeaders(t *testing.T) {
	t.Parallel()
	fixture := newModelPreviewFixture(t, nil, true)
	raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true})
	asset := uploadPreviewFixture(t, fixture, "owner-a", raw)
	response := previewRequest(fixture, "owner-a", asset.ID)
	digest := sha256.Sum256(raw)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), raw) {
		t.Fatalf("preview=%d body_len=%d", response.Code, response.Body.Len())
	}
	if response.Header().Get("Content-Type") != fixture.app.Runtime.InputAssets.ModelProfile.UploadMediaType ||
		response.Header().Get("Content-Length") != stringInt(len(raw)) ||
		response.Header().Get("ETag") != `"`+hex.EncodeToString(digest[:])+`"` ||
		response.Header().Get("Cache-Control") != "private, no-store" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("preview headers = %#v", response.Header())
	}
}

func TestModelPreviewHTTPRejectsAnonymousForeignAndMissingReader(t *testing.T) {
	t.Parallel()
	fixture := newModelPreviewFixture(t, nil, true)
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	owned := uploadPreviewFixture(t, fixture, "owner-a", raw)
	foreign := uploadPreviewFixture(t, fixture, "owner-b", raw)

	if response := previewRequest(fixture, "", owned.ID); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous preview status = %d", response.Code)
	}
	if response := previewRequest(fixture, "owner-a", foreign.ID); response.Code != http.StatusNotFound {
		t.Fatalf("foreign preview status = %d", response.Code)
	}

	missing := newModelPreviewFixture(t, nil, false)
	if response := previewRequest(missing, "owner-a", "some-id"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing preview service status = %d", response.Code)
	}
}

func TestModelPreviewHTTPSchemaAndRouteRevocationFailClosed(t *testing.T) {
	t.Parallel()
	assetBytes := modelinputtest.Triangle(modelinputtest.Options{})
	for _, tc := range []struct {
		name      string
		customize func(*catalog.ApplicationSchema)
	}{
		{name: "profile removed", customize: func(app *catalog.ApplicationSchema) { app.Runtime.InputAssets.ModelProfile = nil }},
		{name: "preview media list empty", customize: func(app *catalog.ApplicationSchema) { app.Runtime.InputAssets.ModelProfile.PreviewMediaTypes = nil }},
		{name: "asset media type withdrawn", customize: func(app *catalog.ApplicationSchema) {
			app.Runtime.InputAssets.ModelProfile.PreviewMediaTypes = []string{"application/octet-stream"}
		}},
		{name: "route removed", customize: func(app *catalog.ApplicationSchema) { app.Runtime.HTTP.Routes.AssetModelPreview = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newModelPreviewFixture(t, tc.customize, true)
			asset := uploadPreviewFixture(t, fixture, "owner-a", assetBytes)
			response := previewRequest(fixture, "owner-a", asset.ID)
			if response.Code != http.StatusNotFound || response.Body.Len() == 0 {
				t.Fatalf("revoked preview status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestModelPreviewHTTPRejectsTamperedBytesAndExternalURIGLB(t *testing.T) {
	t.Parallel()
	t.Run("tampered stored bytes", func(t *testing.T) {
		fixture := newModelPreviewFixture(t, nil, true)
		raw := modelinputtest.Triangle(modelinputtest.Options{})
		asset := uploadPreviewFixture(t, fixture, "owner-a", raw)
		path := filepath.Join(fixture.root, "storage", filepath.FromSlash(asset.StorageKey))
		if err := os.WriteFile(path, []byte("not the original GLB"), 0o600); err != nil {
			t.Fatal(err)
		}
		response := previewRequest(fixture, "owner-a", asset.ID)
		if response.Code == http.StatusOK {
			t.Fatal("tampered stored asset received a success response")
		}
	})

	t.Run("external URI", func(t *testing.T) {
		fixture := newModelPreviewFixture(t, nil, true)
		doc, bin := modelinputtest.Document(modelinputtest.Options{})
		doc["buffers"].([]any)[0].(map[string]any)["uri"] = "https://example.invalid/external.bin"
		external := modelinputtest.GLB(doc, bin)
		asset := uploadPreviewFixture(t, fixture, "owner-a", external)
		response := previewRequest(fixture, "owner-a", asset.ID)
		if response.Code != http.StatusBadRequest || response.Code == http.StatusOK {
			t.Fatalf("external URI preview status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func stringInt(value int) string {
	return strconv.Itoa(value)
}
