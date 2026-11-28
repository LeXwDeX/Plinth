package fakeupstream

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
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
		"glb:pbr":       {"model", "glb", "model/gltf-binary"},
		"gltf":          {"converted_model", "converted.gltf", "model/gltf+json"},
		"fbx":           {"converted_model", "fbx", "model/fbx"},
		"fbx:skeleton":  {"converted_model", "fbx", "model/fbx"},
		"fbx:animation": {"converted_model", "fbx", "model/fbx"},
		// The textured exports pass the same binary-FBX check as the plain one.
		"fbx:texture-png":  {"converted_model", "fbx", "model/fbx"},
		"fbx:texture-jpeg": {"converted_model", "fbx", "model/fbx"},
		"obj":              {"converted_model", "obj", "application/zip"},
		"stl":              {"converted_model", "stl", "model/stl"},
		"usdz":             {"converted_model", "usdz", "model/vnd.usdz+zip"},
		"3mf":              {"converted_model", "3mf", "model/3mf"},
		"3mf:tripo":        {"converted_model", "converted.3mf", "model/3mf"},
		"png":              {"texture", "texture.base_color", "image/png"},
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

// The textured FBX fixtures differ in the format of the one image they embed,
// which is how a delivered conversion shows the texture format it was asked for.
func TestTexturedFBXFixturesEmbedTheirTextureFormat(t *testing.T) {
	files, err := smallModelFiles()
	if err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\n")
	jpeg := []byte("\xff\xd8\xff")
	for name, want := range map[string]struct{ present, absent []byte }{
		"fbx:texture-png":  {png, jpeg},
		"fbx:texture-jpeg": {jpeg, png},
		"fbx":              {nil, png},
	} {
		data := files[name].Data
		if want.present != nil && !bytes.Contains(data, want.present) {
			t.Errorf("%s does not embed the expected image", name)
		}
		if bytes.Contains(data, want.absent) {
			t.Errorf("%s embeds an image of the other format", name)
		}
	}
}

// "glb:pbr" is the PBR pack a Tripo generation with texture and PBR on delivers
// (#704: three textures, three images, one material). Each slot has its own
// picture, the base color is the plain fixture's atlas, and Plinth's model
// reader finds all three slots; the file stays small.
func TestPBRFixtureCarriesThreeDistinctTextures(t *testing.T) {
	files, err := smallModelFiles()
	if err != nil {
		t.Fatal(err)
	}
	data := files["glb:pbr"].Data
	if len(data) < 20 || len(data) > 64<<10 {
		t.Fatalf("glb:pbr is %d bytes", len(data))
	}
	size := int(binary.LittleEndian.Uint32(data[12:16]))
	var document struct {
		Textures, Images, Materials []json.RawMessage
	}
	if err := json.Unmarshal(data[20:20+size], &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Textures) != 3 || len(document.Images) != 3 || len(document.Materials) != 1 {
		t.Fatalf("glb:pbr declares %d textures, %d images, %d materials, want 3, 3, 1",
			len(document.Textures), len(document.Images), len(document.Materials))
	}
	report := inspectGLB(t, data)
	if report.TextureCount != 3 {
		t.Errorf("the reader finds %d textures, want 3", report.TextureCount)
	}
	slots := slotEdges(report)
	for _, slot := range []string{"base_color", "metallic_roughness", "normal"} {
		if slots[slot] == 0 {
			t.Errorf("slot %s is missing (all %v)", slot, slots)
		}
	}
	atlas, err := fixtureTexture(64)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, atlas) {
		t.Error("the base color is not the fixture atlas")
	}
	seen := map[string]bool{}
	for _, png := range pngsIn(data) {
		if seen[string(png)] {
			t.Error("two slots carry the same picture")
		}
		seen[string(png)] = true
	}
	if len(seen) != 3 {
		t.Errorf("glb:pbr embeds %d pictures, want 3", len(seen))
	}
}

// pngsIn cuts every PNG out of a buffer: from its signature to its IEND chunk.
func pngsIn(data []byte) [][]byte {
	signature, end := []byte("\x89PNG\r\n\x1a\n"), []byte("IEND\xaeB`\x82")
	var out [][]byte
	for {
		start := bytes.Index(data, signature)
		if start < 0 {
			return out
		}
		stop := bytes.Index(data[start:], end)
		if stop < 0 {
			return out
		}
		out = append(out, data[start:start+stop+len(end)])
		data = data[start+stop+len(end):]
	}
}

// A low-poly model carries the triangle count its target allows (72 per step
// squared, never above the target), is deterministic, and still passes the
// delivery checks; the plain fixture is the one-step model.
func TestLowPolyModelFileCarriesTheRequestedTriangleCount(t *testing.T) {
	plain, err := smallModelFiles()
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[int]int{0: 72, 71: 72, 72: 72, 287: 72, 288: 288, 500: 288, 8712: 8712, 9370: 8712, 10000: 8712, 20000: 18432, 121032: 121032, 5_000_000: 121032} {
		if got := LowPolyTriangles(target); got != want {
			t.Errorf("LowPolyTriangles(%d) = %d, want %d", target, got, want)
		}
	}
	if file, err := LowPolyModelFile(72); err != nil || !bytes.Equal(file.Data, plain["glb"].Data) {
		t.Errorf("the one-step low-poly model differs from the plain GLB fixture (err %v)", err)
	}
	for _, target := range []int{500, 9370, 20000} {
		file, err := LowPolyModelFile(target)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := LowPolyModelFile(target)
		if file.MediaType != "model/gltf-binary" || !bytes.Equal(file.Data, again.Data) {
			t.Errorf("low-poly model for %d is not a stable GLB", target)
		}
		validateDelivered(t, "lowpoly", file.Data, "model", "glb", "model/gltf-binary")
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
