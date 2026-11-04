package local

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// #81: high-poly models exceed the fixed per-format floors. Meshy 7.1
// image-to-3D at 4k without remeshing (2026-11-03, measured 2026-11-04)
// returned a 111 MB 3MF holding 533 MB of model XML, a 353 MB USDZ holding a
// 295 MB USDC and a 307-385 MB GLB whose float accessors exceed the fixed
// work cap. The fixtures below reproduce each shape with synthetic bytes.

func TestScaledInspectionCeilings(t *testing.T) {
	for _, item := range []struct {
		name      string
		got, want int64
	}{
		{"xml_floor_without_size", archiveXMLLimit(0), maxArchiveXMLBytes},
		{"xml_floor_small_archive", archiveXMLLimit(10 << 20), maxArchiveXMLBytes},
		{"xml_meshy_3mf", archiveXMLLimit(111494733), 8 * 111494733},
		{"xml_expansion_bound", archiveXMLLimit(200 << 20), maxArchiveBytes},
		{"usdc_floor_fixed_budget", newInspectionWorkBudget().bytesCeiling(maxUSDCBytes), maxUSDCBytes},
		{"usdc_meshy_usdz", newInspectionWorkBudgetForSize(352702895).bytesCeiling(maxUSDCBytes), maxArchiveBytes},
		{"work_floor", newInspectionWorkBudgetForSize(1 << 20).remaining, maxInspectionWork},
		{"work_scaled", newInspectionWorkBudgetForSize(384617992).remaining, 8 * 384617992},
		{"work_bound", newInspectionWorkBudgetForSize(1 << 40).remaining, maxScaledInspectionWork},
	} {
		if item.got != item.want {
			t.Errorf("%s = %d, want %d", item.name, item.got, item.want)
		}
	}
}

// The ceiling is the document length. Decoder memory is bounded separately:
// one record and the open element stack, whatever the document length.
func TestArchiveXMLDecoderMemoryStaysBounded(t *testing.T) {
	scan := func(document string) error {
		return scanArchiveXML(strings.NewReader(document), int64(len(document)), archiveXMLLimit(0))
	}
	model := `<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">`
	if err := scan(model + `<metadata>` + strings.Repeat("x", maxArchiveXMLHeldBytes/2) + `</metadata></model>`); err != nil {
		t.Fatalf("record within the held-byte bound rejected: %v", err)
	}
	if got := inspectionReasonOf(scan(model + `<metadata>` + strings.Repeat("x", maxArchiveXMLHeldBytes) + `</metadata></model>`)); got != "xml_token_limit" {
		t.Fatalf("oversized text record reason=%q", got)
	}
	if got := inspectionReasonOf(scan(model + `<object` + strings.Repeat(` a=""`, maxArchiveXMLHeldBytes/5+1) + `/></model>`)); got != "xml_token_limit" {
		t.Fatalf("oversized attribute record reason=%q", got)
	}
	// Open start tags stay held: many medium tags, each under the bound, may
	// not accumulate past it.
	open := maxArchiveXMLDepth - 1
	tag := `<g` + strings.Repeat(` a=""`, maxArchiveXMLHeldBytes/5/(open*4/5)) + `>`
	if got := inspectionReasonOf(scan(model + strings.Repeat(tag, open) + strings.Repeat(`</g>`, open) + `</model>`)); got != "xml_token_limit" {
		t.Fatalf("accumulated open tags reason=%q", got)
	}
	// Closed elements release their bytes, so many sibling records pass.
	siblings := strings.Repeat(`<vertex x="1" y="2" z="3"/>`, 2*maxArchiveXMLHeldBytes/26)
	if err := scan(model + `<mesh>` + siblings + `</mesh></model>`); err != nil {
		t.Fatalf("long document of small records rejected: %v", err)
	}
	if err := scan(strings.Repeat(`<a>`, maxArchiveXMLDepth) + strings.Repeat(`</a>`, maxArchiveXMLDepth)); err != nil {
		t.Fatalf("nesting at the depth bound rejected: %v", err)
	}
	if got := inspectionReasonOf(scan(strings.Repeat(`<a>`, maxArchiveXMLDepth+1) + strings.Repeat(`</a>`, maxArchiveXMLDepth+1))); got != "xml_depth_limit" {
		t.Fatalf("nesting past the depth bound reason=%q", got)
	}
}

// write3MFMesh streams a 3MF whose object model holds about xmlBytes of
// vertex and triangle records with pseudo-random coordinates, so it deflates
// like a real mesh (about 4-5x) rather than like repeated text. tail is
// written into the object's metadata after the mesh.
func write3MFMesh(t *testing.T, path string, xmlBytes int64, seed uint64, tail string) int64 {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	archive.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	for name, body := range map[string]string{
		"[Content_Types].xml":         `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`,
		"_rels/.rels":                 `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`,
		"3D/_rels/3dmodel.model.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/Objects/object_1.model" Id="rel1" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`,
		"3D/3dmodel.model":            `<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06" requiredextensions="p"><resources><object id="2" type="model"><components><component p:path="/3D/Objects/object_1.model" objectid="1"/></components></object></resources><build><item objectid="2"/></build></model>`,
	} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	entry, err := archive.Create("3D/Objects/object_1.model")
	if err != nil {
		t.Fatal(err)
	}
	counter := &countingWriter{writer: entry}
	out := bufio.NewWriterSize(counter, 1<<20)
	random := rand.New(rand.NewPCG(seed, 81))
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources><object id="1" type="model"><mesh><vertices>` + "\n")
	// Each vertex record is about 52 bytes and each triangle about 44.
	vertices := xmlBytes / (52 + 2*44)
	line := make([]byte, 0, 96)
	coordinate := func(name string) {
		line = append(line, ' ')
		line = append(line, name...)
		line = append(line, `="`...)
		if random.IntN(2) == 0 {
			line = append(line, '-')
		}
		line = strconv.AppendFloat(line, float64(random.IntN(100000000))/1e6, 'f', 6, 64)
		line = append(line, '"')
	}
	for range vertices {
		line = append(line[:0], `<vertex`...)
		coordinate("x")
		coordinate("y")
		coordinate("z")
		line = append(line, "/>\n"...)
		out.Write(line)
	}
	out.WriteString("</vertices><triangles>\n")
	for range 2 * vertices {
		line = append(line[:0], `<triangle`...)
		for _, name := range []string{" v1=\"", " v2=\"", " v3=\""} {
			line = append(line, name...)
			line = strconv.AppendInt(line, random.Int64N(vertices), 10)
			line = append(line, '"')
		}
		line = append(line, "/>\n"...)
		out.Write(line)
	}
	out.WriteString(`</triangles></mesh><metadatagroup><metadata name="source">` + tail + `</metadata></metadatagroup></object></resources><build/></model>`)
	if err := out.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return counter.count
}

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (w *countingWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.count += int64(n)
	return n, err
}

func inspectFixtureFile(t *testing.T, path, variant, mediaType string) error {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	return ValidateDeliveredFile(file, "model", variant, mediaType)
}

func TestHighPoly3MFModelXMLScalesWithArchive(t *testing.T) {
	if testing.Short() || raceDetectorEnabled {
		t.Skip("decodes two model XML documents over 256 MiB on one goroutine")
	}
	directory := t.TempDir()
	safe := filepath.Join(directory, "safe.3mf")
	xmlBytes := write3MFMesh(t, safe, maxArchiveXMLBytes+(16<<20), 1, "plinth")
	info, err := os.Stat(safe)
	if err != nil {
		t.Fatal(err)
	}
	if xmlBytes <= maxArchiveXMLBytes || xmlBytes > archiveXMLLimit(info.Size()) {
		t.Fatalf("fixture XML %d bytes in a %d byte archive no longer covers the scaled ceiling", xmlBytes, info.Size())
	}
	if err := inspectFixtureFile(t, safe, "3mf", "model/3mf"); err != nil {
		t.Fatalf("high-poly 3MF rejected: %v (reason %q)", err, inspectionReasonOf(err))
	}
	// The scaled ceiling still reads the complete document: an address after
	// the first 256 MiB is found.
	unsafe := filepath.Join(directory, "unsafe.3mf")
	write3MFMesh(t, unsafe, maxArchiveXMLBytes+(16<<20), 1, "https://private.example/texture.png")
	if err := inspectFixtureFile(t, unsafe, "3mf", "model/3mf"); err == nil {
		t.Fatal("external address after the fixed 256 MiB floor was accepted")
	}
}

// A small archive that expands far does not raise the XML ceiling.
func TestCompressible3MFModelXMLKeepsFixedFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 3MF whose model XML exceeds 256 MiB")
	}
	vertex := `<vertex x="1.000000" y="2.000000" z="3.000000"/>` + "\n"
	var body bytes.Buffer
	archive := zip.NewWriter(&body)
	archive.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	for name, content := range map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`,
		"3D/3dmodel.model":    `<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources><object id="1"><mesh><vertices>` + strings.Repeat(vertex, (maxArchiveXMLBytes+(1<<20))/len(vertex)) + `</vertices></mesh></object></resources></model>`,
	} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if archiveXMLLimit(int64(body.Len())) != maxArchiveXMLBytes {
		t.Fatalf("a %d byte fixture scales the ceiling; it no longer covers the fixed floor", body.Len())
	}
	err := inspectDeliveredAsset(writeFixture(t, body.Bytes()), "model", "3mf", "model/3mf")
	if got := inspectionReasonOf(err); got != "xml_size_limit" {
		t.Fatalf("compressible oversized XML reason=%q: %v", got, err)
	}
}

func writeFixture(t *testing.T, body []byte) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

// usdcLargeVec2fFixture moves the finite_vec2f crate's single Vec2f array to
// a new location and grows it past the fixed USDC floor, as a high-poly mesh
// grows its points and normals. It also returns the array's offset.
func usdcLargeVec2fFixture(t *testing.T, bytesOver int64) ([]byte, int64) {
	t.Helper()
	crate := usdcFixture(t, "finite_vec2f")
	reps := usdcFixtureReps(t, "finite_vec2f")
	const arrayRep = 5
	if reps[arrayRep]>>63 != 1 || uint8(reps[arrayRep]>>48) != 20 {
		t.Fatal("finite_vec2f fixture no longer has an uncompressed Vec2f array rep")
	}
	// The old array's protocol-relative float bytes become unclaimed once
	// the rep moves; zero them so they stay finite and address-free.
	old := int64(reps[arrayRep] & (1<<48 - 1))
	clear(crate[old+8 : old+16])
	offset := usdcTestSections(crate)[0].start
	count := (maxUSDCBytes+bytesOver)/8 + 1
	array := make([]byte, 8+8*count)
	binary.LittleEndian.PutUint64(array, uint64(count))
	one := math.Float32bits(1)
	for at := 8; at < len(array); at += 4 {
		binary.LittleEndian.PutUint32(array[at:], one)
	}
	crate = usdcTestRewrite(crate, "", nil, array)
	reps[arrayRep] = reps[arrayRep]&^(1<<48-1) | uint64(offset)
	return usdcTestReps(crate, reps), offset
}

func TestHighPolyUSDCScalesWithDeliveredFile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a USDC over 128 MiB")
	}
	crate, array := usdcLargeVec2fFixture(t, 1<<20)
	if got := inspectionReasonOf(inspectUSDC(bytes.NewReader(crate), int64(len(crate)), nil, "", newInspectionWorkBudget())); got != "usdc_size_limit" {
		t.Fatalf("the fixed floor admits this USDC (reason %q); this test no longer covers scaling", got)
	}
	if err := inspectDeliveredAsset(writeFixture(t, crate), "converted_model", "usdc", "application/octet-stream"); err != nil {
		t.Fatalf("high-poly USDC rejected: %v (reason %q)", err, inspectionReasonOf(err))
	}
	// The moved array is what the parser walks: a count past the crate fails.
	invalid := append([]byte(nil), crate...)
	binary.LittleEndian.PutUint64(invalid[array:], 1<<40)
	if inspectDeliveredAsset(writeFixture(t, invalid), "converted_model", "usdc", "application/octet-stream") == nil {
		t.Fatal("a Vec2f array past the end of a scaled USDC was accepted")
	}
	usdz := usdcTestZIP(t, crate, true, false)
	if err := inspectDeliveredAsset(writeFixture(t, usdz), "converted_model", "usdz", "model/vnd.usdz+zip"); err != nil {
		t.Fatalf("high-poly USDZ rejected: %v (reason %q)", err, inspectionReasonOf(err))
	}
	// A deflated crate in a small ZIP keeps the fixed floor: the ceiling
	// follows the delivered file, not the size its members claim.
	var small bytes.Buffer
	archive := zip.NewWriter(&small)
	archive.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	entry, err := archive.Create("model.usdc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(crate); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if int64(small.Len())*inspectionWorkPerByte > maxUSDCBytes {
		t.Fatalf("a %d byte deflated fixture scales the ceiling; it no longer covers the floor", small.Len())
	}
	err = inspectDeliveredAsset(writeFixture(t, small.Bytes()), "converted_model", "usdz", "model/vnd.usdz+zip")
	if got := inspectionReasonOf(err); got != "usdc_size_limit" {
		t.Fatalf("deflated oversized USDC reason=%q: %v", got, err)
	}
}

func TestHighPolyGLBNumericWorkScalesWithFile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a GLB over 256 MiB")
	}
	// Float VEC3 positions cost two work units per byte, so more than 256 MiB
	// of them exceeds the fixed 512 MiB work cap on their own.
	const vertices = (maxInspectionWork/2)/12 + (1 << 20)
	bin := make([]byte, vertices*12)
	one := math.Float32bits(1)
	for at := 0; at < len(bin); at += 4 {
		binary.LittleEndian.PutUint32(bin[at:], one)
	}
	document := fmt.Sprintf(`{"asset":{"version":"2.0"},"buffers":[{"byteLength":%d}],"bufferViews":[{"buffer":0,"byteLength":%d}],"accessors":[{"bufferView":0,"componentType":5126,"count":%d,"type":"VEC3"}],"meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}]}`, len(bin), len(bin), vertices)
	body := glbFixtureWithBIN(document, bin)
	if got := inspectionReasonOf(inspectGLBStreamBounded(bytes.NewReader(body), int64(len(body)), nil, "", newInlineInspectionBudget())); got != "inspection_work_limit" {
		t.Fatalf("the fixed work cap admits this GLB (reason %q); this test no longer covers scaling", got)
	}
	if err := inspectDeliveredAsset(writeFixture(t, body), "model", "glb", "model/gltf-binary"); err != nil {
		t.Fatalf("high-poly GLB rejected: %v (reason %q)", err, inspectionReasonOf(err))
	}
	// Scaling does not waive the per-value check across the whole buffer.
	binary.LittleEndian.PutUint32(body[len(body)-4:], 0x7fc00000)
	if got := inspectionReasonOf(inspectDeliveredAsset(writeFixture(t, body), "model", "glb", "model/gltf-binary")); got != "non_finite_float" {
		t.Fatalf("NaN at the end of a scaled GLB reason=%q", got)
	}
}
