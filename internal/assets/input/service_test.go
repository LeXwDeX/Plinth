package input_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestUploadResolveOwnershipAndOpen(t *testing.T) {
	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	store, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := inputassets.New(repository, store)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("\x89PNG\r\n\x1a\ninput")
	uploaded, err := service.Upload(
		context.Background(),
		"owner-a",
		bytes.NewReader(content),
		"image/png",
		1<<20,
	)
	if err != nil {
		t.Fatal(err)
	}
	offering := generation.Offering{Parameters: []generation.Parameter{{
		Name: "reference_assets",
		Type: "asset-array",
	}}}
	resolved, err := service.Resolve(
		context.Background(),
		"owner-a",
		offering,
		[]byte(`{"reference_assets":["`+string(uploaded.ID)+`"]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved["reference_assets"]) != 1 ||
		resolved["reference_assets"][0].StorageKey == "" {
		t.Fatalf("resolved = %#v", resolved)
	}
	body, err := service.Open(context.Background(), resolved["reference_assets"][0])
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	actual, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, content) {
		t.Fatalf("actual = %q", actual)
	}

	_, err = service.Resolve(
		context.Background(),
		"owner-b",
		offering,
		[]byte(`{"reference_assets":["`+string(uploaded.ID)+`"]}`),
	)
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) || domainErr.Code != generation.ErrInvalidInput {
		t.Fatalf("error = %#v", err)
	}
}

func TestResolveEnforcesOfferingExtensionSchema(t *testing.T) {
	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	store, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := inputassets.New(repository, store)
	if err != nil {
		t.Fatal(err)
	}
	upload := func(filename string) generation.InputAsset {
		t.Helper()
		asset, uploadErr := service.UploadAsset(
			context.Background(),
			"owner-a",
			generation.InputAssetUpload{
				Body:             strings.NewReader("model"),
				OriginalFilename: filename,
				AssetType:        "model",
				MediaType:        "application/octet-stream",
				MaxBytes:         1024,
			},
		)
		if uploadErr != nil {
			t.Fatal(uploadErr)
		}
		return asset
	}
	allowed := upload("MODEL.GLB")
	rejected := upload("archive.zip")
	offering := generation.Offering{Parameters: []generation.Parameter{{
		Name:       "source_asset",
		Type:       "asset",
		Extensions: []string{".glb", ".obj"},
	}}}
	if _, err := service.Resolve(
		context.Background(),
		"owner-a",
		offering,
		[]byte(`{"source_asset":"`+string(allowed.ID)+`"}`),
	); err != nil {
		t.Fatalf("configured extension rejected: %v", err)
	}
	_, err = service.Resolve(
		context.Background(),
		"owner-a",
		offering,
		[]byte(`{"source_asset":"`+string(rejected.ID)+`"}`),
	)
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) ||
		domainErr.Code != generation.ErrInvalidInput {
		t.Fatalf("unsupported extension error = %#v", err)
	}
}

func TestResolveEnforcesOfferingAssetLimitsForMasks(t *testing.T) {
	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	store, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := inputassets.New(repository, store)
	if err != nil {
		t.Fatal(err)
	}
	upload := func(filename string, content []byte) generation.InputAsset {
		t.Helper()
		asset, uploadErr := service.UploadAsset(
			context.Background(),
			"owner-a",
			generation.InputAssetUpload{
				Body:             bytes.NewReader(content),
				OriginalFilename: filename,
				AssetType:        "image",
				MediaType:        "image/png",
				MaxBytes:         1 << 20,
			},
		)
		if uploadErr != nil {
			t.Fatal(uploadErr)
		}
		return asset
	}
	maskOffering := generation.Offering{Parameters: []generation.Parameter{{
		Name:       "mask",
		Type:       "asset",
		MaxBytes:   4,
		MediaTypes: []string{"image/png"},
		Extensions: []string{"png"},
	}}}
	oversized := upload("mask.png", []byte("12345"))
	if _, err := service.Resolve(
		context.Background(),
		"owner-a",
		maskOffering,
		[]byte(`{"mask":"`+string(oversized.ID)+`"}`),
	); !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("oversized mask error = %#v", err)
	}
	withinLimit := upload("mask.png", []byte("1234"))
	if _, err := service.Resolve(
		context.Background(),
		"owner-a",
		maskOffering,
		[]byte(`{"mask":"`+string(withinLimit.ID)+`"}`),
	); err != nil {
		t.Fatalf("valid mask rejected: %v", err)
	}
	jpegOffering := generation.Offering{Parameters: []generation.Parameter{{
		Name:       "mask",
		Type:       "asset",
		MaxBytes:   4,
		MediaTypes: []string{"image/jpeg"},
	}}}
	if _, err := service.Resolve(
		context.Background(),
		"owner-a",
		jpegOffering,
		[]byte(`{"mask":"`+string(withinLimit.ID)+`"}`),
	); !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("media type error = %#v", err)
	}
}

func TestSignedProviderURLAuthenticatesAndExpires(t *testing.T) {
	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	store, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	service, err := inputassets.NewWithConfig(
		repository,
		store,
		inputassets.Config{
			PublicBaseURL:      "https://art-ai.test",
			SigningKey:         strings.Repeat("k", 32),
			ProviderAssetRoute: "/configured/provider-assets/{token}/content",
			Now:                func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("provider-readable-model")
	uploaded, err := service.Upload(
		context.Background(),
		"owner-a",
		bytes.NewReader(content),
		"model/gltf-binary",
		1<<20,
	)
	if err != nil {
		t.Fatal(err)
	}
	externalURL, err := service.ExternalURL(
		context.Background(),
		uploaded,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(externalURL)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSuffix(
		strings.TrimPrefix(
			parsed.Path,
			"/configured/provider-assets/",
		),
		"/content",
	)
	asset, body, err := service.OpenSigned(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(body)
	_ = body.Close()
	if asset.ID != uploaded.ID || !bytes.Equal(payload, content) {
		t.Fatalf("asset = %#v, payload = %q", asset, payload)
	}
	if _, _, err := service.OpenSigned(context.Background(), token+"x"); err == nil {
		t.Fatal("tampered token should be rejected")
	}
	now = now.Add(2 * time.Minute)
	if _, _, err := service.OpenSigned(context.Background(), token); err == nil {
		t.Fatal("expired token should be rejected")
	}
}
