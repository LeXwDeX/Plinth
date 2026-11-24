package agenttools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
)

// fakeCropInputs is the crop fixture's in-memory input-asset store. It mirrors
// the SQLite LoadInputAssets bridge: a finished Task output is resolvable by id
// but only OpenOwned reaches a real input asset.
type fakeCropInputs struct {
	mu         sync.Mutex
	assets     map[generation.AssetID]generation.InputAsset
	blobs      map[generation.AssetID][]byte
	uploaded   []generation.InputAsset
	nextUpload int
}

func newFakeCropInputs() *fakeCropInputs {
	return &fakeCropInputs{
		assets: make(map[generation.AssetID]generation.InputAsset),
		blobs:  make(map[generation.AssetID][]byte),
	}
}

func (inputs *fakeCropInputs) addInputAsset(owner generation.OwnerID, id generation.AssetID,
	assetType, mediaType string, body []byte,
) {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	inputs.assets[id] = generation.InputAsset{
		ID: id, OwnerID: owner, StorageKey: "key/" + string(id),
		AssetType: assetType, MediaType: mediaType,
		Size: int64(len(body)), SHA256: sha256Hex(body), CreatedAt: time.Now().UTC(),
	}
	inputs.blobs[id] = body
}

func (inputs *fakeCropInputs) projectTask(owner generation.OwnerID, id generation.AssetID,
	taskID generation.TaskID, mediaType string, body []byte,
) {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	inputs.assets[id] = generation.InputAsset{
		ID: id, OwnerID: owner, MediaType: mediaType,
		Size: int64(len(body)), SHA256: sha256Hex(body), TaskID: taskID,
	}
}

func (inputs *fakeCropInputs) UploadAsset(_ context.Context, owner generation.OwnerID,
	upload generation.InputAssetUpload,
) (generation.InputAsset, error) {
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

func (inputs *fakeCropInputs) OpenOwned(_ context.Context, owner generation.OwnerID,
	id generation.AssetID,
) (generation.InputAsset, io.ReadCloser, error) {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	asset, found := inputs.assets[id]
	if !found || asset.OwnerID != owner || asset.TaskID != "" {
		return generation.InputAsset{}, nil, &generation.Error{Code: generation.ErrNotFound}
	}
	return asset, io.NopCloser(bytes.NewReader(inputs.blobs[id])), nil
}

func (inputs *fakeCropInputs) LoadInputAssets(_ context.Context, owner generation.OwnerID,
	ids []generation.AssetID,
) (map[generation.AssetID]generation.InputAsset, error) {
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

func (inputs *fakeCropInputs) uploads() []generation.InputAsset {
	inputs.mu.Lock()
	defer inputs.mu.Unlock()
	return append([]generation.InputAsset(nil), inputs.uploaded...)
}

// fakeCropTasks only opens finished Task outputs: the crop tool has no port
// that could submit a Task, and the count keeps that observable.
type fakeCropTasks struct {
	mu       sync.Mutex
	contents map[string]fakeCropTaskAsset
	opened   int
}

type fakeCropTaskAsset struct {
	owner     generation.OwnerID
	mediaType string
	data      []byte
}

func newFakeCropTasks() *fakeCropTasks {
	return &fakeCropTasks{contents: make(map[string]fakeCropTaskAsset)}
}

func (tasks *fakeCropTasks) add(owner generation.OwnerID, taskID generation.TaskID,
	assetID generation.AssetID, mediaType string, body []byte,
) {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	tasks.contents[string(taskID)+"/"+string(assetID)] = fakeCropTaskAsset{
		owner: owner, mediaType: mediaType, data: body,
	}
}

func (tasks *fakeCropTasks) OpenAsset(_ context.Context, owner generation.OwnerID,
	taskID generation.TaskID, assetID generation.AssetID,
) (generation.AssetContent, error) {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	tasks.opened++
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

func (tasks *fakeCropTasks) openedCount() int {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	return tasks.opened
}

type fakeCropStore struct {
	mu   sync.Mutex
	rows []listcrop.Crop
}

func (crops *fakeCropStore) insert(crop listcrop.Crop, limit int) (listcrop.Crop, bool, error) {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	for _, row := range crops.rows {
		if row.OwnerID == crop.OwnerID && row.SourceAssetID == crop.SourceAssetID &&
			row.PixelRect == crop.PixelRect {
			return row, false, nil
		}
	}
	if limit > 0 {
		count := 0
		for _, row := range crops.rows {
			if row.OwnerID == crop.OwnerID && row.SourceAssetID == crop.SourceAssetID {
				count++
			}
		}
		if count >= limit {
			return listcrop.Crop{}, false, &listcrop.LimitError{Limit: limit}
		}
	}
	crops.rows = append(crops.rows, crop)
	return crop, true, nil
}

func (crops *fakeCropStore) InsertOrGet(_ context.Context,
	crop listcrop.Crop,
) (listcrop.Crop, bool, error) {
	return crops.insert(crop, 0)
}

func (crops *fakeCropStore) InsertOrGetWithinLimit(_ context.Context,
	crop listcrop.Crop, limit int,
) (listcrop.Crop, bool, error) {
	return crops.insert(crop, limit)
}

func (crops *fakeCropStore) Get(_ context.Context, owner generation.OwnerID,
	cropAssetID generation.AssetID,
) (listcrop.Crop, bool, error) {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	for _, row := range crops.rows {
		if row.OwnerID == owner && row.AssetID == cropAssetID {
			return row, true, nil
		}
	}
	return listcrop.Crop{}, false, nil
}

func (crops *fakeCropStore) Find(_ context.Context, owner generation.OwnerID,
	sourceID generation.AssetID, rect listcrop.PixelRect,
) (listcrop.Crop, bool, error) {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	for _, row := range crops.rows {
		if row.OwnerID == owner && row.SourceAssetID == sourceID && row.PixelRect == rect {
			return row, true, nil
		}
	}
	return listcrop.Crop{}, false, nil
}

func (crops *fakeCropStore) CountBySource(_ context.Context, owner generation.OwnerID,
	sourceID generation.AssetID,
) (int, error) {
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

func (crops *fakeCropStore) SourceInputCopy(_ context.Context, owner generation.OwnerID,
	sourceID generation.AssetID,
) (generation.AssetID, bool, error) {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	for _, row := range crops.rows {
		if row.OwnerID == owner && row.SourceAssetID == sourceID {
			return row.SourceInputAssetID, true, nil
		}
	}
	return "", false, nil
}

func (crops *fakeCropStore) rowsSnapshot() []listcrop.Crop {
	crops.mu.Lock()
	defer crops.mu.Unlock()
	return append([]listcrop.Crop(nil), crops.rows...)
}

type cropToolFixture struct {
	tool   *CropTool
	inputs *fakeCropInputs
	tasks  *fakeCropTasks
	crops  *fakeCropStore
}

// newCropToolFixture wires the real crop service to fake ports and the tool's
// Schema copy, so a test sees exactly the uploads, opens and provenance rows
// one call causes.
func newCropToolFixture(t *testing.T, configure func(*listcrop.Config)) *cropToolFixture {
	t.Helper()
	inputs := newFakeCropInputs()
	tasks := newFakeCropTasks()
	crops := &fakeCropStore{}
	config := listcrop.Config{Inputs: inputs, Tasks: tasks, Crops: crops}
	if configure != nil {
		configure(&config)
	}
	service, err := listcrop.New(config)
	if err != nil {
		t.Fatalf("new crop service: %v", err)
	}
	tool, err := NewCropTool(CropToolConfig{
		Context:        cropToolContext(),
		Schema:         cropToolSchema(),
		Crops:          service,
		AssetItemRoute: cropAssetItemRoute,
	})
	if err != nil {
		t.Fatalf("new crop tool: %v", err)
	}
	return &cropToolFixture{tool: tool, inputs: inputs, tasks: tasks, crops: crops}
}

// cropAssetItemRoute is the Schema's input asset route the crops are shown by.
const cropAssetItemRoute = "/api/v1/assets/{assetID}"

func cropToolContext() catalog.ChatContextSchema {
	return catalog.ChatContextSchema{
		HandlePrefixes:     map[string]string{"image": "@img", "model": "@model"},
		SelectionAlias:     "@selected",
		ResultHandlesField: "handles",
	}
}

func cropToolSchema() catalog.ChatImageCropSchema {
	return catalog.ChatImageCropSchema{
		Tool: "crop_image_regions",
		Texts: catalog.ChatImageCropTexts{
			InvalidArguments: "invalid arguments: {detail}",
			InvalidRegion:    "invalid region: {detail}",
			ImageTooSmall:    "too small: {detail}",
			ImageTooLarge:    "too large: {detail}",
			CropLimitReached: "crop limit {limit}",
			ImageNotFound:    "not found: {detail}",
			ImageUnavailable: "unavailable: {detail}",
			PresentedNote:    "{count} crops are shown to the user",
			NotPresentable:   "not presentable: {detail}",
			NotPresented:     "not presented: {detail}",
		},
	}
}

func solidPicture(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			picture.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func cropCommand(owner, image string, regions string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: owner, SessionID: "s1",
		Call: agents.ToolCall{
			ID:   "crop-1",
			Name: ToolCropImageRegions,
			Arguments: json.RawMessage(fmt.Sprintf(
				`{"image":%q,"regions":[%s]}`, image, regions)),
		},
		History: []chat.Message{{Attachments: []chat.Attachment{{
			Handle: "@img1", Kind: chat.AttachmentKindImage, AssetID: "source_1",
		}}}},
	}
}

type cropContentResult struct {
	Success      bool   `json:"success"`
	SourceWidth  int    `json:"source_width"`
	SourceHeight int    `json:"source_height"`
	Note         string `json:"note"`
	Crops        []struct {
		Label            string             `json:"label"`
		Width            int                `json:"width"`
		Height           int                `json:"height"`
		InputAssetID     string             `json:"input_asset_id"`
		NormalizedRegion listcrop.Region    `json:"normalized_region"`
		PixelRect        listcrop.PixelRect `json:"pixel_rect"`
		SourceWidth      int                `json:"source_width"`
		SourceHeight     int                `json:"source_height"`
		SHA256           string             `json:"sha256"`
	} `json:"crops"`
}

type cropRefusalResult struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Error   string `json:"error"`
}

func decodeCrop[T any](t *testing.T, content string) T {
	t.Helper()
	var decoded T
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		t.Fatalf("decode %q: %v", content, err)
	}
	return decoded
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// #331: a session handle and an input asset id both name the source, the
// result keeps request order and every crop's geometry, and no Task or step is
// touched: an uploaded input asset needs no provenance to be cropped.
func TestCropImageRegionsCropsHandleAndAssetSources(t *testing.T) {
	for _, image := range []string{"@img1", "source_1"} {
		t.Run(image, func(t *testing.T) {
			fixture := newCropToolFixture(t, nil)
			fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png",
				solidPicture(t, 200, 200))
			result, err := fixture.tool.Execute(context.Background(),
				cropCommand("owner-a", image,
					`{"label":"sword","region":{"x":0,"y":0,"width":0.5,"height":0.5}},`+
						`{"region":{"x":0.5,"y":0.5,"width":0.5,"height":0.5}}`))
			if err != nil {
				t.Fatal(err)
			}
			// #552: every crop is shown to the person as a picture of the reply,
			// in request order, and its result entry is where its handle goes.
			if !result.NoSubmission || result.TaskID != "" ||
				strings.Join(result.ImageURLs, ",") != "/api/v1/assets/upload_1,/api/v1/assets/upload_2" ||
				result.PresentedEntries != "crops" {
				t.Fatalf("result does not present the crops: %+v", result)
			}
			content := decodeCrop[cropContentResult](t, result.AgentContent)
			if !content.Success || content.SourceWidth != 200 || content.SourceHeight != 200 ||
				len(content.Crops) != 2 {
				t.Fatalf("content = %+v", content)
			}
			if content.Note != "2 crops are shown to the user" {
				t.Fatalf("note = %q", content.Note)
			}
			first, second := content.Crops[0], content.Crops[1]
			if first.Width != 100 || first.Height != 100 || second.Width != 100 || second.Height != 100 {
				t.Fatalf("crop sizes = %+v", content.Crops)
			}
			// The result the Agent reads instead when a crop gets no handle
			// says so; it never claims success.
			unpresented := decodeCrop[cropRefusalResult](t, result.UnpresentedContent)
			if unpresented.Success || unpresented.Code != cropCodeNotPresented ||
				!strings.HasPrefix(unpresented.Error, "not presented: ") {
				t.Fatalf("unpresented result = %+v", unpresented)
			}
			if first.Label != "sword" || first.InputAssetID != "upload_1" ||
				first.PixelRect != (listcrop.PixelRect{X0: 0, Y0: 0, X1: 100, Y1: 100}) ||
				first.NormalizedRegion != (listcrop.Region{X: 0, Y: 0, Width: 0.5, Height: 0.5}) ||
				first.SourceWidth != 200 || first.SourceHeight != 200 || first.SHA256 == "" {
				t.Fatalf("first crop = %+v", first)
			}
			if second.Label != "" || second.InputAssetID != "upload_2" ||
				second.PixelRect != (listcrop.PixelRect{X0: 100, Y0: 100, X1: 200, Y1: 200}) {
				t.Fatalf("second crop = %+v", second)
			}
			uploads := fixture.inputs.uploads()
			if len(uploads) != 2 {
				t.Fatalf("uploads = %d, want 2 crops", len(uploads))
			}
			for _, upload := range uploads {
				if upload.AssetType != listcrop.CropAssetType || upload.MediaType != "image/png" {
					t.Fatalf("crop upload = %+v", upload)
				}
			}
			if rows := fixture.crops.rowsSnapshot(); len(rows) != 2 {
				t.Fatalf("provenance rows = %d, want 2", len(rows))
			}
			if fixture.tasks.openedCount() != 0 {
				t.Fatalf("Task outputs opened for an input source")
			}
		})
	}
}

// #331: a Task result is accepted and copied into an input asset first, so the
// source form stays uniform; the result still names the stored crops only.
func TestCropImageRegionsCopiesATaskSource(t *testing.T) {
	fixture := newCropToolFixture(t, nil)
	picture := solidPicture(t, 128, 128)
	fixture.inputs.projectTask("owner-a", "task_asset_1", "task_1", "image/png", picture)
	fixture.tasks.add("owner-a", "task_1", "task_asset_1", "image/png", picture)
	result, err := fixture.tool.Execute(context.Background(),
		cropCommand("owner-a", "task_asset_1",
			`{"region":{"x":0,"y":0,"width":1,"height":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	content := decodeCrop[cropContentResult](t, result.AgentContent)
	if !content.Success || len(content.Crops) != 1 || content.Crops[0].InputAssetID != "upload_2" {
		t.Fatalf("content = %+v", content)
	}
	if fixture.tasks.openedCount() != 1 {
		t.Fatalf("Task outputs opened = %d, want the one copy", fixture.tasks.openedCount())
	}
	uploads := fixture.inputs.uploads()
	if len(uploads) != 2 || uploads[0].AssetType != listcrop.DefaultSourceAssetType ||
		uploads[1].AssetType != listcrop.CropAssetType {
		t.Fatalf("uploads = %+v, want the source copy then the crop", uploads)
	}
	rows := fixture.crops.rowsSnapshot()
	if len(rows) != 1 || rows[0].SourceKind != listcrop.SourceKindTask ||
		rows[0].SourceInputAssetID != "upload_1" {
		t.Fatalf("provenance = %+v", rows)
	}
}

// #331: @projectN is not a picture of this session, unknown handles and wrong
// kinds are invalid arguments, and malformed objects are refused whole.
func TestCropImageRegionsRefusesBadPictureArguments(t *testing.T) {
	fixture := newCropToolFixture(t, nil)
	region := `{"region":{"x":0,"y":0,"width":0.5,"height":0.5}}`
	for name, image := range map[string]string{
		"project handle": "@project3",
		"unknown handle": "@img9",
		"wrong kind":     "@model1",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := fixture.tool.Execute(context.Background(),
				cropCommand("owner-a", image, region))
			if err != nil {
				t.Fatal(err)
			}
			refusal := decodeCrop[cropRefusalResult](t, result.AgentContent)
			if refusal.Success || refusal.Code != cropCodeInvalidArguments ||
				!strings.Contains(refusal.Error, image) || !result.NoSubmission {
				t.Fatalf("refusal = %+v result = %+v", refusal, result)
			}
		})
	}
	for name, arguments := range map[string]string{
		"unknown field": `{"image":"@img1","regions":[],"extra":true}`,
		"empty regions": `{"image":"@img1","regions":[]}`,
		"missing image": `{"regions":[{"region":{"x":0,"y":0,"width":0.5,"height":0.5}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			command := cropCommand("owner-a", "@img1", region)
			command.Call.Arguments = json.RawMessage(arguments)
			result, err := fixture.tool.Execute(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			refusal := decodeCrop[cropRefusalResult](t, result.AgentContent)
			if refusal.Code != cropCodeInvalidArguments {
				t.Fatalf("refusal = %+v", refusal)
			}
		})
	}
}

// #331: every service refusal maps to its declared code, a refused region
// names its position and label, and a refused batch writes nothing.
func TestCropImageRegionsMapsServiceRefusals(t *testing.T) {
	region := func(x, y, width, height float64) string {
		return fmt.Sprintf(`{"label":"piece","region":{"x":%v,"y":%v,"width":%v,"height":%v}}`,
			x, y, width, height)
	}
	cases := map[string]struct {
		configure func(*listcrop.Config)
		image     string
		regions   string
		code      string
		quote     string
	}{
		"out of bounds": {
			image: "@img1", regions: region(0.8, 0, 0.5, 0.5),
			code: cropCodeInvalidRegion, quote: "regions[0] (piece)",
		},
		"too small": {
			image: "@img1", regions: region(0.1, 0.1, 0.1, 0.1),
			code: cropCodeImageTooSmall, quote: "regions[0] (piece)",
		},
		"region too large": {
			configure: func(config *listcrop.Config) { config.MaximumRegionPixels = 2500 },
			image:     "@img1", regions: region(0, 0, 1, 1),
			code: cropCodeImageTooLarge, quote: "regions[0] (piece)",
		},
		"batch over the source limit": {
			configure: func(config *listcrop.Config) { config.MaximumCropsPerSource = 1 },
			image:     "@img1",
			regions:   `{"region":{"x":0,"y":0,"width":0.5,"height":0.5}},` + region(0.5, 0.5, 0.5, 0.5),
			code:      cropCodeCropLimitReached, quote: "1",
		},
		"foreign asset": {
			image: "source_other", regions: region(0, 0, 0.5, 0.5),
			code: cropCodeImageNotFound, quote: "source_other",
		},
		"non-image source": {
			image: "source_model", regions: region(0, 0, 0.5, 0.5),
			code: cropCodeImageUnavailable, quote: "model/gltf-binary",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newCropToolFixture(t, testCase.configure)
			fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png",
				solidPicture(t, 200, 200))
			fixture.inputs.addInputAsset("owner-b", "source_other", "image", "image/png",
				solidPicture(t, 200, 200))
			fixture.inputs.addInputAsset("owner-a", "source_model", "model", "model/gltf-binary",
				[]byte("glb"))
			result, err := fixture.tool.Execute(context.Background(),
				cropCommand("owner-a", testCase.image, testCase.regions))
			if err != nil {
				t.Fatal(err)
			}
			refusal := decodeCrop[cropRefusalResult](t, result.AgentContent)
			if refusal.Success || refusal.Code != testCase.code ||
				!strings.Contains(refusal.Error, testCase.quote) || !result.NoSubmission {
				t.Fatalf("refusal = %+v, want code %q naming %q", refusal, testCase.code, testCase.quote)
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

// #331: the executor dispatches the tool and an unwired executor refuses it
// without a submission.
func TestCropImageRegionsDispatchesThroughTheExecutor(t *testing.T) {
	var received chat.ToolCommand
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &exactPendingGenerate{},
		CropImageRegions: func(_ context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
			received = command
			return chat.ToolResult{AgentContent: `{"success":true}`, NoSubmission: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{
		OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "crop-1", Name: ToolCropImageRegions,
			Arguments: json.RawMessage(`{"image":"@img1","regions":[]}`)},
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.AgentContent != `{"success":true}` ||
		!result.NoSubmission || received.Call.ID != "crop-1" {
		t.Fatalf("dispatch result=%+v received=%+v err=%v", result, received.Call, err)
	}

	unwired, err := New(Config{Params: agentparams.NewStore(), Generate: &exactPendingGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err = unwired.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || result.Error != "image crop unavailable" {
		t.Fatalf("unwired crop tool = %+v, %v", result, err)
	}
}

// #552: a session that cannot show the crops to the person as handled
// pictures refuses the call before cutting anything, so no asset the person
// cannot see is ever stored.
func TestCropImageRegionsRefusesWhenTheCropsCannotBeShown(t *testing.T) {
	cases := map[string]func(*CropToolConfig){
		"handles off":         func(config *CropToolConfig) { config.Context.HandlePrefixes = nil },
		"no result handles":   func(config *CropToolConfig) { config.Context.ResultHandlesField = "" },
		"no asset item route": func(config *CropToolConfig) { config.AssetItemRoute = "" },
		"route without id":    func(config *CropToolConfig) { config.AssetItemRoute = "/api/v1/assets" },
	}
	for name, adjust := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newCropToolFixture(t, nil)
			fixture.inputs.addInputAsset("owner-a", "source_1", "image", "image/png",
				solidPicture(t, 200, 200))
			config := CropToolConfig{
				Context:        cropToolContext(),
				Schema:         cropToolSchema(),
				Crops:          fixture.tool.crops,
				AssetItemRoute: cropAssetItemRoute,
			}
			adjust(&config)
			tool, err := NewCropTool(config)
			if err != nil {
				t.Fatal(err)
			}
			result, err := tool.Execute(context.Background(), cropCommand("owner-a", "source_1",
				`{"region":{"x":0,"y":0,"width":0.5,"height":0.5}}`))
			if err != nil {
				t.Fatal(err)
			}
			refusal := decodeCrop[cropRefusalResult](t, result.AgentContent)
			if refusal.Success || refusal.Code != cropCodeNotPresentable ||
				!strings.HasPrefix(refusal.Error, "not presentable: ") || !result.NoSubmission ||
				len(result.ImageURLs) != 0 {
				t.Fatalf("refusal = %+v result = %+v", refusal, result)
			}
			if uploads := fixture.inputs.uploads(); len(uploads) != 0 {
				t.Fatalf("uploads = %d; an unpresentable call must store nothing", len(uploads))
			}
			if rows := fixture.crops.rowsSnapshot(); len(rows) != 0 {
				t.Fatalf("provenance rows = %d; an unpresentable call must store nothing", len(rows))
			}
		})
	}
}
