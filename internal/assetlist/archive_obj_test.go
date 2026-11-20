package assetlist_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// objPackageRules is the model-delivery section the batch archive reuses.
func objPackageRules() *catalog.ModelObjPackageSchema {
	return &catalog.ModelObjPackageSchema{
		FormatID:              "obj",
		LibraryStatement:      "mtllib",
		MaterialFileExtension: ".mtl",
		ReferenceScanBytes:    1 << 20,
		TextureBindings: []catalog.ModelTextureBinding{
			{VariantKey: "base_color", Statements: []string{"map_kd", "map_ka"}},
			{VariantKey: "png", Statements: []string{"map_kd"}},
		},
	}
}

// acceptedItem marks one list item accepted with the given snapshot.
func acceptedItem(t *testing.T, repository *memoryRepository, list assetlist.List,
	index int, snapshot json.RawMessage,
) []assetlist.Item {
	t.Helper()
	items := repository.items[list.ID]
	items[index].State = assetlist.StateAccepted
	items[index].Review = snapshot
	repository.items[list.ID] = items
	return items
}

func TestArchiveKeepsTheObjMaterialAndTextureReferences(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	const object = "# triangle\nmtllib temp.mtl\no Mesh\nv 0 0 0\nv 1 0 0\nv 0 1 0\nusemtl Material\nf 1 2 3\n"
	const library = "newmtl Material\nmap_Kd wood.png\n"
	snapshot, err := json.Marshal(assetlist.ReviewSnapshot{
		TaskID: "task_obj",
		Artifacts: []assetlist.ReviewArtifact{
			{AssetID: "asset_obj", Role: "model", Variant: "obj", MediaType: "model/obj"},
			{AssetID: "asset_mtl", Role: "material", Variant: "mtl", MediaType: "model/mtl"},
			{AssetID: "asset_wood", Role: "texture", Variant: "base_color.png", MediaType: "image/png"},
			{AssetID: "asset_preview", Role: "preview", MediaType: "image/png"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	acceptedItem(t, repository, list, 0, snapshot)
	fake.put("task_obj", "asset_obj", "model/obj", []byte(object))
	fake.put("task_obj", "asset_mtl", "model/mtl", []byte(library))
	fake.put("task_obj", "asset_wood", "image/png", []byte("wood-bytes"))
	fake.put("task_obj", "asset_preview", "image/png", []byte("preview-bytes"))

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	reader, _ := archiveBytes(t, archive)
	names := zipNames(reader)
	// The OBJ names "temp.mtl" and the library names "wood.png": the packed
	// files keep those names, so the extracted OBJ still loads.
	for _, wanted := range []string{
		"01-Hero/obj/model.obj", "01-Hero/obj/temp.mtl", "01-Hero/obj/wood.png",
	} {
		if !containsName(names, wanted) {
			t.Fatalf("entry %q missing; archive has %v", wanted, names)
		}
	}
	if got := zipEntry(t, reader, "01-Hero/obj/model.obj"); !bytes.Equal(got, []byte(object)) {
		t.Fatalf("OBJ bytes changed:\n%q", got)
	}
	if got := zipEntry(t, reader, "01-Hero/obj/temp.mtl"); !bytes.Equal(got, []byte(library)) {
		t.Fatalf("material bytes changed:\n%q", got)
	}
	if got := zipEntry(t, reader, "01-Hero/obj/wood.png"); !bytes.Equal(got, []byte("wood-bytes")) {
		t.Fatalf("texture bytes = %q", got)
	}
	var manifest assetlist.ArchiveManifest
	if err := json.Unmarshal(zipEntry(t, reader, "manifest.json"), &manifest); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(manifest.Items) != 1 || manifest.Items[0].Directory != "01-Hero" {
		t.Fatalf("manifest = %+v", manifest)
	}
	formats := strings.Join(manifest.Items[0].Formats, ",")
	for _, wanted := range []string{"obj", "mtl", "png"} {
		if !strings.Contains(formats, wanted) {
			t.Fatalf("manifest formats = %q; want %q", formats, wanted)
		}
	}
	var material, texture *assetlist.ArchiveManifestFile
	for index := range manifest.Items[0].Files {
		file := &manifest.Items[0].Files[index]
		switch file.Path {
		case "01-Hero/obj/temp.mtl":
			material = file
		case "01-Hero/obj/wood.png":
			texture = file
		}
	}
	if material == nil || material.Format != "mtl" || material.Role != "material" {
		t.Fatalf("material manifest row = %+v", material)
	}
	if texture == nil || texture.Format != "png" || texture.Role != "texture" {
		t.Fatalf("texture manifest row = %+v", texture)
	}
}

func TestArchiveNamesAPackagedObjAsObjZip(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	payload := []byte("zip-of-obj")
	snapshot, err := json.Marshal(assetlist.ReviewSnapshot{
		TaskID: "task_tripo",
		Artifacts: []assetlist.ReviewArtifact{
			{AssetID: "asset_obj_zip", Role: "model", Variant: "obj", MediaType: "application/zip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	acceptedItem(t, repository, list, 0, snapshot)
	fake.put("task_tripo", "asset_obj_zip", "application/zip", payload)

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	reader, _ := archiveBytes(t, archive)
	names := zipNames(reader)
	// A provider-native OBJ package keeps its ZIP bytes and says what it is:
	// model.obj.zip, not an OBJ extension the bytes do not have.
	if !containsName(names, "01-Hero/model.obj.zip") {
		t.Fatalf("archive names = %v; want model.obj.zip", names)
	}
	if got := zipEntry(t, reader, "01-Hero/model.obj.zip"); !bytes.Equal(got, payload) {
		t.Fatalf("packaged OBJ bytes changed: %q", got)
	}
	var manifest assetlist.ArchiveManifest
	if err := json.Unmarshal(zipEntry(t, reader, "manifest.json"), &manifest); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(manifest.Items) != 1 || len(manifest.Items[0].Formats) != 1 ||
		manifest.Items[0].Formats[0] != "obj.zip" {
		t.Fatalf("manifest formats = %+v", manifest.Items)
	}
	// The package is one file: the OBJ inside was never opened for scanning.
	for _, key := range fake.opens {
		if key != "task_tripo/asset_obj_zip" {
			t.Fatalf("opened %q; want only the package itself", fake.opens)
		}
	}
}

func TestArchiveNeverUsesASemanticVariantAsExtension(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	snapshot, err := json.Marshal(assetlist.ReviewSnapshot{
		TaskID: "task_1",
		Artifacts: []assetlist.ReviewArtifact{
			// A preview slot's variant is "front": a slot name, not a format.
			{AssetID: "asset_front", Role: "preview", Variant: "front", MediaType: "image/png"},
			{AssetID: "asset_glb", Role: "model", Variant: "primary", MediaType: "model/gltf-binary"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	acceptedItem(t, repository, list, 0, snapshot)
	fake.put("task_1", "asset_front", "image/png", []byte("front-picture"))
	fake.put("task_1", "asset_glb", "model/gltf-binary", []byte("glb-bytes"))

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	names := zipNames(after(t, archive))
	if !containsName(names, "01-Hero/model.glb") || !containsName(names, "01-Hero/other/preview-1.png") {
		t.Fatalf("archive names = %v; want media-type extensions", names)
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".front") || strings.HasSuffix(name, ".primary") {
			t.Fatalf("semantic variant became an extension: %v", names)
		}
	}
}

func TestArchiveRecordsRecordedFilesTheStoreNoLongerHas(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	acceptedItem(t, repository, list, 0, reviewJSON(t, "task_1", "model_1", "image_1"))
	fake.put("task_1", "model_1", "model/gltf-binary", []byte("model-bytes"))
	// image_1 is recorded in the snapshot but the store no longer has it.

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	reader, _ := archiveBytes(t, archive)
	if !containsName(zipNames(reader), "01-Hero/model.glb") {
		t.Fatalf("archive names = %v; want the readable file packed", zipNames(reader))
	}
	var manifest assetlist.ArchiveManifest
	if err := json.Unmarshal(zipEntry(t, reader, "manifest.json"), &manifest); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(manifest.Items) != 1 || len(manifest.Items[0].Missing) != 1 ||
		manifest.Items[0].Missing[0].AssetID != "image_1" ||
		manifest.Items[0].Missing[0].Role != "preview" {
		t.Fatalf("manifest missing = %+v", manifest.Items)
	}
	readme := string(zipEntry(t, reader, "README.txt"))
	if !strings.Contains(readme, "image_1") || !strings.Contains(readme, "缺失") {
		t.Fatalf("README does not name the missing file:\n%s", readme)
	}
}

func TestArchiveRefusesWhenTheStoreCannotBeRead(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixture(t, nil)
	acceptedItem(t, repository, list, 0, reviewJSON(t, "task_1", "model_1", "image_1"))
	fake.put("task_1", "model_1", "model/gltf-binary", []byte("model-bytes"))
	fake.put("task_1", "image_1", "image/png", []byte("picture"))
	fake.fail = errors.New("storage unavailable")

	// A store failure is a server failure, never a quietly short ZIP.
	if _, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil); err == nil {
		t.Fatal("PrepareArchive answered an archive for an unreadable store")
	}
}

// after writes the archive once so its entries can be read back.
func after(t *testing.T, archive *assetlist.Archive) *zip.Reader {
	t.Helper()
	reader, _ := archiveBytes(t, archive)
	return reader
}

// A Catalog without the model delivery section keeps a usable layout: the OBJ
// and its material are still packed, under default names.
func TestArchivePacksAnObjWithoutTheModelDeliveryRules(t *testing.T) {
	t.Parallel()
	service, repository, list, fake := archiveFixtureWith(t, nil, nil)
	snapshot, err := json.Marshal(assetlist.ReviewSnapshot{
		TaskID: "task_obj",
		Artifacts: []assetlist.ReviewArtifact{
			{AssetID: "asset_obj", Role: "model", Variant: "obj", MediaType: "model/obj"},
			{AssetID: "asset_mtl", Role: "material", Variant: "mtl", MediaType: "model/mtl"},
			{AssetID: "asset_png", Role: "texture", Variant: "png", MediaType: "image/png"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	acceptedItem(t, repository, list, 0, snapshot)
	fake.put("task_obj", "asset_obj", "model/obj", []byte("mtllib temp.mtl\n"))
	fake.put("task_obj", "asset_mtl", "model/mtl", []byte("newmtl M\n"))
	fake.put("task_obj", "asset_png", "image/png", []byte("png-bytes"))

	archive, err := service.PrepareArchive(context.Background(), "owner-a", "prj_1", list.ID, nil)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer archive.Close()
	names := zipNames(after(t, archive))
	for _, wanted := range []string{
		"01-Hero/obj/model.obj", "01-Hero/obj/model.mtl", "01-Hero/obj/texture-1.png",
	} {
		if !containsName(names, wanted) {
			t.Fatalf("entry %q missing; archive has %v", wanted, names)
		}
	}
}
