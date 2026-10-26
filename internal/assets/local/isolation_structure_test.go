package local

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #297: an STL or 3MF slot must hold that format, not merely bytes without
// external addresses. Each case runs at ingest (Put) and at delivery of an
// already stored blob (Open), which use the same inspection.
func checkFormatSlot(t *testing.T, body []byte, variant, mediaType string, wantReason string) {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	asset, putErr := store.Put(context.Background(), modelWrite(body, variant, mediaType))
	if wantReason == "" {
		if putErr != nil {
			t.Fatalf("valid %s rejected: %v", variant, putErr)
		}
		reader, err := store.Open(context.Background(), asset)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("%s bytes changed: %v", variant, err)
		}
	} else {
		var diagnosed generation.DiagnosedAssetStorageError
		if !errors.As(putErr, &diagnosed) {
			t.Fatalf("%s slot accepted or untyped error: %v", variant, putErr)
		}
		if got := diagnosed.StorageDiagnostic(); got.Stage != "inspect" || got.Reason != wantReason {
			t.Fatalf("diagnostic=%+v, want reason %s", got, wantReason)
		}
	}
	blob, err := store.PutBlob(context.Background(), bytes.NewReader(body), mediaType, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	stored := generation.Asset{Role: "model", Variant: variant, StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256, MediaType: blob.MediaType}
	reader, openErr := store.Open(context.Background(), stored)
	if reader != nil {
		reader.Close()
	}
	if (openErr != nil) != (wantReason != "") {
		t.Fatalf("stored %s delivery rejection=%t, want %t: %v", variant, openErr != nil, wantReason != "", openErr)
	}
}

const asciiSTLFixture = "solid cube_part\n" +
	"  facet normal 0 0 -1\n" +
	"    outer loop\n" +
	"      vertex 0 0 0\n" +
	"      vertex 1 0 0\n" +
	"      vertex 1 1 0\n" +
	"    endloop\n" +
	"  endfacet\n" +
	"  facet normal 0.0e0 0.0e0 1.0E+00\n" +
	"    outer loop\n" +
	"      vertex 0 0 1\n" +
	"      vertex 1 0 1\n" +
	"      vertex -1.5e-3 1 1\n" +
	"    endloop\n" +
	"  endfacet\n" +
	"endsolid cube_part\n"

const errorPageHTML = "<!DOCTYPE html><html><head><title>502 Bad Gateway</title></head><body><h1>502 Bad Gateway</h1></body></html>\n"

func TestSTLSlotRequiresSTLStructure(t *testing.T) {
	headerSolid := binarySTLFixture(3)
	copy(headerSolid[:80], "solid exported by a binary writer")
	twoSolids := asciiSTLFixture + strings.ReplaceAll(asciiSTLFixture, "cube_part", "second")
	gzipLike := append([]byte{0x1f, 0x8b, 8, 0}, bytes.Repeat([]byte{0x55}, 200)...)
	glb := glbWithJSON(`{"asset":{"version":"2.0"}}`)
	for _, test := range []struct {
		name, variant, mediaType string
		body                     []byte
		reason                   string
	}{
		{"binary", "stl", "model/stl", binarySTLFixture(4), ""},
		{"binary header starting with solid", "stl", "model/stl", headerSolid, ""},
		{"binary in converted slot", "converted.stl", "application/octet-stream", binarySTLFixture(2), ""},
		{"ASCII", "stl", "model/stl", []byte(asciiSTLFixture), ""},
		{"ASCII CRLF uppercase no final newline", "stl", "application/sla", []byte(strings.TrimSuffix(strings.ToUpper(strings.ReplaceAll(asciiSTLFixture, "\n", "\r\n")), "\r\n")), ""},
		{"ASCII two solids", "stl", "model/stl", []byte(twoSolids), ""},
		{"ASCII unnamed solid", "stl", "model/stl", []byte(strings.ReplaceAll(asciiSTLFixture, "cube_part", "")), ""},

		{"HTML error page declared STL", "stl", "model/stl", []byte(errorPageHTML), "stl_rejected"},
		{"HTML error page with HTML MIME", "stl", "text/html", []byte(errorPageHTML), "stl_rejected"},
		{"HTML error page in converted slot", "converted.stl", "application/octet-stream", []byte(errorPageHTML), "stl_rejected"},
		{"HTML starting with solid", "stl", "model/stl", []byte("solid <html><body>not found</body></html>\n"), "stl_rejected"},
		{"JSON error", "stl", "model/stl", []byte(`{"error":"not found"}`), "stl_rejected"},
		{"compressed bytes", "stl", "model/stl", gzipLike, "stl_rejected"},
		{"GLB in STL slot", "stl", "model/stl", glb, "stl_rejected"},
		{"ZIP in STL slot", "stl", "model/stl", zipModel(t, map[string]string{"scene.stl": asciiSTLFixture}), "stl_rejected"},
		{"binary with trailing bytes", "stl", "model/stl", append(binarySTLFixture(2), 0, 0, 0), "stl_rejected"},
		{"binary truncated", "stl", "model/stl", binarySTLFixture(2)[:120], "stl_rejected"},
		{"ASCII without facets", "stl", "model/stl", []byte("solid empty\nendsolid empty\n"), "stl_rejected"},
		{"ASCII without endsolid", "stl", "model/stl", []byte(strings.TrimSuffix(asciiSTLFixture, "endsolid cube_part\n")), "stl_rejected"},
		{"ASCII two vertices", "stl", "model/stl", []byte(strings.Replace(asciiSTLFixture, "      vertex 1 1 0\n", "", 1)), "stl_rejected"},
		{"ASCII four vertices", "stl", "model/stl", []byte(strings.Replace(asciiSTLFixture, "      vertex 1 1 0\n", "      vertex 1 1 0\n      vertex 2 2 0\n", 1)), "stl_rejected"},
		{"ASCII missing coordinate", "stl", "model/stl", []byte(strings.Replace(asciiSTLFixture, "vertex 1 0 0", "vertex 1 0", 1)), "stl_rejected"},
		{"ASCII NaN", "stl", "model/stl", []byte(strings.Replace(asciiSTLFixture, "vertex 1 0 0", "vertex nan 0 0", 1)), "stl_rejected"},
		{"ASCII overflow", "stl", "model/stl", []byte(strings.Replace(asciiSTLFixture, "vertex 1 0 0", "vertex 1e39 0 0", 1)), "stl_rejected"},
		{"ASCII trailing content", "stl", "model/stl", []byte(asciiSTLFixture + "<html></html>\n"), "stl_rejected"},
		{"ASCII NUL byte", "stl", "model/stl", []byte(strings.Replace(asciiSTLFixture, "cube_part\n", "cube_part\x00\n", 1)), "stl_rejected"},
		{"ASCII overlong line", "stl", "model/stl", []byte("solid " + strings.Repeat("x", maxASCIISTLLineBytes+1) + "\n" + strings.TrimPrefix(asciiSTLFixture, "solid cube_part\n")), "stl_rejected"},
		// The external-address checks remain for both forms.
		{"ASCII address in solid name", "stl", "model/stl", []byte(strings.Replace(asciiSTLFixture, "solid cube_part", "solid https://private.example/model", 1)), "stl_rejected"},
		{"ASCII protocol-relative address", "stl", "model/stl", []byte(strings.Replace(asciiSTLFixture, "endsolid cube_part", "endsolid //localhost/model", 1)), "stl_rejected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			checkFormatSlot(t, test.body, test.variant, test.mediaType, test.reason)
		})
	}
}

func TestBinarySTLRecordsAreNotMistakenForAZIPFooter(t *testing.T) {
	// Binary STL is numeric data. A ZIP end-of-central-directory signature in a
	// coordinate formerly routed a valid STL to the archive inspector.
	body := binarySTLFixture(3)
	binary.LittleEndian.PutUint32(body[84+12:84+16], 0x06054b50)
	if value := math.Float32frombits(0x06054b50); math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		t.Fatal("fixture coordinate must be finite")
	}
	checkFormatSlot(t, body, "stl", "model/stl", "")
}

func standard3MFEntries() map[string]string {
	return map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`,
		"3D/3dmodel.model": `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
 <resources>
  <object id="1" type="model">
   <mesh>
    <vertices><vertex x="0" y="0" z="0"/><vertex x="1" y="0" z="0"/><vertex x="0" y="1" z="0"/><vertex x="0" y="0" z="1"/></vertices>
    <triangles><triangle v1="0" v2="2" v3="1"/><triangle v1="0" v2="1" v3="3"/><triangle v1="1" v2="2" v3="3"/><triangle v1="0" v2="3" v3="2"/></triangles>
   </mesh>
  </object>
 </resources>
 <build><item objectid="1"/></build>
</model>`,
	}
}

func with3MFEntries(changes map[string]string, removed ...string) map[string]string {
	entries := standard3MFEntries()
	for _, name := range removed {
		delete(entries, name)
	}
	for name, content := range changes {
		entries[name] = content
	}
	return entries
}

func Test3MFSlotRequires3MFPackage(t *testing.T) {
	model := standard3MFEntries()["3D/3dmodel.model"]
	for _, test := range []struct {
		name, variant, mediaType string
		body                     []byte
		reason                   string
	}{
		{"standard package", "3mf", "model/3mf", zipModel(t, standard3MFEntries()), ""},
		{"standard package generic MIME", "3mf", "application/octet-stream", zipModel(t, standard3MFEntries()), ""},
		{"converted slot", "converted.3mf", "application/octet-stream", zipModel(t, standard3MFEntries()), ""},
		{"without relationships uses default part", "3mf", "model/3mf", zipModel(t, with3MFEntries(nil, "_rels/.rels")), ""},
		{"start part elsewhere", "3mf", "model/3mf", zipModel(t, with3MFEntries(map[string]string{
			"_rels/.rels":       `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/Models/main.model" Id="r" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`,
			"Models/main.model": model,
		}, "3D/3dmodel.model")), ""},

		{"ZIP without model", "3mf", "model/3mf", zipModel(t, with3MFEntries(nil, "3D/3dmodel.model")), "3mf_rejected"},
		{"ordinary ZIP", "3mf", "model/3mf", zipModel(t, map[string]string{"readme.txt": "not a package"}), "3mf_rejected"},
		{"ZIP without content types", "3mf", "model/3mf", zipModel(t, with3MFEntries(nil, "[Content_Types].xml")), "3mf_rejected"},
		{"model is not XML", "3mf", "model/3mf", zipModel(t, with3MFEntries(map[string]string{"3D/3dmodel.model": "solid binary-ish\n"})), "3mf_rejected"},
		{"model root is not model", "3mf", "model/3mf", zipModel(t, with3MFEntries(map[string]string{"3D/3dmodel.model": `<html><body/></html>`})), "3mf_rejected"},
		{"model with two roots", "3mf", "model/3mf", zipModel(t, with3MFEntries(map[string]string{"3D/3dmodel.model": `<model/><model/>`})), "3mf_rejected"},
		{"content types root wrong", "3mf", "model/3mf", zipModel(t, with3MFEntries(map[string]string{"[Content_Types].xml": `<Relationships/>`})), "3mf_rejected"},
		{"start part missing", "3mf", "model/3mf", zipModel(t, with3MFEntries(map[string]string{
			"_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/Models/absent.model" Id="r" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`,
		})), "3mf_rejected"},
		{"HTML error page", "3mf", "model/3mf", []byte(errorPageHTML), "archive_rejected"},
		{"HTML error page in converted slot", "converted.3mf", "application/octet-stream", []byte(errorPageHTML), "archive_rejected"},
		// The existing whole-package isolation still runs first.
		{"external texture address", "3mf", "model/3mf", zipModel(t, with3MFEntries(map[string]string{"3D/3dmodel.model": `<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" texture="https://private.example/t.png"/>`})), "archive_rejected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			checkFormatSlot(t, test.body, test.variant, test.mediaType, test.reason)
		})
	}
}

func TestOtherArchiveSlotsDoNotRequire3MF(t *testing.T) {
	checkFormatSlot(t, zipModel(t, map[string]string{"readme.txt": "safe"}), "zip", "application/zip", "")
}

// #185: Tripo's native OBJ export is a ZIP holding the OBJ, its material
// library and textures. A slot that declares an OBJ archive must hold an OBJ,
// not just any ZIP.
func TestOBJArchiveSlotRequiresAnOBJMember(t *testing.T) {
	for _, test := range []struct {
		name, variant, mediaType string
		body                     []byte
		reason                   string
	}{
		{"OBJ with material library", "obj", "application/zip", zipModel(t, map[string]string{"model.obj": "mtllib model.mtl\nv 0 0 0\n", "model.mtl": "newmtl base\n"}), ""},
		{"upper-case member name", "obj", "application/zip", zipModel(t, map[string]string{"Mesh/MODEL.OBJ": "v 0 0 0\n"}), ""},
		{"ZIP without OBJ", "obj", "application/zip", zipModel(t, map[string]string{"model.mtl": "map_Kd base.png\n"}), "archive_rejected"},
		{"directory named like OBJ", "obj", "application/zip", zipModel(t, map[string]string{"model.obj/readme.txt": "safe"}), "archive_rejected"},
		{"plain OBJ declared as ZIP", "obj", "application/zip", []byte("v 0 0 0\n"), "archive_rejected"},
		{"HTML error page", "obj", "application/zip", []byte(errorPageHTML), "archive_rejected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			checkFormatSlot(t, test.body, test.variant, test.mediaType, test.reason)
		})
	}
	// A plain OBJ slot keeps the text inspection.
	checkFormatSlot(t, []byte("v 0 0 0\nf 1 1 1\n"), "obj", "model/obj", "")
}
