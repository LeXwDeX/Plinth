package fakeupstream

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
)

// Every container the fixture hands out must pass the delivery checks and stay byte-stable.
func TestSmallModelFilesPassDeliveryChecksAndAreDeterministic(t *testing.T) {
	files, err := smallModelFiles()
	if err != nil {
		t.Fatal(err)
	}
	for format, want := range map[string]struct{ role, variant, media string }{
		"glb":           {"model", "glb", "model/gltf-binary"},
		"glb:skeleton":  {"model", "glb", "model/gltf-binary"},
		"glb:animation": {"model", "glb", "model/gltf-binary"},
		"gltf":          {"converted_model", "converted.gltf", "model/gltf+json"},
		"fbx":           {"converted_model", "fbx", "model/fbx"},
		"fbx:skeleton":  {"converted_model", "fbx", "model/fbx"},
		"fbx:animation": {"converted_model", "fbx", "model/fbx"},
		"obj":           {"converted_model", "obj", "application/zip"},
		"stl":           {"converted_model", "stl", "model/stl"},
		"usdz":          {"converted_model", "usdz", "model/vnd.usdz+zip"},
		"3mf":           {"converted_model", "3mf", "model/3mf"},
		"png":           {"texture", "texture.base_color", "image/png"},
	} {
		file, ok := files[format]
		if !ok || len(file.Data) == 0 || file.MediaType != want.media {
			t.Errorf("%s: missing or wrong media type (%q)", format, file.MediaType)
			continue
		}
		validateDelivered(t, format, file.Data, want.role, want.variant, want.media)
	}
	again, err := smallModelFiles()
	if err != nil {
		t.Fatal(err)
	}
	for format, file := range files {
		if string(again[format].Data) != string(file.Data) {
			t.Errorf("%s is not deterministic", format)
		}
	}
}

func validateDelivered(t *testing.T, name string, data []byte, role, variant, mediaType string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := local.ValidateDeliveredFile(file, role, variant, mediaType); err != nil {
		t.Errorf("%s (%s/%s) rejected: %v", name, role, variant, err)
	}
}
