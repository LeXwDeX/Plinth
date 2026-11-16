package administration

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/samebytes"
)

// #300: the administration download packs a task's files like the one-click ZIP
// does: a file whose record names the same bytes as one already in the package is
// left out, and a README says which entry holds them, in the one-click ZIP's words.

const repeatHeading = "内容相同的文件："
const repeatLine = "- {label} 与 {path} 内容相同，只收录一份"

func repeatText(context.Context) samebytes.Text {
	return samebytes.Text{Filename: "README.txt", Heading: repeatHeading, Line: repeatLine}
}

const (
	glbSum   = "1111111111111111111111111111111111111111111111111111111111111111"
	fbxSum   = "2222222222222222222222222222222222222222222222222222222222222222"
	otherSum = "3333333333333333333333333333333333333333333333333333333333333333"
)

// tripoGameLedger is the ledger of a Tripo game-set task: the model and
// converted.gltf are one GLB, the FBX another file.
func tripoGameLedger() GenerationDetail {
	return GenerationDetail{Ledger: GenerationRecord{
		UserID: "user-3", TaskID: "task-3",
		Assets: []GenerationAsset{
			{ID: "model", Role: "model", MediaType: "model/gltf-binary", Ordinal: 1, SHA256: glbSum, Size: 3},
			{ID: "gltf", Role: "converted_model", Variant: "converted.gltf", MediaType: "model/gltf-binary", Ordinal: 1, SHA256: glbSum, Size: 3},
			{ID: "fbx", Role: "converted_model", Variant: "converted.fbx", MediaType: "model/vnd.fbx", Ordinal: 2, SHA256: fbxSum, Size: 3},
		},
	}}
}

// readyContents serves the bodies by asset ID; the GLB records are typed as such,
// the FBX is the generic octet-stream the CDN reports for it.
func readyContents(bodies map[string]string) *memoryGenerationAssetReader {
	reader := &memoryGenerationAssetReader{contents: map[string]GenerationAssetContent{}}
	for id, body := range bodies {
		mediaType := "application/octet-stream"
		if id == "model" || id == "gltf" {
			mediaType = "model/gltf-binary"
		}
		reader.contents[id] = GenerationAssetContent{Body: io.NopCloser(bytes.NewBufferString(body)), MediaType: mediaType, Size: int64(len(body))}
	}
	return reader
}

func newRepeatingDownloadService(t *testing.T, details GenerationDetailReader, assets GenerationAssetReader, text func(context.Context) samebytes.Text) *GenerationDownloadService {
	t.Helper()
	service := newTestGenerationDownloadService(t, details, assets, 1024)
	if text != nil {
		service.WithRepeatText(text)
	}
	return service
}

func zipFiles(t *testing.T, body []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range reader.File {
		entry, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(entry)
		_ = entry.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name] = string(content)
	}
	return files
}

func sortedKeys(files map[string]string) []string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestGenerationDownloadHoldsTheBytesTwoRecordsShareOnce(t *testing.T) {
	details := &memoryGenerationDetailReader{found: true, detail: tripoGameLedger()}
	assets := readyContents(map[string]string{"model": "GLB", "gltf": "GLB", "fbx": "FBX"})
	service := newRepeatingDownloadService(t, details, assets, repeatText)

	pkg, err := service.Download(context.Background(), "task-3")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Filename != "task-3_assets.zip" || pkg.MediaType != "application/zip" {
		t.Fatalf("package = %q %q", pkg.Filename, pkg.MediaType)
	}
	files := zipFiles(t, pkg.Body)
	// The GLB once, under the name of the record that kept it, plus the FBX and the README.
	want := map[string]string{
		"model_2.glb":                         "GLB",
		"converted_model_converted.fbx_3.bin": "FBX",
		"README.txt": repeatHeading + "\n" +
			"- converted.gltf 与 model_2.glb 内容相同，只收录一份\n",
	}
	if len(files) != len(want) {
		t.Fatalf("entries = %v, want %v", sortedKeys(files), sortedKeys(want))
	}
	for name, body := range want {
		if files[name] != body {
			t.Fatalf("%s = %q, want %q (entries %v)", name, files[name], body, sortedKeys(files))
		}
	}
	// The repeated record is never opened: its bytes are already in the package.
	if slices.Contains(assets.assetIDs, "gltf") {
		t.Fatalf("the repeated record was opened: %v", assets.assetIDs)
	}
}

// Two identical records are still two records: the download is a package that says
// so, not the one file as if the task had saved one.
func TestGenerationDownloadOfTwoIdenticalRecordsIsStillAPackage(t *testing.T) {
	detail := tripoGameLedger()
	detail.Ledger.Assets = detail.Ledger.Assets[:2]
	details := &memoryGenerationDetailReader{found: true, detail: detail}
	service := newRepeatingDownloadService(t, details, readyContents(map[string]string{"model": "GLB", "gltf": "GLB"}), repeatText)

	pkg, err := service.Download(context.Background(), "task-3")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Filename != "task-3_assets.zip" || pkg.MediaType != "application/zip" {
		t.Fatalf("a task with two records downloaded as %q (%s)", pkg.Filename, pkg.MediaType)
	}
	files := zipFiles(t, pkg.Body)
	if got := sortedKeys(files); !reflect.DeepEqual(got, []string{"README.txt", "model_2.glb"}) {
		t.Fatalf("entries = %v", got)
	}
}

func TestGenerationDownloadPacksEveryFileWithoutTheReadmeWording(t *testing.T) {
	details := &memoryGenerationDetailReader{found: true, detail: tripoGameLedger()}
	assets := readyContents(map[string]string{"model": "GLB", "gltf": "GLB", "fbx": "FBX"})
	service := newRepeatingDownloadService(t, details, assets, nil)

	pkg, err := service.Download(context.Background(), "task-3")
	if err != nil {
		t.Fatal(err)
	}
	files := zipFiles(t, pkg.Body)
	if len(files) != 3 || files["README.txt"] != "" {
		t.Fatalf("a file was left out with no README to say so: %v", sortedKeys(files))
	}
	// A wording that names no section is no wording.
	service = newRepeatingDownloadService(t, details, readyContents(map[string]string{"model": "GLB", "gltf": "GLB", "fbx": "FBX"}), func(context.Context) samebytes.Text {
		return samebytes.Text{Filename: "README.txt", Heading: repeatHeading}
	})
	pkg, err = service.Download(context.Background(), "task-3")
	if err != nil {
		t.Fatal(err)
	}
	if got := zipFiles(t, pkg.Body); len(got) != 3 {
		t.Fatalf("an unfinished wording left a file out: %v", sortedKeys(got))
	}
}

// Equal bytes prove nothing for a record that names none: a legacy source file has
// no digest, and a digest with another size is another file.
func TestGenerationDownloadNeverLeavesOutAFileOnAGuess(t *testing.T) {
	for name, mutate := range map[string]func(assets []GenerationAsset){
		"no digest on the repeat": func(assets []GenerationAsset) { assets[1].SHA256 = "" },
		"no digest on either":     func(assets []GenerationAsset) { assets[0].SHA256, assets[1].SHA256 = "", "" },
		"another size":            func(assets []GenerationAsset) { assets[1].Size = 4 },
		"another digest":          func(assets []GenerationAsset) { assets[1].SHA256 = otherSum },
		"no digest on the first":  func(assets []GenerationAsset) { assets[0].SHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			detail := tripoGameLedger()
			mutate(detail.Ledger.Assets)
			details := &memoryGenerationDetailReader{found: true, detail: detail}
			assets := readyContents(map[string]string{"model": "GLB", "gltf": "GLB", "fbx": "FBX"})
			pkg, err := newRepeatingDownloadService(t, details, assets, repeatText).Download(context.Background(), "task-3")
			if err != nil {
				t.Fatal(err)
			}
			files := zipFiles(t, pkg.Body)
			if len(files) != 3 || files["README.txt"] != "" {
				t.Fatalf("entries = %v, want all three files and no README", sortedKeys(files))
			}
		})
	}
}

// The kept record that cannot be read does not lose the bytes: the record that
// repeats it stands in, under its own name, and the README names the other.
func TestGenerationDownloadFallsBackToTheRepeatWhenTheFirstRecordCannotBeRead(t *testing.T) {
	details := &memoryGenerationDetailReader{found: true, detail: tripoGameLedger()}
	assets := readyContents(map[string]string{"gltf": "GLB", "fbx": "FBX"})
	assets.errors = map[string]error{"model": errors.New("the file is gone")}
	service := newRepeatingDownloadService(t, details, assets, repeatText)

	pkg, err := service.Download(context.Background(), "task-3")
	if err != nil {
		t.Fatal(err)
	}
	files := zipFiles(t, pkg.Body)
	if files["converted_model_converted.gltf_2.glb"] != "GLB" || files["converted_model_converted.fbx_3.bin"] != "FBX" {
		t.Fatalf("entries = %v", sortedKeys(files))
	}
	if want := repeatHeading + "\n- model 与 converted_model_converted.gltf_2.glb 内容相同，只收录一份\n"; files["README.txt"] != want {
		t.Fatalf("README = %q, want %q", files["README.txt"], want)
	}
}

// If no record of the bytes can be read the package says nothing about them.
func TestGenerationDownloadNamesNoRepeatWhenNoRecordOfItsBytesCanBeRead(t *testing.T) {
	details := &memoryGenerationDetailReader{found: true, detail: tripoGameLedger()}
	assets := readyContents(map[string]string{"fbx": "FBX"})
	assets.errors = map[string]error{"model": errors.New("gone"), "gltf": errors.New("gone")}
	pkg, err := newRepeatingDownloadService(t, details, assets, repeatText).Download(context.Background(), "task-3")
	if err != nil {
		// A single readable file of three records is still a package.
		t.Fatal(err)
	}
	files := zipFiles(t, pkg.Body)
	if got := sortedKeys(files); !reflect.DeepEqual(got, []string{"converted_model_converted.fbx_3.bin"}) {
		t.Fatalf("entries = %v, want the FBX alone", got)
	}
}

// The README must not take the name of an entry; it is the README that moves.
func TestGenerationDownloadReadmeStepsAsideForAnEntryOfTheSameName(t *testing.T) {
	detail := tripoGameLedger()
	detail.Ledger.Assets[2].PreferredFilename = "README.txt"
	details := &memoryGenerationDetailReader{found: true, detail: detail}
	assets := readyContents(map[string]string{"model": "GLB", "gltf": "GLB", "fbx": "FBX"})
	pkg, err := newRepeatingDownloadService(t, details, assets, repeatText).Download(context.Background(), "task-3")
	if err != nil {
		t.Fatal(err)
	}
	files := zipFiles(t, pkg.Body)
	if files["README.txt"] != "FBX" {
		t.Fatalf("the entry lost its name: %v", sortedKeys(files))
	}
	var readme string
	for name, body := range files {
		if strings.HasPrefix(name, "README_") {
			readme = body
		}
	}
	if !strings.HasPrefix(readme, repeatHeading) {
		t.Fatalf("the README did not move aside: %v", sortedKeys(files))
	}
}

// The bound is on the bytes written: a file left out is not counted.
func TestGenerationDownloadBoundCountsOnlyTheFilesItPacks(t *testing.T) {
	detail := tripoGameLedger()
	for index := range detail.Ledger.Assets {
		detail.Ledger.Assets[index].Size = 5
	}
	details := &memoryGenerationDetailReader{found: true, detail: detail}
	bodies := map[string]string{"model": "12345", "gltf": "12345", "fbx": "abcde"}
	assets := readyContents(bodies)
	service := newTestGenerationDownloadService(t, details, assets, 10)
	service.WithRepeatText(repeatText)
	// Three files of five bytes would not fit in ten; the two that are packed do.
	stream, err := service.OpenDownload(context.Background(), "task-3")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if len(stream.assets) != 2 {
		t.Fatalf("packed %d files, want 2", len(stream.assets))
	}
}
