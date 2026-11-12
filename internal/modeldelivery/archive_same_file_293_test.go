package modeldelivery

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// #293: Tripo fulfils the model role with its GLTF export, which is a GLB, so a
// task keeps two records over the same bytes: the model and converted.gltf. Both
// match the GLB format, a format row takes only one of them, and the other used to
// be written once more under other/. The one-click ZIP now holds the file once and
// says which slot it is the same as; each record still downloads on its own.

const (
	sharedGLB = "SHARED-GLB"
	gameFBX   = "FBX-BYTES"
)

// tripoGameTask is a Tripo task of a game purpose: the model, converted.gltf (the
// GLB the model is made of, or other bytes when the test needs them) and the FBX.
func (f *fixture) tripoGameTask(id, gltfBody string) generation.Task {
	return f.addTask(id, "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary", body: sharedGLB},
		assetSpec{role: "converted_model", variant: "converted.gltf", mediaType: "model/gltf-binary", body: gltfBody},
		assetSpec{role: "converted_model", variant: "converted.fbx", mediaType: "application/octet-stream", body: gameFBX})
}

// missingFormats is the README's "formats not included" section: what follows its
// heading, or nothing when there is none.
func missingFormats(readme string) string {
	_, section, _ := strings.Cut(readme, "未包含的格式：\n")
	return section
}

// zipOf prepares the task's archive and reads it whole. entries and size are what
// the audit and the response report.
func zipOf(t *testing.T, f *fixture, id generation.TaskID) (files map[string]string, entries int, size int64) {
	t.Helper()
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	entries, size = archive.Entries, archive.Bytes
	return readZip(t, archive), entries, size
}

func TestArchiveHoldsAFileTwoSlotsShareOnce(t *testing.T) {
	f := newFixture(t)
	task := f.tripoGameTask("tripo-game", sharedGLB)

	files, entries, size := zipOf(t, f, task.ID)

	if got := keysOf(files); !slices.Equal(got, []string{"README.txt", "model.fbx", "model.glb"}) {
		t.Fatalf("entries = %v, want the GLB once, the FBX and the README", got)
	}
	if files["model.glb"] != sharedGLB || files["model.fbx"] != gameFBX {
		t.Fatalf("bytes: glb %q fbx %q", files["model.glb"], files["model.fbx"])
	}
	// The counts are the files actually packed.
	if want := int64(len(sharedGLB) + len(gameFBX) + len(files["README.txt"])); entries != 3 || size != want {
		t.Fatalf("entries = %d, bytes = %d; want 3 and %d", entries, size, want)
	}
	readme := files["README.txt"]
	for _, want := range []string{
		"内容相同的文件：\n- converted.gltf 与 model.glb 内容相同，只收录一份\n",
		"- GLB：model.glb\n", "- FBX：model.fbx\n",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README lacks %q:\n%s", want, readme)
		}
	}
	if strings.Contains(readme, "其他文件") || strings.Contains(readme, "other/") {
		t.Fatalf("README still lists an attachment:\n%s", readme)
	}
	// The omitted slot is not a missing format; the package is complete for the GLB and the FBX.
	if missing := missingFormats(readme); strings.Contains(missing, "- GLB：") || strings.Contains(missing, "- FBX：") {
		t.Fatalf("README reports a delivered format as missing:\n%s", readme)
	}

	// Every record still downloads on its own and the GLB row points at one of them.
	for _, asset := range task.Assets {
		content, err := f.OpenAssetScoped(context.Background(), generation.OwnTasks("owner"), task.ID, asset.ID)
		if err != nil {
			t.Fatalf("%s/%s is not downloadable: %v", asset.Role, asset.Variant, err)
		}
		data, _ := io.ReadAll(content.Body)
		_ = content.Body.Close()
		if len(data) == 0 {
			t.Fatalf("%s/%s downloads nothing", asset.Role, asset.Variant)
		}
	}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row := formatByID(t, coverage, "glb"); row.Status != StatusAvailable || row.AssetID == "" || !coverage.ArchiveAvailable {
		t.Fatalf("glb row = %#v, archive available = %v", row, coverage.ArchiveAvailable)
	}
}

func TestArchiveKeepsAnAttachmentWhoseBytesDiffer(t *testing.T) {
	f := newFixture(t)
	task := f.tripoGameTask("tripo-game", "A-DIFFERENT-GLB")

	files, entries, _ := zipOf(t, f, task.ID)

	if got := keysOf(files); !slices.Equal(got, []string{"README.txt", "model.fbx", "model.glb", "other/converted_model-converted.gltf.glb"}) {
		t.Fatalf("entries = %v", got)
	}
	if files["other/converted_model-converted.gltf.glb"] != "A-DIFFERENT-GLB" || entries != 4 {
		t.Fatalf("attachment = %q, entries = %d", files["other/converted_model-converted.gltf.glb"], entries)
	}
	readme := files["README.txt"]
	if !strings.Contains(readme, "其他文件：\n- other/converted_model-converted.gltf.glb\n") || strings.Contains(readme, "内容相同的文件") {
		t.Fatalf("README = \n%s", readme)
	}
}

// A historical record may carry no digest. Equal bytes prove nothing then, and a
// file is never left out on a guess.
func TestArchiveDoesNotLeaveOutAFileWithoutADigest(t *testing.T) {
	for name, specs := range map[string][2]bool{
		"neither record has a digest":   {true, true},
		"only the attachment has none":  {false, true},
		"only the packed file has none": {true, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			task := f.addTask("old", "owner", "tripo", "text-to-3d",
				assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary", body: sharedGLB, noDigest: specs[0]},
				assetSpec{role: "converted_model", variant: "converted.gltf", mediaType: "model/gltf-binary", body: sharedGLB, noDigest: specs[1]})

			files, _, _ := zipOf(t, f, task.ID)

			if _, packed := files["other/converted_model-converted.gltf.glb"]; !packed || strings.Contains(files["README.txt"], "内容相同的文件") {
				t.Fatalf("entries = %v\n%s", keysOf(files), files["README.txt"])
			}
		})
	}
}

// Only attachments are compared. A target format is always a file of its own in
// the package, even when another format holds the same bytes, and an attachment
// can repeat an earlier attachment.
func TestArchiveLeavesOutOnlyAttachments(t *testing.T) {
	f := newFixture(t)
	formats := f.addTask("same-formats", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary", body: "ONE"},
		assetSpec{role: "model", variant: "fbx", mediaType: "application/octet-stream", body: "ONE"})
	files, _, _ := zipOf(t, f, formats.ID)
	if got := keysOf(files); !slices.Equal(got, []string{"README.txt", "model.fbx", "model.glb"}) || strings.Contains(files["README.txt"], "内容相同的文件") {
		t.Fatalf("two formats with the same bytes: entries = %v\n%s", got, files["README.txt"])
	}

	attachments := f.addTask("attachments", "owner", "meshy", "text-to-3d",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary", body: "GLB"},
		assetSpec{role: "converted_model", variant: "extra-1", mediaType: "application/octet-stream", body: "X"},
		assetSpec{role: "converted_model", variant: "extra-2", mediaType: "application/octet-stream", body: "X"},
		assetSpec{role: "converted_model", variant: "extra-3", mediaType: "application/octet-stream", body: "GLB"})
	files, _, _ = zipOf(t, f, attachments.ID)
	if got := keysOf(files); !slices.Equal(got, []string{"README.txt", "model.glb", "other/converted_model-extra-1.bin"}) {
		t.Fatalf("entries = %v", got)
	}
	// extra-2 repeats extra-1; extra-3 repeats the GLB format file.
	for _, want := range []string{
		"- extra-2 与 other/converted_model-extra-1.bin 内容相同，只收录一份\n",
		"- extra-3 与 model.glb 内容相同，只收录一份\n",
	} {
		if !strings.Contains(files["README.txt"], want) {
			t.Fatalf("README lacks %q:\n%s", want, files["README.txt"])
		}
	}
}

// The record left out holds the same bytes, so it stands in when the record the
// package names cannot be read: the file is in the ZIP whenever either reads.
func TestArchiveFallsBackToARecordThatRepeatsAnUnreadableFile(t *testing.T) {
	f := newFixture(t)
	task := f.tripoGameTask("tripo-game", sharedGLB)
	f.unreadable[task.Assets[0].ID] = true // the model record, which the GLB row takes

	files, entries, _ := zipOf(t, f, task.ID)

	if files["model.glb"] != sharedGLB || entries != 3 {
		t.Fatalf("the GLB did not come from the record that repeats it: entries = %v", keysOf(files))
	}
	readme := files["README.txt"]
	if !strings.Contains(readme, "- GLB：model.glb\n") || !strings.Contains(readme, "- converted.gltf 与 model.glb 内容相同，只收录一份\n") ||
		strings.Contains(missingFormats(readme), "- GLB：") {
		t.Fatalf("README = \n%s", readme)
	}
}

// The stand-in is read from the Task it belongs to, not from the Task of the file it
// replaces: a format obtained from a finished completion plan lives in the plan's
// result Task, while the attachment that repeats it is a file of the source Task.
func TestArchiveFallbackReadsTheStandInFromItsOwnTask(t *testing.T) {
	f := newFixture(t)
	const gltfJSON = `{"asset":{"version":"2.0"}}`
	specs := []assetSpec{{role: "model", variant: "", mediaType: "model/gltf-binary"}}
	for _, format := range []string{"gltf", "usdz", "fbx", "obj", "stl", "3mf"} {
		media := "application/octet-stream"
		if format == "gltf" {
			media = "model/gltf-binary" // the binary container, so the glTF row is still to be obtained
		}
		specs = append(specs, assetSpec{role: "converted_model", variant: "converted." + format, mediaType: media})
	}
	specs = append(specs, assetSpec{role: "converted_model", variant: "extra", mediaType: "application/octet-stream", body: gltfJSON})
	f.addTask("tripo-task", "owner", "tripo", "text-to-3d", specs...)
	if _, err := f.service.Complete(context.Background(), "owner", "tripo-task"); err != nil {
		t.Fatal(err)
	}
	result := f.addTask("gltf-task", "owner", "local", "format-conversion",
		assetSpec{role: "model", variant: "gltf", mediaType: "model/gltf+json", body: gltfJSON})
	for id, plan := range f.store.plans {
		plan.State, plan.Steps[0].TaskID = processing.Succeeded, result.ID
		f.store.plans[id] = plan
	}
	f.unreadable[result.Assets[0].ID] = true // the obtained glTF, which the package names

	files, _, _ := zipOf(t, f, "tripo-task")

	if files["model.gltf"] != gltfJSON {
		t.Fatalf("the glTF did not come from the repeat in the source Task: entries = %v", keysOf(files))
	}
	if readme := files["README.txt"]; !strings.Contains(readme, "- glTF：model.gltf\n") || !strings.Contains(readme, "- extra 与 model.gltf 内容相同，只收录一份\n") {
		t.Fatalf("README = \n%s", readme)
	}
}

// When no record of the file can be read, the README reports the format as missing
// and names no repeat of a file that is not in the package.
func TestArchiveNamesNoRepeatOfAFileNoRecordCouldRead(t *testing.T) {
	f := newFixture(t)
	task := f.tripoGameTask("tripo-game", sharedGLB)
	f.unreadable[task.Assets[0].ID] = true
	f.unreadable[task.Assets[1].ID] = true

	files, _, _ := zipOf(t, f, task.ID)

	if _, packed := files["model.glb"]; packed {
		t.Fatalf("an unreadable file was packed: %v", keysOf(files))
	}
	if readme := files["README.txt"]; strings.Contains(readme, "内容相同的文件") || !strings.Contains(readme, "未包含的格式：\n- GLB：") {
		t.Fatalf("README = \n%s", readme)
	}
}

// The availability check counts what Prepare packs, so a package that fits only
// because a repeat is left out is not advertised as too large.
func TestArchiveAvailabilityDoesNotCountALeftOutRepeat(t *testing.T) {
	f := newFixture(t)
	task := f.tripoGameTask("tripo-game", sharedGLB)

	packed := f.service.archiveAssets(f.service.resolve(context.Background(), task))
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(packed) != 2 || len(archive.files) != len(packed) {
		t.Fatalf("coverage counts %d files, Prepare packs %d", len(packed), len(archive.files))
	}
	for _, file := range archive.files {
		if !slices.ContainsFunc(packed, func(asset generation.Asset) bool { return asset.ID == file.asset.ID }) {
			t.Fatalf("Prepare packed %s, which coverage did not count", file.asset.ID)
		}
	}

	f.service.schema.Archive.MaxBytes = int64(len(sharedGLB) + len(gameFBX))
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !coverage.ArchiveAvailable {
		t.Fatalf("the package fits its bound without the repeat: %#v", coverage)
	}
	f.service.schema.Archive.MaxBytes--
	if coverage, err = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID); err != nil || coverage.ArchiveAvailable {
		t.Fatalf("one byte over the bound is still advertised: %#v (err=%v)", coverage, err)
	}
}

// The behaviour is declared with its README text: a Schema that names neither packs
// every attachment, as it did before, and never leaves a file out unannounced.
func TestWithoutTheSameFileTextEveryAttachmentIsPacked(t *testing.T) {
	f := newFixture(t)
	f.service.schema.Archive.Readme.SameHeading = ""
	f.service.schema.Archive.Readme.SameLine = ""
	task := f.tripoGameTask("tripo-game", sharedGLB)

	files, entries, _ := zipOf(t, f, task.ID)

	if _, packed := files["other/converted_model-converted.gltf.glb"]; !packed || entries != 4 || strings.Contains(files["README.txt"], "内容相同的文件") {
		t.Fatalf("entries = %v\n%s", keysOf(files), files["README.txt"])
	}
	if packed := f.service.archiveAssets(f.service.resolve(context.Background(), task)); len(packed) != 3 {
		t.Fatalf("coverage counts %d files, want all three", len(packed))
	}
}

func TestSameFileReadmeFollowsTheRequestLocale(t *testing.T) {
	f := newFixture(t)
	task := f.tripoGameTask("tripo-game", sharedGLB)
	application := f.catalog.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Localize(texts)
	english := application.Localization.Bundles["en"].Fields

	archive, err := f.service.Prepare(catalog.WithLocale(context.Background(), "en"), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	readme := readZip(t, archive)["README.txt"]
	archive.Close()

	heading := english["model_delivery.archive.readme.same_heading"]
	line := strings.NewReplacer("{label}", "converted.gltf", "{path}", "model.glb").Replace(english["model_delivery.archive.readme.same_line"])
	if heading == "" || line == "" || !strings.Contains(readme, heading+"\n"+line+"\n") {
		t.Fatalf("English README lacks the section %q / %q:\n%s", heading, line, readme)
	}
	if strings.Contains(readme, application.ModelDelivery.Archive.Readme.SameHeading) {
		t.Fatalf("English README kept the Chinese heading:\n%s", readme)
	}
}
