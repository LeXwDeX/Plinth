package modelconvert

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelconvert/modelconverttest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
)

// generatedPolicy mirrors generated-glb-v1 in the default Product Schema.
func generatedPolicy() modelinput.Policy {
	return modelinput.Policy{ProfileID: modelinput.GeneratedProfileID, Revision: modelinput.GeneratedProfileRevision, Limits: modelinput.Limits{
		MaxBytes: 128 << 20, MaxJSONBytes: 16 << 20, MaxDepth: 64, MaxElements: 1000000, MaxVertices: 3000000, MaxTriangles: 4000000,
		MaxNodes: 10000, MaxWork: 1 << 30, MaxImagePixels: 8192 * 8192, MaxTotalImagePixels: 4 * 8192 * 8192}}
}

// uploadPolicy mirrors static-glb-v1, the upload security limit.
func uploadPolicy() modelinput.Policy {
	return modelinput.Policy{ProfileID: modelinput.ProfileID, Revision: modelinput.ProfileRevision, Limits: modelinput.Limits{
		MaxBytes: 10 << 20, MaxJSONBytes: 2 << 20, MaxDepth: 64, MaxElements: 250000, MaxVertices: 500000, MaxTriangles: 500000,
		MaxNodes: 10000, MaxWork: 128 << 20, MaxImagePixels: 16777216, MaxTotalImagePixels: 33554432}}
}

func packageLimits() Limits {
	return Limits{MaxInputBytes: 128 << 20, MaxOutputBytes: 129 << 20, MaxWork: 2 << 30}
}

func convertPackage(t *testing.T, raw []byte, policy modelinput.Policy, limits Limits) ([]byte, PackageResult) {
	t.Helper()
	original := bytes.Clone(raw)
	var out bytes.Buffer
	result, err := GLBToGLTFPackage(context.Background(), raw, policy, limits, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("conversion mutated its source")
	}
	if result.Bytes != int64(out.Len()) {
		t.Fatalf("reported %d bytes, wrote %d", result.Bytes, out.Len())
	}
	return out.Bytes(), result
}

func TestPackageKeepsEveryAccessorAndImageByteAndReferencesFilesByRelativeURI(t *testing.T) {
	for _, shape := range []modelinputtest.Options{{}, {Indexed: true}, {Interleaved: true, Indexed: true}, {Interleaved: true, Indexed: true, Textured: true, MultiplePrimitives: true}} {
		raw := modelinputtest.Triangle(shape)
		archive, result := convertPackage(t, raw, generatedPolicy(), packageLimits())
		if err := modelconverttest.VerifyPackage(archive, raw); err != nil {
			t.Fatalf("%+v: %v", shape, err)
		}
		wantImages := 0
		if shape.Textured {
			wantImages = 1
		}
		if result.Images != wantImages || result.BufferBytes <= 0 || result.BufferBytes%4 != 0 {
			t.Fatalf("%+v: result = %+v", shape, result)
		}
		again, _ := convertPackage(t, raw, generatedPolicy(), packageLimits())
		if !bytes.Equal(archive, again) {
			t.Fatal("the same source produced different package bytes")
		}
	}
}

// A synthetic generation-sized model: above every static upload limit (bytes,
// vertices, triangles and an 8192-square texture) and within generated-glb-v1.
func TestLargeGeneratedModelConvertsUnderTheGeneratedProfileOnly(t *testing.T) {
	raw := modelinputtest.Large(modelinputtest.LargeOptions{Grid: 722, Images: []modelinputtest.LargeImage{{Width: 8192, Height: 8192}, {Width: 1024, Height: 1024, Noise: true}}})
	if len(raw) <= 10<<20 {
		t.Fatalf("fixture is only %d bytes", len(raw))
	}
	var discarded bytes.Buffer
	_, err := GLBToGLTFPackage(context.Background(), raw, uploadPolicy(), packageLimits(), &discarded)
	var limit *modelinput.Error
	if !errors.As(err, &limit) || limit.Kind != modelinput.Limit || discarded.Len() != 0 {
		t.Fatalf("upload profile accepted a generation-sized model: %v (%d bytes written)", err, discarded.Len())
	}
	archive, result := convertPackage(t, raw, generatedPolicy(), packageLimits())
	if err := modelconverttest.VerifyPackage(archive, raw); err != nil {
		t.Fatal(err)
	}
	if result.Images != 2 || result.Bytes > int64(len(raw))+64<<10 {
		t.Fatalf("result = %+v for a %d byte source", result, len(raw))
	}
}

func TestPackageRejectsBeforeWritingAndHonorsTheOutputLimit(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
	small := packageLimits()
	small.MaxOutputBytes = 512
	var out bytes.Buffer
	_, err := GLBToGLTFPackage(context.Background(), raw, generatedPolicy(), small, &out)
	var limit *modelinput.Error
	if !errors.As(err, &limit) || limit.Kind != modelinput.Limit || limit.Reason != "conversion_output_bytes" || out.Len() != 0 {
		t.Fatalf("output limit: %v, wrote %d", err, out.Len())
	}
	document, binary := modelinputtest.Document(modelinputtest.Options{Indexed: true})
	document["animations"] = []any{map[string]any{}}
	_, err = GLBToGLTFPackage(context.Background(), modelinputtest.GLB(document, binary), generatedPolicy(), packageLimits(), &out)
	if !errors.As(err, &limit) || limit.Kind != modelinput.Unsupported || out.Len() != 0 {
		t.Fatalf("unsupported structure: %v, wrote %d", err, out.Len())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = GLBToGLTFPackage(ctx, raw, generatedPolicy(), packageLimits(), io.Discard); !errors.As(err, &limit) || limit.Kind != modelinput.Canceled {
		t.Fatalf("canceled conversion: %v", err)
	}
}

// The generated profile bounds rasters by header and never decodes pixels;
// the upload profile still decodes them. Pixel content is checked by the
// asset store when the package is committed.
func TestGeneratedProfileChecksRasterHeadersWithoutDecoding(t *testing.T) {
	document, binary := modelinputtest.Document(modelinputtest.Options{Indexed: true, Textured: true})
	views := document["bufferViews"].([]any)
	image := views[len(views)-1].(map[string]any)
	start := image["byteOffset"].(int)
	length := image["byteLength"].(int)
	corrupt := bytes.Clone(binary)
	// Damage the IDAT payload but keep the IHDR header intact.
	corrupt[start+length-14] ^= 0xff
	raw := modelinputtest.GLB(document, corrupt)
	if _, err := modelinput.ValidateGLBBytes(context.Background(), raw, uploadPolicy()); err == nil {
		t.Fatal("upload profile accepted undecodable pixels")
	}
	if _, err := modelinput.ValidateGLBBytes(context.Background(), raw, generatedPolicy()); err != nil {
		t.Fatalf("generated profile decoded pixels: %v", err)
	}
	oversized := modelinputtest.Large(modelinputtest.LargeOptions{Grid: 4, Images: []modelinputtest.LargeImage{{Width: 8193, Height: 8192}}})
	var limit *modelinput.Error
	if _, err := modelinput.ValidateGLBBytes(context.Background(), oversized, generatedPolicy()); !errors.As(err, &limit) || limit.Reason != "image_pixel_limit" {
		t.Fatalf("generated profile accepted a texture above its pixel limit: %v", err)
	}
}

// No member may use a trailing data descriptor (flag bit 3): streaming readers
// such as Java's ZipInputStream reject a STORED member that has one. The
// checker itself must catch an archive written the default way.
func TestPackageMembersCarryFinalSizesWithoutDataDescriptors(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
	archive, _ := convertPackage(t, raw, generatedPolicy(), packageLimits())
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]uint16{}
	for _, file := range reader.File {
		if file.Flags&0x8 != 0 {
			t.Fatalf("%s sets the data-descriptor flag", file.Name)
		}
		methods[file.Name] = file.Method
	}
	if methods[PackageDocument] != zip.Deflate || methods[PackageBuffer] != zip.Store || methods["textures/0.png"] != zip.Store {
		t.Fatalf("member methods = %v", methods)
	}
	var streamed bytes.Buffer
	writer := zip.NewWriter(&streamed)
	entry, _ := writer.CreateHeader(&zip.FileHeader{Name: "model.bin", Method: zip.Store})
	_, _ = entry.Write([]byte("bin"))
	_ = writer.Close()
	if err := modelconverttest.VerifyPackage(streamed.Bytes(), raw); err == nil || !strings.Contains(err.Error(), "data-descriptor") {
		t.Fatalf("checker accepted a STORED member with a data descriptor: %v", err)
	}
}
