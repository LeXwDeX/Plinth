package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
)

// #331 review: a Task source is copied into an input asset before it is
// cropped, and the attachment resolver must name that copy. Naming the Task
// output id would leave source_scene unopenable.
func TestCropProvenanceNamesTheCopiedTaskSource(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "crop-provenance.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	ctx := context.Background()
	at := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	if err := repository.CreateInputAsset(ctx, generation.InputAsset{
		ID: "crop_1", OwnerID: "owner-a", StorageKey: "key/crop_1",
		OriginalFilename: "crop.png", AssetType: listcrop.CropAssetType,
		MediaType: "image/png", Size: 1, SHA256: "sha-crop-1", CreatedAt: at,
	}); err != nil {
		t.Fatalf("seed crop asset: %v", err)
	}
	crops := repository.InputAssetCropRepository()
	if _, _, err := crops.InsertOrGet(ctx, listcrop.Crop{
		AssetID:            "crop_1",
		OwnerID:            "owner-a",
		SourceAssetID:      "task_asset_1",
		SourceKind:         listcrop.SourceKindTask,
		SourceInputAssetID: "copied_input_1",
		SourceWidth:        800,
		SourceHeight:       600,
		Region:             listcrop.Region{X: 0, Y: 0, Width: 0.5, Height: 0.5},
		PixelRect:          listcrop.PixelRect{X0: 0, Y0: 0, X1: 400, Y1: 300},
		SHA256:             "sha-crop-1",
		CreatedAt:          at,
	}); err != nil {
		t.Fatalf("seed crop provenance: %v", err)
	}
	port := cropProvenance{crops: crops}
	source, found, err := port.CropSource(ctx, generation.OwnerID("owner-a"),
		generation.AssetID("crop_1"))
	if err != nil || !found {
		t.Fatalf("crop source found=%v err=%v", found, err)
	}
	if source != "copied_input_1" {
		t.Fatalf("crop source = %q; want the copied input asset", source)
	}
	if _, found, err := port.CropSource(ctx, generation.OwnerID("owner-b"),
		generation.AssetID("crop_1")); err != nil || found {
		t.Fatalf("foreign crop source found=%v err=%v; want not found", found, err)
	}
}

// #331 review: a custom Catalog without application.chat.image_crop must not
// fail startup; the crop tool is simply left unassembled and the executor
// answers its calls with the unavailable result.
func TestApplicationWithoutImageCropSectionSkipsTheCropTool(t *testing.T) {
	root := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("..", "catalog", "default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	application := document["application"].(map[string]any)
	chat := application["chat"].(map[string]any)
	delete(chat, "image_crop")
	localization := application["localization"].(map[string]any)
	coverage := localization["coverage"].([]any)
	filtered := make([]any, 0, len(coverage))
	for _, entry := range coverage {
		if text, _ := entry.(string); text != "chat.image_crop" {
			filtered = append(filtered, entry)
		}
	}
	localization["coverage"] = filtered
	// The English bundle translates the deleted section field by field; those
	// paths would no longer resolve.
	bundles := localization["bundles"].(map[string]any)
	for _, bundle := range bundles {
		fields, _ := bundle.(map[string]any)["fields"].(map[string]any)
		for path := range fields {
			if strings.HasPrefix(path, "chat.image_crop.") {
				delete(fields, path)
			}
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	catalogFile := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(catalogFile, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := New(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		CatalogFile:               catalogFile,
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		RuntimeSettings:           map[string]string{"llm_model": "configured-test-model"},
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("startup without application.chat.image_crop = %v", err)
	}
	if value == nil || value.generation == nil {
		t.Fatal("application without application.chat.image_crop was not assembled")
	}
	_ = value.generation.Close()
}
