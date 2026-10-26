package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (r *Repository) GetLegacyInputAsset(
	ctx context.Context,
	id generation.AssetID,
) (generation.InputAsset, error) {
	exists, err := r.tableExists(ctx, "asset_registry")
	if err != nil {
		return generation.InputAsset{}, err
	}
	if !exists {
		return generation.InputAsset{}, legacyInputAssetNotFound()
	}
	var asset generation.InputAsset
	var createdAt string
	err = r.db.QueryRowContext(ctx, `
		SELECT id, uploaderUserId, storagePath, originalFilename,
		       assetType, mimeType, fileSize, storedSha256, createdAt
		FROM asset_registry
		WHERE id = ?`,
		id,
	).Scan(
		&asset.ID,
		&asset.OwnerID,
		&asset.StorageKey,
		&asset.OriginalFilename,
		&asset.AssetType,
		&asset.MediaType,
		&asset.Size,
		&asset.SHA256,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return generation.InputAsset{}, legacyInputAssetNotFound()
	}
	if err != nil {
		return generation.InputAsset{}, err
	}
	asset.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return generation.InputAsset{}, err
	}
	return asset, nil
}

func legacyInputAssetNotFound() error {
	return &generation.Error{
		Code:      generation.ErrNotFound,
		Operation: "get_legacy_input_asset",
	}
}
