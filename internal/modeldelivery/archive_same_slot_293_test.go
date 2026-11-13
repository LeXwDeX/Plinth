package modeldelivery

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #293 follow-up. A Tripo task made with output_formats [GLTF, FBX] holds its GLTF
// export twice, as the model and as converted.gltf: the export is a GLB, so both
// records match the GLB format and carry the same bytes. The one-click ZIP keeps the
// file once, as model.glb, and the README says converted.gltf is the same file. It
// also listed glTF among the formats not included ("尚未转换"), which contradicted
// that line: the glTF conversion's own file is in the ZIP, as the GLB. A format
// whose slot holds a file left out as a repeat of an included one is delivered, and
// the README does not report it missing. The rule reads the Schema's format slots
// (role and variant), not a provider, and the live format rows are not touched.

// prepared prepares the task's archive and returns what the README and the audit
// report: the README text and the formats the ZIP is said to leave out.
func prepared(t *testing.T, f *fixture, id generation.TaskID) (readme string, missing []string) {
	t.Helper()
	archive, err := f.service.Prepare(context.Background(), generation.OwnTasks("owner"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	missing = slices.Clone(archive.MissingFormats)
	return readZip(t, archive)["README.txt"], missing
}

func TestArchiveDoesNotReportAFormatMissingWhoseSlotHoldsARepeat(t *testing.T) {
	f := newFixture(t)
	task := f.tripoGameTask("tripo-game", sharedGLB)

	readme, missingIDs := prepared(t, f, task.ID)

	// The repeat is still named, so the reader can find the file the glTF export is.
	if !strings.Contains(readme, "内容相同的文件：\n- converted.gltf 与 model.glb 内容相同，只收录一份\n") {
		t.Fatalf("README does not name the repeat:\n%s", readme)
	}
	// glTF is not also reported as not converted, in the README or in the audit's list.
	missing := missingFormats(readme)
	if strings.Contains(readme, "glTF：尚未转换") || strings.Contains(missing, "- glTF：") || slices.Contains(missingIDs, "gltf") {
		t.Fatalf("glTF is reported missing although its conversion slot is in the ZIP (missing = %v):\n%s", missingIDs, readme)
	}
	// What nothing in the task delivers is still reported, each with the Schema's reason.
	for id, label := range map[string]string{"obj": "OBJ", "stl": "STL", "usdz": "USDZ", "3mf": "3MF"} {
		if !strings.Contains(missing, "- "+label+"：") || !slices.Contains(missingIDs, id) {
			t.Fatalf("%s is no longer reported missing (missing = %v):\n%s", label, missingIDs, readme)
		}
	}
	if !strings.Contains(readme, "部分格式未包含") || strings.Contains(readme, "所有目标格式均已包含") {
		t.Fatalf("README misjudges completeness:\n%s", readme)
	}
	// Only the README changed. The live rows still say a glTF file can be obtained, and
	// the GLB row still points at a record of the task.
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row := formatByID(t, coverage, "gltf"); row.Status != StatusObtainable || row.AssetID != "" {
		t.Fatalf("gltf row = %#v, want it still obtainable", row)
	}
	if row := formatByID(t, coverage, "glb"); row.Status != StatusAvailable || row.AssetID == "" {
		t.Fatalf("glb row = %#v", row)
	}
}

// A task with nothing in the glTF slot is told the glTF is not converted, as before.
func TestArchiveStillReportsGltfWhenNoRecordHoldsItsSlot(t *testing.T) {
	for name, build := range map[string]func(*fixture) generation.Task{
		"only the model": func(f *fixture) generation.Task { return f.tripoGlbOnly("tripo-glb", "owner") },
		"the model and an FBX, no glTF conversion": func(f *fixture) generation.Task {
			return f.addTask("tripo-fbx", "owner", "tripo", "text-to-3d",
				assetSpec{role: "model", variant: "", mediaType: "model/gltf-binary", body: sharedGLB},
				assetSpec{role: "converted_model", variant: "converted.fbx", mediaType: "application/octet-stream", body: gameFBX})
		},
		"a Meshy task with every other format": func(f *fixture) generation.Task { return f.meshyText("meshy-all", "owner") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			task := build(f)

			readme, missingIDs := prepared(t, f, task.ID)

			if !strings.Contains(missingFormats(readme), "- glTF：尚未转换") || !slices.Contains(missingIDs, "gltf") {
				t.Fatalf("glTF is not reported missing (missing = %v):\n%s", missingIDs, readme)
			}
			if strings.Contains(readme, "内容相同的文件") {
				t.Fatalf("README names a repeat that is not there:\n%s", readme)
			}
		})
	}
}

// The repeat counts only while the file that holds its bytes is in the ZIP, as for
// the README line that names it. When one record cannot be read, the other stands in
// for it and the format is delivered; when neither can, nothing is in the ZIP and
// glTF is reported like the GLB.
func TestArchiveHoldsAFormatOnlyWhileItsRepeatIsInThePackage(t *testing.T) {
	t.Run("the record the GLB row names is unreadable and the repeat stands in", func(t *testing.T) {
		f := newFixture(t)
		task := f.tripoGameTask("tripo-game", sharedGLB)
		f.unreadable[task.Assets[0].ID] = true

		readme, missingIDs := prepared(t, f, task.ID)

		if !strings.Contains(readme, "- converted.gltf 与 model.glb 内容相同，只收录一份\n") ||
			strings.Contains(missingFormats(readme), "- glTF：") || slices.Contains(missingIDs, "gltf") {
			t.Fatalf("missing = %v\n%s", missingIDs, readme)
		}
	})
	t.Run("neither record can be read", func(t *testing.T) {
		f := newFixture(t)
		task := f.tripoGameTask("tripo-game", sharedGLB)
		f.unreadable[task.Assets[0].ID] = true
		f.unreadable[task.Assets[1].ID] = true

		readme, missingIDs := prepared(t, f, task.ID)

		if strings.Contains(readme, "内容相同的文件") ||
			!strings.Contains(missingFormats(readme), "- GLB：") || !strings.Contains(missingFormats(readme), "- glTF：") ||
			!slices.Contains(missingIDs, "glb") || !slices.Contains(missingIDs, "gltf") {
			t.Fatalf("missing = %v\n%s", missingIDs, readme)
		}
	})
}

// A repeat that is in the ZIP under other/ because its bytes differ is not a repeat the
// README leaves out, and the rule keys on that: the format is still reported.
func TestArchiveStillReportsGltfWhenTheSlotFileIsPackedForItsOwnBytes(t *testing.T) {
	f := newFixture(t)
	task := f.tripoGameTask("tripo-game", "A-DIFFERENT-GLB")

	readme, missingIDs := prepared(t, f, task.ID)

	if !strings.Contains(readme, "其他文件：\n- other/converted_model-converted.gltf.glb\n") ||
		!strings.Contains(missingFormats(readme), "- glTF：尚未转换") || !slices.Contains(missingIDs, "gltf") {
		t.Fatalf("missing = %v\n%s", missingIDs, readme)
	}
}

// A file with no variant names no slot: the media type is all that tells the GLB
// row's empty-variant match apart. A model record of another media type that repeats
// an included FBX therefore does not stand for a GLB.
func TestArchiveRepeatWithoutAVariantHoldsNoFormat(t *testing.T) {
	f := newFixture(t)
	f.addTask("no-variant", "owner", "tripo", "text-to-3d",
		assetSpec{role: "converted_model", variant: "converted.fbx", mediaType: "application/octet-stream", body: gameFBX},
		assetSpec{role: "model", variant: "", mediaType: "application/octet-stream", body: gameFBX})

	readme, missingIDs := prepared(t, f, "no-variant")

	if !strings.Contains(readme, "- model 与 model.fbx 内容相同，只收录一份\n") {
		t.Fatalf("the repeat is not named:\n%s", readme)
	}
	if !strings.Contains(missingFormats(readme), "- GLB：") || !slices.Contains(missingIDs, "glb") {
		t.Fatalf("a repeat with no variant held the GLB (missing = %v):\n%s", missingIDs, readme)
	}
}

// The rule is about the slot, not about the words: the English README leaves glTF out
// of the formats not included too.
func TestArchiveHeldFormatFollowsTheRequestLocale(t *testing.T) {
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

	line := func(label, reason string) string {
		return strings.NewReplacer("{label}", label, "{reason}", reason).Replace(english["model_delivery.archive.readme.missing_line"])
	}
	notConverted := english["model_delivery.reasons.not_converted"]
	if !strings.Contains(readme, english["model_delivery.archive.readme.missing_heading"]) || notConverted == "" {
		t.Fatalf("English README has no missing section:\n%s", readme)
	}
	if strings.Contains(readme, line("glTF", notConverted)) {
		t.Fatalf("English README reports glTF not converted:\n%s", readme)
	}
	if !strings.Contains(readme, strings.NewReplacer("{label}", "converted.gltf", "{path}", "model.glb").Replace(english["model_delivery.archive.readme.same_line"])) {
		t.Fatalf("English README does not name the repeat:\n%s", readme)
	}
}
