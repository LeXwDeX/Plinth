package lineage

import (
	"context"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Native lookup keeps an unrelated Task output projected by LoadInputAssets
// from being presented as a person's upload. It reads metadata only: lineage
// describes stored input facts, never advances or repairs a public Task.
type nativeInputAssets interface {
	GetInputAsset(context.Context, generation.AssetID) (generation.InputAsset, error)
}

func (s *Service) loadNativeModelInputs(ctx context.Context, owner string, refs []reference) (map[generation.AssetID]generation.InputAsset, error) {
	result := map[generation.AssetID]generation.InputAsset{}
	native, ok := s.inputs.(nativeInputAssets)
	if !ok {
		return result, nil
	}
	seen := map[generation.AssetID]bool{}
	for _, ref := range refs {
		if !ref.uploadedModel || ref.assetID == "" {
			continue
		}
		id := generation.AssetID(ref.assetID)
		if seen[id] {
			continue
		}
		seen[id] = true
		asset, err := native.GetInputAsset(ctx, id)
		if generation.IsCode(err, generation.ErrNotFound) || errors.Is(err, generation.RepositoryNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if asset.ID == id && string(asset.OwnerID) == owner && asset.SHA256 != "" && asset.Size > 0 {
			result[id] = asset
		}
	}
	return result, nil
}
