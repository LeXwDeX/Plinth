package assetlist_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// archiveTestSchema is reviewTestSchema with the #329c batch delivery section.
func archiveTestSchema() catalog.AssetListSchema {
	schema := reviewTestSchema()
	schema.Review.Archive = &catalog.AssetListArchiveSchema{
		FilenameTemplate:      "asset-list-{list_id}.zip",
		MediaType:             "application/zip",
		ManifestFilename:      "manifest.json",
		ReadmeFilename:        "README.txt",
		DirectoryNameMaxBytes: 80,
		MaxEntries:            64,
		MaxBytes:              1 << 20,
		StoredMediaTypes:      []string{"model/gltf-binary", "image/png"},
		Messages: catalog.AssetListArchiveMessagesSchema{
			Empty:    "没有可打包的项",
			TooLarge: "超过 {maximum_entries} 个文件 / {maximum_bytes} 字节",
		},
		Audit: catalog.AssetListArchiveAuditSchema{
			Action: "download_asset_list_archive", ResourceType: "asset_list_archive",
			DetailFields: catalog.AssetListArchiveAuditDetailFields{
				ListID: "listId", Items: "items", Entries: "entries", Bytes: "bytes",
			},
		},
	}
	return schema
}

// fakeArchiveAssets is the owner-scoped reader over in-memory stored files. A
// file it never received answers the delivery layer's not-found code; fail is
// a store that cannot be read at all.
type fakeArchiveAssets struct {
	files map[string]fakeArchiveBlob
	opens []string
	fail  error
}

type fakeArchiveBlob struct {
	data      []byte
	mediaType string
}

func (fake *fakeArchiveAssets) OpenAssetScoped(
	_ context.Context, scope generation.TaskListScope,
	taskID generation.TaskID, assetID generation.AssetID,
) (generation.AssetContent, error) {
	key := string(taskID) + "/" + string(assetID)
	fake.opens = append(fake.opens, key)
	if fake.fail != nil {
		return generation.AssetContent{}, fake.fail
	}
	blob, found := fake.files[key]
	if !found {
		return generation.AssetContent{}, &generation.Error{
			Code: generation.ErrNotFound, Operation: "open_asset",
		}
	}
	sum := sha256.Sum256(blob.data)
	return generation.AssetContent{
		Body:      io.NopCloser(bytes.NewReader(blob.data)),
		MediaType: blob.mediaType,
		Size:      int64(len(blob.data)),
		SHA256:    hex.EncodeToString(sum[:]),
	}, nil
}

func (fake *fakeArchiveAssets) put(taskID, assetID, mediaType string, data []byte) {
	if fake.files == nil {
		fake.files = map[string]fakeArchiveBlob{}
	}
	fake.files[taskID+"/"+assetID] = fakeArchiveBlob{data: data, mediaType: mediaType}
}

// archiveFixture proposes three items and lets the test set their states and
// result snapshots. The proposal session and owner are owner-a / prj_1.
func archiveFixture(
	t *testing.T,
	mutate func(*catalog.AssetListArchiveSchema),
) (*assetlist.Service, *memoryRepository, assetlist.List, *fakeArchiveAssets) {
	t.Helper()
	return archiveFixtureWith(t, mutate, objPackageRules())
}

// archiveFixtureWith is archiveFixture with a chosen OBJ rule set (nil keeps a
// Catalog that declares no model delivery section).
func archiveFixtureWith(
	t *testing.T,
	mutate func(*catalog.AssetListArchiveSchema),
	rules *catalog.ModelObjPackageSchema,
) (*assetlist.Service, *memoryRepository, assetlist.List, *fakeArchiveAssets) {
	t.Helper()
	fake := &fakeArchiveAssets{}
	schema := archiveTestSchema()
	if mutate != nil {
		mutate(schema.Review.Archive)
	}
	service, repository, _ := newService(t, func(config *assetlist.Config) {
		config.Schema = schema
		config.Archive = fake
		config.ObjPackage = rules
	})
	ctx := context.Background()
	input := proposal()
	input.Items = []assetlist.ItemInput{
		{Kind: "character", Name: "Hero"},
		{Kind: "image", Name: "Icon", RouteID: "text-to-image"},
		{Kind: "image", Name: "Badge", RouteID: "text-to-image"},
	}
	list, err := service.Propose(ctx, input)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := service.Decide(ctx, assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: list.ID,
		Decision: assetlist.DecisionConfirm,
	}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return service, repository, list, fake
}

// reviewJSON is the snapshot of one item with a GLB and a preview picture.
func reviewJSON(t *testing.T, taskID, modelID, imageID string) json.RawMessage {
	t.Helper()
	snapshot, err := json.Marshal(assetlist.ReviewSnapshot{
		TaskID: taskID,
		Artifacts: []assetlist.ReviewArtifact{
			{AssetID: imageID, Role: "preview", MediaType: "image/png", Size: 9, SHA256: "img-sha"},
			{AssetID: modelID, Role: "model", MediaType: "model/gltf-binary", Size: 20, SHA256: "model-sha"},
		},
		Model3D: &assetlist.ReviewModel3D{
			AssetID: modelID,
			Checks: []assetlist.ReviewCheck{
				{Check: "triangles", Status: "pass", Actual: float64(9000), Budget: "max 20000"},
				{Check: "texture_size", Status: "pass", Actual: float64(2048), Budget: "max 2048"},
			},
		},
		Image2D: &assetlist.ReviewImage2D{
			AssetID: imageID, Width: 512, Height: 512,
			Checks: []assetlist.ReviewCheck{{Check: "readable", Status: "pass"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// archiveBytes writes the prepared archive into memory and opens it with the
// standard library, proving a normal unzip reads it.
func archiveBytes(t *testing.T, archive *assetlist.Archive) (*zip.Reader, []byte) {
	t.Helper()
	var buffer bytes.Buffer
	if _, err := archive.WriteTo(&buffer); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	return reader, buffer.Bytes()
}

func zipEntry(t *testing.T, reader *zip.Reader, name string) []byte {
	t.Helper()
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		body, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer body.Close()
		data, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return data
	}
	t.Fatalf("entry %s missing; archive has %v", name, zipNames(reader))
	return nil
}

func zipNames(reader *zip.Reader) []string {
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	return names
}

func TestArchivePacksOnlyAcceptedItems(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	fake.put("task_hero", "asset_model_hero", "model/gltf-binary", []byte("hero-glb-bytes"))
	fake.put("task_hero", "asset_image_hero", "image/png", []byte("hero-png"))
	items := repository.items[list.ID]
	items[0].State = assetlist.StateAccepted
	items[0].Review = reviewJSON(t, "task_hero", "asset_model_hero", "asset_image_hero")
	items[1].State = assetlist.StateReview
	items[1].Review = reviewJSON(t, "task_icon", "asset_model_icon", "asset_image_icon")
	items[2].State = assetlist.StateFailed
	items[2].Review = reviewJSON(t, "task_badge", "asset_model_badge", "asset_image_badge")
	repository.items[list.ID] = items

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	if archive.ItemCount != 1 || archive.Entries != 4 {
		t.Fatalf("archive counts = %d items / %d entries", archive.ItemCount, archive.Entries)
	}
	reader, _ := archiveBytes(t, archive)
	names := zipNames(reader)
	for _, name := range names {
		if strings.HasPrefix(name, "02-") || strings.HasPrefix(name, "03-") {
			t.Fatalf("non-accepted item packed: %v", names)
		}
	}
	if !containsName(names, "01-Hero/model.glb") || !containsName(names, "README.txt") ||
		!containsName(names, "manifest.json") {
		t.Fatalf("archive names = %v", names)
	}
	for _, key := range fake.opens {
		if strings.HasPrefix(key, "task_icon/") || strings.HasPrefix(key, "task_badge/") {
			t.Fatalf("opened a non-accepted item's file: %v", fake.opens)
		}
	}
}

func TestArchiveSanitizesAndDeduplicatesDirectoryNames(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	items := repository.items[list.ID]
	items[0].Name = "Hero/红   "
	items[0].State = assetlist.StateAccepted
	items[0].Review = reviewJSON(t, "task_1", "model_1", "image_1")
	items[1].Name = " Hero\\红\u0000 "
	items[1].State = assetlist.StateAccepted
	items[1].Review = reviewJSON(t, "task_2", "model_2", "image_2")
	items[2].State = assetlist.StateSkipped
	repository.items[list.ID] = items
	fake.put("task_1", "model_1", "model/gltf-binary", []byte("one"))
	fake.put("task_1", "image_1", "image/png", []byte("pic-one"))
	fake.put("task_2", "model_2", "model/gltf-binary", []byte("two"))
	fake.put("task_2", "image_2", "image/png", []byte("pic-two"))

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	reader, _ := archiveBytes(t, archive)
	names := zipNames(reader)
	for _, wanted := range []string{
		"01-Hero-红/model.glb", "02-Hero-红-2/model.glb",
	} {
		if !containsName(names, wanted) {
			t.Fatalf("directory %q missing; archive has %v", wanted, names)
		}
	}
	for _, name := range names {
		segment := strings.SplitN(name, "/", 2)[0]
		if strings.ContainsAny(segment, "/\\\x00") {
			t.Fatalf("unsafe directory segment %q", segment)
		}
	}
}

func TestArchiveManifestRepeatsBoardFacts(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	items := repository.items[list.ID]
	items[0].State = assetlist.StateAccepted
	items[0].Round = 2
	items[0].Review = reviewJSON(t, "task_hero", "asset_model", "asset_image")
	repository.items[list.ID] = items
	fake.put("task_hero", "asset_model", "model/gltf-binary", []byte("model-bytes"))
	fake.put("task_hero", "asset_image", "image/png", []byte("picture"))

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	reader, _ := archiveBytes(t, archive)
	var manifest assetlist.ArchiveManifest
	if err := json.Unmarshal(zipEntry(t, reader, "manifest.json"), &manifest); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if manifest.ListID != list.ID || len(manifest.Items) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	item := manifest.Items[0]
	if item.Name != "Hero" || item.Kind != "character" || item.Round != 2 ||
		item.Position != 1 || item.TaskID != "task_hero" || item.Directory != "01-Hero" {
		t.Fatalf("manifest item = %+v", item)
	}
	if item.Triangles == nil || *item.Triangles != 9000 ||
		item.TextureMaxEdge == nil || *item.TextureMaxEdge != 2048 {
		t.Fatalf("manifest facts = %+v", item)
	}
	if len(item.Formats) != 2 || item.Formats[0] != "png" || item.Formats[1] != "glb" {
		t.Fatalf("manifest formats = %v", item.Formats)
	}
	if len(item.Files) != 2 {
		t.Fatalf("manifest files = %+v", item.Files)
	}
	var model *assetlist.ArchiveManifestFile
	for index := range item.Files {
		if item.Files[index].Path == "01-Hero/model.glb" {
			model = &item.Files[index]
		}
	}
	if model == nil || model.Role != "model" || model.Format != "glb" ||
		model.Size != int64(len("model-bytes")) || model.SHA256 != "model-sha" {
		t.Fatalf("manifest model file = %+v", item.Files)
	}
	if len(item.Checks) < 3 || item.Checks[0].Check != "triangles" || item.Checks[0].Status != "pass" {
		t.Fatalf("manifest checks = %+v", item.Checks)
	}
	// The packed bytes are the stored bytes, byte for byte.
	if got := zipEntry(t, reader, "01-Hero/model.glb"); !bytes.Equal(got, []byte("model-bytes")) {
		t.Fatalf("packed model = %q", got)
	}
	if got := zipEntry(t, reader, "01-Hero/other/preview-1.png"); !bytes.Equal(got, []byte("picture")) {
		t.Fatalf("packed picture = %q", got)
	}
	readme := string(zipEntry(t, reader, "README.txt"))
	for _, want := range []string{"Hero", "面数：9000", "贴图最大边：2048", "model-sha"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README misses %q:\n%s", want, readme)
		}
	}
}

func TestArchiveFallsBackToResultSnapshot(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	items := repository.items[list.ID]
	items[0].State = assetlist.StateAccepted
	items[0].Review = nil
	items[0].Result = json.RawMessage(`{"task_id":"task_1","assets":[{"id":"img_1","name":"preview/png","role":"preview"}]}`)
	repository.items[list.ID] = items
	fake.put("task_1", "img_1", "image/png", []byte("fallback-picture"))

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	reader, _ := archiveBytes(t, archive)
	if got := zipEntry(t, reader, "01-Hero/image.png"); !bytes.Equal(got, []byte("fallback-picture")) {
		t.Fatalf("fallback picture = %q", got)
	}
}

func TestArchiveRefusesWithoutAcceptedItems(t *testing.T) {
	t.Parallel()
	service, repository, list, _ := archiveFixture(t, nil)
	items := repository.items[list.ID]
	for index := range items {
		items[index].State = assetlist.StateReview
	}
	repository.items[list.ID] = items
	if _, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil); !errors.Is(err, assetlist.ErrArchiveEmpty) {
		t.Fatalf("empty archive error = %v", err)
	}
	// Selecting an accepted item's id does not make a non-accepted item packable.
	items[0].State = assetlist.StateAccepted
	items[0].Review = reviewJSON(t, "task_1", "model_1", "image_1")
	repository.items[list.ID] = items
	if _, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID,
		[]string{items[1].ID}); !errors.Is(err, assetlist.ErrArchiveEmpty) {
		t.Fatalf("non-accepted selection error = %v", err)
	}
}

func TestArchiveIsOwnerScoped(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	items := repository.items[list.ID]
	items[0].State = assetlist.StateAccepted
	items[0].Review = reviewJSON(t, "task_1", "model_1", "image_1")
	repository.items[list.ID] = items
	fake.put("task_1", "model_1", "model/gltf-binary", []byte("one"))
	fake.put("task_1", "image_1", "image/png", []byte("pic"))
	ctx := context.Background()
	if _, err := service.PrepareArchive(ctx, "owner-b", "prj_1", list.ID, nil); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("foreign owner error = %v", err)
	}
	if _, err := service.PrepareArchive(ctx, "owner-a", "prj_2", list.ID, nil); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("wrong project error = %v", err)
	}
	if _, err := service.PrepareArchive(ctx, "owner-a", "prj_1", list.ID, []string{"aitem_other"}); !errors.Is(err, assetlist.ErrNotFound) {
		t.Fatalf("unknown item error = %v", err)
	}
	if len(fake.opens) != 0 {
		t.Fatalf("opened files before the ownership check: %v", fake.opens)
	}
}

func TestArchivePrecheckRefusesOversizedSelections(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, func(archive *catalog.AssetListArchiveSchema) {
		archive.MaxBytes = 10
	})
	items := repository.items[list.ID]
	items[0].State = assetlist.StateAccepted
	items[0].Review = reviewJSON(t, "task_1", "model_1", "image_1") // 20 + 9 recorded bytes
	repository.items[list.ID] = items
	fake.put("task_1", "model_1", "model/gltf-binary", []byte("model-bytes"))
	fake.put("task_1", "image_1", "image/png", []byte("picture"))
	if _, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil); !errors.Is(err, assetlist.ErrArchiveTooLarge) {
		t.Fatalf("oversized bytes error = %v", err)
	}
	if len(fake.opens) != 0 {
		t.Fatalf("opened files before the byte precheck: %v", fake.opens)
	}

	entriesService, entriesRepository, entriesList, _ := archiveFixture(t, func(archive *catalog.AssetListArchiveSchema) {
		archive.MaxEntries = 3 // manifest + README + one file
	})
	entries := entriesRepository.items[entriesList.ID]
	entries[0].State = assetlist.StateAccepted
	entries[0].Review = reviewJSON(t, "task_1", "model_1", "image_1")
	entriesRepository.items[entriesList.ID] = entries
	if _, err := entriesService.PrepareArchive(context.Background(), "owner-a", "prj_1", entriesList.ID, nil); !errors.Is(err, assetlist.ErrArchiveTooLarge) {
		t.Fatalf("oversized entries error = %v", err)
	}
}

func TestArchiveNarrowsToRequestedItems(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	items := repository.items[list.ID]
	items[0].State = assetlist.StateAccepted
	items[0].Review = reviewJSON(t, "task_1", "model_1", "image_1")
	items[1].State = assetlist.StateAccepted
	items[1].Review = reviewJSON(t, "task_2", "model_2", "image_2")
	items[2].State = assetlist.StateSkipped
	repository.items[list.ID] = items
	fake.put("task_1", "model_1", "model/gltf-binary", []byte("one"))
	fake.put("task_1", "image_1", "image/png", []byte("pic-one"))
	fake.put("task_2", "model_2", "model/gltf-binary", []byte("two"))
	fake.put("task_2", "image_2", "image/png", []byte("pic-two"))

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID,
		[]string{items[1].ID})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	if archive.ItemCount != 1 {
		t.Fatalf("item count = %d", archive.ItemCount)
	}
	reader, _ := archiveBytes(t, archive)
	names := zipNames(reader)
	if !containsName(names, "02-Icon/model.glb") || containsName(names, "01-Hero/model.glb") {
		t.Fatalf("archive names = %v", names)
	}
}

func containsName(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}
