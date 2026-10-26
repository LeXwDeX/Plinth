package legacyinput_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/legacyinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type legacyInputAssetRepository struct {
	asset generation.InputAsset
	err   error
}

func (r legacyInputAssetRepository) GetLegacyInputAsset(
	context.Context,
	generation.AssetID,
) (generation.InputAsset, error) {
	return r.asset, r.err
}

func TestOwnerCanOpenLegacyInputAssetThroughOneModuleInterface(t *testing.T) {
	root := t.TempDir()
	storageKey := filepath.Join("image", "ab", "cd", "legacy.png")
	absolutePath := filepath.Join(root, storageKey)
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	const expectedBody = "legacy input asset bytes"
	if err := os.WriteFile(absolutePath, []byte(expectedBody), 0o600); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 26, 8, 30, 0, 0, time.UTC)
	module, err := legacyinput.New(
		legacyinput.Config{Root: root},
		legacyInputAssetRepository{asset: generation.InputAsset{
			ID:               "legacy-asset",
			OwnerID:          "owner-1",
			StorageKey:       storageKey,
			OriginalFilename: "source.png",
			AssetType:        "image",
			MediaType:        "image/png",
			Size:             999,
			SHA256:           "stored-sha256",
			CreatedAt:        createdAt,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	asset, body, err := module.OpenAuthorized(
		context.Background(),
		"owner-1",
		"legacy-asset",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != expectedBody {
		t.Fatalf("content = %q", content)
	}
	if asset.ID != "legacy-asset" ||
		asset.OwnerID != "owner-1" ||
		asset.OriginalFilename != "source.png" ||
		asset.MediaType != "image/png" ||
		asset.Size != int64(len(expectedBody)) ||
		!asset.CreatedAt.Equal(createdAt) {
		t.Fatalf("asset = %#v", asset)
	}
}
