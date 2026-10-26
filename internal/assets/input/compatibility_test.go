package input_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/legacyinput"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type missingNativeInputAssets struct{}

type compatibleLegacyInputAssets struct {
	asset generation.InputAsset
}

func (r compatibleLegacyInputAssets) GetLegacyInputAsset(
	context.Context,
	generation.AssetID,
) (generation.InputAsset, error) {
	return r.asset, nil
}

func (missingNativeInputAssets) CreateInputAsset(
	context.Context,
	generation.InputAsset,
) error {
	return nil
}

func (missingNativeInputAssets) GetInputAsset(
	context.Context,
	generation.AssetID,
) (generation.InputAsset, error) {
	return generation.InputAsset{}, &generation.Error{
		Code: generation.ErrNotFound,
	}
}

func (missingNativeInputAssets) LoadInputAssets(
	context.Context,
	generation.OwnerID,
	[]generation.AssetID,
) (map[generation.AssetID]generation.InputAsset, error) {
	return map[generation.AssetID]generation.InputAsset{}, nil
}

func TestCompatibleInputAssetsFallBackToLegacyOnlyWhenNativeAssetIsMissing(
	t *testing.T,
) {
	nativeBlobs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	native, err := input.NewWithConfig(
		missingNativeInputAssets{},
		nativeBlobs,
		input.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	legacyRoot := t.TempDir()
	const storageKey = "image/ab/cd/legacy.png"
	absolutePath := filepath.Join(legacyRoot, filepath.FromSlash(storageKey))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte("legacy-body"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := legacyinput.New(
		legacyinput.Config{Root: legacyRoot},
		compatibleLegacyInputAssets{asset: generation.InputAsset{
			ID:         "legacy-asset",
			OwnerID:    "owner-1",
			StorageKey: storageKey,
			MediaType:  "image/png",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	compatible, err := input.NewCompatible(native, legacy)
	if err != nil {
		t.Fatal(err)
	}
	asset, body, err := compatible.OpenAuthorized(
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
	if asset.ID != "legacy-asset" || string(content) != "legacy-body" {
		t.Fatalf("asset = %#v, content = %q", asset, content)
	}
}
