package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
)

func newInputAssetCropRepository(t *testing.T) (*Repository, *InputAssetCropRepository) {
	t.Helper()
	repository, err := openTestRepository(filepath.Join(t.TempDir(), "input-asset-crops.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository, repository.InputAssetCropRepository()
}

func seedInputAssetForCrops(t *testing.T, repository *Repository, id generation.AssetID, owner generation.OwnerID, assetType, mediaType string) {
	t.Helper()
	at := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	if err := repository.CreateInputAsset(context.Background(), generation.InputAsset{
		ID: id, OwnerID: owner, StorageKey: "key/" + string(id),
		OriginalFilename: string(id), AssetType: assetType, MediaType: mediaType,
		Size: 1, SHA256: "sha-" + string(id), CreatedAt: at,
	}); err != nil {
		t.Fatalf("seed input asset %s: %v", id, err)
	}
}

func sampleCrop() listcrop.Crop {
	return listcrop.Crop{
		AssetID:            "crop_1",
		OwnerID:            "owner-a",
		SourceAssetID:      "source_1",
		SourceKind:         listcrop.SourceKindInput,
		SourceInputAssetID: "source_1",
		SourceWidth:        800,
		SourceHeight:       600,
		Region:             listcrop.Region{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4},
		PixelRect:          listcrop.PixelRect{X0: 80, Y0: 120, X1: 320, Y1: 360},
		SHA256:             "sha-crop-1",
		CreatedAt:          time.Date(2026, 11, 19, 8, 30, 0, 0, time.UTC),
	}
}

func TestInputAssetCropsMigrationCreatesTableAndIndexes(t *testing.T) {
	repository, _ := newInputAssetCropRepository(t)
	var name string
	if err := repository.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'input_asset_crops'`).
		Scan(&name); err != nil || name != "input_asset_crops" {
		t.Fatalf("input_asset_crops table missing: %v", err)
	}
	for _, index := range []string{
		"input_asset_crops_source_rect",
		"input_asset_crops_owner_source_created",
		"input_asset_crops_owner_created",
	} {
		if err := repository.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).
			Scan(&name); err != nil || name != index {
			t.Fatalf("index %s missing: %v", index, err)
		}
	}
	var unique int
	if err := repository.db.QueryRow(
		`SELECT "unique" FROM pragma_index_list('input_asset_crops') WHERE name = 'input_asset_crops_source_rect'`).
		Scan(&unique); err != nil || unique != 1 {
		t.Fatalf("source-rect index is not unique: %v (%d)", err, unique)
	}
}

func TestInputAssetCropRepositoryRoundTripIsOwnerScoped(t *testing.T) {
	repository, crops := newInputAssetCropRepository(t)
	seedInputAssetForCrops(t, repository, "crop_1", "owner-a", listcrop.CropAssetType, "image/png")
	ctx := context.Background()
	crop := sampleCrop()

	stored, inserted, err := crops.InsertOrGet(ctx, crop)
	if err != nil {
		t.Fatalf("insert crop: %v", err)
	}
	if !inserted {
		t.Fatal("first insert reported an existing row")
	}
	if stored.AssetID != crop.AssetID || stored.SourceAssetID != crop.SourceAssetID ||
		stored.SourceKind != crop.SourceKind || stored.SourceInputAssetID != crop.SourceInputAssetID ||
		stored.SourceWidth != crop.SourceWidth || stored.SourceHeight != crop.SourceHeight ||
		stored.Region != crop.Region || stored.PixelRect != crop.PixelRect || stored.SHA256 != crop.SHA256 {
		t.Fatalf("stored crop = %+v; want %+v", stored, crop)
	}
	if !stored.CreatedAt.Equal(crop.CreatedAt) {
		t.Fatalf("stored created_at = %s; want %s", stored.CreatedAt, crop.CreatedAt)
	}

	found, ok, err := crops.Find(ctx, "owner-a", "source_1", crop.PixelRect)
	if err != nil || !ok || found.AssetID != "crop_1" {
		t.Fatalf("find crop = %+v, %v, %v; want crop_1", found, ok, err)
	}
	byAsset, ok, err := crops.Get(ctx, "owner-a", "crop_1")
	if err != nil || !ok || byAsset.PixelRect != crop.PixelRect {
		t.Fatalf("get crop = %+v, %v, %v", byAsset, ok, err)
	}
	loaded, err := crops.Load(ctx, "owner-a", []generation.AssetID{"crop_1", "crop_missing"})
	if err != nil || len(loaded) != 1 || loaded["crop_1"].SHA256 != crop.SHA256 {
		t.Fatalf("load crops = %+v, %v", loaded, err)
	}
	count, err := crops.CountBySource(ctx, "owner-a", "source_1")
	if err != nil || count != 1 {
		t.Fatalf("count = %d, %v; want 1", count, err)
	}
	copied, ok, err := crops.SourceInputCopy(ctx, "owner-a", "source_1")
	if err != nil || !ok || copied != "source_1" {
		t.Fatalf("source copy = %q, %v, %v; want source_1", copied, ok, err)
	}

	// Another owner sees nothing, including by id.
	if _, ok, err := crops.Get(ctx, "owner-b", "crop_1"); err != nil || ok {
		t.Fatalf("foreign get = %v, %v; want not found", ok, err)
	}
	if _, ok, err := crops.Find(ctx, "owner-b", "source_1", crop.PixelRect); err != nil || ok {
		t.Fatalf("foreign find = %v, %v; want not found", ok, err)
	}
	if count, err := crops.CountBySource(ctx, "owner-b", "source_1"); err != nil || count != 0 {
		t.Fatalf("foreign count = %d, %v; want 0", count, err)
	}
	if loaded, err := crops.Load(ctx, "owner-b", []generation.AssetID{"crop_1"}); err != nil || len(loaded) != 0 {
		t.Fatalf("foreign load = %+v, %v; want empty", loaded, err)
	}
	if _, ok, err := crops.SourceInputCopy(ctx, "owner-b", "source_1"); err != nil || ok {
		t.Fatalf("foreign source copy = %v, %v; want not found", ok, err)
	}
}

func TestInputAssetCropRepositoryConflictReturnsExistingRow(t *testing.T) {
	repository, crops := newInputAssetCropRepository(t)
	seedInputAssetForCrops(t, repository, "crop_1", "owner-a", listcrop.CropAssetType, "image/png")
	seedInputAssetForCrops(t, repository, "crop_2", "owner-a", listcrop.CropAssetType, "image/png")
	ctx := context.Background()
	first := sampleCrop()
	if _, inserted, err := crops.InsertOrGet(ctx, first); err != nil || !inserted {
		t.Fatalf("first insert = %v, %v", inserted, err)
	}
	second := first
	second.AssetID = "crop_2"
	second.SHA256 = "sha-crop-2"
	stored, inserted, err := crops.InsertOrGet(ctx, second)
	if err != nil {
		t.Fatalf("conflicting insert: %v", err)
	}
	if inserted || stored.AssetID != "crop_1" || stored.SHA256 != "sha-crop-1" {
		t.Fatalf("conflict returned %+v, inserted=%v; want the first row", stored, inserted)
	}
	count, err := crops.CountBySource(ctx, "owner-a", "source_1")
	if err != nil || count != 1 {
		t.Fatalf("count = %d, %v; want 1", count, err)
	}
}

// #331 review: the crop-count guard and the insert share one write
// transaction, so two batches that both see count = limit-1 cannot both
// insert. The loser gets a LimitError and the source keeps exactly one row.
func TestInputAssetCropRepositoryInsertWithinLimitIsSerialized(t *testing.T) {
	repository, crops := newInputAssetCropRepository(t)
	seedInputAssetForCrops(t, repository, "crop_1", "owner-a", listcrop.CropAssetType, "image/png")
	seedInputAssetForCrops(t, repository, "crop_2", "owner-a", listcrop.CropAssetType, "image/png")
	ctx := context.Background()
	first := sampleCrop()
	second := first
	second.AssetID = "crop_2"
	second.PixelRect = listcrop.PixelRect{X0: 90, Y0: 120, X1: 330, Y1: 360}
	second.SHA256 = "sha-crop-2"

	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, crop := range []listcrop.Crop{first, second} {
		group.Add(1)
		go func(crop listcrop.Crop) {
			defer group.Done()
			<-start
			_, _, err := crops.InsertOrGetWithinLimit(ctx, crop, 1)
			results <- err
		}(crop)
	}
	close(start)
	group.Wait()
	close(results)
	inserted, limited := 0, 0
	for err := range results {
		var limit *listcrop.LimitError
		switch {
		case err == nil:
			inserted++
		case errors.As(err, &limit):
			limited++
		default:
			t.Fatalf("concurrent insert error = %v", err)
		}
	}
	if inserted != 1 || limited != 1 {
		t.Fatalf("inserted=%d limited=%d; want exactly one insert and one LimitError", inserted, limited)
	}
	count, err := crops.CountBySource(ctx, "owner-a", "source_1")
	if err != nil || count != 1 {
		t.Fatalf("count = %d, %v; want 1", count, err)
	}
}

// #331 review: a repeated rectangle still resolves at the limit, because the
// existing row is returned before the count is checked.
func TestInputAssetCropRepositoryInsertWithinLimitKeepsExistingRect(t *testing.T) {
	repository, crops := newInputAssetCropRepository(t)
	seedInputAssetForCrops(t, repository, "crop_1", "owner-a", listcrop.CropAssetType, "image/png")
	seedInputAssetForCrops(t, repository, "crop_2", "owner-a", listcrop.CropAssetType, "image/png")
	ctx := context.Background()
	crop := sampleCrop()
	if _, inserted, err := crops.InsertOrGetWithinLimit(ctx, crop, 1); err != nil || !inserted {
		t.Fatalf("first insert = %v, %v", inserted, err)
	}
	repeated := crop
	repeated.AssetID = "crop_2"
	stored, inserted, err := crops.InsertOrGetWithinLimit(ctx, repeated, 1)
	if err != nil {
		t.Fatalf("repeated insert at the limit: %v", err)
	}
	if inserted || stored.AssetID != "crop_1" {
		t.Fatalf("repeated insert = %+v, inserted=%v; want the existing row", stored, inserted)
	}
}

func TestInputAssetCropsCascadeOnlyFromTheCropAsset(t *testing.T) {
	repository, crops := newInputAssetCropRepository(t)
	ctx := context.Background()
	seedInputAssetForCrops(t, repository, "source_1", "owner-a", "image", "image/png")
	seedInputAssetForCrops(t, repository, "crop_1", "owner-a", listcrop.CropAssetType, "image/png")
	if _, inserted, err := crops.InsertOrGet(ctx, sampleCrop()); err != nil || !inserted {
		t.Fatalf("insert crop: %v, %v", inserted, err)
	}

	// Deleting the source keeps the provenance: there is deliberately no FK.
	if _, err := repository.db.ExecContext(ctx,
		`DELETE FROM generation_input_assets WHERE id = 'source_1'`); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	if count, err := crops.CountBySource(ctx, "owner-a", "source_1"); err != nil || count != 1 {
		t.Fatalf("count after source delete = %d, %v; want 1", count, err)
	}

	// Deleting the crop asset removes its provenance through ON DELETE CASCADE.
	if _, err := repository.db.ExecContext(ctx,
		`DELETE FROM generation_input_assets WHERE id = 'crop_1'`); err != nil {
		t.Fatalf("delete crop asset: %v", err)
	}
	if count, err := crops.CountBySource(ctx, "owner-a", "source_1"); err != nil || count != 0 {
		t.Fatalf("count after crop delete = %d, %v; want 0", count, err)
	}
}

func TestCreateInputAssetLeavesDerivationColumnsNullForCrops(t *testing.T) {
	repository, _ := newInputAssetCropRepository(t)
	seedInputAssetForCrops(t, repository, "crop_1", "owner-a", listcrop.CropAssetType, "image/png")
	var source, method sql.NullString
	var size sql.NullInt64
	if err := repository.db.QueryRow(`
		SELECT derived_from_asset_id, derivation_method, source_size_bytes
		FROM generation_input_assets WHERE id = 'crop_1'`).
		Scan(&source, &method, &size); err != nil {
		t.Fatalf("read derivation columns: %v", err)
	}
	if source.Valid || method.Valid || size.Valid {
		t.Fatalf("derivation columns = %v, %v, %v; a crop keeps all three NULL (#276)",
			source, method, size)
	}
}

func TestInputAssetCropRepositoryLoadEmptyIsEmpty(t *testing.T) {
	_, crops := newInputAssetCropRepository(t)
	loaded, err := crops.Load(context.Background(), "owner-a", nil)
	if err != nil || len(loaded) != 0 {
		t.Fatalf("empty load = %+v, %v; want empty", loaded, err)
	}
}
