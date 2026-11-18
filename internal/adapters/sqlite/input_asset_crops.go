package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
)

// InputAssetCropRepository is the owner-scoped SQLite provenance store of the
// read-only crop_image_regions service (#331). Every method filters by owner;
// a missing or foreign row is reported as not found so transports can refuse
// without leaking existence.
type InputAssetCropRepository struct {
	db *sql.DB
}

// InputAssetCropRepository returns the crop provenance store.
func (r *Repository) InputAssetCropRepository() *InputAssetCropRepository {
	return &InputAssetCropRepository{db: r.db}
}

var _ listcrop.CropStore = (*InputAssetCropRepository)(nil)

const inputAssetCropColumns = `crop_asset_id, owner_id, source_asset_id,
	source_kind, source_input_asset_id, source_width, source_height,
	region_json, x0, y0, x1, y1, width, height, sha256, created_at`

// InsertOrGet stores one crop. When the unique
// (owner_id, source_asset_id, x0, y0, x1, y1) row already exists it returns
// that row with inserted=false, so parallel calls share one asset.
func (r *InputAssetCropRepository) InsertOrGet(
	ctx context.Context,
	crop listcrop.Crop,
) (listcrop.Crop, bool, error) {
	region, err := json.Marshal(crop.Region)
	if err != nil {
		return listcrop.Crop{}, false, fmt.Errorf("encode crop region: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO input_asset_crops (
			crop_asset_id, owner_id, source_asset_id, source_kind,
			source_input_asset_id, source_width, source_height, region_json,
			x0, y0, x1, y1, width, height, sha256, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_id, source_asset_id, x0, y0, x1, y1) DO NOTHING`,
		crop.AssetID,
		crop.OwnerID,
		crop.SourceAssetID,
		crop.SourceKind,
		crop.SourceInputAssetID,
		crop.SourceWidth,
		crop.SourceHeight,
		string(region),
		crop.PixelRect.X0,
		crop.PixelRect.Y0,
		crop.PixelRect.X1,
		crop.PixelRect.Y1,
		crop.PixelRect.Width(),
		crop.PixelRect.Height(),
		crop.SHA256,
		formatTaskTime(crop.CreatedAt),
	)
	if err != nil {
		return listcrop.Crop{}, false, fmt.Errorf("insert input asset crop: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return listcrop.Crop{}, false, fmt.Errorf("insert input asset crop: %w", err)
	}
	if affected > 0 {
		return crop, true, nil
	}
	existing, found, err := r.Find(ctx, crop.OwnerID, crop.SourceAssetID, crop.PixelRect)
	if err != nil {
		return listcrop.Crop{}, false, err
	}
	if !found {
		return listcrop.Crop{}, false, errors.New("sqlite: crop conflict without an existing row")
	}
	return existing, false, nil
}

// Find returns the stored crop of one source rectangle.
func (r *InputAssetCropRepository) Find(
	ctx context.Context,
	owner generation.OwnerID,
	sourceID generation.AssetID,
	rect listcrop.PixelRect,
) (listcrop.Crop, bool, error) {
	return r.queryOne(ctx, `
		SELECT `+inputAssetCropColumns+` FROM input_asset_crops
		WHERE owner_id = ? AND source_asset_id = ?
		  AND x0 = ? AND y0 = ? AND x1 = ? AND y1 = ?`,
		owner, sourceID, rect.X0, rect.Y0, rect.X1, rect.Y1)
}

// Get returns one crop by its crop asset id.
func (r *InputAssetCropRepository) Get(
	ctx context.Context,
	owner generation.OwnerID,
	cropAssetID generation.AssetID,
) (listcrop.Crop, bool, error) {
	return r.queryOne(ctx, `
		SELECT `+inputAssetCropColumns+` FROM input_asset_crops
		WHERE owner_id = ? AND crop_asset_id = ?`,
		owner, cropAssetID)
}

// Load returns the crops of the given crop asset ids, owner-scoped. It bounds
// the lookup below SQLite's default bound-parameter limit like
// LoadInputAssets.
func (r *InputAssetCropRepository) Load(
	ctx context.Context,
	owner generation.OwnerID,
	cropAssetIDs []generation.AssetID,
) (map[generation.AssetID]listcrop.Crop, error) {
	if len(cropAssetIDs) > maxInputAssetLookup {
		return nil, fmt.Errorf("sqlite: %d crops in one lookup exceed %d", len(cropAssetIDs), maxInputAssetLookup)
	}
	result := make(map[generation.AssetID]listcrop.Crop, len(cropAssetIDs))
	if len(cropAssetIDs) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(cropAssetIDs))
	args := make([]any, 0, len(cropAssetIDs)+1)
	args = append(args, owner)
	for index, id := range cropAssetIDs {
		placeholders[index] = "?"
		args = append(args, id)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+inputAssetCropColumns+` FROM input_asset_crops
		WHERE owner_id = ? AND crop_asset_id IN (`+strings.Join(placeholders, ",")+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("load input asset crops: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		crop, err := scanInputAssetCrop(rows)
		if err != nil {
			return nil, err
		}
		result[crop.AssetID] = crop
	}
	return result, rows.Err()
}

// CountBySource counts every crop of one source, across calls.
func (r *InputAssetCropRepository) CountBySource(
	ctx context.Context,
	owner generation.OwnerID,
	sourceID generation.AssetID,
) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM input_asset_crops
		WHERE owner_id = ? AND source_asset_id = ?`,
		owner, sourceID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count input asset crops: %w", err)
	}
	return count, nil
}

// SourceInputCopy returns the input asset an earlier crop copied a Task source
// into. A source with no crop yet has no copy. The newest row wins so a copy
// made after an earlier one disappeared is the one reused.
func (r *InputAssetCropRepository) SourceInputCopy(
	ctx context.Context,
	owner generation.OwnerID,
	sourceID generation.AssetID,
) (generation.AssetID, bool, error) {
	var copied generation.AssetID
	err := r.db.QueryRowContext(ctx, `
		SELECT source_input_asset_id FROM input_asset_crops
		WHERE owner_id = ? AND source_asset_id = ?
		ORDER BY created_at DESC, crop_asset_id DESC
		LIMIT 1`,
		owner, sourceID,
	).Scan(&copied)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read crop source copy: %w", err)
	}
	return copied, true, nil
}

func (r *InputAssetCropRepository) queryOne(
	ctx context.Context,
	query string,
	args ...any,
) (listcrop.Crop, bool, error) {
	crop, err := scanInputAssetCrop(r.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return listcrop.Crop{}, false, nil
	}
	if err != nil {
		return listcrop.Crop{}, false, err
	}
	return crop, true, nil
}

type cropScanner interface {
	Scan(...any) error
}

func scanInputAssetCrop(scanner cropScanner) (listcrop.Crop, error) {
	var crop listcrop.Crop
	var regionJSON, createdAt string
	// width/height are the stored denormalized copy of the pixel rect; the
	// rect columns are authoritative on read.
	var width, height int
	if err := scanner.Scan(
		&crop.AssetID,
		&crop.OwnerID,
		&crop.SourceAssetID,
		&crop.SourceKind,
		&crop.SourceInputAssetID,
		&crop.SourceWidth,
		&crop.SourceHeight,
		&regionJSON,
		&crop.PixelRect.X0,
		&crop.PixelRect.Y0,
		&crop.PixelRect.X1,
		&crop.PixelRect.Y1,
		&width,
		&height,
		&crop.SHA256,
		&createdAt,
	); err != nil {
		return listcrop.Crop{}, err
	}
	if err := json.Unmarshal([]byte(regionJSON), &crop.Region); err != nil {
		return listcrop.Crop{}, fmt.Errorf("decode crop region: %w", err)
	}
	parsed, err := parseTime(createdAt)
	if err != nil {
		return listcrop.Crop{}, err
	}
	crop.CreatedAt = parsed
	return crop, nil
}