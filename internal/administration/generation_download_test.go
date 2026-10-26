package administration

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestGenerationDownloadServiceReturnsOneNativeAssetDirectly(
	t *testing.T,
) {
	details := &memoryGenerationDetailReader{
		found: true,
		detail: GenerationDetail{Ledger: GenerationRecord{
			UserID: "user-1",
			TaskID: "task-1",
			Assets: []GenerationAsset{{
				ID:                "asset-1",
				Role:              "image",
				Variant:           "preview",
				MediaType:         "image/png",
				Ordinal:           0,
				PreferredFilename: "../../historical image.png",
			}},
		}},
	}
	assets := &memoryGenerationAssetReader{
		contents: map[string]GenerationAssetContent{
			"asset-1": {
				Body:      io.NopCloser(bytes.NewBufferString("png")),
				MediaType: "image/png",
				Size:      3,
			},
		},
	}
	service := newTestGenerationDownloadService(t, details, assets, 1024)

	pkg, err := service.Download(context.Background(), " task-1 ")
	if err != nil {
		t.Fatal(err)
	}
	if details.taskID != "task-1" {
		t.Fatalf("task id = %q", details.taskID)
	}
	if assets.ownerID != "user-1" ||
		assets.taskID != "task-1" ||
		assets.assetIDs[0] != "asset-1" {
		t.Fatalf("asset request = %+v", assets)
	}
	if assets.requests[0].Asset.PreferredFilename !=
		"../../historical image.png" {
		t.Fatalf("asset descriptor = %+v", assets.requests[0].Asset)
	}
	if pkg.Filename != "historical_image.png" ||
		pkg.MediaType != "image/png" ||
		string(pkg.Body) != "png" {
		t.Fatalf("package = %+v", pkg)
	}
}

func TestGenerationDownloadServicePackagesMultipleAssetsAndDeduplicatesNames(
	t *testing.T,
) {
	details := &memoryGenerationDetailReader{
		found: true,
		detail: GenerationDetail{Ledger: GenerationRecord{
			UserID: "user-2",
			TaskID: "task-2",
			Assets: []GenerationAsset{
				{ID: "one", Role: "model", Ordinal: 0},
				{ID: "two", Role: "model", Ordinal: 0},
				{ID: "failed", Role: "texture", Ordinal: 1},
			},
		}},
	}
	assets := &memoryGenerationAssetReader{
		contents: map[string]GenerationAssetContent{
			"one": {
				Body:      io.NopCloser(bytes.NewBufferString("one")),
				MediaType: "model/gltf-binary",
				Size:      3,
			},
			"two": {
				Body:      io.NopCloser(bytes.NewBufferString("two")),
				MediaType: "model/gltf-binary",
				Size:      3,
			},
		},
		errors: map[string]error{"failed": errors.New("unavailable")},
	}
	service := newTestGenerationDownloadService(t, details, assets, 1024)

	pkg, err := service.Download(context.Background(), "task-2")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Filename != "task-2_assets.zip" ||
		pkg.MediaType != "application/zip" {
		t.Fatalf("package metadata = %+v", pkg)
	}
	reader, err := zip.NewReader(
		bytes.NewReader(pkg.Body),
		int64(len(pkg.Body)),
	)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, file := range reader.File {
		body, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[file.Name] = string(content)
	}
	want := map[string]string{
		"model_1.glb":   "one",
		"model_1_1.glb": "two",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zip files = %#v, want %#v", got, want)
	}
}

func TestGenerationDownloadServiceClassifiesFailures(t *testing.T) {
	tests := []struct {
		name    string
		reader  *memoryGenerationDetailReader
		assets  *memoryGenerationAssetReader
		maximum int64
		want    error
	}{
		{
			name:    "missing task",
			reader:  &memoryGenerationDetailReader{},
			assets:  &memoryGenerationAssetReader{},
			maximum: 1024,
			want:    ErrGenerationDownloadNotFound,
		},
		{
			name: "no assets",
			reader: &memoryGenerationDetailReader{
				found:  true,
				detail: GenerationDetail{Ledger: GenerationRecord{}},
			},
			assets:  &memoryGenerationAssetReader{},
			maximum: 1024,
			want:    ErrGenerationDownloadNoAssets,
		},
		{
			name: "all assets failed",
			reader: &memoryGenerationDetailReader{
				found: true,
				detail: GenerationDetail{Ledger: GenerationRecord{
					TaskID: "task",
					Assets: []GenerationAsset{{ID: "asset", Role: "image"}},
				}},
			},
			assets: &memoryGenerationAssetReader{
				errors: map[string]error{"asset": errors.New("failed")},
			},
			maximum: 1024,
			want:    ErrGenerationDownloadFailed,
		},
		{
			name: "package too large",
			reader: &memoryGenerationDetailReader{
				found: true,
				detail: GenerationDetail{Ledger: GenerationRecord{
					TaskID: "task",
					Assets: []GenerationAsset{{ID: "asset", Role: "image"}},
				}},
			},
			assets: &memoryGenerationAssetReader{
				contents: map[string]GenerationAssetContent{
					"asset": {
						Body: io.NopCloser(
							bytes.NewBufferString("oversized"),
						),
						Size: 9,
					},
				},
			},
			maximum: 4,
			want:    ErrGenerationDownloadTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newTestGenerationDownloadService(
				t,
				test.reader,
				test.assets,
				test.maximum,
			)
			_, err := service.Download(context.Background(), "task")
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func newTestGenerationDownloadService(
	t *testing.T,
	details GenerationDetailReader,
	assets GenerationAssetReader,
	maximumPackageBytes int64,
) *GenerationDownloadService {
	t.Helper()
	service, err := NewGenerationDownloadService(
		GenerationDownloadServiceConfig{
			Details:                  details,
			Assets:                   assets,
			DownloadFilenameTemplate: "{task_id}_assets.zip",
			AssetFilenameTemplate:    "{role}{variant_segment}_{ordinal}{extension}",
			VariantSeparator:         "_",
			DuplicateSeparator:       "_",
			OrdinalBase:              1,
			ArchiveMediaType:         "application/zip",
			DefaultMediaType:         "application/octet-stream",
			UnknownExtension:         ".bin",
			MaximumPackageBytes:      maximumPackageBytes,
			MediaTypeExtensions: map[string]string{
				"image/png":         ".png",
				"model/gltf-binary": ".glb",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type memoryGenerationDetailReader struct {
	detail GenerationDetail
	found  bool
	taskID string
}

func (reader *memoryGenerationDetailReader) GetGeneration(
	_ context.Context,
	taskID string,
) (GenerationDetail, bool, error) {
	reader.taskID = taskID
	return reader.detail, reader.found, nil
}

type memoryGenerationAssetReader struct {
	contents map[string]GenerationAssetContent
	errors   map[string]error
	ownerID  string
	taskID   string
	assetIDs []string
	requests []GenerationAssetRequest
}

func (reader *memoryGenerationAssetReader) OpenGenerationAsset(
	_ context.Context,
	request GenerationAssetRequest,
) (GenerationAssetContent, error) {
	reader.ownerID = request.OwnerID
	reader.taskID = request.TaskID
	reader.assetIDs = append(reader.assetIDs, request.Asset.ID)
	reader.requests = append(reader.requests, request)
	if err := reader.errors[request.Asset.ID]; err != nil {
		return GenerationAssetContent{}, err
	}
	return reader.contents[request.Asset.ID], nil
}
