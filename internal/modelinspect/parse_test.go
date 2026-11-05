package modelinspect

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/color"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestParseAccessorSpanOverflowIsRejected(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("needs a 64-bit int for a wrapping accessor count")
	}
	builder := newTestGLB()
	view := builder.addBufferView(make([]byte, 12), 0)
	// stride 12 * (count-1) == 12 * (2^62) wraps to 0 in int64, so the old
	// bounds check accepted a span of only elemSize.
	builder.addAccessor(view, 0, 5126, int(int64(1)<<62)+1, "VEC3", false, nil, nil)
	raw := builder.finish()
	_, err := Parse(bytes.NewReader(raw), int64(len(raw)), DefaultLimits())
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Parse error = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "out of bufferView bounds") {
		t.Fatalf("Parse error = %v, want the bufferView bounds error", err)
	}
}

func TestParseCubeMetadata(t *testing.T) {
	data := cubeGLB()
	model := mustParseModel(t, data, DefaultLimits())
	if model.FileBytes != int64(len(data)) {
		t.Fatalf("FileBytes = %d, want %d", model.FileBytes, len(data))
	}
	if len(model.Nodes) != 1 || len(model.Meshes) != 1 || len(model.Scenes) != 1 {
		t.Fatalf("unexpected metadata: nodes=%d meshes=%d scenes=%d",
			len(model.Nodes), len(model.Meshes), len(model.Scenes))
	}
	if len(model.parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(model.parts))
	}
	part := model.parts[0]
	if part.id != "n0.p0" || part.vertexCount != 8 || part.triangleCount != 12 {
		t.Fatalf("unexpected part: %+v", part)
	}
	if model.Nodes[0].Name != "cube" || model.Nodes[0].Mesh != 0 {
		t.Fatalf("unexpected node: %+v", model.Nodes[0])
	}
}

func TestParseExtensionsUsedVerbatim(t *testing.T) {
	builder := cubeDocument()
	builder.doc["extensionsUsed"] = []string{"VENDOR_alpha", "KHR_texture_transform"}
	model := mustParseModel(t, builder.finish(), DefaultLimits())
	want := []string{"VENDOR_alpha", "KHR_texture_transform"}
	if !reflect.DeepEqual(model.ExtensionsUsed, want) {
		t.Fatalf("ExtensionsUsed = %v, want %v", model.ExtensionsUsed, want)
	}
	if len(model.ExtensionsRequired) != 0 {
		t.Fatalf("ExtensionsRequired = %v, want none", model.ExtensionsRequired)
	}
}

func TestParseRequiredExtensionsUnsupported(t *testing.T) {
	for _, extension := range []string{
		"KHR_draco_mesh_compression",
		"EXT_meshopt_compression",
		"KHR_texture_basisu",
		"VENDOR_unknown_extension",
	} {
		t.Run(extension, func(t *testing.T) {
			builder := cubeDocument()
			builder.doc["extensionsRequired"] = []string{extension}
			data := builder.finish()
			_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
			if err == nil {
				t.Fatal("Parse succeeded, want ErrUnsupported")
			}
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), extension) {
				t.Fatalf("error %q does not name %q", err, extension)
			}
		})
	}
}

func TestParseExternalBuffersUnsupported(t *testing.T) {
	for name, uri := range map[string]string{
		"relative path": "model.bin",
		"data URI":      "data:application/octet-stream;base64,AAAAAAAA",
	} {
		t.Run(name, func(t *testing.T) {
			builder := cubeDocument()
			builder.bufferURI = uri
			data := builder.finish()
			// Parse needs the actual size; finish knows it, so call directly.
			_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), "external buffer") {
				t.Fatalf("error %q does not mention external buffer", err)
			}
		})
	}
}

func TestParseExternalImageUnsupported(t *testing.T) {
	solid := color.NRGBA{R: 200, G: 100, B: 50, A: 255}
	builder := texturedQuadDocument(t, [4]color.NRGBA{solid, solid, solid, solid},
		[4]float64{1, 1, 1, 1})
	imageValue := builder.doc["images"].([]any)[0].(map[string]any)
	delete(imageValue, "bufferView")
	imageValue["uri"] = "texture.png"
	data := builder.finish()
	_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestParseSparseUnsupported(t *testing.T) {
	builder := cubeDocument()
	builder.appendDoc("accessors", map[string]any{
		"componentType": 5126,
		"count":         1,
		"type":          "SCALAR",
		"sparse":        map[string]any{"count": 1},
	})
	data := builder.finish()
	_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestParseInvalidContainers(t *testing.T) {
	cube := cubeGLB()

	t.Run("nil reader", func(t *testing.T) {
		if _, err := Parse(nil, 0, DefaultLimits()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})
	t.Run("bad magic", func(t *testing.T) {
		data := append([]byte(nil), cube...)
		data[0] = 0
		if _, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})
	t.Run("unsupported version", func(t *testing.T) {
		data := append([]byte(nil), cube...)
		data[4] = 1
		if _, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits()); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("error = %v, want ErrUnsupported", err)
		}
	})
	t.Run("declared length mismatch", func(t *testing.T) {
		data := cube[:len(cube)-4]
		if _, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})
	t.Run("accessor out of bounds", func(t *testing.T) {
		builder := cubeDocument()
		builder.mutateAccessor(0, func(accessor map[string]any) {
			accessor["count"] = 1000
		})
		data := builder.finish()
		if _, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})
	t.Run("truncated BIN", func(t *testing.T) {
		builder := cubeDocument()
		builder.mutateAccessor(0, func(accessor map[string]any) {
			accessor["byteOffset"] = 4
		})
		// Shrink the position bufferView below the accessor span.
		view := builder.doc["bufferViews"].([]any)[0].(map[string]any)
		view["byteLength"] = 8
		data := builder.finish()
		if _, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
	})
}

func TestParseLimits(t *testing.T) {
	cube := cubeGLB()
	hierarchy := trsHierarchyDocument().finish()
	tests := []struct {
		name   string
		data   []byte
		limits Limits
	}{
		{"MaxBytes", cube, Limits{MaxBytes: int64(len(cube)) - 1}},
		{"MaxTriangles", cube, Limits{MaxTriangles: 11}},
		{"MaxVertices", cube, Limits{MaxVertices: 7}},
		{"MaxNodes", hierarchy, Limits{MaxNodes: 1}},
		{"MaxDepth", hierarchy, Limits{MaxDepth: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(bytes.NewReader(test.data), int64(len(test.data)), test.limits)
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("error = %v, want ErrTooLarge", err)
			}
		})
	}
}

func TestParseWithoutScenesUsesRoots(t *testing.T) {
	builder := cubeDocument()
	delete(builder.doc, "scenes")
	data := builder.finish()
	model := mustParseModel(t, data, DefaultLimits())
	if len(model.parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(model.parts))
	}
}

func TestParseSkinAndAnimation(t *testing.T) {
	data := skinAnimationDocument().finish()
	model := mustParseModel(t, data, DefaultLimits())
	if len(model.Skins) != 1 || len(model.Skins[0].Joints) != 2 {
		t.Fatalf("unexpected skins: %+v", model.Skins)
	}
	if len(model.Animations) != 1 {
		t.Fatalf("animations = %d, want 1", len(model.Animations))
	}
	animation := model.Animations[0]
	if animation.Name != "wave" || animation.Channels != 1 || animation.Translations != 1 {
		t.Fatalf("unexpected animation: %+v", animation)
	}
	if animation.Rotations != 0 || animation.Scales != 0 || animation.Weights != 0 {
		t.Fatalf("unexpected animation channel counts: %+v", animation)
	}
	if animation.Duration != 2.5 {
		t.Fatalf("Duration = %v, want 2.5", animation.Duration)
	}
}

func TestParseTriangleStrip(t *testing.T) {
	data := stripDocument().finish()
	model := mustParseModel(t, data, DefaultLimits())
	if len(model.parts) != 1 || model.parts[0].triangleCount != 2 {
		t.Fatalf("strip part = %+v, want 2 triangles", model.parts)
	}
	report := Inspect(model, DefaultInspectOptions())
	if report.Triangles != 2 {
		t.Fatalf("report.Triangles = %d, want 2", report.Triangles)
	}
}

func TestParseAttributeCountMismatch(t *testing.T) {
	pixel := color.NRGBA{R: 200, G: 100, B: 50, A: 255}
	builder := texturedQuadDocument(t, [4]color.NRGBA{pixel, pixel, pixel, pixel},
		[4]float64{1, 1, 1, 1})
	builder.mutateAccessor(1, func(accessor map[string]any) {
		accessor["count"] = 3
	})
	data := builder.finish()
	_, err := Parse(bytes.NewReader(data), int64(len(data)), DefaultLimits())
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestParseInterleavedByteStride(t *testing.T) {
	builder := newTestGLB()
	positions := cubePositions()
	interleaved := make([]byte, 8*16)
	for i := 0; i < 8; i++ {
		copy(interleaved[i*16:], floatBytes(positions[i*3:i*3+3]))
	}
	view := builder.addBufferView(interleaved, 16)
	position := builder.addAccessor(view, 0, 5126, 8, "VEC3", false,
		[]float64{-0.5, -0.5, -0.5}, []float64{0.5, 0.5, 0.5})
	indices := builder.addIndices16(flattenFaces(cubeFaces()))
	mesh := builder.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    indices,
	}})
	node := builder.addNode(map[string]any{"mesh": mesh})
	builder.addScene([]int{node})
	model := mustParseModel(t, builder.finish(), DefaultLimits())
	report := Inspect(model, DefaultInspectOptions())
	for i := 0; i < 3; i++ {
		approx(t, report.BBox.Min[i], -0.5, 1e-9, "interleaved bbox min")
		approx(t, report.BBox.Max[i], 0.5, 1e-9, "interleaved bbox max")
	}
}

func TestParseNodeMatrix(t *testing.T) {
	builder := newTestGLB()
	position := builder.addVec3Accessor(cubePositions(), true)
	indices := builder.addIndices16(flattenFaces(cubeFaces()))
	mesh := builder.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    indices,
	}})
	// Column-major: scale by two and translate by (10, 1, 0).
	matrix := []float64{
		2, 0, 0, 0,
		0, 2, 0, 0,
		0, 0, 2, 0,
		10, 1, 0, 1,
	}
	node := builder.addNode(map[string]any{"mesh": mesh, "matrix": matrix})
	builder.addScene([]int{node})
	model := mustParseModel(t, builder.finish(), DefaultLimits())
	if !model.Nodes[0].HasMatrix {
		t.Fatal("node matrix was not recorded")
	}
	report := Inspect(model, DefaultInspectOptions())
	wantMin := [3]float64{9, 0, -1}
	wantMax := [3]float64{11, 2, 1}
	for i := 0; i < 3; i++ {
		approx(t, report.BBox.Min[i], wantMin[i], 1e-9, "matrix bbox min")
		approx(t, report.BBox.Max[i], wantMax[i], 1e-9, "matrix bbox max")
	}
}

func TestDecodeComponentF32(t *testing.T) {
	floatOnePointFive := make([]byte, 4)
	binary.LittleEndian.PutUint32(floatOnePointFive, math.Float32bits(1.5))
	tests := []struct {
		name          string
		componentType int
		normalized    bool
		raw           []byte
		want          float32
	}{
		{"i8", 5120, false, []byte{0xff}, -1},
		{"i8 normalized", 5120, true, []byte{0x80}, -1},
		{"u8", 5121, false, []byte{200}, 200},
		{"u8 normalized", 5121, true, []byte{255}, 1},
		{"i16 normalized", 5122, true, []byte{0, 0x80}, -1},
		{"u16", 5123, false, []byte{0x34, 0x12}, 0x1234},
		{"u16 normalized", 5123, true, []byte{0xff, 0xff}, 1},
		{"u32 normalized", 5125, true, []byte{0xff, 0xff, 0xff, 0xff}, 1},
		{"f32", 5126, false, floatOnePointFive, 1.5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := decodeComponentF32(test.componentType, test.normalized, test.raw)
			if math.Abs(float64(got-test.want)) > 1e-6 {
				t.Fatalf("decodeComponentF32 = %v, want %v", got, test.want)
			}
		})
	}
}
