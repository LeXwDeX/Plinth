package legacygeneration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/assets/legacygenerated"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestCollectAssetHostsDeduplicatesSchemaOfferings(t *testing.T) {
	got := CollectAssetHosts([]generation.Offering{
		{AssetHosts: []string{"cdn.test", "assets.test"}},
		{AssetHosts: []string{"CDN.TEST.", "assets.test"}},
	})
	if strings.Join(got, ",") != "assets.test,cdn.test" {
		t.Fatalf("asset hosts = %#v", got)
	}
}

func TestCollectAssetDownloadsUsesCapabilitySchema(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	got := CollectAssetDownloads(taskCatalog.Application().Capabilities)
	if len(got) != 1 ||
		got[0].Path !=
			"/api/meshy/background-removal/download/{assetToken}" ||
		got[0].CacheSubdirectory != "matting" {
		t.Fatalf("asset downloads = %#v", got)
	}
}

func TestReaderDelegatesNativeAssetsWithoutALegacySource(t *testing.T) {
	native := &recordingGenerationAssetReader{
		content: administration.GenerationAssetContent{
			Body:      io.NopCloser(strings.NewReader("native")),
			MediaType: "image/png",
			Size:      6,
		},
	}
	reader := newTestLegacyReader(t, ReaderConfig{Native: native})
	request := administration.GenerationAssetRequest{
		OwnerID: "owner",
		TaskID:  "task",
		Asset: administration.GenerationAsset{
			ID: "asset",
		},
	}

	content, err := reader.OpenGenerationAsset(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(content.Body)
	_ = content.Body.Close()
	if string(body) != "native" || native.request.Asset.ID != "asset" {
		t.Fatalf("native result = %q, request = %+v", body, native.request)
	}
}

func TestReaderOpensConfiguredLegacyGeneratedImageRoute(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "historical.webp"),
		[]byte("historical"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	generated, err := legacygenerated.New(root)
	if err != nil {
		t.Fatal(err)
	}
	reader := newTestLegacyReader(t, ReaderConfig{
		GeneratedImages:     generated,
		GeneratedImageRoute: "/custom/generated/{file}",
	})

	content, err := reader.OpenGenerationAsset(
		context.Background(),
		administration.GenerationAssetRequest{
			Asset: administration.GenerationAsset{
				SourceURL: "/custom/generated/historical.webp",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(content.Body)
	_ = content.Body.Close()
	if string(body) != "historical" ||
		content.MediaType != "image/webp" ||
		content.Size != int64(len("historical")) {
		t.Fatalf("generated content = %+v, body = %q", content, body)
	}

	_, err = reader.OpenGenerationAsset(
		context.Background(),
		administration.GenerationAssetRequest{
			Asset: administration.GenerationAsset{
				SourceURL: "/custom/generated/../secret",
			},
		},
	)
	if err == nil {
		t.Fatal("expected traversal source to fail")
	}
}

func TestReaderOpensConfiguredLegacyDownloadFromReadOnlyCache(t *testing.T) {
	cacheRoot := t.TempDir()
	cacheDirectory := filepath.Join(cacheRoot, "configured-results")
	if err := os.MkdirAll(cacheDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cacheDirectory, "result-subtask-a.png"),
		validLegacyReaderPNG(t),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	reader := newTestLegacyReader(t, ReaderConfig{
		CacheRoot: cacheRoot,
		AssetDownloads: []catalog.LegacyAssetDownloadSchema{{
			Path:                  "/custom/remove/{job}",
			TokenParameter:        "job",
			FilenameTemplate:      "{job}_download.png",
			MediaType:             "image/png",
			CacheSubdirectory:     "configured-results",
			CacheFilenameTemplate: "result-{job}.png",
		}},
	})

	content, err := reader.OpenGenerationAsset(
		context.Background(),
		administration.GenerationAssetRequest{
			Asset: administration.GenerationAsset{
				SourceURL: "/custom/remove/subtask-a",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(content.Body)
	_ = content.Body.Close()
	if !bytes.Equal(body, validLegacyReaderPNG(t)) || content.MediaType != "image/png" {
		t.Fatalf("legacy download = %+v, body = %q", content, body)
	}
}

func validLegacyReaderPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 1, 1))
	canvas.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestReaderUsesConfiguredReadOnlyLegacyCacheBeforeNetwork(t *testing.T) {
	cacheRoot := t.TempDir()
	sourceBytes := modelinputtest.Triangle(modelinputtest.Options{})
	source := "https://cdn.test/assets/model.glb?signature=rotated"
	hash := sha256.Sum256([]byte("/assets/model.glb"))
	hashText := hex.EncodeToString(hash[:])
	cacheDirectory := filepath.Join(cacheRoot, hashText[:3])
	if err := os.MkdirAll(cacheDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cacheDirectory, hashText+".glb"),
		sourceBytes,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cacheDirectory, hashText+".metadata"),
		[]byte(`{"mime":"model/gltf-binary"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	networkCalls := 0
	reader := newTestLegacyReader(t, ReaderConfig{
		CacheRoot:             cacheRoot,
		CachePrefixCharacters: 3,
		CacheMetadataSuffix:   ".metadata",
		CacheContentTypeField: "mime",
		AllowedHosts:          []string{"cdn.test"},
		HTTPClient: &http.Client{Transport: roundTripFunc(
			func(*http.Request) (*http.Response, error) {
				networkCalls++
				return nil, errors.New("network must not be used")
			},
		)},
	})

	content, err := reader.OpenGenerationAsset(
		context.Background(),
		administration.GenerationAssetRequest{
			Asset: administration.GenerationAsset{SourceURL: source},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(content.Body)
	_ = content.Body.Close()
	if !bytes.Equal(body, sourceBytes) ||
		content.MediaType != "model/gltf-binary" ||
		networkCalls != 0 {
		t.Fatalf(
			"cache body = %q, media = %q, network calls = %d",
			body,
			content.MediaType,
			networkCalls,
		)
	}
}

func TestReaderRejectsExternalAddressInsideHistoricalCachedModel(t *testing.T) {
	cacheRoot := t.TempDir()
	source := "https://cdn.test/assets/model.gltf?signature=expired"
	hash := sha256.Sum256([]byte("/assets/model.gltf"))
	hashText := hex.EncodeToString(hash[:])
	cacheDirectory := filepath.Join(cacheRoot, hashText[:3])
	if err := os.MkdirAll(cacheDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDirectory, hashText+".gltf"),
		[]byte(`{"images":[{"uri":"https://private.example/texture.png?token=secret"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDirectory, hashText+".metadata"),
		[]byte(`{"mime":"model/gltf+json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := newTestLegacyReader(t, ReaderConfig{
		CacheRoot: cacheRoot, CachePrefixCharacters: 3,
		CacheMetadataSuffix: ".metadata", CacheContentTypeField: "mime",
		AllowedHosts: []string{"cdn.test"},
	})
	_, err := reader.OpenGenerationAsset(context.Background(), administration.GenerationAssetRequest{
		Asset: administration.GenerationAsset{Role: "model", Variant: "gltf", SourceURL: source},
	})
	if !errors.Is(err, ErrLegacyAssetRejected) {
		t.Fatalf("historical model was served or failed unclearly: %v", err)
	}
}

func TestReaderNeverFetchesUncachedHistoricalProviderAsset(t *testing.T) {
	requested := []string{}
	client := &http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			requested = append(requested, request.URL.String())
			return nil, errors.New("network must not be used")
		},
	)}
	reader := newTestLegacyReader(t, ReaderConfig{
		AllowedHosts: []string{"cdn.test"},
		HTTPClient:   client,
	})

	for _, source := range []string{
		"https://cdn.test/asset.png",
		"https://evil.test/asset.png",
		"https://cdn.test/redirect",
	} {
		if _, err := reader.OpenGenerationAsset(
			context.Background(),
			administration.GenerationAssetRequest{
				Asset: administration.GenerationAsset{SourceURL: source},
			},
		); err == nil {
			t.Fatalf("expected source %q to be rejected", source)
		}
	}
	if len(requested) != 0 {
		t.Fatalf("requested URLs = %#v", requested)
	}
}

func newTestLegacyReader(
	t *testing.T,
	override ReaderConfig,
) *Reader {
	t.Helper()
	config := ReaderConfig{
		Native:                    unavailableGenerationAssetReader{},
		GeneratedImages:           unavailableGeneratedImageReader{},
		GeneratedImageRoute:       "/generated/{filename}",
		CacheRoot:                 t.TempDir(),
		CachePrefixCharacters:     2,
		CacheMetadataSuffix:       ".meta.json",
		CacheContentTypeField:     "contentType",
		AllowedHosts:              []string{"assets.test"},
		DefaultMediaType:          "application/octet-stream",
		UnknownExtension:          ".bin",
		MaximumBytes:              1024,
		RemoteTimeoutMilliseconds: 1000,
		MaximumRedirects:          2,
		MediaTypeExtensions: map[string]string{
			"image/png":         ".png",
			"image/webp":        ".webp",
			"model/gltf-binary": ".glb",
		},
	}
	mergeReaderConfig(&config, override)
	reader, err := NewReader(config)
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func mergeReaderConfig(target *ReaderConfig, override ReaderConfig) {
	if override.Native != nil {
		target.Native = override.Native
	}
	if override.GeneratedImages != nil {
		target.GeneratedImages = override.GeneratedImages
	}
	if override.GeneratedImageRoute != "" {
		target.GeneratedImageRoute = override.GeneratedImageRoute
	}
	if override.CacheRoot != "" {
		target.CacheRoot = override.CacheRoot
	}
	if override.CachePrefixCharacters != 0 {
		target.CachePrefixCharacters = override.CachePrefixCharacters
	}
	if override.CacheMetadataSuffix != "" {
		target.CacheMetadataSuffix = override.CacheMetadataSuffix
	}
	if override.CacheContentTypeField != "" {
		target.CacheContentTypeField = override.CacheContentTypeField
	}
	if len(override.AllowedHosts) != 0 {
		target.AllowedHosts = override.AllowedHosts
	}
	if override.HTTPClient != nil {
		target.HTTPClient = override.HTTPClient
	}
	if len(override.AssetDownloads) != 0 {
		target.AssetDownloads = override.AssetDownloads
	}
}

type recordingGenerationAssetReader struct {
	request administration.GenerationAssetRequest
	content administration.GenerationAssetContent
}

func (reader *recordingGenerationAssetReader) OpenGenerationAsset(
	_ context.Context,
	request administration.GenerationAssetRequest,
) (administration.GenerationAssetContent, error) {
	reader.request = request
	return reader.content, nil
}

type unavailableGenerationAssetReader struct{}

func (unavailableGenerationAssetReader) OpenGenerationAsset(
	context.Context,
	administration.GenerationAssetRequest,
) (administration.GenerationAssetContent, error) {
	return administration.GenerationAssetContent{}, errors.New("unavailable")
}

type unavailableGeneratedImageReader struct{}

func (unavailableGeneratedImageReader) Open(
	context.Context,
	string,
) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("unavailable")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func response(
	request *http.Request,
	status int,
	contentType string,
	body string,
) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Request:    request,
	}
}
