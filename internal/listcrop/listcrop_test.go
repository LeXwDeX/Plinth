package listcrop_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
)

// fakeInputs is an in-memory input-asset store. It mirrors the SQLite
// LoadInputAssets bridge: a finished Task output is resolvable by id but only
// OpenOwned reaches a real input asset.
type fakeInputs struct {
	mu         sync.Mutex
	assets     map[generation.AssetID]generation.InputAsset
	blobs      map[generation.AssetID][]byte
	uploaded   []generation.InputAsset
	nextUpload int
}

func newFakeInputs() *fakeInputs {
	return &fakeInputs{
		assets: make(map[generation.AssetID]generation.InputAsset),
		blobs:  make(map[generation.AssetID][]byte),
	}
}

func (inputs *fakeInputs) addInputAsset(owner generation.OwnerID, id generation.AssetID, assetType, mediaType string, body []byte) {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	inputs.assets[id] = generation.InputAsset{
		ID: id, OwnerID: owner, StorageKey: "key/" + string(id),
		AssetType: assetType, MediaType: mediaType,
		Size: int64(len(body)), SHA256: sha256Hex(body), CreatedAt: time.Now().UTC(),
	}
	inputs.blobs[id] = body
}

func (inputs *fakeInputs) projectTask(owner generation.OwnerID, id, taskID generation.AssetID, mediaType string, body []byte) {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	inputs.assets[id] = generation.InputAsset{
		ID: id, OwnerID: owner, MediaType: mediaType,
		Size: int64(len(body)), SHA256: sha256Hex(body), TaskID: generation.TaskID(taskID),
	}
}

func (inputs *fakeInputs) UploadAsset(_ context.Context, owner generation.OwnerID, upload generation.InputAssetUpload) (generation.InputAsset, error) {
	if owner == "" || upload.Body == nil || upload.MaxBytes <= 0 {
		return generation.InputAsset{}, &generation.Error{Code: generation.ErrInvalidInput}
	}
	data, err := io.ReadAll(io.LimitReader(upload.Body, upload.MaxBytes+1))
	if err != nil {
		return generation.InputAsset{}, err
	}
	if int64(len(data)) > upload.MaxBytes {
		return generation.InputAsset{}, &generation.Error{Code: generation.ErrInvalidInput}
	}
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	inputs.nextUpload++
	asset := generation.InputAsset{
		ID:               generation.AssetID(fmt.Sprintf("upload_%d", inputs.nextUpload)),
		OwnerID:          owner,
		StorageKey:       fmt.Sprintf("key/upload_%d", inputs.nextUpload),
		OriginalFilename: upload.OriginalFilename,
		AssetType:        upload.AssetType,
		MediaType:        upload.MediaType,
		Size:             int64(len(data)),
		SHA256:           sha256Hex(data),
		CreatedAt:        time.Now().UTC(),
	}
	inputs.assets[asset.ID] = asset
	inputs.blobs[asset.ID] = data
	inputs.uploaded = append(inputs.uploaded, asset)
	return asset, nil
}

func (inputs *fakeInputs) OpenOwned(_ context.Context, owner generation.OwnerID, id generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	asset, found := inputs.assets[id]
	if !found || asset.OwnerID != owner || asset.TaskID != "" {
		return generation.InputAsset{}, nil, &generation.Error{Code: generation.ErrNotFound}
	}
	return asset, io.NopCloser(bytes.NewReader(inputs.blobs[id])), nil
}

func (inputs *fakeInputs) LoadInputAssets(_ context.Context, owner generation.OwnerID, ids []generation.AssetID) (map[generation.AssetID]generation.InputAsset, error) {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	result := make(map[generation.AssetID]generation.InputAsset, len(ids))
	for _, id := range ids {
		if asset, found := inputs.assets[id]; found && asset.OwnerID == owner {
			result[id] = asset
		}
	}
	return result, nil
}

func (inputs *fakeInputs) uploads() []generation.InputAsset {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	return append([]generation.InputAsset(nil), inputs.uploaded...)
}

func (inputs *fakeInputs) blob(id generation.AssetID) []byte {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	return append([]byte(nil), inputs.blobs[id]...)
}

type fakeTaskAsset struct {
	owner     generation.OwnerID
	mediaType string
	data      []byte
}

type fakeTasks struct {
	mu       sync.Mutex
	contents map[string]fakeTaskAsset
}

func newFakeTasks() *fakeTasks {
	return &fakeTasks{contents: make(map[string]fakeTaskAsset)}
}

func (tasks *fakeTasks) add(owner generation.OwnerID, taskID, assetID generation.AssetID, mediaType string, body []byte) {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	tasks.contents[string(taskID)+"/"+string(assetID)] = fakeTaskAsset{owner: owner, mediaType: mediaType, data: body}
}

func (tasks *fakeTasks) OpenAsset(_ context.Context, owner generation.OwnerID, taskID generation.TaskID, assetID generation.AssetID) (generation.AssetContent, error) {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	content, found := tasks.contents[string(taskID)+"/"+string(assetID)]
	if !found || content.owner != owner {
		return generation.AssetContent{}, &generation.Error{Code: generation.ErrNotFound}
	}
	return generation.AssetContent{
		Body:      io.NopCloser(bytes.NewReader(content.data)),
		MediaType: content.mediaType,
		Size:      int64(len(content.data)),
	}, nil
}

type fakeCrops struct {
	mu   sync.Mutex
	rows []listcrop.Crop
}

func (crops *fakeCrops) InsertOrGet(_ context.Context, crop listcrop.Crop) (listcrop.Crop, bool, error) {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	for _, row := range crops.rows {
		if row.OwnerID == crop.OwnerID && row.SourceAssetID == crop.SourceAssetID &&
			row.PixelRect == crop.PixelRect {
			return row, false, nil
		}
	}
	crops.rows = append(crops.rows, crop)
	return crop, true, nil
}

func (crops *fakeCrops) Find(_ context.Context, owner generation.OwnerID, sourceID generation.AssetID, rect listcrop.PixelRect) (listcrop.Crop, bool, error) {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	for _, row := range crops.rows {
		if row.OwnerID == owner && row.SourceAssetID == sourceID && row.PixelRect == rect {
			return row, true, nil
		}
	}
	return listcrop.Crop{}, false, nil
}

func (crops *fakeCrops) CountBySource(_ context.Context, owner generation.OwnerID, sourceID generation.AssetID) (int, error) {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	count := 0
	for _, row := range crops.rows {
		if row.OwnerID == owner && row.SourceAssetID == sourceID {
			count++
		}
	}
	return count, nil
}

func (crops *fakeCrops) SourceInputCopy(_ context.Context, owner generation.OwnerID, sourceID generation.AssetID) (generation.AssetID, bool, error) {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	for _, row := range crops.rows {
		if row.OwnerID == owner && row.SourceAssetID == sourceID {
			return row.SourceInputAssetID, true, nil
		}
	}
	return "", false, nil
}

func (crops *fakeCrops) rowsSnapshot() []listcrop.Crop {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	return append([]listcrop.Crop(nil), crops.rows...)
}

type cropFixture struct {
	service *listcrop.Service
	inputs  *fakeInputs
	tasks   *fakeTasks
	crops   *fakeCrops
}

func newCropFixture(t *testing.T, configure func(*listcrop.Config)) *cropFixture {
	t.Helper()
	inputs := newFakeInputs()
	tasks := newFakeTasks()
	crops := &fakeCrops{}
	config := listcrop.Config{Inputs: inputs, Tasks: tasks, Crops: crops}
	if configure != nil {
		configure(&config)
	}
	service, err := listcrop.New(config)
	if err != nil {
		t.Fatalf("new listcrop service: %v", err)
	}
	return &cropFixture{service: service, inputs: inputs, tasks: tasks, crops: crops}
}

// quadrantPicture paints four solid quadrants so every rectangle has
// predictable pixels.
func quadrantPicture(t *testing.T, width, height int) (*image.NRGBA, []byte) {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var value color.NRGBA
			switch {
			case x < width/2 && y < height/2:
				value = color.NRGBA{R: 220, G: 30, B: 20, A: 255}
			case x >= width/2 && y < height/2:
				value = color.NRGBA{R: 20, G: 200, B: 40, A: 255}
			case x < width/2 && y >= height/2:
				value = color.NRGBA{R: 25, G: 40, B: 230, A: 255}
			default:
				value = color.NRGBA{R: 240, G: 210, B: 60, A: 255}
			}
			picture.SetNRGBA(x, y, value)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatalf("encode quadrant picture: %v", err)
	}
	return picture, encoded.Bytes()
}

func decodePNG(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode stored crop: %v", err)
	}
	bounds := decoded.Bounds()
	picture := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(picture, picture.Bounds(), decoded, bounds.Min, draw.Src)
	return picture
}

func assertRectPixels(t *testing.T, source *image.NRGBA, rect listcrop.PixelRect, got *image.NRGBA) {
	t.Helper()
	if bounds := got.Bounds(); bounds.Dx() != rect.Width() || bounds.Dy() != rect.Height() {
		t.Fatalf("crop is %dx%d; want %dx%d (no scaling)",
			bounds.Dx(), bounds.Dy(), rect.Width(), rect.Height())
	}
	for y := 0; y < rect.Height(); y++ {
		for x := 0; x < rect.Width(); x++ {
			want := source.NRGBAAt(rect.X0+x, rect.Y0+y)
			if actual := got.NRGBAAt(x, y); actual != want {
				t.Fatalf("pixel (%d,%d) = %v; want source (%d,%d) = %v",
					x, y, actual, rect.X0+x, rect.Y0+y, want)
			}
		}
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestCropRegionsCutsOriginalPixelsWithoutScaling(t *testing.T) {
	fixture := newCropFixture(t, nil)
	source, encoded := quadrantPicture(t, 256, 192)
	fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png", encoded)
	region := listcrop.Region{X: 0.25, Y: 0.25, Width: 0.5, Height: 0.5}

	results, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
		Owner: "owner-a", Source: "source_1", Regions: []listcrop.Region{region},
	})
	if err != nil {
		t.Fatalf("crop regions: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d; want 1", len(results))
	}
	result := results[0]
	wantRect := listcrop.PixelRect{X0: 64, Y0: 48, X1: 192, Y1: 144}
	if result.PixelRect != wantRect {
		t.Fatalf("pixel rect = %+v; want %+v", result.PixelRect, wantRect)
	}
	if result.NormalizedRegion != region {
		t.Fatalf("normalized region = %+v; want %+v", result.NormalizedRegion, region)
	}
	if result.SourceWidth != 256 || result.SourceHeight != 192 {
		t.Fatalf("source size = %dx%d; want 256x192", result.SourceWidth, result.SourceHeight)
	}
	uploads := fixture.inputs.uploads()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d; want 1", len(uploads))
	}
	if uploads[0].ID != result.InputAssetID || uploads[0].AssetType != listcrop.CropAssetType ||
		uploads[0].MediaType != "image/png" {
		t.Fatalf("uploaded asset = %+v; want the list_crop PNG result", uploads[0])
	}
	if uploads[0].SHA256 != result.SHA256 {
		t.Fatalf("result sha = %q; want uploaded sha %q", result.SHA256, uploads[0].SHA256)
	}
	crop := decodePNG(t, fixture.inputs.blob(result.InputAssetID))
	assertRectPixels(t, source, wantRect, crop)

	rows := fixture.crops.rowsSnapshot()
	if len(rows) != 1 {
		t.Fatalf("provenance rows = %d; want 1", len(rows))
	}
	row := rows[0]
	if row.AssetID != result.InputAssetID || row.SourceAssetID != "source_1" ||
		row.SourceKind != listcrop.SourceKindInput || row.SourceInputAssetID != "source_1" ||
		row.SourceWidth != 256 || row.SourceHeight != 192 || row.PixelRect != wantRect ||
		row.Region != region || row.SHA256 != result.SHA256 {
		t.Fatalf("provenance row = %+v; want the source snapshot and pixel rect", row)
	}
}

func TestPixelRectForRegionRoundsAndKeepsRightBottomExclusive(t *testing.T) {
	cases := []struct {
		name   string
		region listcrop.Region
		width  int
		height int
		want   listcrop.PixelRect
	}{
		{
			name:   "rounds every edge",
			region: listcrop.Region{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4},
			width:  333, height: 333,
			want: listcrop.PixelRect{X0: 33, Y0: 67, X1: 133, Y1: 200},
		},
		{
			name:   "rounds half away from zero on the left edge",
			region: listcrop.Region{X: 0.5, Y: 0.5, Width: 0.5, Height: 0.5},
			width:  401, height: 401,
			want: listcrop.PixelRect{X0: 201, Y0: 201, X1: 401, Y1: 401},
		},
		{
			name:   "right and bottom edges are exclusive",
			region: listcrop.Region{X: 0.25, Y: 0.5, Width: 0.25, Height: 0.25},
			width:  400, height: 400,
			want: listcrop.PixelRect{X0: 100, Y0: 200, X1: 200, Y1: 300},
		},
		{
			name:   "full picture",
			region: listcrop.Region{X: 0, Y: 0, Width: 1, Height: 1},
			width:  64, height: 64,
			want: listcrop.PixelRect{X0: 0, Y0: 0, X1: 64, Y1: 64},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rect, err := listcrop.PixelRectForRegion(testCase.region, testCase.width, testCase.height)
			if err != nil {
				t.Fatalf("pixel rect: %v", err)
			}
			if rect != testCase.want {
				t.Fatalf("rect = %+v; want %+v", rect, testCase.want)
			}
		})
	}
}

func TestPixelRectForRegionRejectsInvalidRegions(t *testing.T) {
	cases := []struct {
		name   string
		region listcrop.Region
	}{
		{name: "negative x", region: listcrop.Region{X: -0.1, Width: 0.2, Height: 0.2}},
		{name: "negative y", region: listcrop.Region{Y: -0.1, Width: 0.2, Height: 0.2}},
		{name: "starts at right edge", region: listcrop.Region{X: 1, Width: 0.2, Height: 0.2}},
		{name: "past right edge", region: listcrop.Region{X: 0.9, Width: 0.2, Height: 0.2}},
		{name: "past bottom edge", region: listcrop.Region{Y: 0.9, Width: 0.2, Height: 0.2}},
		{name: "zero width", region: listcrop.Region{X: 0.1, Width: 0, Height: 0.2}},
		{name: "zero height", region: listcrop.Region{X: 0.1, Width: 0.2, Height: 0}},
		{name: "sub-pixel", region: listcrop.Region{X: 0.999, Y: 0.999, Width: 0.001, Height: 0.001}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := listcrop.PixelRectForRegion(testCase.region, 100, 100); !errors.Is(err, listcrop.ErrInvalidRegion) {
				t.Fatalf("err = %v; want ErrInvalidRegion", err)
			}
		})
	}
	if _, err := listcrop.PixelRectForRegion(listcrop.Region{X: 0.1, Width: 0.2, Height: 0.2}, 0, 100); !errors.Is(err, listcrop.ErrInvalidRegion) {
		t.Fatalf("empty source: err = %v; want ErrInvalidRegion", err)
	}
}

func TestCropRegionsRejectsBadRegionBeforeWriting(t *testing.T) {
	cases := []struct {
		name     string
		config   func(*listcrop.Config)
		regions  []listcrop.Region
		wantErr  error
		pictureW int
	}{
		{
			name:     "too small",
			regions:  []listcrop.Region{{X: 0.25, Y: 0.25, Width: 0.5, Height: 0.5}},
			pictureW: 100,
			wantErr:  listcrop.ErrInvalidRegion,
		},
		{
			name:     "too many pixels",
			config:   func(config *listcrop.Config) { config.MaximumRegionPixels = 5000 },
			regions:  []listcrop.Region{{X: 0, Y: 0, Width: 1, Height: 1}},
			pictureW: 100,
			wantErr:  listcrop.ErrTooLarge,
		},
		{
			name:     "empty batch",
			regions:  nil,
			pictureW: 100,
			wantErr:  listcrop.ErrInvalid,
		},
		{
			name:     "batch over the per-source limit",
			config:   func(config *listcrop.Config) { config.MaximumCropsPerSource = 1 },
			regions:  []listcrop.Region{{X: 0, Y: 0, Width: 0.5, Height: 0.5}, {X: 0.5, Y: 0.5, Width: 0.5, Height: 0.5}},
			pictureW: 100,
			wantErr:  listcrop.ErrLimit,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newCropFixture(t, testCase.config)
			_, encoded := quadrantPicture(t, testCase.pictureW, testCase.pictureW)
			fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png", encoded)
			_, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
				Owner: "owner-a", Source: "source_1", Regions: testCase.regions,
			})
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("err = %v; want %v", err, testCase.wantErr)
			}
			if uploads := fixture.inputs.uploads(); len(uploads) != 0 {
				t.Fatalf("uploads = %d; a refused batch must write nothing", len(uploads))
			}
			if rows := fixture.crops.rowsSnapshot(); len(rows) != 0 {
				t.Fatalf("provenance rows = %d; a refused batch must write nothing", len(rows))
			}
		})
	}
}

func TestCropRegionsRejectsForeignMissingAndNonImageSources(t *testing.T) {
	fixture := newCropFixture(t, nil)
	_, encoded := quadrantPicture(t, 128, 128)
	fixture.inputs.addInputAsset("owner-a", "source_model", "model", "model/gltf-binary", []byte("glb"))
	fixture.inputs.addInputAsset("owner-b", "source_other", "image", "image/png", encoded)
	region := listcrop.Region{X: 0, Y: 0, Width: 1, Height: 1}

	cases := []struct {
		name   string
		source generation.AssetID
		want   error
	}{
		{name: "missing", source: "source_missing", want: listcrop.ErrNotFound},
		{name: "foreign owner", source: "source_other", want: listcrop.ErrNotFound},
		{name: "non-image", source: "source_model", want: listcrop.ErrInvalidSource},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
				Owner: "owner-a", Source: testCase.source, Regions: []listcrop.Region{region},
			})
			if !errors.Is(err, testCase.want) {
				t.Fatalf("err = %v; want %v", err, testCase.want)
			}
		})
	}
	if uploads := fixture.inputs.uploads(); len(uploads) != 0 {
		t.Fatalf("uploads = %d; refused sources must write nothing", len(uploads))
	}
}

func TestCropRegionsRepeatedRectangleReturnsSameAsset(t *testing.T) {
	fixture := newCropFixture(t, nil)
	_, encoded := quadrantPicture(t, 256, 256)
	fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png", encoded)
	region := listcrop.Region{X: 0.25, Y: 0.25, Width: 0.5, Height: 0.5}
	request := listcrop.Request{Owner: "owner-a", Source: "source_1", Regions: []listcrop.Region{region}}

	first, err := fixture.service.CropRegions(context.Background(), request)
	if err != nil {
		t.Fatalf("first crop: %v", err)
	}
	second, err := fixture.service.CropRegions(context.Background(), request)
	if err != nil {
		t.Fatalf("second crop: %v", err)
	}
	if first[0].InputAssetID != second[0].InputAssetID {
		t.Fatalf("repeated rectangle returned %q then %q", first[0].InputAssetID, second[0].InputAssetID)
	}
	if uploads := fixture.inputs.uploads(); len(uploads) != 1 {
		t.Fatalf("uploads = %d; a repeated rectangle must reuse the stored asset", len(uploads))
	}
	if rows := fixture.crops.rowsSnapshot(); len(rows) != 1 {
		t.Fatalf("provenance rows = %d; want 1", len(rows))
	}
}

func TestCropRegionsConcurrentSameRectangleReturnsOneAsset(t *testing.T) {
	fixture := newCropFixture(t, nil)
	_, encoded := quadrantPicture(t, 256, 256)
	fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png", encoded)
	request := listcrop.Request{
		Owner: "owner-a", Source: "source_1",
		Regions: []listcrop.Region{{X: 0.25, Y: 0.25, Width: 0.5, Height: 0.5}},
	}
	const calls = 4
	ids := make(chan generation.AssetID, calls)
	errs := make(chan error, calls)
	var started sync.WaitGroup
	started.Add(calls)
	for index := 0; index < calls; index++ {
		go func() {
			started.Done()
			started.Wait()
			results, err := fixture.service.CropRegions(context.Background(), request)
			if err != nil {
				errs <- err
				return
			}
			ids <- results[0].InputAssetID
		}()
	}
	first := generation.AssetID("")
	for index := 0; index < calls; index++ {
		select {
		case err := <-errs:
			t.Fatalf("concurrent crop: %v", err)
		case id := <-ids:
			if first == "" {
				first = id
			} else if id != first {
				t.Fatalf("concurrent crop returned %q; want %q", id, first)
			}
		}
	}
	if rows := fixture.crops.rowsSnapshot(); len(rows) != 1 {
		t.Fatalf("provenance rows = %d; concurrent calls must not duplicate", len(rows))
	}
}

func TestCropRegionsCopiesTaskSourceIntoInputAsset(t *testing.T) {
	fixture := newCropFixture(t, nil)
	source, encoded := quadrantPicture(t, 256, 192)
	fixture.inputs.projectTask("owner-a", "task_asset_1", "task_1", "image/png", encoded)
	fixture.tasks.add("owner-a", "task_1", "task_asset_1", "image/png", encoded)
	first := listcrop.Region{X: 0.25, Y: 0.25, Width: 0.5, Height: 0.5}
	second := listcrop.Region{X: 0, Y: 0, Width: 0.5, Height: 0.5}

	results, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
		Owner: "owner-a", Source: "task_asset_1", Regions: []listcrop.Region{first},
	})
	if err != nil {
		t.Fatalf("crop task source: %v", err)
	}
	uploads := fixture.inputs.uploads()
	if len(uploads) != 2 {
		t.Fatalf("uploads = %d; want one source copy and one crop", len(uploads))
	}
	if uploads[0].AssetType != listcrop.DefaultSourceAssetType || uploads[1].AssetType != listcrop.CropAssetType {
		t.Fatalf("upload order/types = %q, %q; want source copy then list_crop",
			uploads[0].AssetType, uploads[1].AssetType)
	}
	copiedID := uploads[0].ID
	if results[0].InputAssetID != uploads[1].ID {
		t.Fatalf("result asset = %q; want the crop upload %q", results[0].InputAssetID, uploads[1].ID)
	}
	if !bytes.Equal(fixture.inputs.blob(copiedID), encoded) {
		t.Fatal("Task source copy does not hold the source bytes")
	}
	rows := fixture.crops.rowsSnapshot()
	if len(rows) != 1 {
		t.Fatalf("provenance rows = %d; want 1", len(rows))
	}
	row := rows[0]
	if row.SourceKind != listcrop.SourceKindTask || row.SourceAssetID != "task_asset_1" ||
		row.SourceInputAssetID != copiedID || row.SourceWidth != 256 || row.SourceHeight != 192 {
		t.Fatalf("task provenance = %+v; want the task snapshot and the copied input asset", row)
	}
	assertRectPixels(t, source, results[0].PixelRect, decodePNG(t, fixture.inputs.blob(results[0].InputAssetID)))

	// A later call reuses the remembered copy and only uploads the new crop.
	if _, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
		Owner: "owner-a", Source: "task_asset_1", Regions: []listcrop.Region{second},
	}); err != nil {
		t.Fatalf("second task crop: %v", err)
	}
	if uploads := fixture.inputs.uploads(); len(uploads) != 3 {
		t.Fatalf("uploads = %d; a second region must reuse the source copy", len(uploads))
	}
}

func TestCropRegionsEnforcesSourceCropLimitAcrossCalls(t *testing.T) {
	fixture := newCropFixture(t, func(config *listcrop.Config) {
		config.MaximumCropsPerSource = 2
	})
	_, encoded := quadrantPicture(t, 400, 400)
	fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png", encoded)
	first := listcrop.Region{X: 0, Y: 0, Width: 0.25, Height: 0.25}
	second := listcrop.Region{X: 0.25, Y: 0.25, Width: 0.25, Height: 0.25}
	third := listcrop.Region{X: 0.5, Y: 0.5, Width: 0.25, Height: 0.25}

	if _, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
		Owner: "owner-a", Source: "source_1", Regions: []listcrop.Region{first, second},
	}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	_, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
		Owner: "owner-a", Source: "source_1", Regions: []listcrop.Region{third},
	})
	var limit *listcrop.LimitError
	if !errors.As(err, &limit) || !errors.Is(err, listcrop.ErrLimit) {
		t.Fatalf("err = %v; want a LimitError carrying the limit", err)
	}
	if limit.Limit != 2 {
		t.Fatalf("limit = %d; want 2", limit.Limit)
	}
	// An already stored rectangle still resolves at the limit.
	results, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
		Owner: "owner-a", Source: "source_1", Regions: []listcrop.Region{first},
	})
	if err != nil {
		t.Fatalf("existing rectangle at the limit: %v", err)
	}
	if results[0].InputAssetID == "" {
		t.Fatal("existing rectangle returned no asset")
	}
	if rows := fixture.crops.rowsSnapshot(); len(rows) != 2 {
		t.Fatalf("provenance rows = %d; want 2", len(rows))
	}
}

func TestCropRegionsRefusesOversizedSourceAndOutput(t *testing.T) {
	t.Run("source bytes", func(t *testing.T) {
		fixture := newCropFixture(t, func(config *listcrop.Config) {
			config.SourceMaxBytes = 32
		})
		_, encoded := quadrantPicture(t, 128, 128)
		fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png", encoded)
		_, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
			Owner: "owner-a", Source: "source_1",
			Regions: []listcrop.Region{{X: 0, Y: 0, Width: 1, Height: 1}},
		})
		if !errors.Is(err, listcrop.ErrTooLarge) {
			t.Fatalf("err = %v; want ErrTooLarge", err)
		}
	})
	t.Run("source pixels", func(t *testing.T) {
		fixture := newCropFixture(t, func(config *listcrop.Config) {
			config.SourceMaxPixels = 1000
		})
		_, encoded := quadrantPicture(t, 128, 128)
		fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png", encoded)
		_, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
			Owner: "owner-a", Source: "source_1",
			Regions: []listcrop.Region{{X: 0, Y: 0, Width: 1, Height: 1}},
		})
		if !errors.Is(err, listcrop.ErrTooLarge) {
			t.Fatalf("err = %v; want ErrTooLarge", err)
		}
	})
	t.Run("encoded crop", func(t *testing.T) {
		fixture := newCropFixture(t, func(config *listcrop.Config) {
			config.UploadMaxBytes = 16
		})
		_, encoded := quadrantPicture(t, 100, 100)
		fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png", encoded)
		_, err := fixture.service.CropRegions(context.Background(), listcrop.Request{
			Owner: "owner-a", Source: "source_1",
			Regions: []listcrop.Region{{X: 0, Y: 0, Width: 1, Height: 1}},
		})
		if !errors.Is(err, listcrop.ErrTooLarge) {
			t.Fatalf("err = %v; want ErrTooLarge", err)
		}
		if uploads := fixture.inputs.uploads(); len(uploads) != 0 {
			t.Fatalf("uploads = %d; an oversized crop must not be stored", len(uploads))
		}
	})
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	if _, err := listcrop.New(listcrop.Config{}); err == nil {
		t.Fatal("New accepted missing dependencies")
	}
}