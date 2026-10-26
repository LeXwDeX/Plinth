package modeldelivery

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func objNote(f *fixture, path string) string {
	return strings.ReplaceAll(f.service.schema.Archive.Readme.ConvertedObjNote, "{path}", path)
}

func readmeOf(t *testing.T, f *fixture, task string) string {
	t.Helper()
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), generation.TaskID(task))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	return readZip(t, archive)["README.txt"]
}

// An OBJ that comes out of a provider's format conversion is geometry only. The
// README says so, in the Schema's words, instead of leaving the user to wonder
// where the material and textures went.
func TestReadmeExplainsAConvertedObjWithoutMaterialOrTextures(t *testing.T) {
	f := newFixture(t)
	if strings.TrimSpace(f.service.schema.Archive.Readme.ConvertedObjNote) == "" {
		t.Fatal("the Schema must declare the converted OBJ note")
	}
	f.tripoFull("tripo-task", "owner")
	readme := readmeOf(t, f, "tripo-task")
	if !strings.Contains(readme, objNote(f, "model.obj")) {
		t.Fatalf("README lacks the converted OBJ note:\n%s", readme)
	}
	if strings.Count(readme, objNote(f, "model.obj")) != 1 {
		t.Fatalf("note repeated:\n%s", readme)
	}
}

func TestReadmeLeavesOtherObjFilesAlone(t *testing.T) {
	// A generation provider's own OBJ is not a conversion result.
	f := newFixture(t)
	f.meshyText("meshy-task", "owner")
	if readme := readmeOf(t, f, "meshy-task"); strings.Contains(readme, objNote(f, "model.obj")) {
		t.Fatalf("a native OBJ got the conversion note:\n%s", readme)
	}

	// A converted OBJ that does arrive with a material library and textures is
	// complete, so the "no material" note would be false.
	f = newFixture(t)
	f.addTask("tripo-full", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"},
		assetSpec{role: "converted_model", variant: "converted.obj", body: "mtllib model.mtl\n"},
		assetSpec{role: "model", variant: "mtl", body: "map_Kd base.png\n"},
		assetSpec{role: "texture", variant: "texture.1.base_color", mediaType: "image/png", body: "PNG"},
	)
	// The OBJ that ships a material library is complete, so no form of the
	// note may appear, whatever path it would name.
	prefix := strings.SplitN(f.service.schema.Archive.Readme.ConvertedObjNote, "{path}", 2)[0]
	if readme := readmeOf(t, f, "tripo-full"); strings.Contains(readme, prefix) {
		t.Fatalf("an OBJ with material got the no-material note:\n%s", readme)
	}

	// Another provider's converted_model role is not Tripo's: the Schema names
	// the provider, so the note is not guessed from the role alone.
	f = newFixture(t)
	f.addTask("other", "owner", "someone-else", "text-to-3d",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
		assetSpec{role: "converted_model", variant: "converted.obj"},
	)
	if readme := readmeOf(t, f, "other"); strings.Contains(readme, objNote(f, "model.obj")) {
		t.Fatalf("an undeclared provider got the note:\n%s", readme)
	}
}

func TestReadmeOmitsTheNoteForAnObjThatCouldNotBeRead(t *testing.T) {
	f := newFixture(t)
	task := f.tripoFull("tripo-task", "owner")
	for _, asset := range task.Assets {
		if asset.Variant == "converted.obj" {
			f.unreadable[asset.ID] = true
		}
	}
	if readme := readmeOf(t, f, "tripo-task"); strings.Contains(readme, objNote(f, "model.obj")) {
		t.Fatalf("a note was written for a file that is not in the ZIP:\n%s", readme)
	}
}

func zipBytes(t *testing.T, names ...string) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		// Stored, so the OBJ's own mtllib line is visible in the raw bytes.
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("mtllib model.mtl\n" + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.String()
}

// #264 follow-up: Tripo's converted OBJ is a ZIP that carries the OBJ, its
// material library and textures. In the one-click ZIP it is named as the
// package it is, its own references are not taken for missing task files, and
// the README lists what the package holds instead of claiming it has no
// material or textures.
func TestConvertedObjPackageIsNamedAndDescribedByItsContent(t *testing.T) {
	members := []string{"tripo_convert.obj", "tripo_convert.mtl", "textures/basecolor.jpg", "textures/normal.jpg"}
	body := zipBytes(t, members...)
	for _, variant := range []string{"converted.obj", "obj"} {
		t.Run(variant, func(t *testing.T) {
			f := newFixture(t)
			f.addTask("tripo-zip", "owner", "tripo", "text-to-3d",
				assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"},
				assetSpec{role: "converted_model", variant: variant, mediaType: "application/zip", body: body})
			archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "tripo-zip")
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			files := readZip(t, archive)
			if files["model.obj.zip"] != body {
				t.Fatalf("package not delivered as model.obj.zip: %v", keys(files))
			}
			if _, named := files["model.obj"]; named {
				t.Fatal("a ZIP is still named model.obj")
			}
			readme := files["README.txt"]
			text := f.service.schema.Archive.Readme
			want := strings.NewReplacer("{path}", "model.obj.zip", "{count}", "4").Replace(text.ConvertedObjPackageNote)
			for _, member := range members {
				want += "\n" + strings.ReplaceAll(text.DependencyLine, "{path}", member)
			}
			if !strings.Contains(readme, want) {
				t.Fatalf("README lacks the package note %q:\n%s", want, readme)
			}
			// The geometry-only claim (the bare note after its path) must not appear.
			bare := strings.SplitN(text.ConvertedObjNote, "{path}", 2)[1]
			if strings.Contains(readme, bare) || strings.Contains(readme, text.DependencyHeading) {
				t.Fatalf("README misdescribes the package:\n%s", readme)
			}
			if !strings.Contains(readme, strings.NewReplacer("{label}", "OBJ", "{path}", "model.obj.zip").Replace(text.IncludedLine)) {
				t.Fatalf("README does not list model.obj.zip:\n%s", readme)
			}
		})
	}
}

func keys(files map[string]string) []string {
	result := make([]string, 0, len(files))
	for name := range files {
		result = append(result, name)
	}
	return result
}

// Names inside the package are untrusted: unsafe ones are counted, not printed,
// and the listing stays bounded.
func TestConvertedObjPackageNoteListsOnlySafeNames(t *testing.T) {
	f := newFixture(t)
	f.service.schema.Archive.MaxEntries = 3
	body := zipBytes(t, "a.obj", "../escape.mtl", "http://example.invalid/x.png", "b.mtl", "c.png", "d.png")
	f.addTask("tripo-zip", "owner", "tripo", "text-to-3d",
		assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary"},
		assetSpec{role: "converted_model", variant: "converted.obj", mediaType: "application/zip", body: body})
	readme := readmeOf(t, f, "tripo-zip")
	text := f.service.schema.Archive.Readme
	line := func(name string) string { return strings.ReplaceAll(text.DependencyLine, "{path}", name) }
	want := strings.NewReplacer("{path}", "model.obj.zip", "{count}", "6").Replace(text.ConvertedObjPackageNote) +
		"\n" + line("a.obj") + "\n" + line("b.mtl") + "\n" + line("c.png") + "\n" + line("...")
	if !strings.Contains(readme, want) || strings.Contains(readme, "escape") || strings.Contains(readme, "://") || strings.Contains(readme, "d.png") {
		t.Fatalf("README = %s", readme)
	}
}

// A single download is named like the format's file in the one-click ZIP.
func TestDownloadFilenameFollowsTheFormatNaming(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct {
		asset generation.Asset
		want  string
		named bool
	}{
		{generation.Asset{Role: "model", Variant: "glb", MediaType: "model/gltf-binary"}, "model-task_1.glb", true},
		{generation.Asset{Role: "model", Variant: "gltf", MediaType: "application/zip"}, "model-task_1.gltf.zip", true},
		{generation.Asset{Role: "converted_model", Variant: "obj", MediaType: "application/zip"}, "model-task_1.obj.zip", true},
		{generation.Asset{Role: "model", Variant: "obj", MediaType: "model/obj"}, "model-task_1.obj", true},
		{generation.Asset{Role: "model", Variant: "mtl", MediaType: "text/plain"}, "", false},
		{generation.Asset{Role: "thumbnail", Variant: "primary", MediaType: "image/png"}, "", false},
	} {
		name, named := f.service.DownloadFilename("task_1", test.asset)
		if name != test.want || named != test.named {
			t.Errorf("%+v => %q %v; want %q %v", test.asset, name, named, test.want, test.named)
		}
	}
	if name, _ := f.service.DownloadFilename(`task"; x=/..\`, generation.Asset{Role: "model", Variant: "glb", MediaType: "model/gltf-binary"}); strings.ContainsAny(name, "\"; /\\") {
		t.Fatalf("unsafe name %q", name)
	}
}

// #189: Creative Lab figure builds (figure, vinyl figure, brick figure) store
// the OBJ's material library as material/mtl and its texture as
// texture/1.base_color (texture_urls[0] of the official build response). The
// archive packs both next to the OBJ under the name the files reference,
// instead of reporting the saved material as missing.
func TestCreativeLabFigureBuildArchivePacksTheObjMaterialAndTexture(t *testing.T) {
	f := newFixture(t)
	f.addTask("figure-build", "owner", "meshy", "creative-lab-build",
		assetSpec{role: "model", variant: "glb", mediaType: "model/gltf-binary"},
		assetSpec{role: "model", variant: "obj", mediaType: "model/obj", body: "# Blender 4.4.0\nmtllib model.mtl\no model\n"},
		assetSpec{role: "material", variant: "mtl", mediaType: "text/plain", body: "newmtl Material.001\nmap_Kd texture_0.png\n"},
		assetSpec{role: "texture", variant: "1.base_color", mediaType: "image/png", body: "PNG"},
	)
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), "figure-build")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := readZip(t, archive)
	readme := files["README.txt"]
	if strings.Contains(readme, "任务没有保存它") {
		t.Fatalf("saved material reported missing:\n%s", readme)
	}
	var material, texture string
	for name := range files {
		switch {
		case strings.HasSuffix(name, "model.mtl"):
			material = name
		case strings.HasSuffix(name, "texture_0.png"):
			texture = name
		case strings.HasPrefix(name, "other/"):
			t.Fatalf("OBJ dependency left among other files: %s", name)
		}
	}
	if material == "" || texture == "" || files[material] != "newmtl Material.001\nmap_Kd texture_0.png\n" || files[texture] != "PNG" {
		t.Fatalf("archive files = %v\n%s", keys(files), readme)
	}
}
