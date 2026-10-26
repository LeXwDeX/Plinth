package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ListOwnedInputAssets returns page metadata only after applying owner scope in
// SQL. Storage keys remain server-side metadata and are omitted by the HTTP DTO.
func (r *Repository) ListOwnedInputAssets(
	ctx context.Context,
	owner generation.OwnerID,
	page int,
	pageSize int,
) ([]generation.InputAsset, int, error) {
	maxInt := int(^uint(0) >> 1)
	if strings.TrimSpace(string(owner)) == "" || page < 1 || pageSize < 1 ||
		page-1 > maxInt/pageSize {
		return nil, 0, fmt.Errorf("invalid account input-asset query")
	}
	source, err := r.accountInputAssetSource(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := r.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM ("+source+") AS owned_asset WHERE owner_id = ?",
		owner,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count owned input assets: %w", err)
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, owner_id, storage_key, original_filename, asset_type,
			media_type, size_bytes, sha256, created_at
		FROM (`+source+`) AS owned_asset
		WHERE owner_id = ?
		ORDER BY created_unix DESC, id DESC
		LIMIT ? OFFSET ?`,
		owner,
		pageSize,
		(page-1)*pageSize,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list owned input assets: %w", err)
	}
	defer rows.Close()
	assets := make([]generation.InputAsset, 0)
	for rows.Next() {
		var asset generation.InputAsset
		var createdAt string
		if err := rows.Scan(
			&asset.ID,
			&asset.OwnerID,
			&asset.StorageKey,
			&asset.OriginalFilename,
			&asset.AssetType,
			&asset.MediaType,
			&asset.Size,
			&asset.SHA256,
			&createdAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan owned input asset: %w", err)
		}
		asset.CreatedAt, err = parseDatabaseTime(createdAt)
		if err != nil {
			return nil, 0, err
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate owned input assets: %w", err)
	}
	return assets, total, nil
}

func (r *Repository) accountInputAssetSource(
	ctx context.Context,
) (string, error) {
	const native = `
		SELECT id, owner_id, storage_key, original_filename, asset_type,
			media_type, size_bytes, sha256, created_at,
			unixepoch(created_at, 'subsec') AS created_unix
		FROM generation_input_assets`
	exists, err := r.tableExists(ctx, "asset_registry")
	if err != nil {
		return "", fmt.Errorf("check legacy input-asset table: %w", err)
	}
	if !exists {
		return native, nil
	}
	legacy := `
		SELECT CAST(legacy.id AS TEXT) AS id,
			legacy.uploaderUserId AS owner_id,
			legacy.storagePath AS storage_key,
			legacy.originalFilename AS original_filename,
			COALESCE(legacy.assetType, '') AS asset_type,
			COALESCE(legacy.mimeType, '') AS media_type,
			COALESCE(legacy.fileSize, 0) AS size_bytes,
			'' AS sha256,
			CAST(legacy.createdAt AS TEXT) AS created_at,
			` + databaseUnixExpression("legacy.createdAt") + ` AS created_unix
		FROM asset_registry AS legacy
		WHERE NOT EXISTS (
			SELECT 1 FROM generation_input_assets AS native
			WHERE native.id = legacy.id
		)`
	return native + "\nUNION ALL\n" + legacy, nil
}

var _ interface {
	ListOwnedInputAssets(context.Context, generation.OwnerID, int, int) ([]generation.InputAsset, int, error)
} = (*Repository)(nil)
