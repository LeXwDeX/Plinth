package local

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func modelWrite(body []byte, variant, mediaType string) generation.AssetWrite {
	return generation.AssetWrite{
		AssetID: "asset-test", OwnerID: "owner-test", TaskID: "task-test",
		AttemptID: "attempt-test", Role: "model", Variant: variant,
		MediaType: mediaType, MaxBytes: 1 << 20, Body: bytes.NewReader(body),
	}
}

func glbWithJSON(document string) []byte {
	jsonBytes := []byte(document)
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	output := make([]byte, 20+len(jsonBytes))
	copy(output[:4], "glTF")
	binary.LittleEndian.PutUint32(output[4:8], 2)
	binary.LittleEndian.PutUint32(output[8:12], uint32(len(output)))
	binary.LittleEndian.PutUint32(output[12:16], uint32(len(jsonBytes)))
	copy(output[16:20], "JSON")
	copy(output[20:], jsonBytes)
	return output
}

func zipModel(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestGeneratedModelsRejectProviderAddressesBeforeCacheCommit(t *testing.T) {
	encoded := `%68%74%74%70%73%3A%2F%2Fprivate.example%2Ftexture.png`
	cases := []struct {
		name      string
		body      []byte
		variant   string
		mediaType string
	}{
		{"GLB JSON URI", glbWithJSON(`{"images":[{"uri":"https://private.example/texture.png?token=secret"}]}`), "glb", "model/gltf-binary"},
		{"glTF JSON URI", []byte(`{"buffers":[{"uri":"https:\/\/private.example\/model.bin"}]}`), "gltf", "model/gltf+json"},
		{"glTF escaped extras address", []byte(`{"asset":{"version":"2.0"},"extras":{"source":"\\u0068ttps://private\\u002eexample/secret"}}`), "gltf", "model/gltf+json"},
		{"glTF missing relative dependency", []byte(`{"images":[{"uri":"texture.png"}]}`), "gltf", "model/gltf+json"},
		{"OBJ encoded URL", []byte("mtllib " + encoded + "\n"), "obj", "text/plain"},
		{"FBX UTF16 URL", utf16LE("https://private.example/model.fbx"), "fbx", "application/octet-stream"},
		{"3MF ZIP encoded URL", zipModel(t, map[string]string{"3D/3dmodel.model": `<model texture="` + encoded + `"/>`}), "3mf", "application/zip"},
		{"3MF vocabulary outside namespace", zipModel(t, map[string]string{"3D/3dmodel.model": `<model source="http://schemas.bambulab.com/package/2021"/>`}), "3mf", "application/zip"},
		{"3MF forged vocabulary host", zipModel(t, map[string]string{"3D/3dmodel.model": `<model xmlns:BambuStudio="http://schemas.bambulab.com.attacker.example/package/2021"/>`}), "3mf", "application/zip"},
		{"3MF signed namespace", zipModel(t, map[string]string{"3D/3dmodel.model": `<model xmlns:BambuStudio="http://schemas.bambulab.com/package/2021?sig=secret"/>`}), "3mf", "application/zip"},
		{"ZIP embedded GLB escaped URL", zipModel(t, map[string]string{"scene.glb": string(glbWithJSON(`{"asset":{"version":"2.0"},"extras":{"source":"\\u0068ttps://private\\u002eexample/secret"}}`))}), "3mf", "application/zip"},
		{"ZIP nested USDZ address", zipModel(t, map[string]string{"inner.usdz": string(zipModel(t, map[string]string{"3D/scene.model": `<model source="https://private.example/secret"/>`}))}), "3mf", "application/zip"},
		{"ZIP local extra address", zipWithPrivateLocalExtra(t), "usdz", "model/vnd.usdz+zip"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			_, err = store.Put(context.Background(), modelWrite(testCase.body, testCase.variant, testCase.mediaType))
			if err == nil {
				t.Fatal("external or unresolved dependency was committed")
			}
		})
	}
}

func zipWithPrivateLocalExtra(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	extra := make([]byte, 68)
	binary.LittleEndian.PutUint16(extra[:2], 0xbeef)
	binary.LittleEndian.PutUint16(extra[2:4], 64)
	for index := 4; index < len(extra); index++ {
		extra[index] = ' '
	}
	entry, err := archive.CreateHeader(&zip.FileHeader{Name: "scene.usda", Method: zip.Store, Extra: extra})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, "#usda 1.0\n"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	result := output.Bytes()
	localExtra := 30 + int(binary.LittleEndian.Uint16(result[26:28])) + 4
	copy(result[localExtra:localExtra+64], "https://private.example/secret")
	return result
}

func pngWithProvenance(t *testing.T, chunkType, metadata string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&buffer, pixel); err != nil {
		t.Fatal(err)
	}
	encoded := buffer.Bytes()
	payload := []byte(metadata)
	if chunkType == "tEXt" && !strings.ContainsRune(metadata, '\x00') {
		payload = []byte("Comment\x00" + metadata)
	}
	if chunkType == "caBX" {
		payload = syntheticC2PAIdentifiers(strings.Fields(metadata))
	}
	chunk := make([]byte, 12+len(payload))
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(payload)))
	copy(chunk[4:8], chunkType)
	copy(chunk[8:], payload)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	result := append([]byte(nil), encoded[:len(encoded)-12]...)
	result = append(result, chunk...)
	return append(result, encoded[len(encoded)-12:]...)
}

// Definite CBOR strings inside a real JUMBF content box expose the exact URI
// boundaries, including the adjacent ASCII CBOR keys/tags of genuine files.
func syntheticC2PAIdentifiers(identifiers []string) []byte {
	var cbor []byte
	if len(identifiers) < 24 {
		cbor = append(cbor, 0x80|byte(len(identifiers)))
	} else {
		cbor = append(cbor, 0x98, byte(len(identifiers)))
	}
	for _, identifier := range identifiers {
		switch length := len(identifier); {
		case length < 24:
			cbor = append(cbor, 0x60|byte(length))
		case length < 256:
			cbor = append(cbor, 0x78, byte(length))
		default:
			cbor = append(cbor, 0x79, byte(length>>8), byte(length))
		}
		cbor = append(cbor, identifier...)
	}
	box := func(kind string, data []byte) []byte {
		result := make([]byte, 8+len(data))
		binary.BigEndian.PutUint32(result[:4], uint32(len(result)))
		copy(result[4:8], kind)
		copy(result[8:], data)
		return result
	}
	return box("jumb", box("cbor", cbor))
}

func TestRasterProvenanceNamespacesDoNotBlockCachedImages(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	write := modelWrite(pngWithProvenance(t, "caBX",
		"http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia "+
			"http://www.microsoft.com/pkiops/certs/MicrosoftRoot "+
			"http://oneocsp.microsoft.com/ocsp"), "", "image/png")
	write.Role = "image"
	asset, err := store.Put(context.Background(), write)
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Open(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()

	// Images are cached exactly as delivered, metadata included: addresses of
	// any kind inside a structurally valid image never block it, wherever
	// they sit (Lex, 2026-10-09). The image is shown from a same-origin URL.
	for _, testCase := range []struct {
		name string
		body []byte
	}{
		{"provider address in provenance", pngWithProvenance(t, "caBX",
			"http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia "+
				"https://cdn.tripo3d.ai/signed/model.png")},
		{"signed metadata URL", pngWithProvenance(t, "caBX", "https://www.microsoft.com/pkiops/private-model.png?sig=secret")},
		{"namespace outside provenance", pngWithProvenance(t, "tEXt", "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			write := modelWrite(testCase.body, "", "image/png")
			write.Role = "image"
			asset, err := store.Put(context.Background(), write)
			if err != nil {
				t.Fatalf("valid PNG with metadata addresses was not cached: %v", err)
			}
			reader, err := store.Open(context.Background(), asset)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil || !bytes.Equal(got, testCase.body) {
				t.Fatal("cached image bytes changed")
			}
		})
	}
	t.Run("non-PNG image", func(t *testing.T) {
		write := modelWrite([]byte("GIF89a http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"), "", "image/gif")
		write.Role = "image"
		if _, err := store.Put(context.Background(), write); err == nil {
			t.Fatal("unvalidated non-PNG/JPEG bytes were cached")
		}
	})
}

func TestHistoricalModelReadRejectsProviderAddress(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	body := glbWithJSON(`{"images":[{"uri":"https://private.example/old.png"}]}`)
	blob, err := store.PutBlob(context.Background(), bytes.NewReader(body), "model/gltf-binary", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	asset := generation.Asset{Role: "model", Variant: "glb", StorageKey: blob.StorageKey, SHA256: blob.SHA256, Size: blob.Size, MediaType: blob.MediaType}
	reader, err := store.Open(context.Background(), asset)
	if err == nil {
		reader.Close()
		t.Fatal("historical asset leaked provider URL")
	}
	if strings.Contains(err.Error(), "private.example") {
		t.Fatalf("rejection exposed provider address: %v", err)
	}
}

func TestSelfContainedModelStillReadsFromLocalCache(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	body := glbWithJSON(`{"asset":{"version":"2.0"},"images":[{"bufferView":0}]}`)
	asset, err := store.Put(context.Background(), modelWrite(body, "glb", "model/gltf-binary"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("local model bytes changed: %v", err)
	}
	archive := zipModel(t, map[string]string{
		"scene.gltf":          `{"images":[{"uri":"textures/albedo.png"}]}`,
		"textures/albedo.png": string(pngWithProvenance(t, "tEXt", "fixture")),
	})
	if _, err := store.Put(context.Background(), modelWrite(archive, "zip", "application/zip")); err != nil {
		t.Fatalf("contained ZIP dependency rejected: %v", err)
	}
	standard3MF := zipModel(t, map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel" Id="rel0"/></Relationships>`,
		"3D/3dmodel.model":    `<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:BambuStudio="http://schemas.bambulab.com/package/2021"><resources/><build/></model>`,
	})
	if _, err := store.Put(context.Background(), modelWrite(standard3MF, "3mf", "application/zip")); err != nil {
		t.Fatalf("valid standard 3MF namespace rejected: %v", err)
	}
}

func TestLarge3MFXMLIsInspectedBeyondGLTFJSONLimit(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// 3MF mesh XML can expand far beyond a glTF JSON chunk while remaining
	// within the archive and Offering limits. The complete XML must still be
	// scanned, including content after the previous 32 MiB cutoff.
	geometry := strings.Repeat("x", maxModelJSONBytes+1)
	safe := zipModel(t, map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`,
		"3D/3dmodel.model":    `<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><metadata>` + geometry + `</metadata></model>`,
	})
	if _, err := store.Put(context.Background(), modelWrite(safe, "3mf", "application/zip")); err != nil {
		t.Fatalf("large self-contained 3MF rejected: %v", err)
	}
	unsafe := zipModel(t, map[string]string{
		"3D/3dmodel.model": `<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><metadata>` + geometry + `https://private.example/texture.png</metadata></model>`,
	})
	if _, err := store.Put(context.Background(), modelWrite(unsafe, "3mf", "application/zip")); err == nil {
		t.Fatal("external address beyond the former 32 MiB boundary was accepted")
	}
}

func utf16LE(value string) []byte {
	result := make([]byte, 0, len(value)*2)
	for _, char := range []byte(value) {
		result = append(result, char, 0)
	}
	return result
}
