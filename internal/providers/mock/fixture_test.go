package mock

import "testing"

// SmallModelFiles feeds test tooling (the fake upstream, #96); every container
// it hands out must pass the delivery checks and stay byte-stable.
func TestSmallModelFilesPassDeliveryChecksAndAreDeterministic(t *testing.T) {
	files, err := SmallModelFiles()
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
		if !ok || len(file.Data) == 0 || file.MediaType != want.media && format != "png" {
			t.Errorf("%s: missing or wrong media type (%q)", format, file.MediaType)
			continue
		}
		validateBytes(t, format, file.Data, want.role, want.variant, want.media)
	}
	again, err := SmallModelFiles()
	if err != nil {
		t.Fatal(err)
	}
	for format, file := range files {
		if string(again[format].Data) != string(file.Data) {
			t.Errorf("%s is not deterministic", format)
		}
	}
}
